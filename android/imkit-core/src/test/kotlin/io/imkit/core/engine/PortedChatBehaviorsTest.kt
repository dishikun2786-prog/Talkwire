package io.imkit.core.engine

import io.imkit.core.ImIds
import io.imkit.core.client.ImClient
import io.imkit.core.client.ImClientConfig
import io.imkit.core.client.TokenProvider
import io.imkit.core.json.JsonObject
import io.imkit.core.json.jsonObject
import io.imkit.core.model.ChatMessage
import io.imkit.core.model.ConversationId
import io.imkit.core.model.MessageStatus
import io.imkit.core.store.InMemoryChatStore
import io.imkit.core.testing.MiniServer
import io.imkit.core.testing.eventually
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.joinAll
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import java.util.concurrent.CopyOnWriteArrayList
import kotlin.random.Random

/**
 * The cases of the source app's chat unit tests (app/src/test/.../chat,
 * about 1.5k lines) that apply to a core SDK without UI, ported to the
 * engine (task 3.10). Each test names the source test class it comes from;
 * android/README.md lists where the other applicable cases are covered and
 * why the rest (formatting, contacts, avatars, chat-id rules) does not apply.
 */
class PortedChatBehaviorsTest {
    private val scope = CoroutineScope(Dispatchers.Default + SupervisorJob())
    private val server = MiniServer()
    private val alice = ImIds.uid("alice")
    private val bob = ImIds.uid("bob")
    private val withBob = ConversationId(bob)
    private val group = "grp_gggggggggggg"
    private val store = InMemoryChatStore()
    private var now = 1_000_000L
    private val config = EngineConfig(retryBaseDelayMs = 20, retryMaxDelayMs = 50, clock = { now })

    @After
    fun tearDown() = scope.cancel()

    private fun engine(): ChatEngine {
        val client = ImClient(
            ImClientConfig(
                endpoints = listOf("ws://mini"),
                tokenProvider = TokenProvider { "alice" },
                transport = server.transport,
                loginTimeoutMs = 1_000,
                requestTimeoutMs = 1_000,
                publishTimeoutMs = 300,
                reconnectBaseDelayMs = 20,
                reconnectMaxDelayMs = 40,
                random = Random(4),
            ),
            scope,
        )
        return ChatEngine(client, store, "alice", scope, config)
    }

    private fun ChatEngine.startAndSync(prepare: suspend ChatEngine.() -> Unit = {}) = runBlocking {
        val synced = CopyOnWriteArrayList<ChatEvent>()
        scope.launch { events.collect { if (it is ChatEvent.Synced) synced += it } }
        open()
        prepare()
        client.start()
        eventually { synced.isNotEmpty() }
    }

    private fun ChatEngine.msgs(): List<ChatMessage> = runBlocking {
        val flow = messages(withBob)
        kotlinx.coroutines.delay(30)
        flow.value
    }

    /** A direct-chat push as the server sends it to alice: the topic is the peer as she sees it. */
    private fun data(from: String, seq: Long, content: String, clientMsgId: String? = null) = jsonObject {
        put("action", "data"); put("topic", bob); put("from", from); put("seq", seq); put("ts", 5_000 + seq)
        put("content", content); put("contentType", "text")
        clientMsgId?.let { put("clientMsgId", it) }
    }

    private fun info(what: String, topic: String, from: String, seq: Long? = null): JsonObject = jsonObject {
        put("action", "info"); put("what", what); put("topic", topic); put("from", from)
        seq?.let { put("seq", it) }
    }

    /** ChatRepositoryTest: the optimistic message, its lost ack and the server's echo end up as one message. */
    @Test
    fun ownEchoBeforeTheAckIsOneMessage() {
        server.dropReply = { it.string("action") == "pub" }
        val a = engine()
        a.startAndSync()
        val sent = runBlocking { a.send(withBob, OutgoingMessage("hi")) }
        eventually { server.messages(ImIds.p2pTopic(alice, bob)).size == 1 }
        server.pushTo(alice, data(alice, 1, "hi", sent.localId)) // the echo to my connection
        eventually { a.msgs().singleOrNull()?.status == MessageStatus.SENT }
        Thread.sleep(500) // the publish timeout passes
        val only = a.msgs().single()
        assertEquals(1L, only.seq)
        assertEquals(MessageStatus.SENT, only.status)
        assertEquals(sent.localId, only.localId)
        assertEquals("not sent again", 1, server.requests.count { it.string("action") == "pub" })
    }

    /** ChatRepositoryTest, ChatIntegrationTest: a burst out of order and with duplicates is kept once each, in seq order. */
    @Test
    fun aBurstOutOfOrderIsKeptOnceInSeqOrder() {
        val topic = ImIds.p2pTopic(alice, bob)
        val a = engine()
        a.startAndSync { ensureConversation(withBob) }
        // The server holds them too, so the pulls a gap causes find the same messages.
        val seqs = (1..30).map { server.inject(topic, bob, "m$it") }
        val frames = seqs.shuffled(Random(5)).flatMap { listOf(it, it) }
        runBlocking {
            frames.map { s -> scope.launch { server.pushTo(alice, data(bob, s, "m$s")) } }.joinAll()
        }
        eventually { a.msgs().map { it.seq } == (1L..30L).toList() }
        assertEquals((1..30).map { "m$it" }, a.msgs().map { it.content })
        eventually { a.conversations.value.single().unread == 30 }
    }

    /** ChatRepositoryLogicTest: typing shows for its time to live, then goes; mine from another device is not shown. */
    @Test
    fun typingExpiresAndMineIsNotShown() {
        val a = engine()
        a.startAndSync { ensureConversation(withBob) }
        val at = now
        server.pushTo(alice, info("kp", bob, alice))
        server.pushTo(alice, info("kp", bob, bob))
        eventually { a.typingIn(withBob, at).isNotEmpty() }
        assertEquals(setOf(bob), a.typingIn(withBob, at))
        assertEquals("just inside the time to live", setOf(bob), a.typingIn(withBob, at + config.typingTtlMs))
        assertTrue(a.typingIn(withBob, at + config.typingTtlMs + 1).isEmpty())
    }

    /** Protocol §15: "the assistant is typing" for a group alice is in, until it answers or the note runs out. */
    @Test
    fun theAssistantTypesInGroupsAliceIsIn() {
        val g = ConversationId(group)
        val a = engine()
        a.startAndSync { ensureConversation(g) }
        val aiTyping = { topic: String -> jsonObject { put("type", "ai_typing"); put("topic", topic) } }
        val at = now
        server.pushTo(alice, aiTyping("grp_unknownunkno")) // not a group she has: ignored
        server.pushTo(alice, aiTyping(group))
        eventually { a.typingIn(g, at).isNotEmpty() }
        assertEquals(setOf(ChatMessage.AI_ASSISTANT_UID), a.typingIn(g, at))
        assertEquals(setOf(ChatMessage.AI_ASSISTANT_UID), a.typingIn(g, at + config.aiTypingTtlMs))
        assertTrue(a.typingIn(g, at + config.aiTypingTtlMs + 1).isEmpty())
        assertTrue(a.typingIn(ConversationId("grp_unknownunkno"), at).isEmpty())

        // The answer ends the note at once, and so does a message from someone typing.
        server.pushTo(alice, info("kp", group, bob))
        eventually { a.typingIn(g, at).size == 2 }
        server.pushTo(alice, groupData(ChatMessage.AI_ASSISTANT_UID, 1, "hi"))
        eventually { a.typingIn(g, at) == setOf(bob) }
        server.pushTo(alice, groupData(bob, 2, "thanks"))
        eventually { a.typingIn(g, at).isEmpty() }
    }

    private fun groupData(from: String, seq: Long, content: String) = jsonObject {
        put("action", "data"); put("topic", group); put("from", from); put("seq", seq); put("ts", 5_000 + seq)
        put("content", content); put("contentType", "text")
    }

    /** ChatRepositoryTest, ChatIntegrationTest: a read note moves the cursor it belongs to, never backwards. */
    @Test
    fun readNotesMoveTheRightCursor() = runBlocking {
        val g = ConversationId(group)
        val a = engine()
        a.startAndSync {
            ensureConversation(withBob)
            ensureConversation(g)
        }
        server.pushTo(alice, info("read", bob, bob, 3))
        eventually { runBlocking { store.conversation(withBob)?.peerReadSeq } == 3L }
        server.pushTo(alice, info("read", bob, bob, 2)) // older: ignored
        server.pushTo(alice, info("read", bob, alice, 5)) // me, on another device
        eventually { runBlocking { store.conversation(withBob)?.lastReadSeq } == 5L }
        assertEquals("the peer's position is the peer's", 3L, store.conversation(withBob)?.peerReadSeq)

        // What the UI reads: the summary for direct chats, the live cursors for groups.
        eventually { a.conversations.value.firstOrNull { it.id == withBob }?.peerReadSeq == 3L }
        assertEquals(5L, a.conversations.value.first { it.id == withBob }.lastReadSeq)
        val cursors = a.readCursors(g)
        server.pushTo(alice, info("read", group, bob, 4))
        eventually { runBlocking { store.readCursors(g) } == mapOf(bob to 4L) }
        eventually { cursors.value == mapOf(bob to 4L) }
        assertEquals(0L, store.conversation(g)?.peerReadSeq)
        assertTrue(a.readCursors(withBob).value.isEmpty())
    }
}
