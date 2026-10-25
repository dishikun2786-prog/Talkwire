package io.imkit.core.client

import io.imkit.core.json.Json
import io.imkit.core.json.JsonException
import io.imkit.core.json.JsonNull
import io.imkit.core.json.JsonObject
import io.imkit.core.json.JsonString
import io.imkit.core.json.jsonObject
import io.imkit.core.protocol.Actions
import io.imkit.core.protocol.Reply
import io.imkit.core.protocol.WireMessage
import io.imkit.core.transport.ImSocket
import io.imkit.core.transport.ImSocketListener
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.selects.select
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withTimeoutOrNull
import java.util.UUID
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.atomic.AtomicLong
import kotlin.math.min

/**
 * One logical connection to the IM server: connect, log in, keep reconnecting,
 * correlate replies, and turn pushes into [events].
 *
 * The connection rules (red line C1) and how they are kept:
 *
 * - **Serialized**: a single connection loop owns every connect attempt;
 *   [start] while it runs is a no-op, so two callers cannot race two sockets.
 * - **Identity cleared before connecting**: the previous session is dropped
 *   before a new socket opens, so nothing can use a uid the new socket has
 *   not confirmed.
 * - **Connected only after the login reply**: [state] becomes
 *   [ConnectionState.Connected] when the server answered the login with 200,
 *   never when the socket merely opened.
 * - **Generations**: every socket gets a number; callbacks of a replaced
 *   socket are ignored, so a late close of an old socket cannot tear down the
 *   new one.
 * - **Jittered backoff** over all endpoints; [onNetworkAvailable] shortens a
 *   wait but never interrupts a handshake in progress.
 *
 * All frames are handled in arrival order by one coroutine, so
 * [ImEvent.Connected] always comes before the pushes that follow the login.
 * [events] buffers without limit, so a collector may make requests from its
 * handler without stalling reply delivery.
 */
public class ImClient(
    private val config: ImClientConfig,
    parentScope: CoroutineScope,
) {
    private val scope = CoroutineScope(parentScope.coroutineContext + SupervisorJob(parentScope.coroutineContext[Job]))

    private val _state = MutableStateFlow<ConnectionState>(ConnectionState.Idle)
    public val state: StateFlow<ConnectionState> = _state.asStateFlow()

    private val _events = MutableSharedFlow<ImEvent>(extraBufferCapacity = Int.MAX_VALUE)
    public val events: SharedFlow<ImEvent> = _events.asSharedFlow()

    internal val publishTimeoutMs: Long get() = config.publishTimeoutMs

    /** The current session, or null while not logged in. */
    public val session: ImSession? get() = synchronized(lock) { session_ }

    private sealed interface Inbound {
        val gen: Long

        class Opened(override val gen: Long) : Inbound

        class Text(override val gen: Long, val text: String) : Inbound

        class Gone(override val gen: Long, val reason: String) : Inbound
    }

    private class Attempt(val gen: Long, val endpoint: String) {
        val opened = CompletableDeferred<Unit>()
        val login = CompletableDeferred<JsonObject>()
        val gone = CompletableDeferred<String>()

        @Volatile var replaced = false
        var loggedInAt = 0L
    }

    private class Pending(
        val action: String,
        val gen: Long,
        /** Legacy servers answer a `pres` query without an id (spec 3.3). */
        val presFallback: Boolean,
    ) {
        val reply = CompletableDeferred<JsonObject>()
    }

    private val lock = Any()
    private var gen = 0L
    private var socket: ImSocket? = null
    private var session_: ImSession? = null
    private var attempt: Attempt? = null
    private var loop: Job? = null
    private var stopping = false
    private var preferred: String? = null

    private val inbound = Channel<Inbound>(Channel.UNLIMITED)
    private val wake = Channel<Unit>(Channel.CONFLATED)
    private val pending = ConcurrentHashMap<String, Pending>()
    private val typed = ConcurrentHashMap<String, Pending>()
    private val ids = AtomicLong()
    private val presLock = Mutex()

    init {
        scope.launch {
            for (item in inbound) process(item)
        }
    }

    // ---------------------------------------------------------------- lifecycle

    /** Starts connecting (and reconnecting). Does nothing while already running. */
    public fun start() {
        synchronized(lock) {
            if (loop?.isActive == true) return
            stopping = false
            // Leave Idle/Stopped before returning: a caller waiting right
            // after start() must not see the previous run's state.
            _state.value = ConnectionState.Connecting(orderedEndpoints().firstOrNull() ?: "", 0)
            loop = scope.launch { runLoop() }
        }
    }

    /**
     * Disconnects for good, e.g. on logout. Requests in flight fail with
     * [ImException.ConnectionLost].
     */
    public fun stop() {
        stopWith(StopReason.STOPPED, cancelLoop = true)
    }

    /** The network came back: skip the rest of a reconnect wait. */
    public fun onNetworkAvailable() {
        wake.trySend(Unit)
    }

    /** Suspends until logged in; null when [timeoutMs] passed or the client stopped. */
    public suspend fun awaitConnected(timeoutMs: Long): ImSession? = withTimeoutOrNull(timeoutMs) {
        val s = _state.first { it is ConnectionState.Connected || it is ConnectionState.Stopped }
        (s as? ConnectionState.Connected)?.session
    }

    private fun stopWith(reason: StopReason, cancelLoop: Boolean) {
        val (job, old, had) = synchronized(lock) {
            stopping = true
            val j = if (cancelLoop) loop.also { loop = null } else null
            val s = socket
            val h = session_
            socket = null
            session_ = null
            attempt = null
            gen++
            Triple(j, s, h)
        }
        job?.cancel()
        old?.close(1000, "bye")
        failAll(ImException.ConnectionLost())
        _state.value = ConnectionState.Stopped(reason)
        if (had != null) _events.tryEmit(ImEvent.Disconnected(willReconnect = false))
    }

    private sealed interface Outcome {
        class Ended(val replaced: Boolean, val livedMs: Long) : Outcome

        class Failed(val error: String) : Outcome

        data object AuthRejected : Outcome

        class Fatal(val reason: StopReason) : Outcome
    }

    private suspend fun runLoop() {
        var round = 0
        var authFailures = 0
        var lastError: String? = null
        while (currentCoroutineContext().isActive) {
            var outcome: Outcome = Outcome.Failed("no endpoint")
            for (url in orderedEndpoints()) {
                outcome = connectOnce(url, round, forceRefresh = authFailures > 0)
                if (outcome !is Outcome.Failed) break
                lastError = outcome.error
            }
            when (val o = outcome) {
                is Outcome.Ended -> {
                    if (o.replaced) {
                        stopWith(StopReason.REPLACED, cancelLoop = false)
                        return
                    }
                    authFailures = 0
                    lastError = "connection closed"
                    // A connection that dies right after login keeps the backoff
                    // growing, so a server that drops us at once is not hammered.
                    if (o.livedMs >= STABLE_CONNECTION_MS) round = 0
                }
                Outcome.AuthRejected -> {
                    authFailures++
                    lastError = "token refused"
                    if (authFailures >= config.maxAuthFailures) {
                        stopWith(StopReason.AUTH_REJECTED, cancelLoop = false)
                        return
                    }
                }
                is Outcome.Fatal -> {
                    stopWith(o.reason, cancelLoop = false)
                    return
                }
                is Outcome.Failed -> Unit
            }
            val delayMs = backoffDelay(round)
            round++
            _state.value = ConnectionState.WaitingToReconnect(round, delayMs, lastError)
            withTimeoutOrNull(delayMs) { wake.receive() }
        }
    }

    private fun orderedEndpoints(): List<String> {
        val p = synchronized(lock) { preferred }
        return if (p != null && p in config.endpoints) listOf(p) + (config.endpoints - p) else config.endpoints
    }

    /** Exponential backoff with ±20% jitter so clients dropped together do not return together. */
    internal fun backoffDelay(round: Int): Long {
        val base = min(config.reconnectMaxDelayMs, config.reconnectBaseDelayMs shl min(round, 5))
        return (base * (0.8 + config.random.nextDouble() * 0.4)).toLong()
    }

    private suspend fun connectOnce(url: String, round: Int, forceRefresh: Boolean): Outcome {
        val token = try {
            config.tokenProvider.accessToken(forceRefresh)
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            return Outcome.Failed("token provider failed: ${e.message}")
        } ?: return Outcome.Fatal(StopReason.NO_CREDENTIALS)

        val a: Attempt
        val old: ImSocket?
        synchronized(lock) {
            if (stopping) return Outcome.Fatal(StopReason.STOPPED)
            old = socket
            socket = null
            session_ = null // identity cleared before the new socket exists (C1)
            gen++
            a = Attempt(gen, url)
            attempt = a
        }
        old?.cancel()
        failAll(ImException.ConnectionLost())
        _state.value = ConnectionState.Connecting(url, round)

        val sock = try {
            config.transport.open(url, listenerFor(a.gen))
        } catch (e: Exception) {
            return Outcome.Failed("cannot open $url: ${e.message}")
        }
        synchronized(lock) {
            if (attempt !== a) {
                sock.cancel()
                return Outcome.Failed("superseded")
            }
            socket = sock
        }

        val login = jsonObject {
            put("id", "login-${a.gen}")
            put("action", Actions.LOGIN)
            put("token", token)
            put("device", config.deviceName)
        }
        val reply: JsonObject? = withTimeoutOrNull(config.loginTimeoutMs) {
            val opened = select {
                a.opened.onAwait { true }
                a.gone.onAwait { false }
            }
            if (!opened || !sock.send(login.toString())) {
                null
            } else {
                select<JsonObject?> {
                    a.login.onAwait { it }
                    a.gone.onAwait { null }
                }
            }
        }
        if (reply == null) {
            teardown(a)
            return Outcome.Failed(if (a.gone.isCompleted) "closed during login" else "no login reply from $url")
        }
        val code = reply.int("code") ?: 0
        if (code == 200) {
            synchronized(lock) { preferred = url }
            a.gone.await()
            return Outcome.Ended(a.replaced, System.currentTimeMillis() - a.loggedInAt)
        }
        teardown(a)
        val reason = reply.nonEmptyString("reason") ?: ""
        val errCode = reply.nonEmptyString("errCode")
        return when {
            code == 401 -> Outcome.AuthRejected
            code == 409 -> Outcome.Fatal(StopReason.TOO_MANY_DEVICES)
            errCode == "invalid_device" || reason == "invalid device" -> Outcome.Fatal(StopReason.INVALID_DEVICE)
            else -> Outcome.Failed("login refused: $code $reason")
        }
    }

    private fun teardown(a: Attempt) {
        val s = synchronized(lock) {
            if (attempt !== a) return
            session_ = null
            socket.also { socket = null }
        }
        s?.cancel()
        failGen(a.gen, ImException.ConnectionLost())
    }

    private fun listenerFor(g: Long) = object : ImSocketListener {
        override fun onOpen() {
            inbound.trySend(Inbound.Opened(g))
        }

        override fun onText(text: String) {
            inbound.trySend(Inbound.Text(g, text))
        }

        override fun onClosed(code: Int, reason: String) {
            inbound.trySend(Inbound.Gone(g, "closed $code $reason"))
        }

        override fun onFailure(error: Throwable) {
            inbound.trySend(Inbound.Gone(g, "failure: ${error.message}"))
        }
    }

    // ---------------------------------------------------------------- inbound

    private fun process(item: Inbound) {
        val a = synchronized(lock) {
            val cur = attempt
            if (cur == null || cur.gen != item.gen) return // a replaced socket
            cur
        }
        when (item) {
            is Inbound.Opened -> a.opened.complete(Unit)
            is Inbound.Gone -> onGone(a)
            is Inbound.Text -> {
                val frame = try {
                    Json.parse(item.text) as? JsonObject
                } catch (e: JsonException) {
                    null
                } ?: return
                onFrame(a, frame)
            }
        }
    }

    private fun onGone(a: Attempt) {
        val (had, stop) = synchronized(lock) {
            if (attempt !== a) return
            socket = null
            val h = session_
            session_ = null
            h to stopping
        }
        a.gone.complete("gone")
        failGen(a.gen, ImException.ConnectionLost())
        if (had != null) {
            val willReconnect = !stop && !a.replaced
            if (willReconnect) _state.value = ConnectionState.WaitingToReconnect(0, 0, "connection closed")
            _events.tryEmit(ImEvent.Disconnected(willReconnect))
        }
    }

    private fun onFrame(a: Attempt, f: JsonObject) {
        val type = f.nonEmptyString("type")
        if (type != null) {
            val reqId = f.nonEmptyString("reqId")
            val p = reqId?.let { typed[it] }
            if (p != null && p.gen == a.gen) {
                p.reply.complete(f)
            } else {
                _events.tryEmit(ImEvent.Module(f, null))
            }
            return
        }
        val action = f.nonEmptyString("action") ?: return
        val id = f.nonEmptyString("id")
        if (id != null) {
            val p = pending[id]
            if (p != null && p.gen == a.gen && p.action == action) {
                p.reply.complete(f)
                return
            }
        }
        // The handshake's reply: old servers send it without an id (R19).
        if (action == Actions.LOGIN) {
            onLoginFrame(a, f)
            return
        }
        if (action == Actions.PRES && f.string("what") == "snapshot" && id == null) {
            pending.values.firstOrNull { it.presFallback && it.gen == a.gen && !it.reply.isCompleted }
                ?.reply?.complete(f)
            return
        }
        parsePush(action, f)?.let { _events.tryEmit(it) }
    }

    private fun onLoginFrame(a: Attempt, f: JsonObject) {
        val code = f.int("code") ?: 0
        val loggedIn = synchronized(lock) { session_ != null }
        if (!a.login.isCompleted && !loggedIn) {
            if (code == 200) {
                val uid = f.nonEmptyString("uid")
                if (uid == null) {
                    a.login.complete(f.with("code", io.imkit.core.json.JsonNumber("502")))
                    return
                }
                val s = ImSession(
                    uid = uid,
                    protocol = f.int("protocol") ?: 0,
                    capabilities = f.array("capabilities")?.items?.mapNotNull { (it as? JsonString)?.value }?.toSet() ?: emptySet(),
                    endpoint = a.endpoint,
                )
                synchronized(lock) {
                    if (attempt !== a) return
                    session_ = s
                }
                a.loggedInAt = System.currentTimeMillis()
                _state.value = ConnectionState.Connected(s)
                _events.tryEmit(ImEvent.Connected(s))
            }
            a.login.complete(f)
            return
        }
        if (code == 401 && loggedIn) {
            // "logged in from another device": the same device logged in again
            // somewhere; reconnecting would only take the session back (spec 6.2).
            a.replaced = true
            val s = synchronized(lock) { socket }
            s?.cancel()
            onGone(a)
        }
    }

    private fun parsePush(action: String, f: JsonObject): ImEvent? {
        val topic = f.string("topic") ?: ""
        val from = f.string("from") ?: ""
        val eventId = f.long("eventId")
        return when (action) {
            Actions.DATA -> WireMessage.parse(f)?.let { ImEvent.Message(it) }
            Actions.RECALLED -> f.long("seq")?.let { ImEvent.Recalled(topic, it, from) }
            Actions.TOPIC_CLEARED -> ImEvent.TopicCleared(
                topic, from, f.long("clearVersion") ?: 0, f.long("clearBeforeSeq") ?: 0, f.nonEmptyString("id"),
            )
            Actions.PRES -> when (f.string("what")) {
                "on" -> ImEvent.Presence(topic, true, f.long("presRev") ?: 0)
                "off" -> ImEvent.Presence(topic, false, f.long("presRev") ?: 0)
                else -> null
            }
            Actions.INFO -> when (f.string("what")) {
                "kp" -> ImEvent.Typing(topic, from)
                "read" -> f.long("seq")?.let { ImEvent.Read(topic, from, it) }
                else -> null
            }
            Actions.GRP_SYNC -> ImEvent.GroupSync(topic, f.nonEmptyString("name"), f.nonEmptyString("from"), eventId)
            Actions.GRP_DISBAND -> ImEvent.GroupRemoved(topic, f.nonEmptyString("from"), eventId)
            Actions.CONTACT_SYNC -> ImEvent.ContactSync(f.string("what") ?: "", from)
            Actions.CUSTOM_UPDATE -> {
                val what = f.nonEmptyString("what") ?: return null
                ImEvent.Custom(f.nonEmptyString("topic"), what, f["extra"] ?: JsonNull)
            }
            Actions.RED_PACKET_UPDATE -> ImEvent.Custom(f.nonEmptyString("topic"), "red_packet", f["extra"] ?: JsonNull)
            else -> if (f.has("code")) null else ImEvent.Module(f, eventId) // a late reply nobody waits for is dropped
        }
    }

    // ---------------------------------------------------------------- requests

    /**
     * Sends an action-family request and waits for its reply, matched by `id`
     * only (3.4). Throws [ImException.NotConnected] without sending when not
     * logged in, [ImException.Timeout] or [ImException.ConnectionLost] when no
     * reply came. A reply with an error code is returned, not thrown.
     */
    public suspend fun request(frame: JsonObject, timeoutMs: Long = config.requestTimeoutMs): Reply {
        val action = requireNotNull(frame.nonEmptyString("action")) { "request without action" }
        val (s, g, sock) = synchronized(lock) { Triple(session_, gen, socket) }
        if (s == null || sock == null) throw ImException.NotConnected()
        val legacyPres = action == Actions.PRES && s.protocol < 1
        if (legacyPres) {
            // Old servers answer a presence query without its id; keep one in flight.
            return presLock.withLock { send(frame, action, g, sock, timeoutMs, presFallback = true) }
        }
        return send(frame, action, g, sock, timeoutMs, presFallback = false)
    }

    private suspend fun send(frame: JsonObject, action: String, g: Long, sock: ImSocket, timeoutMs: Long, presFallback: Boolean): Reply {
        val id = "r" + ids.incrementAndGet()
        val p = Pending(action, g, presFallback)
        pending[id] = p
        try {
            if (!sock.send(frame.with("id", JsonString(id)).toString())) throw ImException.NotConnected()
            val reply = withTimeoutOrNull(timeoutMs) { p.reply.await() }
                ?: throw ImException.Timeout(connectionAlive = synchronized(lock) { gen == g && session_ != null })
            return Reply(reply)
        } finally {
            pending.remove(id)
        }
    }

    /** Sends a type-family request (forwarding, module requests) matched by `reqId` (3.3). */
    public suspend fun requestTyped(frame: JsonObject, timeoutMs: Long = config.requestTimeoutMs): JsonObject {
        val (s, g, sock) = synchronized(lock) { Triple(session_, gen, socket) }
        if (s == null || sock == null) throw ImException.NotConnected()
        val reqId = UUID.randomUUID().toString()
        val p = Pending(frame.string("action") ?: "", g, presFallback = false)
        typed[reqId] = p
        try {
            if (!sock.send(frame.with("reqId", JsonString(reqId)).toString())) throw ImException.NotConnected()
            return withTimeoutOrNull(timeoutMs) { p.reply.await() }
                ?: throw ImException.Timeout(connectionAlive = synchronized(lock) { gen == g && session_ != null })
        } finally {
            typed.remove(reqId)
        }
    }

    /** Sends a request that has no reply (`note`, acknowledgements, push tokens). False when not logged in. */
    public fun sendNoReply(frame: JsonObject): Boolean {
        val sock = synchronized(lock) { if (session_ == null) null else socket } ?: return false
        return sock.send(frame.toString())
    }

    private fun failAll(error: Exception) {
        for (k in pending.keys.toList()) pending.remove(k)?.reply?.completeExceptionally(error)
        for (k in typed.keys.toList()) typed.remove(k)?.reply?.completeExceptionally(error)
    }

    private fun failGen(g: Long, error: Exception) {
        for ((k, p) in pending.entries.toList()) if (p.gen == g) pending.remove(k)?.reply?.completeExceptionally(error)
        for ((k, p) in typed.entries.toList()) if (p.gen == g) typed.remove(k)?.reply?.completeExceptionally(error)
    }

    private companion object {
        const val STABLE_CONNECTION_MS = 10_000L
    }
}
