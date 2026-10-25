package io.imkit.core.engine

import io.imkit.core.ImIds
import io.imkit.core.client.ConnectionState
import io.imkit.core.client.ImClient
import io.imkit.core.client.ImClientConfig
import io.imkit.core.client.TokenProvider
import io.imkit.core.json.jsonObject
import io.imkit.core.model.ChatMessage
import io.imkit.core.model.ConversationId
import io.imkit.core.model.GroupRole
import io.imkit.core.model.MessageStatus
import io.imkit.core.store.InMemoryChatStore
import io.imkit.core.store.StoredConversation
import io.imkit.core.testing.MiniServer
import io.imkit.core.testing.eventually
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.util.concurrent.CopyOnWriteArrayList
import kotlin.random.Random

class ChatEngineTest {
    private val scope = CoroutineScope(Dispatchers.Default + SupervisorJob())
    private val server = MiniServer()
    private val alice = ImIds.uid("alice")
    private val bob = ImIds.uid("bob")
    private val withBob = ConversationId(bob)

    @After
    fun tearDown() = scope.cancel()

    private fun engine(
        user: String = "alice",
        store: InMemoryChatStore = InMemoryChatStore(),
        config: EngineConfig = EngineConfig(retryBaseDelayMs = 20, retryMaxDelayMs = 50, pageSize = 3, staleSendingMs = 60_000),
    ): ChatEngine {
        val client = ImClient(
            ImClientConfig(
                endpoints = listOf("ws://mini"),
                tokenProvider = TokenProvider { user },
                transport = server.transport,
                loginTimeoutMs = 1_000,
                requestTimeoutMs = 1_000,
                publishTimeoutMs = 300,
                reconnectBaseDelayMs = 20,
                reconnectMaxDelayMs = 40,
                random = Random(2),
            ),
            scope,
        )
        return ChatEngine(client, store, user, scope, config)
    }

    private fun ChatEngine.startAndSync(prepare: suspend ChatEngine.() -> Unit = {}) = runBlocking {
        val synced = CopyOnWriteArrayList<ChatEvent>()
        scope.launch { events.collect { if (it is ChatEvent.Synced) synced += it } }
        open()
        prepare()
        client.start()
        eventually { synced.isNotEmpty() }
    }

    private fun ChatEngine.msgs(c: ConversationId = withBob): List<ChatMessage> = runBlocking {
        val flow = messages(c)
        kotlinx.coroutines.delay(30)
        flow.value
    }

    @Test
    fun c4_switchingAccountClearsAllCaches() = runBlocking {
        val store = InMemoryChatStore()
        store.setOwner(ImIds.uid("someone-else"))
        store.putConversation(StoredConversation(withBob, lastReadSeq = 3))
        store.putMessage(ChatMessage(withBob, 1, null, bob, "old", "text", ts = 1, status = MessageStatus.RECEIVED))
        val e = engine(store = store)
        e.open()
        assertEquals(alice, store.owner())
        assertTrue(store.conversations().isEmpty())
        assertTrue(store.seqs(withBob).isEmpty())
        e.close()
    }

    @Test
    fun c4_sameAccountKeepsItsCache() = runBlocking {
        val store = InMemoryChatStore()
        store.setOwner(alice)
        store.putConversation(StoredConversation(withBob))
        store.putMessage(ChatMessage(withBob, 1, null, bob, "kept", "text", ts = 1, status = MessageStatus.RECEIVED))
        val e = engine(store = store)
        e.open()
        assertEquals(listOf(1L), store.seqs(withBob))
        assertEquals(1, e.conversations.value.size)
        e.close()
    }

    @Test
    fun sendsAndReceives() {
        val a = engine("alice")
        val b = engine("bob")
        a.startAndSync()
        b.startAndSync()
        val arrived = CopyOnWriteArrayList<ChatEvent.MessageArrived>()
        scope.launch { b.events.collect { if (it is ChatEvent.MessageArrived) arrived += it } }
        val sent = runBlocking { a.send(withBob, OutgoingMessage("hi")) }
        assertEquals(MessageStatus.SENDING, sent.status)
        eventually { a.msgs().singleOrNull()?.status == MessageStatus.SENT }
        assertEquals(1L, a.msgs().single().seq)
        eventually { arrived.size == 1 }
        assertEquals("hi", arrived.single().message.content)
        assertEquals(ConversationId(alice), arrived.single().message.conversation)
        eventually { b.conversations.value.singleOrNull()?.unread == 1 }
        runBlocking { b.markRead(ConversationId(alice)) }
        eventually { b.conversations.value.single().unread == 0 }
        // The read note reaches alice as the peer's read position.
        eventually { server.requests.any { it.string("action") == "note" && it.string("what") == "read" } }
    }

    @Test
    fun c2_timedOutPublishStaysSendingAndIsNotResent() {
        server.dropReply = { it.string("action") == "pub" }
        val a = engine()
        a.startAndSync()
        runBlocking { a.send(withBob, OutgoingMessage("lost reply")) }
        Thread.sleep(800) // publish timeout (300 ms) plus several retry delays
        assertEquals(MessageStatus.SENDING, a.msgs().single().status)
        assertEquals("one pub only", 1, server.requests.count { it.string("action") == "pub" })
        assertEquals("the server stored it once", 1, server.messages(ImIds.p2pTopic(alice, bob)).size)
    }

    @Test
    fun c2_lostReplyIsReconciledFromHistoryWithoutResending() {
        server.dropReply = { it.string("action") == "pub" }
        val a = engine()
        a.startAndSync()
        runBlocking { a.send(withBob, OutgoingMessage("again")) }
        Thread.sleep(500)
        server.dropReply = { false }
        server.dropAll(alice)
        // After the reconnect the sync finds the stored copy: no second pub needed.
        eventually { a.msgs().single().status == MessageStatus.SENT }
        assertEquals(1, server.requests.count { it.string("action") == "pub" })
        assertEquals(1, server.messages(ImIds.p2pTopic(alice, bob)).size)
    }

    @Test
    fun c2_lostRequestIsResentWithTheSameIdAfterReconnect() {
        server.loseRequest = { it.string("action") == "pub" }
        val a = engine()
        a.startAndSync()
        runBlocking { a.send(withBob, OutgoingMessage("again")) }
        Thread.sleep(500)
        assertEquals("not resent on the same connection", MessageStatus.SENDING, a.msgs().single().status)
        server.loseRequest = { false }
        server.dropAll(alice)
        eventually { a.msgs().single().status == MessageStatus.SENT }
        val pubs = server.requests.filter { it.string("action") == "pub" }
        assertEquals(2, pubs.size)
        assertEquals("same clientMsgId", 1, pubs.map { it.string("clientMsgId") }.toSet().size)
        assertEquals("stored once", 1, server.messages(ImIds.p2pTopic(alice, bob)).size)
    }

    @Test
    fun c2_refusedMessageFailsWithoutRetry() {
        server.override = { f ->
            if (f.string("action") == "pub") jsonObject { put("action", "pub"); put("code", 400); put("reason", "message too long"); put("errCode", "message_too_long") } else null
        }
        val a = engine()
        a.startAndSync()
        val failed = CopyOnWriteArrayList<ChatEvent.SendFailed>()
        scope.launch { a.events.collect { if (it is ChatEvent.SendFailed) failed += it } }
        runBlocking { a.send(withBob, OutgoingMessage("x".repeat(10))) }
        eventually { failed.size == 1 }
        assertEquals("message_too_long", failed.single().message.failure?.errCode)
        Thread.sleep(200)
        assertEquals(1, server.requests.count { it.string("action") == "pub" })
    }

    @Test
    fun C2_MediaReconciledByUrl() {
        // The reply is lost; later the server's copy comes back through
        // history with another content text and no clientMsgId.
        server.dropReply = { it.string("action") == "pub" }
        val a = engine()
        a.startAndSync()
        runBlocking { a.send(withBob, OutgoingMessage(content = "", contentType = "image/png", fileUrl = "https://m/x.png", fileName = "x.png")) }
        eventually { server.messages(ImIds.p2pTopic(alice, bob)).size == 1 }
        // Rewrite the stored copy the way a server echo can look.
        val topic = ImIds.p2pTopic(alice, bob)
        val stored = server.messages(topic).single()
        server.recallSilently(topic, stored.seq)
        server.inject(topic, alice, "[Image]", fileUrl = "https://m/x.png", contentType = "image/png")
        server.dropReply = { false }
        runBlocking { a.syncConversation(withBob) }
        val list = a.msgs()
        assertEquals("matched, not duplicated: $list", 1, list.size)
        assertEquals(MessageStatus.SENT, list.single().status)
        assertEquals("https://m/x.png", list.single().fileUrl)
        assertEquals("not sent again", 1, server.requests.count { it.string("action") == "pub" })
    }

    @Test
    fun c3_pullsFromTheContiguousEndAndDropsWhatWasRecalledAway() {
        val topic = ImIds.p2pTopic(alice, bob)
        for (i in 1..4) server.inject(topic, bob, "m$i")
        val a = engine()
        a.startAndSync { ensureConversation(withBob) }
        eventually { a.msgs().size == 3 } // pageSize 3: the newest page (2..4)
        assertEquals(listOf(2L, 3L, 4L), a.msgs().map { it.seq })
        // 5 is missed (nothing pushed), 6 arrives live: a hole at 5.
        server.inject(topic, bob, "m5")
        val six = server.inject(topic, bob, "m6")
        server.pushTo(alice, jsonObject { put("action", "data"); put("topic", bob); put("from", bob); put("seq", six); put("ts", 1); put("content", "m6") })
        eventually { a.msgs().map { it.seq } == listOf(2L, 3L, 4L, 6L) }
        // While away: 6 is recalled and 7 sent.
        server.recallSilently(topic, 6)
        server.inject(topic, bob, "m7")
        server.requests.clear()
        runBlocking { a.syncConversation(withBob) }
        val get = server.requests.first { it.string("action") == "get" }
        assertEquals("from the end of the run, not the maximum", 4L, get.long("afterSeq"))
        eventually { a.msgs().map { it.seq } == listOf(2L, 3L, 4L, 5L, 7L) }
        // 6 is a tombstone now, not a hole: the next pull starts at 7.
        server.requests.clear()
        runBlocking { a.syncConversation(withBob) }
        assertEquals(7L, server.requests.first { it.string("action") == "get" }.long("afterSeq"))
    }

    @Test
    fun c3_aRecallBelowTheEndOfARunDoesNotPinTheCursor() {
        val topic = ImIds.p2pTopic(alice, bob)
        for (i in 1..3) server.inject(topic, bob, "m$i")
        val a = engine()
        a.startAndSync { ensureConversation(withBob) }
        eventually { a.msgs().size == 3 }
        // bob recalls 2 live.
        server.recallSilently(topic, 2)
        server.pushTo(alice, jsonObject { put("action", "recalled"); put("topic", alice); put("seq", 2); put("from", bob) })
        eventually { a.msgs().map { it.seq } == listOf(1L, 3L) }
        server.inject(topic, bob, "m4")
        server.requests.clear()
        runBlocking { a.syncConversation(withBob) }
        assertEquals(3L, server.requests.first { it.string("action") == "get" }.long("afterSeq"))
        eventually { a.msgs().map { it.seq } == listOf(1L, 3L, 4L) }
    }

    @Test
    fun clearAppliesTheWatermarkOnBothSides() {
        val a = engine("alice")
        val b = engine("bob")
        a.startAndSync()
        b.startAndSync()
        runBlocking {
            a.send(withBob, OutgoingMessage("1"))
            a.send(withBob, OutgoingMessage("2"))
        }
        eventually { b.msgs(ConversationId(alice)).size == 2 }
        runBlocking { a.clear(withBob) }
        eventually { a.msgs().isEmpty() && b.msgs(ConversationId(alice)).isEmpty() }
        runBlocking { a.send(withBob, OutgoingMessage("after")) }
        eventually { b.msgs(ConversationId(alice)).map { it.content } == listOf("after") }
    }

    @Test
    fun c9_roleComesFromTheRosterAndGroupSyncRefreshesIt() = runBlocking {
        val g = "grp_abcdefabcdef"
        server.groups[g] = MiniServer.Group("team", mutableMapOf(bob to "owner", alice to "member"))
        val a = engine()
        a.startAndSync()
        assertEquals(GroupRole.MEMBER, a.myRole(g))
        // Promoted: the server's roster changes and a grp_sync arrives.
        server.groups[g]!!.members[alice] = "admin"
        assertEquals("cached until told", GroupRole.MEMBER, a.myRole(g))
        server.pushTo(alice, jsonObject { put("action", "grp_sync"); put("topic", g); put("from", bob) })
        eventually { runBlocking { a.myRole(g) } == GroupRole.ADMIN }
        val fetches = server.requests.count { it.string("action") == "get_members" }
        assertEquals(2, fetches)
    }

    @Test
    fun c9_failedRosterMeansMember() = runBlocking {
        val g = "grp_abcdefabcdef"
        server.groups[g] = MiniServer.Group("team", mutableMapOf(alice to "owner"))
        server.dropReply = { it.string("action") == "get_members" }
        val a = engine()
        a.startAndSync()
        assertEquals("fail closed", GroupRole.MEMBER, a.myRole(g))
        server.dropReply = { false }
        assertEquals("not cached as member", GroupRole.OWNER, a.myRole(g))
    }

    @Test
    fun groupsMissingFromTheListAreMarkedLeft() = runBlocking {
        val g = "grp_abcdefabcdef"
        server.groups[g] = MiniServer.Group("team", mutableMapOf(alice to "member", bob to "owner"))
        val a = engine()
        val removed = CopyOnWriteArrayList<ChatEvent.RemovedFromGroup>()
        scope.launch { a.events.collect { if (it is ChatEvent.RemovedFromGroup) removed += it } }
        a.startAndSync()
        eventually { a.conversations.value.any { it.id.id == g && it.title == "team" && !it.left } }
        server.groups[g]!!.members.remove(alice)
        a.syncAll()
        eventually { a.conversations.value.single { it.id.id == g }.left }
        assertEquals(1, removed.size)
    }

    // ---------------------------------------------------------------- own read position from the server (spec 8, 9.3)

    private fun ChatEngine.unreadWithBob(): Int? = conversations.value.firstOrNull { it.id == withBob }?.unread

    /** A device that has never seen the chat finds it the way the UI does: by its contact. */
    private fun newDevice(): ChatEngine = engine().also { it.startAndSync { ensureConversation(withBob) } }

    @Test
    fun aNewDeviceTakesTheReadPositionFromTheServer() {
        server.storeReads = true
        val p2p = ImIds.p2pTopic(alice, bob)
        repeat(5) { server.inject(p2p, bob, "m$it") }
        val phone = engine()
        phone.startAndSync { ensureConversation(withBob) }
        eventually { phone.unreadWithBob() == 3 }
        runBlocking { phone.markRead(withBob) }
        eventually { server.readOf(p2p, alice) == 5L }
        server.inject(p2p, bob, "after the read")

        val tablet = newDevice()
        // A device with nothing stored pulls the latest page (three here).
        eventually { tablet.msgs().mapNotNull { it.seq } == listOf(4L, 5L, 6L) }
        assertEquals(1, tablet.unreadWithBob())
        assertEquals(5L, tablet.conversations.value.single { it.id == withBob }.lastReadSeq)
    }

    @Test
    fun withoutAReadPositionFromTheServerTheHistoryIsUnread() {
        // A legacy server stores no direct-chat read: this is the behaviour the
        // readSeq fixes, kept here as the control of the test above.
        val p2p = ImIds.p2pTopic(alice, bob)
        repeat(5) { server.inject(p2p, bob, "m$it") }
        server.setRead(p2p, alice, 5)
        val tablet = newDevice()
        eventually { tablet.msgs().size == 3 }
        assertEquals(3, tablet.unreadWithBob())
    }

    @Test
    fun theServersReadPositionNeverMovesTheLocalOneBack() = runBlocking {
        server.storeReads = true
        val p2p = ImIds.p2pTopic(alice, bob)
        repeat(5) { server.inject(p2p, bob, "m$it") }
        server.setRead(p2p, alice, 2)
        val store = InMemoryChatStore()
        store.setOwner(alice)
        store.putConversation(StoredConversation(withBob, lastReadSeq = 4))
        val e = engine(store = store)
        e.startAndSync()
        eventually { e.msgs().mapNotNull { it.seq } == listOf(3L, 4L, 5L) }
        assertEquals(4L, store.conversation(withBob)?.lastReadSeq)
        assertEquals(1, e.unreadWithBob())
    }

    @Test
    fun aGroupTakesTheOwnEntryOfReadCursors() {
        // readCursors is sent by a server with read receipts on, legacy too.
        server.storeReads = true
        val g = "grp_abcdefabcdef"
        server.groups[g] = MiniServer.Group("team", mutableMapOf(bob to "owner", alice to "member"))
        repeat(4) { server.inject(g, bob, "g$it") }
        server.setRead(g, alice, 3)
        server.setRead(g, bob, 4)
        val e = engine()
        e.startAndSync()
        val id = ConversationId(g)
        eventually { e.msgs(id).mapNotNull { it.seq } == listOf(2L, 3L, 4L) }
        assertEquals(1, e.conversations.value.single { it.id == id }.unread)
        assertEquals(3L, e.conversations.value.single { it.id == id }.lastReadSeq)
        // The others' cursors stay read receipts; the own one is not among them.
        val cursors = e.readCursors(id)
        eventually { cursors.value == mapOf(bob to 4L) }
    }

    @Test
    fun aMessageFromAnotherAccountIsRefused() {
        val wrong = CopyOnWriteArrayList<ChatEvent.WrongAccount>()
        // The token provider hands out bob's token to alice's engine.
        val client = ImClient(
            ImClientConfig(listOf("ws://mini"), TokenProvider { "bob" }, transport = server.transport, reconnectBaseDelayMs = 20),
            scope,
        )
        val e = ChatEngine(client, InMemoryChatStore(), "alice", scope)
        scope.launch { e.events.collect { if (it is ChatEvent.WrongAccount) wrong += it } }
        runBlocking { e.start() }
        eventually { wrong.isNotEmpty() }
        assertTrue(client.state.value is ConnectionState.Stopped)
        assertNull(client.session)
    }
}
