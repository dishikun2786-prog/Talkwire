package io.imkit.core.client

import io.imkit.core.json.JsonObject
import io.imkit.core.json.JsonValue
import io.imkit.core.protocol.WireMessage
import io.imkit.core.transport.ImTransport
import io.imkit.core.transport.OkHttpTransport
import kotlin.random.Random

/** Supplies the host-issued access token; called for every login attempt. */
public fun interface TokenProvider {
    /**
     * @param forceRefresh the previous attempt was refused with an invalid
     *   token, so a cached token should not be returned again.
     * @return the token, or null when the user is not signed in.
     */
    public suspend fun accessToken(forceRefresh: Boolean): String?
}

public data class ImClientConfig(
    /**
     * WebSocket endpoints (`wss://…/ws/im`), tried in order. After a success
     * the endpoint that worked is tried first on the next round.
     */
    val endpoints: List<String>,
    val tokenProvider: TokenProvider,
    /**
     * This device's stable name for multi-device login (spec 6.2): 1–64 of
     * letters, digits and `. _ : -`, the same on every login of this install.
     * Null logs in without one, so another connection of the same account
     * replaces this one. Servers without multi-device ignore it.
     */
    val deviceName: String? = null,
    val transport: ImTransport = OkHttpTransport(),
    /** Time allowed from opening a socket to the login reply. */
    val loginTimeoutMs: Long = 10_000L,
    val requestTimeoutMs: Long = 10_000L,
    /** A `pub` reply can be slow for large messages; a timeout here is not a failure (C2). */
    val publishTimeoutMs: Long = 20_000L,
    /** First reconnect delay; doubles per failed round up to [reconnectMaxDelayMs]. */
    val reconnectBaseDelayMs: Long = 3_000L,
    val reconnectMaxDelayMs: Long = 30_000L,
    /** Consecutive logins refused with an invalid token before giving up. */
    val maxAuthFailures: Int = 3,
    val random: Random = Random.Default,
) {
    init {
        require(endpoints.isNotEmpty()) { "no IM endpoint" }
        deviceName?.let { require(DEVICE.matches(it)) { "invalid device name" } }
    }

    private companion object {
        val DEVICE = Regex("^[A-Za-z0-9._:-]{1,64}$")
    }
}

/** A logged-in connection. */
public data class ImSession(
    val uid: String,
    /** `protocol` from the login reply; 0 for servers that do not send it (legacy). */
    val protocol: Int,
    val capabilities: Set<String>,
    val endpoint: String,
) {
    public fun has(capability: String): Boolean = capability in capabilities
}

public enum class StopReason {
    /** [ImClient.stop] was called. */
    STOPPED,

    /** Another login of the same device took the session (spec 6.2); reconnecting would steal it back. */
    REPLACED,

    /** The server kept refusing the token. */
    AUTH_REJECTED,

    /** The token provider has no token: nobody is signed in. */
    NO_CREDENTIALS,

    /** The account is online on the maximum number of devices. */
    TOO_MANY_DEVICES,

    /** The server refused [ImClientConfig.deviceName]. */
    INVALID_DEVICE,
}

public sealed interface ConnectionState {
    /** Not started. */
    public data object Idle : ConnectionState

    public data class Connecting(val endpoint: String, val attempt: Int) : ConnectionState

    /** Logged in. The client is only connected once the login reply arrived (C1). */
    public data class Connected(val session: ImSession) : ConnectionState

    public data class WaitingToReconnect(val attempt: Int, val delayMs: Long, val lastError: String?) : ConnectionState

    /** Will not reconnect by itself; [ImClient.start] starts again. */
    public data class Stopped(val reason: StopReason) : ConnectionState
}

public sealed class ImException(message: String) : Exception(message) {
    /** No logged-in connection; the request was not sent. */
    public class NotConnected : ImException("not connected")

    /**
     * No reply in time. [connectionAlive] tells whether the socket that carried
     * the request was still up: then the server most likely got the request.
     */
    public class Timeout(public val connectionAlive: Boolean) : ImException("no reply in time")

    /** The connection closed after the request was sent; its outcome is unknown. */
    public class ConnectionLost : ImException("connection lost before the reply")

    /** A reply that does not make sense for the request (wrong topic, missing fields). */
    public class BadReply(detail: String) : ImException("bad reply: $detail")

    /** The server answered with an error code. */
    public class Refused(public val reply: io.imkit.core.protocol.Reply) :
        ImException("refused: ${reply.code} ${reply.reason ?: ""}")
}

/** Everything the server pushes, parsed but not interpreted (spec 3.4, appendix B). */
public sealed interface ImEvent {
    public data class Connected(val session: ImSession) : ImEvent

    /** The logged-in connection ended; [willReconnect] is false once the client stopped. */
    public data class Disconnected(val willReconnect: Boolean) : ImEvent

    public data class Message(val message: WireMessage) : ImEvent

    public data class Recalled(val topic: String, val seq: Long, val from: String) : ImEvent

    /** [offlineId] is set when the server replays it after login; acknowledge it then (spec 11.3). */
    public data class TopicCleared(
        val topic: String,
        val from: String,
        val clearVersion: Long,
        val clearBeforeSeq: Long,
        val offlineId: String?,
    ) : ImEvent

    public data class Presence(val uid: String, val online: Boolean, val rev: Long) : ImEvent

    public data class Typing(val topic: String, val from: String) : ImEvent

    public data class Read(val topic: String, val from: String, val seq: Long) : ImEvent

    /** A hint to refresh the group; [eventId] is set on replays and must be acknowledged. */
    public data class GroupSync(val topic: String, val name: String?, val from: String?, val eventId: Long?) : ImEvent

    /** "I am no longer in this group": kicked or disbanded (spec 10.4). */
    public data class GroupRemoved(val topic: String, val from: String?, val eventId: Long?) : ImEvent

    public data class ContactSync(val what: String, val peerUid: String) : ImEvent

    /** `custom_update` (and the older `red_packet_update` as `what = "red_packet"`). */
    public data class Custom(val topic: String?, val what: String, val extra: JsonValue) : ImEvent

    /**
     * Pushes of the optional modules (group join requests, E2EE, calls) and
     * anything unknown. [eventId] is set on replayed group join events.
     */
    public data class Module(val frame: JsonObject, val eventId: Long?) : ImEvent
}
