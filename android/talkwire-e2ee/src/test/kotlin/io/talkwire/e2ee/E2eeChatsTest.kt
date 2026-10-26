// SPDX-License-Identifier: AGPL-3.0-only
package io.talkwire.e2ee

import android.content.Context
import androidx.test.core.app.ApplicationProvider
import io.talkwire.core.ImIds
import io.talkwire.core.client.ImClient
import io.talkwire.core.client.ImClientConfig
import io.talkwire.core.client.TokenProvider
import io.talkwire.core.testing.MiniServer
import io.talkwire.core.testing.eventually
import io.talkwire.storage.room.KeyWrapper
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import java.util.concurrent.CopyOnWriteArrayList
import kotlin.random.Random

/**
 * Encrypted conversations end to end: real libsignal on every device, the
 * in-memory server, and a stand-in for the server's E2EE relay that follows
 * section 13 (keys, one-time keys, envelopes, clear and terminate, groups).
 */
@RunWith(RobolectricTestRunner::class)
class E2eeChatsTest {
    private val context = ApplicationProvider.getApplicationContext<Context>()
    private val scope = CoroutineScope(Dispatchers.Default + SupervisorJob())
    private val server = MiniServer()
    private val relay = TestRelay(server)
    private val parties = CopyOnWriteArrayList<Party>()
    private val group = "grp_secretsecret"

    @After
    fun tearDown() {
        parties.forEach { it.client.stop(); it.chats.stop() }
        scope.cancel()
        parties.forEach { it.db.close() }
    }

    class Party(val chats: E2eeChats, val db: E2eeDatabase, val notices: CopyOnWriteArrayList<E2eeNotice>) {
        val client get() = chats.client
        val uid get() = chats.me
    }

    private fun party(user: String, wrapper: KeyWrapper? = TestWrapper()): Party = runBlocking {
        val client = ImClient(
            ImClientConfig(
                endpoints = listOf("ws://mini"),
                tokenProvider = TokenProvider { user },
                transport = server.transport,
                requestTimeoutMs = 1_500,
                reconnectBaseDelayMs = 20,
                reconnectMaxDelayMs = 40,
                random = Random(user.hashCode()),
            ),
            scope,
        )
        val db = E2eeDatabase.inMemory(context)
        val chats = E2eeChats(client, server.uidOf(user), db, scope, wrapper)
        val notices = CopyOnWriteArrayList<E2eeNotice>()
        scope.launch(start = CoroutineStart.UNDISPATCHED) { chats.notices.collect { notices += it } }
        chats.start()
        client.start()
        client.awaitConnected(5_000)
        eventually { relay.bundles.containsKey(server.uidOf(user)) }
        Party(chats, db, notices).also { parties += it }
    }

    private fun Party.texts(topic: String): List<String> = chats.messages(topic).value.map { it.text }

    @Test
    fun aDirectChatBothWays() = runBlocking {
        val alice = party("alice")
        val bob = party("bob")
        val topic = alice.chats.topicFor(bob.uid)
        assertEquals(ImIds.p2pTopic(alice.uid, bob.uid), topic)

        val sent = (alice.chats.sendDirect(bob.uid, "hello bob") as E2eeResult.Ok).value
        assertEquals(E2eeMessage.Status.SENT, sent.status)
        assertTrue(sent.mine)
        eventually { bob.texts(topic) == listOf("hello bob") }
        assertEquals(E2eeMessage.Status.RECEIVED, bob.chats.messages(topic).value.single().status)
        assertTrue(bob.notices.contains(E2eeNotice.Incoming(topic, alice.uid, false)))
        assertEquals(listOf(E2eeThread(topic, alice.uid, bob.chats.threads.value.single().updatedAt, "hello bob")), bob.chats.threads.value)

        bob.chats.sendDirect(alice.uid, "hi alice")
        eventually { alice.texts(topic) == listOf("hello bob", "hi alice") }
        assertEquals(alice.chats.safetyNumber(bob.uid), bob.chats.safetyNumber(alice.uid))
        assertTrue("nothing went through the plain pipeline", server.requests.none { it.string("action") == "pub" })
        assertTrue("the server saw only ciphertext", server.requests.none { it.toString().contains("hello bob") })
    }

    @Test
    fun theTextIsSealedOnDiskAndAClearShredsIt() = runBlocking {
        val alice = party("alice")
        val bob = party("bob")
        val topic = alice.chats.topicFor(bob.uid)
        alice.chats.sendDirect(bob.uid, "for your eyes only")
        eventually { bob.texts(topic).size == 1 }
        val rows = bob.db.room.chat().messages(topic)
        assertTrue("sealed, not plain", rows.single().text.startsWith("v1:"))
        assertFalse(rows.single().text.contains("eyes"))
        assertTrue(bob.db.room.chat().contentKey(topic) != null)

        assertEquals(E2eeResult.Ok(Unit), alice.chats.clearDirect(bob.uid))
        eventually { bob.texts(topic).isEmpty() }
        eventually { bob.notices.contains(E2eeNotice.Cleared(topic, alice.uid)) }
        assertEquals("the data key is gone", null, bob.db.room.chat().contentKey(topic))
        assertEquals(null, alice.db.room.chat().contentKey(topic))
        assertEquals("", bob.chats.threads.value.single().preview)

        // The session stays: no new session needed.
        alice.chats.sendDirect(bob.uid, "still here")
        eventually { bob.texts(topic) == listOf("still here") }
    }

    @Test
    fun endingTheChatRemovesItOnBothSides() = runBlocking {
        val alice = party("alice")
        val bob = party("bob")
        val topic = alice.chats.topicFor(bob.uid)
        alice.chats.sendDirect(bob.uid, "one")
        eventually { bob.texts(topic).size == 1 }

        assertEquals(E2eeResult.Ok(Unit), alice.chats.endDirect(bob.uid))
        eventually { bob.notices.contains(E2eeNotice.Ended(topic, alice.uid)) }
        assertTrue(alice.chats.threads.value.isEmpty())
        eventually { bob.chats.threads.value.isEmpty() && bob.texts(topic).isEmpty() }
        assertFalse(bob.chats.crypto.hasSession(alice.uid))

        // A new message starts a new session from bob's published keys.
        bob.chats.sendDirect(alice.uid, "again")
        eventually { alice.texts(topic) == listOf("again") }
    }

    @Test
    fun failuresComeInClassesAndAFailedMessageCanGoAgain() = runBlocking {
        val alice = party("alice")
        val bob = party("bob")
        val topic = alice.chats.topicFor(bob.uid)

        // A peer who never published: nothing is kept.
        val nobody = server.uidOf("nobody")
        assertEquals(E2eeResult.Failed(E2eeFailure.PEER_KEYS_UNAVAILABLE), alice.chats.sendDirect(nobody, "x"))
        assertTrue(alice.chats.threads.value.none { it.peer == nobody })

        // Refused by the server: kept as failed, sent again once allowed, received once.
        relay.refuseSends = true
        val refused = alice.chats.sendDirect(bob.uid, "let me through") as E2eeResult.Failed
        assertEquals(E2eeFailure.NOT_ALLOWED, refused.failure)
        eventually { alice.chats.messages(topic).value.size == 1 }
        val failed = alice.chats.messages(topic).value.single()
        assertEquals(E2eeMessage.Status.FAILED, failed.status)
        relay.refuseSends = false
        assertEquals(E2eeMessage.Status.SENT, (alice.chats.resend(failed.id) as E2eeResult.Ok).value.status)
        eventually { bob.texts(topic) == listOf("let me through") }

        // Offline: the only class worth retrying as is.
        alice.client.stop()
        val offline = alice.chats.sendDirect(bob.uid, "later") as E2eeResult.Failed
        assertEquals(E2eeFailure.OFFLINE, offline.failure)
        assertTrue(offline.failure.retryable)
        alice.client.start()
        alice.client.awaitConnected(5_000)
        alice.chats.resend(alice.chats.messages(topic).value.last().id)
        eventually { bob.texts(topic) == listOf("let me through", "later") }

        // A group the owner has not encrypted.
        server.groups[group] = MiniServer.Group("g", mutableMapOf(alice.uid to "owner", bob.uid to "member"))
        assertEquals(E2eeFailure.GROUP_NOT_ENCRYPTED, (alice.chats.sendGroup(group, listOf(bob.uid), "x") as E2eeResult.Failed).failure)
    }

    @Test
    fun aResendOfWhatDidArriveIsDropped() = runBlocking {
        val alice = party("alice")
        val bob = party("bob")
        val topic = alice.chats.topicFor(bob.uid)
        // Delivered, but the reply is lost: alice cannot know.
        server.dropReply = { it.string("action") == "e2e_send" }
        assertEquals(E2eeFailure.OFFLINE, (alice.chats.sendDirect(bob.uid, "once") as E2eeResult.Failed).failure)
        eventually { bob.texts(topic) == listOf("once") }
        server.dropReply = { false }
        eventually { alice.chats.messages(topic).value.size == 1 }
        alice.chats.resend(alice.chats.messages(topic).value.single().id)
        Thread.sleep(300)
        assertEquals("the copy is dropped", listOf("once"), bob.texts(topic))
    }

    @Test
    fun aMessageThatCannotBeDecryptedIsShownAsSuch() = runBlocking {
        val alice = party("alice")
        val bob = party("bob")
        val topic = alice.chats.topicFor(bob.uid)
        alice.chats.sendDirect(bob.uid, "real")
        eventually { bob.texts(topic).size == 1 }
        server.pushTo(bob.uid, e2eDeliver(topic, alice.uid, "msg", 2, "bm90IGEgbWVzc2FnZQ"))
        eventually { bob.chats.messages(topic).value.size == 2 }
        assertEquals(E2eeMessage.Status.UNDECRYPTABLE, bob.chats.messages(topic).value.last().status)
        assertEquals("real", bob.chats.threads.value.single().preview)
    }

    @Test
    fun aGroupWithSenderKeysAndARotation() = runBlocking {
        val alice = party("alice")
        val bob = party("bob")
        val carol = party("carol")
        server.groups[group] = MiniServer.Group("g", mutableMapOf(alice.uid to "owner", bob.uid to "member", carol.uid to "member"))
        val everyone = listOf(alice.uid, bob.uid, carol.uid)

        assertEquals(E2eeFailure.NOT_ALLOWED, (bob.chats.enableGroup(group) as E2eeResult.Failed).failure)
        assertEquals(E2eeResult.Ok(Unit), alice.chats.enableGroup(group))
        eventually { bob.notices.contains(E2eeNotice.GroupEncryptionOn(group, alice.uid)) }

        val first = (alice.chats.sendGroup(group, everyone, "hello all") as E2eeResult.Ok).value
        assertTrue(first.withoutKey.isEmpty())
        eventually { bob.texts(group) == listOf("hello all") && carol.texts(group) == listOf("hello all") }
        assertTrue("one sender key each, over the direct channel", relay.senderKeyTopics.all { !ImIds.isGroupTopic(it) })

        bob.chats.sendGroup(group, everyone, "hi from bob")
        eventually { alice.texts(group).size == 2 && carol.texts(group).size == 2 }

        // Carol is removed: the rest rotate, and alice's next key reaches bob only.
        server.groups.getValue(group).members.remove(carol.uid)
        relay.rotate(group, alice.uid, "member_removed")
        val after = (alice.chats.sendGroup(group, listOf(alice.uid, bob.uid), "without carol") as E2eeResult.Ok).value
        assertTrue(after.withoutKey.isEmpty())
        eventually { bob.texts(group).last() == "without carol" }
        assertEquals(listOf("hello all", "hi from bob"), carol.texts(group))

        // A member who never published cannot get the key and is reported.
        val dave = server.uidOf("dave")
        server.groups.getValue(group).members[dave] = "member"
        assertEquals(setOf(dave), (alice.chats.sendGroup(group, listOf(alice.uid, bob.uid, dave), "x") as E2eeResult.Ok).value.withoutKey)
    }

    @Test
    fun aGroupMessageThatComesBeforeItsKeyWaitsForIt() = runBlocking {
        val alice = party("alice")
        val bob = party("bob")
        server.groups[group] = MiniServer.Group("g", mutableMapOf(alice.uid to "owner", bob.uid to "member"))
        alice.chats.enableGroup(group)
        relay.holdSenderKeys = true
        alice.chats.sendGroup(group, listOf(bob.uid), "patience")
        eventually { relay.held.isNotEmpty() }
        Thread.sleep(200)
        assertTrue(bob.texts(group).isEmpty())
        relay.release()
        eventually { bob.texts(group) == listOf("patience") }
    }

    @Test
    fun keysArePublishedAgainOnEveryLogin() = runBlocking {
        val alice = party("alice")
        val before = relay.publishes.get()
        server.dropAll(alice.uid)
        eventually { relay.publishes.get() > before }
    }

}
