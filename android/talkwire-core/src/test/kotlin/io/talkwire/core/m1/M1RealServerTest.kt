package io.talkwire.core.m1

import io.talkwire.core.ImIds
import io.talkwire.core.client.ConnectionState
import io.talkwire.core.client.ImClient
import io.talkwire.core.client.ImClientConfig
import io.talkwire.core.client.PublishOutcome
import io.talkwire.core.client.TokenProvider
import io.talkwire.core.client.createGroup
import io.talkwire.core.client.publish
import io.talkwire.core.engine.ChatEngine
import io.talkwire.core.engine.ChatEvent
import io.talkwire.core.engine.EngineConfig
import io.talkwire.core.engine.OutgoingMessage
import io.talkwire.core.model.ChatMessage
import io.talkwire.core.model.ConversationId
import io.talkwire.core.model.GroupRole
import io.talkwire.core.model.MessageStatus
import io.talkwire.core.model.PresenceState
import io.talkwire.core.store.InMemoryChatStore
import io.talkwire.core.transport.ImSocket
import io.talkwire.core.transport.ImSocketListener
import io.talkwire.core.transport.ImTransport
import io.talkwire.core.transport.OkHttpTransport
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import java.security.SecureRandom
import java.util.concurrent.CopyOnWriteArrayList

/**
 * Milestone M1: the SDK against the real server, end to end, under both
 * protocol profiles: login and first sync, a direct message, publish
 * idempotency, offline catch-up over several pages, reconnecting after a
 * dropped socket, a group round trip, history paging on a fresh device,
 * recall, read receipts, a new device's unread count, clearing for
 * everyone, and presence.
 */
class M1RealServerTest {
    private val scope = CoroutineScope(Dispatchers.Default + SupervisorJob())
    private val clients = CopyOnWriteArrayList<ImClient>()

    @After
    fun tearDown() {
        clients.forEach { it.stop() }
        scope.cancel()
    }

    /** Lets a test cut the network under a client. */
    private class KillableTransport(private val delegate: ImTransport = OkHttpTransport()) : ImTransport {
        private val sockets = CopyOnWriteArrayList<ImSocket>()

        fun killAll() {
            sockets.forEach { it.cancel() }
            sockets.clear()
        }

        override fun open(url: String, listener: ImSocketListener): ImSocket = delegate.open(url, listener).also { sockets += it }
    }

    private class Device(val engine: ChatEngine, val store: InMemoryChatStore, val transport: KillableTransport) {
        val events = CopyOnWriteArrayList<ChatEvent>()
        val client: ImClient get() = engine.client

        fun synced() = events.count { it is ChatEvent.Synced }
    }

    private suspend fun device(server: RealServer, appUserId: String, deviceName: String? = null): Device {
        val transport = KillableTransport()
        val client = ImClient(
            ImClientConfig(
                endpoints = listOf(server.wsUrl),
                tokenProvider = TokenProvider { server.token(appUserId) },
                deviceName = deviceName,
                transport = transport,
                requestTimeoutMs = 5_000,
                publishTimeoutMs = 5_000,
                reconnectBaseDelayMs = 200,
                reconnectMaxDelayMs = 400,
            ),
            scope,
        ).also { clients += it }
        val store = InMemoryChatStore()
        val d = Device(ChatEngine(client, store, appUserId, scope, EngineConfig(pageSize = 3)), store, transport)
        scope.launch(start = CoroutineStart.UNDISPATCHED) { d.engine.events.collect { d.events += it } }
        d.engine.open()
        return d
    }

    private suspend fun until(what: String, timeoutMs: Long = 10_000, check: suspend () -> Boolean) {
        try {
            withTimeout(timeoutMs) { while (!check()) delay(20) }
        } catch (e: kotlinx.coroutines.TimeoutCancellationException) {
            throw AssertionError("timed out waiting for $what")
        }
    }

    private suspend fun sendAndConfirm(d: Device, c: ConversationId, window: StateFlow<List<ChatMessage>>, text: String): ChatMessage {
        val pending = d.engine.send(c, OutgoingMessage(text))
        until("\"$text\" stored") { window.value.any { it.localId == pending.localId && it.status == MessageStatus.SENT } }
        return window.value.first { it.localId == pending.localId }
    }

    private fun StateFlow<List<ChatMessage>>.seqs() = value.mapNotNull { it.seq }

    private fun runFlow(profile: String) = runBlocking {
        RealServer.start(profile).use { server ->
            try {
                flow(server, profile)
            } catch (e: Throwable) {
                throw AssertionError("$e\n--- server log ($profile) ---\n${server.logTail()}", e)
            }
        }
    }

    @Test(timeout = 180_000)
    fun strictProfile() = runFlow("strict")

    @Test(timeout = 180_000)
    fun legacyProfile() = runFlow("legacy")

    private suspend fun flow(server: RealServer, profile: String) {
        val run = ByteArray(4).also { SecureRandom().nextBytes(it) }.joinToString("") { "%02x".format(it) }
        val aliceId = "m1-$run-alice"
        val bobId = "m1-$run-bob"
        val aliceUid = ImIds.uid(aliceId)
        val bobUid = ImIds.uid(bobId)
        val withBob = ConversationId(bobUid)
        val withAlice = ConversationId(aliceUid)

        val alice = device(server, aliceId)
        val bob = device(server, bobId)
        // Direct conversations come from the host's contact list.
        alice.engine.ensureConversation(withBob, "Bob")
        bob.engine.ensureConversation(withAlice, "Alice")
        val aliceWin = alice.engine.messages(withBob)
        val bobWin = bob.engine.messages(withAlice)

        // Login and the first sync.
        alice.client.start()
        bob.client.start()
        until("first sync") { alice.synced() >= 1 && bob.synced() >= 1 }
        val session = alice.client.session!!
        assertEquals(aliceUid, session.uid)
        if (profile == "strict") assertTrue("strict login reply carries protocol", session.protocol >= 1)
        else assertEquals("legacy login reply has no protocol", 0, session.protocol)

        // A direct message.
        val hello = sendAndConfirm(alice, withBob, aliceWin, "hello")
        assertEquals(1L, hello.seq)
        until("Bob receives hello") { bobWin.value.any { it.content == "hello" && it.status == MessageStatus.RECEIVED } }
        assertTrue(bob.events.any { it is ChatEvent.MessageArrived && it.message.content == "hello" })
        // Both open the chat: presence pushes follow subscriptions.
        assertEquals(200, alice.engine.subscribe(withBob).code)
        assertEquals(200, bob.engine.subscribe(withAlice).code)

        // Idempotency: the same clientMsgId twice is stored once (C2's foundation).
        val cid = "m1-$run-dup"
        val first = alice.client.publish(bobUid, "dup", "text", cid) as PublishOutcome.Stored
        val again = alice.client.publish(bobUid, "dup", "text", cid) as PublishOutcome.Stored
        assertEquals(first.seq, again.seq)
        until("Bob receives dup") { bobWin.value.any { it.content == "dup" } }
        delay(300)
        assertEquals(1, bobWin.value.count { it.content == "dup" })
        alice.engine.syncConversation(withBob) // Alice's own engine did not see the direct publish
        assertEquals(listOf(1L, 2L), aliceWin.seqs())

        // Offline catch-up: more than one page (3) while Bob is away.
        bob.client.stop()
        until("Bob offline for Alice") { alice.engine.presence.state(bobUid) == PresenceState.OFFLINE }
        repeat(4) { sendAndConfirm(alice, withBob, aliceWin, "while away $it") }
        val syncedBefore = bob.synced()
        bob.client.start()
        until("Bob's sync after login") { bob.synced() > syncedBefore }
        assertEquals((1L..6L).toList(), bobWin.seqs())
        until("Bob online for Alice") { alice.engine.presence.state(bobUid) == PresenceState.ONLINE }

        // Reconnect after the socket dies under the client.
        val beforeDrop = bob.synced()
        bob.transport.killAll()
        sendAndConfirm(alice, withBob, aliceWin, "after the drop")
        until("Bob resynced") { bob.synced() > beforeDrop && bob.client.state.value is ConnectionState.Connected }
        until("Bob has the message") { bobWin.value.any { it.content == "after the drop" } }
        assertEquals((1L..7L).toList(), bobWin.seqs())

        // A group round trip; roles come from the roster (C9).
        val topic = alice.client.createGroup("m1 $run", listOf(bobUid))
        val group = ConversationId(topic)
        until("Bob learns of the group") { bob.engine.conversations.value.any { it.id == group } }
        val aliceGroup = alice.engine.messages(group)
        val bobGroup = bob.engine.messages(group)
        sendAndConfirm(alice, group, aliceGroup, "to the group")
        until("Bob receives the group message") { bobGroup.value.any { it.content == "to the group" } }
        assertEquals(GroupRole.OWNER, alice.engine.myRole(topic))
        assertEquals(GroupRole.MEMBER, bob.engine.myRole(topic))

        // History paging on a fresh device: newest page first, then older pages.
        bob.client.stop() // the legacy profile allows one connection per user
        val tablet = device(server, bobId, deviceName = "tablet")
        tablet.engine.ensureConversation(withAlice, "Alice")
        val tabletWin = tablet.engine.messages(withAlice)
        tablet.client.start()
        until("tablet sync") { tablet.synced() >= 1 }
        assertEquals(listOf(5L, 6L, 7L), tabletWin.seqs())
        var pages = 0
        while (tablet.engine.loadOlder(withAlice) > 0) pages++
        assertEquals((1L..7L).toList(), tabletWin.seqs())
        assertEquals(2, pages)
        assertEquals(listOf("hello", "dup"), tabletWin.value.take(2).map { it.content })
        tablet.client.stop()
        val bobBack = bob.synced()
        bob.client.start()
        until("Bob back") { bob.synced() > bobBack }
        // Nothing changed while Bob was away: under strict the summary says so
        // and nothing is pulled (spec 8.1); legacy pulls each conversation.
        val back = bob.events.filterIsInstance<ChatEvent.Synced>().last()
        assertEquals(ChatEvent.Synced(2, 0, if (profile == "strict") 0 else 2), back)

        // Recall: the content is gone on both sides; a tombstone says who unsent it.
        val target = aliceWin.value.first { it.content == "while away 0" }
        alice.engine.recall(target)
        assertTrue(aliceWin.value.single { it.seq == target.seq }.isRecalled)
        until("recall reaches Bob") { bobWin.value.singleOrNull { it.seq == target.seq }?.isRecalled == true }
        assertEquals(aliceUid, bobWin.value.single { it.seq == target.seq }.from)

        // Read receipts.
        bob.engine.markRead(withAlice)
        val bobMax = bobWin.seqs().max()
        until("Alice sees the read position") { alice.store.conversation(withBob)?.peerReadSeq == bobMax }
        assertEquals(0, bob.engine.conversations.value.first { it.id == withAlice }.unread)

        // A new device of Bob's pulls the history already read: strict keeps
        // Bob's own read position (stored before the read was relayed to
        // Alice, so it is there now) and returns it as readSeq; legacy keeps
        // none, so everything pulled counts as unread there.
        bob.client.stop()
        val laptop = device(server, bobId, deviceName = "laptop")
        laptop.engine.ensureConversation(withAlice, "Alice")
        val laptopWin = laptop.engine.messages(withAlice)
        laptop.client.start()
        until("laptop sync") { laptop.synced() >= 1 }
        assertEquals(listOf(5L, 6L, 7L), laptopWin.seqs())
        val laptopChat = laptop.engine.conversations.value.first { it.id == withAlice }
        if (profile == "strict") {
            assertEquals("a new device's history is already read", 0, laptopChat.unread)
            assertEquals(bobMax, laptopChat.lastReadSeq)
        } else {
            assertEquals("legacy keeps no direct-chat read position", 3, laptopChat.unread)
        }
        laptop.client.stop()
        val bobAgain = bob.synced()
        bob.client.start()
        until("Bob back again") { bob.synced() > bobAgain }

        // Clear for everyone, permanently; later messages arrive normally.
        alice.engine.clear(withBob)
        assertTrue(aliceWin.value.isEmpty())
        until("clear reaches Bob") { bobWin.value.isEmpty() }
        // The window updates before the event is emitted.
        until("Bob is told") { bob.events.any { it is ChatEvent.ConversationCleared && it.conversation == withAlice } }
        bob.engine.syncConversation(withAlice)
        assertTrue("nothing comes back after a clear", bobWin.value.isEmpty())
        val fresh = sendAndConfirm(alice, withBob, aliceWin, "fresh start")
        assertTrue(fresh.seq!! > bobMax)
        until("Bob receives after the clear") { bobWin.value.map { it.content } == listOf("fresh start") }

        // Presence when Alice leaves.
        alice.client.stop()
        until("Alice offline for Bob") { bob.engine.presence.state(aliceUid) == PresenceState.OFFLINE }
    }
}
