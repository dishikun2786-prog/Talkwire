// SPDX-License-Identifier: AGPL-3.0-only
package io.imkit.e2ee

import org.signal.libsignal.protocol.IdentityKey
import org.signal.libsignal.protocol.IdentityKeyPair
import org.signal.libsignal.protocol.InvalidKeyIdException
import org.signal.libsignal.protocol.NoSessionException
import org.signal.libsignal.protocol.SignalProtocolAddress
import org.signal.libsignal.protocol.groups.state.SenderKeyRecord
import org.signal.libsignal.protocol.state.IdentityKeyStore
import org.signal.libsignal.protocol.state.KyberPreKeyRecord
import org.signal.libsignal.protocol.state.PreKeyRecord
import org.signal.libsignal.protocol.state.SessionRecord
import org.signal.libsignal.protocol.state.SignalProtocolStore
import org.signal.libsignal.protocol.state.SignedPreKeyRecord
import java.util.UUID

/**
 * libsignal's store on the E2EE database. Every method is synchronous: native
 * code calls them in the middle of an encrypt or decrypt, so [E2eeCrypto]
 * runs those off the main thread. "Not found" is null or false, as libsignal
 * expects, except where its contract says to throw.
 */
internal class RoomSignalStore(private val dao: SignalDao, private val clock: () -> Long) : SignalProtocolStore {

    override fun getIdentityKeyPair(): IdentityKeyPair =
        IdentityKeyPair(checkNotNull(dao.identity()) { "no identity yet" }.keyPair)

    override fun getLocalRegistrationId(): Int = checkNotNull(dao.identity()) { "no identity yet" }.registrationId

    override fun saveIdentity(address: SignalProtocolAddress, identityKey: IdentityKey): IdentityKeyStore.IdentityChange {
        val before = dao.remoteIdentity(address.name, address.deviceId)
        dao.putRemoteIdentity(RemoteIdentityRow(address.name, address.deviceId, identityKey.serialize(), clock()))
        val replaced = before != null && !before.contentEquals(identityKey.serialize())
        return if (replaced) IdentityKeyStore.IdentityChange.REPLACED_EXISTING else IdentityKeyStore.IdentityChange.NEW_OR_UNCHANGED
    }

    /**
     * Trust on first use, and after: a changed identity key is accepted (and
     * changes the safety number) rather than blocked. Encryption turns on
     * without a confirmation step, so there is no one to ask; comparing safety
     * numbers is how users check.
     */
    override fun isTrustedIdentity(address: SignalProtocolAddress, identityKey: IdentityKey, direction: IdentityKeyStore.Direction): Boolean = true

    override fun getIdentity(address: SignalProtocolAddress): IdentityKey? =
        dao.remoteIdentity(address.name, address.deviceId)?.let { IdentityKey(it) }

    override fun loadPreKey(preKeyId: Int): PreKeyRecord =
        PreKeyRecord(dao.preKey(preKeyId) ?: throw InvalidKeyIdException("no prekey $preKeyId"))

    override fun storePreKey(preKeyId: Int, record: PreKeyRecord) = dao.putPreKey(PreKeyRow(preKeyId, record.serialize()))

    override fun containsPreKey(preKeyId: Int): Boolean = dao.preKey(preKeyId) != null

    override fun removePreKey(preKeyId: Int) = dao.deletePreKey(preKeyId)

    override fun loadSignedPreKey(signedPreKeyId: Int): SignedPreKeyRecord =
        SignedPreKeyRecord(dao.signedPreKey(signedPreKeyId) ?: throw InvalidKeyIdException("no signed prekey $signedPreKeyId"))

    override fun loadSignedPreKeys(): MutableList<SignedPreKeyRecord> = dao.signedPreKeys().map { SignedPreKeyRecord(it) }.toMutableList()

    override fun storeSignedPreKey(signedPreKeyId: Int, record: SignedPreKeyRecord) =
        dao.putSignedPreKey(SignedPreKeyRow(signedPreKeyId, record.serialize()))

    override fun containsSignedPreKey(signedPreKeyId: Int): Boolean = dao.signedPreKey(signedPreKeyId) != null

    override fun removeSignedPreKey(signedPreKeyId: Int) = dao.deleteSignedPreKey(signedPreKeyId)

    override fun loadKyberPreKey(kyberPreKeyId: Int): KyberPreKeyRecord =
        KyberPreKeyRecord(dao.kyberPreKey(kyberPreKeyId) ?: throw InvalidKeyIdException("no kyber prekey $kyberPreKeyId"))

    override fun loadKyberPreKeys(): MutableList<KyberPreKeyRecord> = dao.kyberPreKeys().map { KyberPreKeyRecord(it) }.toMutableList()

    override fun storeKyberPreKey(kyberPreKeyId: Int, record: KyberPreKeyRecord) =
        dao.putKyberPreKey(KyberPreKeyRow(kyberPreKeyId, record.serialize()))

    override fun containsKyberPreKey(kyberPreKeyId: Int): Boolean = dao.kyberPreKey(kyberPreKeyId) != null

    /** The Kyber prekey is a last-resort key, published with every bundle: using it must not delete it. */
    override fun markKyberPreKeyUsed(kyberPreKeyId: Int) = Unit

    override fun loadSession(address: SignalProtocolAddress): SessionRecord =
        dao.session(address.name, address.deviceId)?.let { SessionRecord(it) } ?: SessionRecord()

    override fun loadExistingSessions(addresses: MutableList<SignalProtocolAddress>): MutableList<SessionRecord> =
        addresses.map { a ->
            SessionRecord(dao.session(a.name, a.deviceId) ?: throw NoSessionException("no session with ${a.name}.${a.deviceId}"))
        }.toMutableList()

    override fun getSubDeviceSessions(name: String): MutableList<Int> = dao.sessionDevices(name).filter { it != 1 }.toMutableList()

    override fun storeSession(address: SignalProtocolAddress, record: SessionRecord) =
        dao.putSession(SessionRow(address.name, address.deviceId, record.serialize()))

    override fun containsSession(address: SignalProtocolAddress): Boolean = dao.session(address.name, address.deviceId) != null

    override fun deleteSession(address: SignalProtocolAddress) = dao.deleteSession(address.name, address.deviceId)

    override fun deleteAllSessions(name: String) = dao.deleteSessions(name)

    override fun storeSenderKey(sender: SignalProtocolAddress, distributionId: UUID, record: SenderKeyRecord) =
        dao.putSenderKey(SenderKeyRow(sender.name, sender.deviceId, distributionId.toString(), record.serialize()))

    override fun loadSenderKey(sender: SignalProtocolAddress, distributionId: UUID): SenderKeyRecord? =
        dao.senderKey(sender.name, sender.deviceId, distributionId.toString())?.let { SenderKeyRecord(it) }
}
