// SPDX-License-Identifier: AGPL-3.0-only
package io.talkwire.e2ee

import android.content.Context
import androidx.test.core.app.ApplicationProvider
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.runBlocking
import org.junit.After
import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.signal.libsignal.protocol.message.CiphertextMessage
import java.util.concurrent.ConcurrentHashMap

/**
 * libsignal for real on both ends: the JVM build of the same pinned version
 * (0.73.0) plays every peer (plan 7.1: the test peer must run the same
 * version). Keys go through an in-memory stand-in for the server's key store.
 */
@RunWith(RobolectricTestRunner::class)
class E2eeCryptoTest {
    private val context = ApplicationProvider.getApplicationContext<Context>()
    private val directory = KeyDirectory()
    private val databases = mutableListOf<E2eeDatabase>()

    @After
    fun tearDown() = databases.forEach { it.close() }

    private fun device(uid: String, db: E2eeDatabase = E2eeDatabase.inMemory(context).also { databases += it }) =
        E2eeCrypto(uid, db, directory.serverFor(uid))

    private fun text(d: Decrypted): String = String((d as Decrypted.Ok).plaintext, Charsets.UTF_8)

    @Test
    fun R17_IdentityInitIsMutuallyExclusive() = runBlocking {
        val alice = device("uid_alice")
        val keys = (1..32).map { async(Dispatchers.Default) { alice.ensureIdentity() } }.awaitAll()
        assertEquals("one identity, not one per caller", 1, keys.map { it.serialize().toList() }.toSet().size)
        assertArrayEquals(keys.first().serialize(), alice.ensureIdentity().serialize())
    }

    @Test
    fun oneTimeKeysAreToppedUpOnlyWhenRunningLow() = runBlocking {
        val alice = device("uid_alice")
        assertEquals(E2eeResult.Ok(Unit), alice.publishKeys())
        assertEquals(20, directory.left("uid_alice"))
        alice.publishKeys() // plenty left: none added
        assertEquals(20, directory.left("uid_alice"))
        repeat(16) { directory.take("uid_alice") }
        alice.publishKeys() // 4 left, under the low water mark
        assertEquals(24, directory.left("uid_alice"))
        assertEquals("ids never repeat", 24, directory.pool("uid_alice").map { it.keyId }.toSet().size)
    }

    @Test
    fun aDirectConversationBothWays() = runBlocking {
        val alice = device("uid_alice")
        val bob = device("uid_bob")
        alice.publishKeys()
        bob.publishKeys()

        val first = (alice.encrypt("uid_bob", "hello bob".toByteArray()) as E2eeResult.Ok).value
        assertEquals("the first message carries the session", CiphertextMessage.PREKEY_TYPE, first.type)
        assertEquals("hello bob", text(bob.decrypt("uid_alice", first.type, first.content)))
        assertEquals("one of bob's one-time keys was used", 19, directory.left("uid_bob"))

        val reply = (bob.encrypt("uid_alice", "hi alice".toByteArray()) as E2eeResult.Ok).value
        assertEquals(CiphertextMessage.WHISPER_TYPE, reply.type)
        assertEquals("hi alice", text(alice.decrypt("uid_bob", reply.type, reply.content)))

        // Out of order and a resend of what already arrived.
        val m1 = (alice.encrypt("uid_bob", "one".toByteArray()) as E2eeResult.Ok).value
        val m2 = (alice.encrypt("uid_bob", "two".toByteArray()) as E2eeResult.Ok).value
        assertEquals("two", text(bob.decrypt("uid_alice", m2.type, m2.content)))
        assertEquals("one", text(bob.decrypt("uid_alice", m1.type, m1.content)))
        assertTrue(bob.decrypt("uid_alice", m1.type, m1.content) is Decrypted.Duplicate)

        val safety = alice.safetyNumber("uid_bob")!!
        assertEquals(60, safety.length)
        assertTrue(safety.all { it.isDigit() })
        assertEquals("the same number on both devices", safety, bob.safetyNumber("uid_alice"))
    }

    @Test
    fun aSessionStartsWithoutAOneTimeKeyAndAfterItEnds() = runBlocking {
        val alice = device("uid_alice")
        val bob = device("uid_bob")
        bob.publishKeys()
        directory.drain("uid_bob")
        val first = (alice.encrypt("uid_bob", "no one-time key".toByteArray()) as E2eeResult.Ok).value
        assertEquals("no one-time key", text(bob.decrypt("uid_alice", first.type, first.content)))

        alice.endSession("uid_bob")
        assertFalse(alice.hasSession("uid_bob"))
        val again = (alice.encrypt("uid_bob", "a new session".toByteArray()) as E2eeResult.Ok).value
        assertEquals(CiphertextMessage.PREKEY_TYPE, again.type)
        assertEquals("a new session", text(bob.decrypt("uid_alice", again.type, again.content)))
    }

    @Test
    fun failuresComeInClasses() = runBlocking {
        val alice = device("uid_alice")
        assertEquals(E2eeResult.Failed(E2eeFailure.PEER_KEYS_UNAVAILABLE), alice.encrypt("uid_nobody", "x".toByteArray()))
        // Keys that do not verify: a signature over something else.
        val bob = device("uid_bob")
        bob.publishKeys()
        directory.bundles["uid_bob"] = directory.bundles.getValue("uid_bob").let { it.copy(signedPreKeySignature = it.kyberPreKeySignature) }
        val bad = alice.encrypt("uid_bob", "x".toByteArray()) as E2eeResult.Failed
        assertEquals(E2eeFailure.CRYPTO, bad.failure)
        assertTrue(alice.decrypt("uid_bob", 2, "bm90IGEgbWVzc2FnZQ") is Decrypted.Failed)
    }

    @Test
    fun aGroupThroughSenderKeysAndARotation() = runBlocking {
        val alice = device("uid_alice")
        val bob = device("uid_bob")
        val carol = device("uid_carol")
        listOf(alice, bob, carol).forEach { it.publishKeys() }
        val topic = "grp_secret"

        val first = alice.groupSenderKey(topic)
        for ((name, member) in listOf("uid_bob" to bob, "uid_carol" to carol)) {
            val key = (alice.senderKeyFor(topic, name) as E2eeResult.Ok).value!!
            assertTrue(member.applySenderKey("uid_alice", key.type, key.content) is Decrypted.Ok)
            alice.markSenderKeySent(topic, name)
        }
        assertNull("bob has it already", (alice.senderKeyFor(topic, "uid_bob") as E2eeResult.Ok).value)

        val m = (alice.encryptGroup(topic, "to all".toByteArray()) as E2eeResult.Ok).value
        assertEquals(CiphertextMessage.SENDERKEY_TYPE, m.type)
        assertEquals("to all", text(bob.decryptGroup("uid_alice", m.content)))
        assertEquals("to all", text(carol.decryptGroup("uid_alice", m.content)))
        assertTrue(bob.decryptGroup("uid_alice", m.content) is Decrypted.Duplicate)

        // Carol leaves: a new key, given to bob only.
        val second = alice.groupSenderKey(topic, rotate = true)
        assertNotEquals(first, second)
        val key = (alice.senderKeyFor(topic, "uid_bob") as E2eeResult.Ok).value!!
        bob.applySenderKey("uid_alice", key.type, key.content)
        val after = (alice.encryptGroup(topic, "without carol".toByteArray()) as E2eeResult.Ok).value
        assertEquals("without carol", text(bob.decryptGroup("uid_alice", after.content)))
        assertTrue("carol cannot read it", carol.decryptGroup("uid_alice", after.content) is Decrypted.NoSenderKey)
    }

    @Test
    fun anotherAccountOnTheDeviceStartsFromNothing() = runBlocking {
        val db = E2eeDatabase.inMemory(context).also { databases += it }
        val alice = device("uid_alice", db)
        val bob = device("uid_bob")
        bob.publishKeys()
        val aliceKey = alice.ensureIdentity()
        alice.encrypt("uid_bob", "x".toByteArray())
        assertTrue(alice.hasSession("uid_bob"))

        val mallory = device("uid_mallory", db)
        assertNotEquals(aliceKey, mallory.ensureIdentity())
        assertFalse("alice's sessions are gone", mallory.hasSession("uid_bob"))
    }

    /** What the server keeps: the latest bundle per account and its pool of one-time keys. */
    class KeyDirectory {
        val bundles = ConcurrentHashMap<String, WireBundle>()
        private val pools = ConcurrentHashMap<String, ArrayDeque<WireOneTimeKey>>()

        fun pool(uid: String): List<WireOneTimeKey> = synchronized(this) { pools[uid]?.toList() ?: emptyList() }

        fun left(uid: String) = pool(uid).size

        fun take(uid: String): WireOneTimeKey? = synchronized(this) { pools[uid]?.removeFirstOrNull() }

        fun drain(uid: String) = synchronized(this) { pools[uid]?.clear() }

        fun serverFor(uid: String): E2eeKeyServer = object : E2eeKeyServer {
            override suspend fun publish(bundle: WireBundle): E2eeResult<Unit> {
                synchronized(this@KeyDirectory) {
                    bundles[uid] = bundle.copy(oneTimePreKeys = emptyList())
                    pools.getOrPut(uid) { ArrayDeque() }.addAll(bundle.oneTimePreKeys)
                }
                return E2eeResult.Ok(Unit)
            }

            override suspend fun fetch(uid: String): E2eeResult<WireBundle> {
                val b = bundles[uid] ?: return E2eeResult.Failed(E2eeFailure.PEER_KEYS_UNAVAILABLE)
                return E2eeResult.Ok(b.copy(oneTimePreKeys = listOfNotNull(take(uid))))
            }

            override suspend fun oneTimeKeysLeft(): E2eeResult<Int> = E2eeResult.Ok(left(uid))
        }
    }
}
