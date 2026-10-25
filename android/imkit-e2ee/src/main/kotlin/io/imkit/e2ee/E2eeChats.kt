// SPDX-License-Identifier: AGPL-3.0-only
package io.imkit.e2ee

import android.util.Log
import io.imkit.core.ImIds
import io.imkit.core.client.ImClient
import io.imkit.core.client.ImEvent
import io.imkit.storage.room.KeyWrapper
import io.imkit.storage.room.WrappedKey
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import java.security.SecureRandom
import java.util.Base64
import java.util.concurrent.ConcurrentHashMap
import javax.crypto.Cipher
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec
import javax.crypto.spec.SecretKeySpec

/** An encrypted conversation on this device. */
public data class E2eeThread(
    val topic: String,
    /** The peer of a direct chat; null for a group. */
    val peer: String?,
    val updatedAt: Long,
    /** The last message's text; empty after a clear or for one that could not be decrypted. */
    val preview: String,
) {
    val isGroup: Boolean get() = peer == null
}

/** A message of an encrypted conversation. Kept on this device only. */
public data class E2eeMessage(
    val id: Long,
    val topic: String,
    val from: String,
    val text: String,
    val ts: Long,
    val status: Status,
    val mine: Boolean,
) {
    public enum class Status {
        SENT,

        /** Mine, did not go out; [E2eeChats.resend] sends the same ciphertext. */
        FAILED,
        RECEIVED,

        /** Arrived but could not be decrypted; [text] is empty. */
        UNDECRYPTABLE,
    }
}

/** What the app may want to show or notify about. None of them carries message text. */
public sealed interface E2eeNotice {
    public data class Incoming(val topic: String, val from: String, val isGroup: Boolean) : E2eeNotice

    /** The peer ended the encrypted session; this device's copy of the chat is gone. */
    public data class Ended(val topic: String, val peer: String) : E2eeNotice

    /** The peer, or a group manager, cleared the messages; this device cleared its copy. */
    public data class Cleared(val topic: String, val from: String) : E2eeNotice

    /** The group's owner turned encryption on. */
    public data class GroupEncryptionOn(val topic: String, val by: String) : E2eeNotice
}

/** A group message as sent: [withoutKey] are members who could not get my key this time and cannot read it. */
public data class GroupSent(val message: E2eeMessage, val withoutKey: Set<String>)

/**
 * Encrypted conversations, direct and group, next to the plain ones and
 * separate from them: their messages never go through the plain message
 * pipeline or the server's history.
 *
 * - [start] listens for envelopes and publishes this account's keys on every
 *   login, so anyone may start a session with it at any time.
 * - A direct chat's topic is the canonical one ([topicFor]); envelopes are
 *   filed by sender, whatever label they came with (F43).
 * - Groups use sender keys. Mine goes to each member over our direct
 *   session; a rotation is done whenever the server says the group is
 *   encrypted (including on members leaving), as section 13.3 requires.
 * - Text is stored on this device only, sealed per conversation with a data
 *   key wrapped by [keyWrapper] when there is one; a clear deletes the data
 *   key, so leftovers on disk cannot be read.
 *
 * Every failure comes back as an [E2eeFailure] class (red line C10).
 */
public class E2eeChats(
    public val client: ImClient,
    public val me: String,
    database: E2eeDatabase,
    private val scope: CoroutineScope,
    private val keyWrapper: KeyWrapper? = null,
    private val config: E2eeConfig = E2eeConfig(),
    private val io: CoroutineDispatcher = Dispatchers.IO,
) {
    public val signaling: E2eeSignaling = E2eeSignaling(client)
    public val crypto: E2eeCrypto = E2eeCrypto(me, database, signaling, config, io)
    private val db = database
    private val dao = database.room.chat()

    private val _threads = MutableStateFlow<List<E2eeThread>>(emptyList())
    public val threads: StateFlow<List<E2eeThread>> = _threads.asStateFlow()
    private val flows = ConcurrentHashMap<String, MutableStateFlow<List<E2eeMessage>>>()
    private val _notices = MutableSharedFlow<E2eeNotice>(extraBufferCapacity = 64)
    public val notices: SharedFlow<E2eeNotice> = _notices.asSharedFlow()

    /** Group messages that came before their sender's key, by sender. */
    private val waiting = ConcurrentHashMap<String, MutableList<E2eeEvent.Deliver>>()
    private val keyLock = Mutex()
    /**
     * Per group: handing out my key and encrypting with it must not be split
     * by a rotation, or members get one key and the message uses the next.
     */
    private val groupLocks = ConcurrentHashMap<String, Mutex>()
    private val random = SecureRandom()
    private var job: Job? = null

    public fun topicFor(peer: String): String = ImIds.p2pTopic(me, peer)

    /** Starts listening; subscribed by the time it returns, so call it before the client connects. */
    public fun start() {
        if (job != null) return
        job = scope.launch(start = CoroutineStart.UNDISPATCHED) {
            // Subscribed before anything else runs: queued envelopes arrive right after login.
            launch(start = CoroutineStart.UNDISPATCHED) {
                signaling.events.collect { e ->
                    try {
                        handle(e)
                    } catch (c: CancellationException) {
                        throw c
                    } catch (t: Throwable) {
                        Log.w(TAG, "could not handle $e", t)
                    }
                }
            }
            launch(start = CoroutineStart.UNDISPATCHED) {
                client.events.collect { if (it is ImEvent.Connected) publishQuietly() }
            }
            if (client.session != null) publishQuietly()
            refreshThreads()
        }
    }

    public fun stop() {
        job?.cancel()
        job = null
    }

    private suspend fun publishQuietly() {
        val r = crypto.publishKeys()
        if (r is E2eeResult.Failed) Log.w(TAG, "keys not published: ${r.failure} ${r.detail}")
    }

    /** The messages of [topic], loaded on first use. */
    public fun messages(topic: String): StateFlow<List<E2eeMessage>> {
        val known = flows[topic]
        if (known != null) return known
        val flow = flows.getOrPut(topic) { MutableStateFlow(emptyList()) }
        scope.launch { reload(topic) }
        return flow
    }

    // ------------------------------------------------------------------ direct

    /** Starts (or reuses) the session with [peer]; their keys must be published. Returns the topic. */
    public suspend fun startDirect(peer: String): E2eeResult<String> = crypto.ensureSession(peer).then {
        val topic = topicFor(peer)
        ensureThread(topic, peer)
        refreshThreads()
        E2eeResult.Ok(topic)
    }

    /**
     * Encrypts and sends [text] to [peer]. When encrypting fails nothing is
     * kept; when only the sending fails the message is kept as
     * [E2eeMessage.Status.FAILED] for [resend], and the failure says why.
     */
    public suspend fun sendDirect(peer: String, text: String): E2eeResult<E2eeMessage> =
        crypto.encrypt(peer, text.toByteArray(Charsets.UTF_8)).then { env ->
            val topic = topicFor(peer)
            ensureThread(topic, peer)
            val sent = signaling.send(topic, KIND_MSG, env.content, env.type, peer)
            val message = store(topic, me, text, config.clock(), if (sent is E2eeResult.Ok) STATUS_SENT else STATUS_FAILED, env.takeIf { sent !is E2eeResult.Ok })
            when (sent) {
                is E2eeResult.Ok -> E2eeResult.Ok(message)
                is E2eeResult.Failed -> sent
            }
        }

    /**
     * Sends a failed message again, as the same ciphertext: if the first try
     * did arrive after all, the receiver drops the copy. In a group,
     * [members] get my key first if they lack it.
     */
    public suspend fun resend(messageId: Long, members: Collection<String> = emptyList()): E2eeResult<E2eeMessage> {
        val row = withContext(io) { dao.message(messageId) } ?: return E2eeResult.Failed(E2eeFailure.SERVER, "no message $messageId")
        val env = row.envelope ?: return E2eeResult.Ok(toMessage(row, row.text))
        val group = ImIds.isGroupTopic(row.topic)
        val sent = if (group) {
            groupLock(row.topic).withLock {
                distributeKey(row.topic, members)
                signaling.send(row.topic, KIND_MSG, env, row.envelopeType)
            }
        } else {
            val peer = withContext(io) { dao.thread(row.topic)?.peer } ?: return E2eeResult.Failed(E2eeFailure.SERVER, "no thread ${row.topic}")
            signaling.send(row.topic, KIND_MSG, env, row.envelopeType, peer)
        }
        if (sent is E2eeResult.Failed) return sent
        withContext(io) { dao.setStatus(messageId, STATUS_SENT, null, null) }
        reload(row.topic)
        return E2eeResult.Ok(flows[row.topic]?.value?.firstOrNull { it.id == messageId } ?: toMessage(row, ""))
    }

    /**
     * Clears this chat's messages here and asks [peer] to clear theirs; the
     * session stays. The local clear always happens; the result is whether
     * the peer was told (only if online: the notice is not queued).
     */
    public suspend fun clearDirect(peer: String): E2eeResult<Unit> {
        val topic = topicFor(peer)
        clearLocal(topic)
        return signaling.clear(topic, peer)
    }

    /**
     * Ends the session with [peer] and deletes this device's copy of the
     * chat; the next message starts a new session. The local part always
     * happens; the result is whether the peer was told.
     */
    public suspend fun endDirect(peer: String): E2eeResult<Unit> {
        val topic = topicFor(peer)
        val told = signaling.terminate(topic, peer)
        crypto.endSession(peer)
        deleteLocal(topic)
        return told
    }

    /** The safety number with [peer], to compare in person; null before any session. */
    public suspend fun safetyNumber(peer: String): String? = crypto.safetyNumber(peer)

    // ------------------------------------------------------------------ groups

    /** Turns encryption on for [topic]; owner only, and it cannot be turned off. */
    public suspend fun enableGroup(topic: String): E2eeResult<Unit> = signaling.setGroupEncrypted(topic)

    /**
     * Encrypts and sends [text] to the group. [members] are the group's
     * members (mine included or not): those without my current key get it
     * first. The server delivers to its own member list.
     */
    public suspend fun sendGroup(topic: String, members: Collection<String>, text: String): E2eeResult<GroupSent> = groupLock(topic).withLock {
        val withoutKey = distributeKey(topic, members)
        crypto.encryptGroup(topic, text.toByteArray(Charsets.UTF_8)).then { env ->
            ensureThread(topic, null)
            val sent = signaling.send(topic, KIND_MSG, env.content, env.type)
            val message = store(topic, me, text, config.clock(), if (sent is E2eeResult.Ok) STATUS_SENT else STATUS_FAILED, env.takeIf { sent !is E2eeResult.Ok })
            when (sent) {
                is E2eeResult.Ok -> E2eeResult.Ok(GroupSent(message, withoutKey))
                is E2eeResult.Failed -> sent
            }
        }
    }

    private fun groupLock(topic: String) = groupLocks.getOrPut(topic) { Mutex() }

    /** Gives my current key to every member who lacks it; returns those it could not reach. Under [groupLock]. */
    private suspend fun distributeKey(topic: String, members: Collection<String>): Set<String> {
        crypto.groupSenderKey(topic)
        val missed = mutableSetOf<String>()
        for (m in members.toSet() - me) {
            when (val k = crypto.senderKeyFor(topic, m)) {
                is E2eeResult.Failed -> missed += m
                is E2eeResult.Ok -> {
                    val env = k.value ?: continue
                    // Over the direct channel: in a group topic the server would hand one member's envelope to all.
                    val sent = signaling.send(topicFor(m), KIND_SENDER_KEY, env.content, env.type, m)
                    if (sent is E2eeResult.Ok) crypto.markSenderKeySent(topic, m) else missed += m
                }
            }
        }
        return missed
    }

    /**
     * Clears the group's messages here and, for a manager, for every online
     * member. The local clear always happens; [E2eeFailure.NOT_ALLOWED] means
     * only this device was cleared.
     */
    public suspend fun clearGroup(topic: String): E2eeResult<Unit> {
        clearLocal(topic)
        return signaling.groupClear(topic)
    }

    // ------------------------------------------------------------------ incoming

    private suspend fun handle(e: E2eeEvent) {
        when (e) {
            is E2eeEvent.Deliver -> when (e.kind) {
                KIND_SENDER_KEY -> {
                    // A key wrapped for someone else (an old client fanning it out in the group) fails here; that is expected.
                    if (crypto.applySenderKey(e.from, e.msgType, e.content) is Decrypted.Ok) {
                        waiting.remove(e.from)?.forEach { receiveGroup(it) }
                    }
                }
                KIND_MSG -> if (ImIds.isGroupTopic(e.topic)) receiveGroup(e) else receiveDirect(e)
                else -> Log.w(TAG, "unknown envelope kind ${e.kind}")
            }
            is E2eeEvent.Cleared -> {
                val topic = if (ImIds.isGroupTopic(e.topic)) e.topic else topicFor(e.from)
                clearLocal(topic)
                _notices.tryEmit(E2eeNotice.Cleared(topic, e.from))
            }
            is E2eeEvent.Terminated -> {
                val topic = topicFor(e.from)
                crypto.endSession(e.from)
                deleteLocal(topic)
                _notices.tryEmit(E2eeNotice.Ended(topic, e.from))
            }
            is E2eeEvent.GroupCleared -> {
                clearLocal(e.topic)
                _notices.tryEmit(E2eeNotice.Cleared(e.topic, e.from))
            }
            is E2eeEvent.GroupEncrypted -> {
                // Section 13.3: on any of these, make a new sender key; it goes out with my next message.
                groupLock(e.topic).withLock { crypto.groupSenderKey(e.topic, rotate = true) }
                if (e.reason == "owner_enabled") _notices.tryEmit(E2eeNotice.GroupEncryptionOn(e.topic, e.from))
            }
        }
    }

    private suspend fun receiveDirect(e: E2eeEvent.Deliver) {
        val topic = topicFor(e.from)
        when (val d = crypto.decrypt(e.from, e.msgType, e.content)) {
            is Decrypted.Ok -> received(topic, e.from, String(d.plaintext, Charsets.UTF_8), e.ts, STATUS_RECEIVED, peer = e.from)
            is Decrypted.Failed -> {
                Log.w(TAG, "undecryptable: ${d.detail}")
                received(topic, e.from, "", e.ts, STATUS_UNDECRYPTABLE, peer = e.from)
            }
            Decrypted.Duplicate, Decrypted.NoSenderKey -> Unit
        }
    }

    private suspend fun receiveGroup(e: E2eeEvent.Deliver) {
        when (val d = crypto.decryptGroup(e.from, e.content)) {
            is Decrypted.Ok -> received(e.topic, e.from, String(d.plaintext, Charsets.UTF_8), e.ts, STATUS_RECEIVED, peer = null)
            Decrypted.NoSenderKey -> waiting.getOrPut(e.from) { mutableListOf() }.let { list ->
                synchronized(list) { if (list.size < MAX_WAITING) list += e }
            }
            is Decrypted.Failed -> {
                Log.w(TAG, "undecryptable: ${d.detail}")
                received(e.topic, e.from, "", e.ts, STATUS_UNDECRYPTABLE, peer = null)
            }
            Decrypted.Duplicate -> Unit
        }
    }

    private suspend fun received(topic: String, from: String, text: String, ts: Long, status: String, peer: String?) {
        ensureThread(topic, peer)
        store(topic, from, text, if (ts > 0) ts else config.clock(), status, null)
        _notices.tryEmit(E2eeNotice.Incoming(topic, from, peer == null))
    }

    // ------------------------------------------------------------------ local store

    private suspend fun ensureThread(topic: String, peer: String?) = withContext(io) {
        if (dao.thread(topic) == null) dao.putThread(ThreadRow(topic, peer, config.clock(), ""))
    }

    private suspend fun store(topic: String, from: String, text: String, ts: Long, status: String, env: Envelope?): E2eeMessage {
        val row = withContext(io) {
            val sealed = seal(topic, text)
            val id = dao.insertMessage(MessageRow(0, topic, from, sealed, ts, status, env?.type, env?.content))
            val thread = dao.thread(topic)
            // A message that could not be decrypted has no text: the preview stays.
            if (thread != null) dao.putThread(ThreadRow(topic, thread.peer, maxOf(thread.updatedAt, ts), if (text.isEmpty()) thread.preview else sealed))
            MessageRow(id, topic, from, sealed, ts, status, env?.type, env?.content)
        }
        reload(topic)
        refreshThreads()
        return toMessage(row, text)
    }

    private suspend fun clearLocal(topic: String) {
        withContext(io) {
            dao.deleteMessages(topic)
            dao.deleteContentKey(topic)
            dao.thread(topic)?.let { dao.putThread(ThreadRow(topic, it.peer, config.clock(), "")) }
            db.checkpoint()
        }
        waiting.values.forEach { list -> synchronized(list) { list.removeAll { it.topic == topic } } }
        flows[topic]?.value = emptyList()
        refreshThreads()
    }

    private suspend fun deleteLocal(topic: String) {
        withContext(io) {
            dao.deleteMessages(topic)
            dao.deleteContentKey(topic)
            dao.deleteThread(topic)
            db.checkpoint()
        }
        flows[topic]?.value = emptyList()
        refreshThreads()
    }

    private suspend fun reload(topic: String) {
        val flow = flows[topic] ?: return
        flow.value = withContext(io) { dao.messages(topic).map { toMessage(it, open(topic, it.text)) } }
    }

    private suspend fun refreshThreads() {
        _threads.value = withContext(io) {
            dao.threads().map { t -> E2eeThread(t.topic, t.peer, t.updatedAt, if (t.preview.isEmpty()) "" else open(t.topic, t.preview)) }
        }
    }

    private fun toMessage(row: MessageRow, text: String) = E2eeMessage(
        id = row.id,
        topic = row.topic,
        from = row.from,
        text = text,
        ts = row.ts,
        status = when (row.status) {
            STATUS_SENT -> E2eeMessage.Status.SENT
            STATUS_FAILED -> E2eeMessage.Status.FAILED
            STATUS_UNDECRYPTABLE -> E2eeMessage.Status.UNDECRYPTABLE
            else -> E2eeMessage.Status.RECEIVED
        },
        mine = row.from == me,
    )

    // ------------------------------------------------------------------ sealing (crypto-shredding)

    private suspend fun dataKey(topic: String, create: Boolean): SecretKey? = keyLock.withLock {
        val wrapper = keyWrapper ?: return@withLock null
        val row = dao.contentKey(topic)
        val raw = when {
            row != null -> wrapper.unwrap(WrappedKey(row.iv, row.ciphertext))
            create -> ByteArray(32).also { random.nextBytes(it) }.also { k ->
                val w = wrapper.wrap(k)
                dao.putContentKey(ContentKeyRow(topic, w.iv, w.ciphertext))
            }
            else -> return@withLock null
        }
        SecretKeySpec(raw, "AES")
    }

    private suspend fun seal(topic: String, plain: String): String {
        if (plain.isEmpty()) return plain
        val key = dataKey(topic, create = true) ?: return plain
        val cipher = Cipher.getInstance(AES_GCM)
        cipher.init(Cipher.ENCRYPT_MODE, key)
        val b64 = Base64.getEncoder()
        return SEALED + b64.encodeToString(cipher.iv) + ":" + b64.encodeToString(cipher.doFinal(plain.toByteArray(Charsets.UTF_8)))
    }

    /** The text, or empty when its key is gone (shredded) or it does not open. */
    private suspend fun open(topic: String, stored: String): String {
        if (!stored.startsWith(SEALED)) return stored
        val key = dataKey(topic, create = false) ?: return ""
        val parts = stored.removePrefix(SEALED).split(':')
        if (parts.size != 2) return ""
        return try {
            val b64 = Base64.getDecoder()
            val cipher = Cipher.getInstance(AES_GCM)
            cipher.init(Cipher.DECRYPT_MODE, key, GCMParameterSpec(128, b64.decode(parts[0])))
            String(cipher.doFinal(b64.decode(parts[1])), Charsets.UTF_8)
        } catch (e: Exception) {
            ""
        }
    }

    private companion object {
        const val TAG = "E2eeChats"
        const val KIND_MSG = "msg"
        const val KIND_SENDER_KEY = "sender_key"
        const val STATUS_SENT = "sent"
        const val STATUS_FAILED = "failed"
        const val STATUS_RECEIVED = "received"
        const val STATUS_UNDECRYPTABLE = "undecryptable"
        const val MAX_WAITING = 50
        const val AES_GCM = "AES/GCM/NoPadding"
        const val SEALED = "v1:"
    }
}
