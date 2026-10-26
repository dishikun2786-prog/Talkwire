// SPDX-License-Identifier: AGPL-3.0-only
package io.talkwire.e2ee

import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import org.signal.libsignal.protocol.DuplicateMessageException
import org.signal.libsignal.protocol.IdentityKey
import org.signal.libsignal.protocol.IdentityKeyPair
import org.signal.libsignal.protocol.NoSessionException
import org.signal.libsignal.protocol.SessionBuilder
import org.signal.libsignal.protocol.SessionCipher
import org.signal.libsignal.protocol.SignalProtocolAddress
import org.signal.libsignal.protocol.ecc.Curve
import org.signal.libsignal.protocol.ecc.ECPublicKey
import org.signal.libsignal.protocol.fingerprint.NumericFingerprintGenerator
import org.signal.libsignal.protocol.groups.GroupCipher
import org.signal.libsignal.protocol.groups.GroupSessionBuilder
import org.signal.libsignal.protocol.kem.KEMKeyPair
import org.signal.libsignal.protocol.kem.KEMKeyType
import org.signal.libsignal.protocol.kem.KEMPublicKey
import org.signal.libsignal.protocol.message.CiphertextMessage
import org.signal.libsignal.protocol.message.PreKeySignalMessage
import org.signal.libsignal.protocol.message.SenderKeyDistributionMessage
import org.signal.libsignal.protocol.message.SignalMessage
import org.signal.libsignal.protocol.state.KyberPreKeyRecord
import org.signal.libsignal.protocol.state.PreKeyBundle
import org.signal.libsignal.protocol.state.PreKeyRecord
import org.signal.libsignal.protocol.state.SignedPreKeyRecord
import java.security.SecureRandom
import java.util.Base64
import java.util.UUID
import java.util.concurrent.ConcurrentHashMap

/** Tunables of the E2EE module. */
public data class E2eeConfig(
    /** One-time prekeys published at a time. */
    val oneTimeKeyBatch: Int = 20,
    /** A new batch is published when the server has fewer than this many left. */
    val oneTimeKeyLowWater: Int = 5,
    val clock: () -> Long = System::currentTimeMillis,
)

/** A ciphertext as `e2e_send` carries it: libsignal's message type and the bytes in base64. */
public data class Envelope(val type: Int, val content: String)

/** What decrypting an envelope gave. */
public sealed interface Decrypted {
    public class Ok(public val plaintext: ByteArray) : Decrypted

    /** Already decrypted once: a resend of something that did arrive. Not an error. */
    public object Duplicate : Decrypted

    /** A group message from someone whose sender key has not arrived yet. */
    public object NoSenderKey : Decrypted

    /** Could not be decrypted; [detail] is for logs. */
    public class Failed(public val detail: String) : Decrypted
}

/**
 * libsignal on this device: the account's identity, its published keys,
 * sessions with peers (X3DH with PQXDH, then the double ratchet), and sender
 * keys for groups. Everything else in the module goes through here.
 *
 * Sessions and ciphers are not thread-safe, so every operation holds a lock
 * for what it touches: the peer for a direct session, the sender for a
 * group's incoming messages, the group for my own sender key. Callers may run
 * anything concurrently.
 *
 * One account per [E2eeDatabase]: opening it for another uid wipes it first.
 * One set of keys per account, device id 1 (F42): v1 has no multi-device
 * encryption, and the device that publishes last receives the envelopes.
 */
public class E2eeCrypto(
    public val me: String,
    database: E2eeDatabase,
    private val keys: E2eeKeyServer,
    private val config: E2eeConfig = E2eeConfig(),
    private val io: CoroutineDispatcher = Dispatchers.IO,
) {
    private val db = database
    private val dao = database.room.signal()
    private val store = RoomSignalStore(dao, config.clock)
    private val identityLock = Mutex()
    private val publishLock = Mutex()
    private val locks = ConcurrentHashMap<String, Mutex>()
    private val random = SecureRandom()

    private suspend fun <T> locked(key: String, block: suspend () -> T): T = locks.getOrPut(key) { Mutex() }.withLock { block() }

    private fun address(uid: String) = SignalProtocolAddress(uid, DEVICE_ID)

    /**
     * This account's identity key, made on first use. Concurrent first calls
     * make one key, not one each (red line R17): a second key overwriting a
     * published one would break every session built on the first, with no
     * way back.
     */
    public suspend fun ensureIdentity(): IdentityKey = withContext(io) {
        identityLock.withLock {
            val row = dao.identity()
            if (row != null && row.owner == me) return@withLock IdentityKeyPair(row.keyPair).publicKey
            // Another account's state on this device must not survive into this one.
            if (row != null) db.wipe()
            val pair = IdentityKeyPair.generate()
            dao.putIdentity(IdentityRow(0, me, pair.serialize(), 1 + random.nextInt(16380)))
            pair.publicKey
        }
    }

    /**
     * Publishes this account's keys: the identity, a signed prekey and a Kyber
     * prekey (made once, kept), and a batch of one-time prekeys when the
     * server has fewer than [E2eeConfig.oneTimeKeyLowWater] left or cannot
     * say. Safe to call on every login, which is when it should be called:
     * whoever cannot fetch these keys cannot start a session with me.
     */
    public suspend fun publishKeys(): E2eeResult<Unit> = publishLock.withLock { publishKeysLocked() }

    private suspend fun publishKeysLocked(): E2eeResult<Unit> {
        ensureIdentity()
        val left = keys.oneTimeKeysLeft()
        val bundle = withContext(io) {
            identityLock.withLock {
                val identity = IdentityKeyPair(checkNotNull(dao.identity()).keyPair)
                val registrationId = checkNotNull(dao.identity()).registrationId
                val now = config.clock()
                if (dao.signedPreKey(SIGNED_PRE_KEY_ID) == null) {
                    val kp = Curve.generateKeyPair()
                    val sig = identity.privateKey.calculateSignature(kp.publicKey.serialize())
                    store.storeSignedPreKey(SIGNED_PRE_KEY_ID, SignedPreKeyRecord(SIGNED_PRE_KEY_ID, now, kp, sig))
                }
                if (dao.kyberPreKey(KYBER_PRE_KEY_ID) == null) {
                    val kp = KEMKeyPair.generate(KEMKeyType.KYBER_1024)
                    val sig = identity.privateKey.calculateSignature(kp.publicKey.serialize())
                    store.storeKyberPreKey(KYBER_PRE_KEY_ID, KyberPreKeyRecord(KYBER_PRE_KEY_ID, now, kp, sig))
                }
                val enough = left is E2eeResult.Ok && left.value >= config.oneTimeKeyLowWater
                val fresh = if (enough) {
                    emptyList()
                } else {
                    val first = (dao.maxPreKeyId() ?: 0) + 1
                    (first until first + config.oneTimeKeyBatch.coerceIn(1, 200)).map { id ->
                        val kp = Curve.generateKeyPair()
                        store.storePreKey(id, PreKeyRecord(id, kp))
                        WireOneTimeKey(id, b64(kp.publicKey.serialize()))
                    }
                }
                val signed = store.loadSignedPreKey(SIGNED_PRE_KEY_ID)
                val kyber = store.loadKyberPreKey(KYBER_PRE_KEY_ID)
                WireBundle(
                    registrationId = registrationId,
                    identityKey = b64(identity.publicKey.serialize()),
                    signedPreKeyId = SIGNED_PRE_KEY_ID,
                    signedPreKey = b64(signed.keyPair.publicKey.serialize()),
                    signedPreKeySignature = b64(signed.signature),
                    kyberPreKeyId = KYBER_PRE_KEY_ID,
                    kyberPreKey = b64(kyber.keyPair.publicKey.serialize()),
                    kyberPreKeySignature = b64(kyber.signature),
                    oneTimePreKeys = fresh,
                )
            }
        }
        return keys.publish(bundle)
    }

    /** Whether a session with [peer] exists on this device. */
    public suspend fun hasSession(peer: String): Boolean = withContext(io) { dao.session(peer, DEVICE_ID) != null }

    /**
     * Makes sure there is a session with [peer], building one from their
     * published keys if not; they need not be online. Fails with
     * [E2eeFailure.PEER_KEYS_UNAVAILABLE] when there are no keys to fetch.
     */
    public suspend fun ensureSession(peer: String): E2eeResult<Unit> = locked(direct(peer)) { ensureSessionLocked(peer) }

    private suspend fun ensureSessionLocked(peer: String): E2eeResult<Unit> {
        ensureIdentity()
        if (hasSession(peer)) return E2eeResult.Ok(Unit)
        return keys.fetch(peer).then { remote ->
            withContext(io) {
                try {
                    val otpk = remote.oneTimePreKeys.firstOrNull()
                    val bundle = PreKeyBundle(
                        remote.registrationId,
                        remote.deviceId,
                        otpk?.keyId ?: PreKeyBundle.NULL_PRE_KEY_ID,
                        otpk?.let { ECPublicKey(unb64(it.preKey)) },
                        remote.signedPreKeyId,
                        ECPublicKey(unb64(remote.signedPreKey)),
                        unb64(remote.signedPreKeySignature),
                        IdentityKey(unb64(remote.identityKey)),
                        remote.kyberPreKeyId,
                        KEMPublicKey(unb64(remote.kyberPreKey)),
                        unb64(remote.kyberPreKeySignature),
                    )
                    SessionBuilder(store, address(peer)).process(bundle)
                    E2eeResult.Ok(Unit)
                } catch (e: CancellationException) {
                    throw e
                } catch (e: Exception) {
                    // A bundle that does not verify (bad signature, wrong length) or bad base64.
                    E2eeResult.Failed(E2eeFailure.CRYPTO, "session with $peer: $e")
                }
            }
        }
    }

    /** Encrypts for [peer], starting the session first if there is none. */
    public suspend fun encrypt(peer: String, plaintext: ByteArray): E2eeResult<Envelope> = locked(direct(peer)) {
        ensureSessionLocked(peer).then {
            withContext(io) {
                try {
                    val ct = SessionCipher(store, address(peer)).encrypt(plaintext)
                    E2eeResult.Ok(Envelope(ct.type, b64(ct.serialize())))
                } catch (e: Exception) {
                    E2eeResult.Failed(E2eeFailure.CRYPTO, "encrypt for $peer: $e")
                }
            }
        }
    }

    /** Decrypts what [peer] sent; type 3 (a prekey message) also completes my half of their new session. */
    public suspend fun decrypt(peer: String, type: Int?, content: String): Decrypted = locked(direct(peer)) {
        ensureIdentity()
        withContext(io) {
            try {
                val cipher = SessionCipher(store, address(peer))
                val bytes = unb64(content)
                Decrypted.Ok(if (type == CiphertextMessage.PREKEY_TYPE) cipher.decrypt(PreKeySignalMessage(bytes)) else cipher.decrypt(SignalMessage(bytes)))
            } catch (e: DuplicateMessageException) {
                Decrypted.Duplicate
            } catch (e: Exception) {
                Decrypted.Failed("from $peer: $e")
            }
        }
    }

    /** Forgets the session with [peer]; the next message starts a new one from their published keys. */
    public suspend fun endSession(peer: String): Unit = locked(direct(peer)) { withContext(io) { dao.deleteSessions(peer) } }

    /**
     * The safety number of the session with [peer]: 60 digits, the same on
     * both devices, from the two identity keys (libsignal's numeric
     * fingerprint, version 2, uids as stable identifiers). Null until a
     * session has seen their identity.
     */
    public suspend fun safetyNumber(peer: String): String? {
        val mine = ensureIdentity()
        return withContext(io) {
            val theirs = dao.remoteIdentity(peer, DEVICE_ID)?.let { IdentityKey(it) } ?: return@withContext null
            NumericFingerprintGenerator(FINGERPRINT_ITERATIONS)
                .createFor(FINGERPRINT_VERSION, me.toByteArray(), mine, peer.toByteArray(), theirs)
                .displayableFingerprint.displayText
        }
    }

    // ------------------------------------------------------------------ groups

    /**
     * My current sender key in [topic], made if there is none. [rotate]
     * replaces it (after a member leaves or is removed): from then on new
     * messages use the new key, which the one who left never gets.
     */
    public suspend fun groupSenderKey(topic: String, rotate: Boolean = false): UUID = locked(groupSend(topic)) {
        ensureIdentity()
        withContext(io) {
            if (rotate) {
                dao.deleteGroupSender(topic)
                dao.deleteGroupKeySent(topic)
            }
            dao.groupSender(topic)?.let { return@withContext UUID.fromString(it.distributionId) }
            val id = UUID.randomUUID()
            // create() stores the key's first state; its message is kept and reused for every member.
            val distribution = GroupSessionBuilder(store).create(address(me), id)
            dao.putGroupSender(GroupSenderRow(topic, id.toString(), distribution.serialize()))
            id
        }
    }

    /**
     * My sender key for [member], wrapped in our direct session; null when
     * they already have it. The bare distribution message is the key itself,
     * so it never leaves this device unwrapped. Call [markSenderKeySent] once
     * the envelope went out.
     */
    public suspend fun senderKeyFor(topic: String, member: String): E2eeResult<Envelope?> {
        val distribution = locked(groupSend(topic)) {
            withContext(io) {
                if (member in dao.groupKeySentTo(topic)) null else dao.groupSender(topic)?.distribution
            }
        } ?: return E2eeResult.Ok(null)
        return encrypt(member, b64(distribution).toByteArray(Charsets.UTF_8))
    }

    public suspend fun markSenderKeySent(topic: String, member: String): Unit = locked(groupSend(topic)) {
        withContext(io) { dao.putGroupKeySent(GroupKeySentRow(topic, member)) }
    }

    /** Takes in a sender key [sender] sent me, wrapped in our direct session. */
    public suspend fun applySenderKey(sender: String, type: Int?, content: String): Decrypted {
        val unwrapped = decrypt(sender, type, content)
        if (unwrapped !is Decrypted.Ok) return unwrapped
        return locked(groupReceive(sender)) {
            withContext(io) {
                try {
                    val message = SenderKeyDistributionMessage(unb64(String(unwrapped.plaintext, Charsets.UTF_8)))
                    GroupSessionBuilder(store).process(address(sender), message)
                    Decrypted.Ok(ByteArray(0))
                } catch (e: Exception) {
                    Decrypted.Failed("sender key from $sender: $e")
                }
            }
        }
    }

    /** Encrypts for everyone holding my sender key in [topic]; [groupSenderKey] must have made one. */
    public suspend fun encryptGroup(topic: String, plaintext: ByteArray): E2eeResult<Envelope> {
        val id = groupSenderKey(topic)
        return locked(groupCipher(id)) {
            withContext(io) {
                try {
                    val ct = GroupCipher(store, address(me)).encrypt(id, plaintext)
                    E2eeResult.Ok(Envelope(ct.type, b64(ct.serialize())))
                } catch (e: Exception) {
                    E2eeResult.Failed(E2eeFailure.CRYPTO, "group encrypt in $topic: $e")
                }
            }
        }
    }

    public suspend fun decryptGroup(sender: String, content: String): Decrypted = locked(groupReceive(sender)) {
        withContext(io) {
            try {
                Decrypted.Ok(GroupCipher(store, address(sender)).decrypt(unb64(content)))
            } catch (e: DuplicateMessageException) {
                Decrypted.Duplicate
            } catch (e: NoSessionException) {
                Decrypted.NoSenderKey
            } catch (e: Exception) {
                Decrypted.Failed("group message from $sender: $e")
            }
        }
    }

    private fun direct(peer: String) = "dm:$peer"

    private fun groupSend(topic: String) = "group-send:$topic"

    private fun groupCipher(id: UUID) = "group-cipher:$id"

    private fun groupReceive(sender: String) = "group-receive:$sender"

    internal companion object {
        const val DEVICE_ID = 1
        const val SIGNED_PRE_KEY_ID = 1
        const val KYBER_PRE_KEY_ID = 1
        const val FINGERPRINT_ITERATIONS = 5200
        const val FINGERPRINT_VERSION = 2

        fun b64(bytes: ByteArray): String = Base64.getEncoder().encodeToString(bytes)

        /** Padding optional: what other clients send varies. */
        fun unb64(s: String): ByteArray = Base64.getDecoder().decode(s)
    }
}
