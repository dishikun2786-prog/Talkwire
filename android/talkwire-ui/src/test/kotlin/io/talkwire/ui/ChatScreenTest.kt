package io.talkwire.ui

import android.content.Context
import androidx.compose.foundation.layout.RowScope
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onAllNodesWithTag
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.hasText
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performScrollToNode
import androidx.compose.ui.test.performTextInput
import androidx.compose.ui.test.performTextReplacement
import androidx.test.core.app.ApplicationProvider
import io.talkwire.core.ImIds
import io.talkwire.core.client.ImClient
import io.talkwire.core.client.ImClientConfig
import io.talkwire.core.client.TokenProvider
import io.talkwire.core.engine.ChatEngine
import io.talkwire.core.engine.EngineConfig
import io.talkwire.core.engine.OutgoingMessage
import io.talkwire.core.json.jsonObject
import io.talkwire.core.model.ConversationId
import io.talkwire.core.model.GroupRole
import io.talkwire.core.store.InMemoryChatStore
import io.talkwire.core.testing.MiniServer
import io.talkwire.ui.host.ImDeepLink
import io.talkwire.ui.host.ImProfile
import io.talkwire.ui.host.ImUiHost
import io.talkwire.ui.screens.ImChatApp
import io.talkwire.ui.screens.rememberImChatState
import io.talkwire.ui.theme.ImTheme
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.runBlocking
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import java.util.concurrent.atomic.AtomicLong
import kotlin.random.Random

/** The chat screen against an engine on the in-memory server. */
@RunWith(RobolectricTestRunner::class)
class ChatScreenTest {
    @get:Rule
    val rule = createComposeRule()

    private val server = MiniServer()
    private val scope = CoroutineScope(Dispatchers.Default + SupervisorJob())

    @After
    fun tearDown() = scope.cancel()

    private fun engine(user: String, seed: Int, config: EngineConfig = EngineConfig()): ChatEngine = runBlocking {
        val client = ImClient(
            ImClientConfig(endpoints = listOf("ws://mini"), tokenProvider = TokenProvider { user }, transport = server.transport, random = Random(seed)),
            scope,
        )
        ChatEngine(client, InMemoryChatStore(), user, scope, config).also {
            it.open()
            it.start()
            client.awaitConnected(5_000)
        }
    }

    private object Host : ImUiHost {
        override val contacts: StateFlow<List<ImProfile>> = MutableStateFlow(emptyList())
        override suspend fun profiles(uids: Collection<String>): Map<String, ImProfile> = emptyMap()
    }

    /** Found on a phone: only the messages there at opening were marked read. */
    @Test
    fun aMessageThatArrivesWhileTheChatIsOpenIsRead() {
        val alice = engine("alice", 1)
        val bob = engine("bob", 2)
        val withBob = ConversationId(server.uidOf("bob"))
        val withAlice = ConversationId(server.uidOf("alice"))
        runBlocking {
            alice.ensureConversation(withBob)
            bob.ensureConversation(withAlice)
        }
        rule.setContent {
            ImTheme {
                ImChatApp(rememberImChatState(alice, Host, start = ImDeepLink.Conversation(withBob)))
            }
        }
        rule.waitForIdle()

        runBlocking { bob.send(withAlice, OutgoingMessage("while you look")) }
        rule.waitUntil(5_000) { alice.messages(withBob).value.any { it.content == "while you look" } }
        rule.onNodeWithText("while you look").assertExists()
        rule.waitUntil(5_000) { alice.conversations.value.firstOrNull { it.id == withBob }?.unread == 0 }

        val seq = alice.messages(withBob).value.first { it.content == "while you look" }.seq!!
        assertEquals(seq, alice.conversations.value.first { it.id == withBob }.lastReadSeq)
        rule.waitUntil(5_000) { server.requests.any { it.string("action") == "note" && it.string("what") == "read" } }
        assertTrue(server.requests.any { it.string("action") == "note" && it.string("what") == "read" && it.long("seq") == seq })
    }

    /** Found on a phone: after an account switch only groups came back, not direct chats. */
    @Test
    fun aFreshDeviceFindsDirectChatsThroughTheContacts() {
        val bobUid = server.uidOf("bob")
        server.inject(ImIds.p2pTopic(server.uidOf("alice"), bobUid), bobUid, "from before")
        val alice = engine("alice", 3)
        val host = object : ImUiHost {
            override val contacts: StateFlow<List<ImProfile>> = MutableStateFlow(listOf(ImProfile(bobUid, "Bob")))
            override suspend fun profiles(uids: Collection<String>): Map<String, ImProfile> = emptyMap()
        }
        rule.setContent { ImTheme { ImChatApp(rememberImChatState(alice, host)) } }
        rule.waitUntil(5_000) { alice.conversations.value.any { it.id.id == bobUid && it.lastMessage?.content == "from before" } }
        rule.onNodeWithText("from before").assertExists()
    }

    /** Found on a phone: "typing…" stayed for good once the other person had typed. */
    @Test
    fun typingGoesAwayOnceTheNoteIsTooOld() {
        val now = AtomicLong(1_000_000)
        val alice = engine("alice", 4, EngineConfig(typingTtlMs = 10_000, clock = { now.get() }))
        val bob = engine("bob", 5)
        val withBob = ConversationId(server.uidOf("bob"))
        val withAlice = ConversationId(server.uidOf("alice"))
        runBlocking {
            alice.ensureConversation(withBob)
            bob.ensureConversation(withAlice)
        }
        rule.setContent {
            ImTheme {
                ImChatApp(rememberImChatState(alice, Host, start = ImDeepLink.Conversation(withBob)))
            }
        }
        rule.waitForIdle()
        val typing = ApplicationProvider.getApplicationContext<Context>().getString(R.string.talkwire_typing)

        bob.sendTyping(withAlice)
        rule.waitUntil(5_000) { alice.typingIn(withBob).isNotEmpty() }
        rule.waitUntil(5_000) { rule.onAllNodesWithText(typing).fetchSemanticsNodes().isNotEmpty() }

        // Still within the limit: the indicator stays.
        now.addAndGet(9_000)
        rule.mainClock.advanceTimeBy(1_500)
        rule.waitForIdle()
        assertTrue(rule.onAllNodesWithText(typing).fetchSemanticsNodes().isNotEmpty())

        // Past it, with nothing new from Bob: it goes on its own.
        now.addAndGet(1_500)
        rule.mainClock.advanceTimeBy(1_500)
        rule.waitUntil(5_000) { rule.onAllNodesWithText(typing).fetchSemanticsNodes().isEmpty() }
    }

    /** Protocol §15: the group assistant shows by name, not as a bare uid. */
    @Test
    fun theGroupAssistantTypesByName() {
        val group = ConversationId("grp_aiaiaiaiaiai")
        val aliceUid = server.uidOf("alice")
        server.groups[group.id] = MiniServer.Group("team", mutableMapOf(aliceUid to "owner"))
        val alice = engine("alice", 6)
        runBlocking { alice.ensureConversation(group) }
        rule.setContent {
            ImTheme {
                ImChatApp(rememberImChatState(alice, Host, start = ImDeepLink.Conversation(group)))
            }
        }
        rule.waitForIdle()
        val context = ApplicationProvider.getApplicationContext<Context>()
        val text = context.getString(R.string.talkwire_typing_named, context.getString(R.string.talkwire_assistant))

        server.pushTo(aliceUid, jsonObject { put("type", "ai_typing"); put("topic", group.id) })
        rule.waitUntil(5_000) { rule.onAllNodesWithText(text).fetchSemanticsNodes().isNotEmpty() }
    }

    /** The slots optional modules use: the top bar of a chat, and a group's info with the user's role. */
    @Test
    fun theHostAddsToTheTopBarAndTheGroupInfo() {
        val group = ConversationId("grp_slotslotslot")
        val aliceUid = server.uidOf("alice")
        server.groups[group.id] = MiniServer.Group("team", mutableMapOf(aliceUid to "owner"))
        val alice = engine("alice", 7)
        val withBob = ConversationId(server.uidOf("bob"))
        runBlocking {
            alice.ensureConversation(withBob)
            alice.ensureConversation(group)
        }
        val host = object : ImUiHost {
            override val contacts: StateFlow<List<ImProfile>> = MutableStateFlow(emptyList())
            override suspend fun profiles(uids: Collection<String>): Map<String, ImProfile> = emptyMap()
            override val topBarExtras: (@Composable RowScope.(ConversationId) -> Unit) = { id -> Text("bar:${id.id}") }
            override val groupInfoExtras: (@Composable (String, GroupRole) -> Unit) = { topic, role -> Text("info:$topic:$role") }
        }
        val link = MutableStateFlow<ImDeepLink>(ImDeepLink.Conversation(withBob))
        rule.setContent {
            ImTheme {
                val state = rememberImChatState(alice, host)
                val l by link.collectAsState()
                LaunchedEffect(l) { state.open(l) }
                ImChatApp(state)
            }
        }
        rule.waitUntil(5_000) { rule.onAllNodesWithText("bar:${withBob.id}").fetchSemanticsNodes().isNotEmpty() }

        link.value = ImDeepLink.GroupInfo(group.id)
        // The role comes from the member list, so it is the owner's once that has loaded.
        rule.waitUntil(5_000) { rule.onAllNodesWithText("Owner").fetchSemanticsNodes().isNotEmpty() }
        // Below the members, so off this small screen until scrolled to.
        rule.onNodeWithTag("talkwire_info").performScrollToNode(hasText("info:${group.id}:OWNER"))
        rule.onNodeWithText("info:${group.id}:OWNER").assertExists()
    }

    /** The list's search box finds chats by name and messages by their words, and opens the chat. */
    @Test
    fun searchFindsChatsAndMessages() {
        val alice = engine("alice", 10)
        val bob = engine("bob", 11)
        val withBob = ConversationId(server.uidOf("bob"))
        val withAlice = ConversationId(server.uidOf("alice"))
        runBlocking {
            alice.ensureConversation(withBob)
            bob.ensureConversation(withAlice)
            bob.send(withAlice, OutgoingMessage("Lunch at noon?"))
            bob.send(withAlice, OutgoingMessage("unrelated"))
        }
        rule.waitUntil(5_000) { alice.messages(withBob).value.count { it.seq != null } == 2 }
        val host = object : ImUiHost {
            override val contacts: StateFlow<List<ImProfile>> = MutableStateFlow(emptyList())
            override suspend fun profiles(uids: Collection<String>): Map<String, ImProfile> =
                uids.associateWith { ImProfile(it, if (it == server.uidOf("bob")) "Bobby" else "Someone") }
        }
        rule.setContent { ImTheme { ImChatApp(rememberImChatState(alice, host)) } }
        rule.waitUntil(5_000) { rule.onAllNodesWithText("Bobby").fetchSemanticsNodes().isNotEmpty() }

        // By name: the chat, no message section.
        rule.onNodeWithTag("talkwire_search").performTextInput("bob")
        rule.waitUntil(5_000) { rule.onAllNodesWithTag("talkwire_search_results").fetchSemanticsNodes().isNotEmpty() }
        rule.onNodeWithTag("talkwire_search_results").performScrollToNode(hasText("Bobby"))
        Thread.sleep(400)
        rule.waitForIdle()
        assertTrue(rule.onAllNodesWithText("Messages").fetchSemanticsNodes().isEmpty())

        rule.onNodeWithTag("talkwire_search").performTextReplacement("zzz")
        rule.waitUntil(5_000) { rule.onAllNodesWithText("No results").fetchSemanticsNodes().isNotEmpty() }

        // By words: the message, opening its chat.
        rule.onNodeWithTag("talkwire_search").performTextReplacement("LUNCH")
        rule.waitUntil(5_000) { rule.onAllNodesWithText("Messages").fetchSemanticsNodes().isNotEmpty() }
        rule.onNodeWithText("Lunch at noon?").performClick()
        rule.waitUntil(5_000) { rule.onAllNodesWithTag("talkwire_search").fetchSemanticsNodes().isEmpty() }
        rule.onNodeWithText("Lunch at noon?").assertExists()
    }

    /** A recalled message leaves a note saying who unsent it, on both sides and in the list. */
    @Test
    fun aRecallLeavesANoteSayingWho() {
        val alice = engine("alice", 8)
        val bob = engine("bob", 9)
        val withBob = ConversationId(server.uidOf("bob"))
        val withAlice = ConversationId(server.uidOf("alice"))
        runBlocking {
            alice.ensureConversation(withBob)
            bob.ensureConversation(withAlice)
        }
        val host = object : ImUiHost {
            override val contacts: StateFlow<List<ImProfile>> = MutableStateFlow(emptyList())
            override suspend fun profiles(uids: Collection<String>): Map<String, ImProfile> =
                uids.associateWith { ImProfile(it, if (it == server.uidOf("alice")) "Alice" else "Someone") }
        }
        rule.setContent {
            ImTheme { ImChatApp(rememberImChatState(bob, host, start = ImDeepLink.Conversation(withAlice))) }
        }
        val sent = runBlocking { alice.send(withBob, OutgoingMessage("wrong chat")) }
        rule.waitUntil(5_000) { rule.onAllNodesWithText("wrong chat").fetchSemanticsNodes().isNotEmpty() }
        val stored = run {
            var m: io.talkwire.core.model.ChatMessage? = null
            rule.waitUntil(5_000) { m = alice.messages(withBob).value.firstOrNull { it.seq != null && it.content == "wrong chat" }; m != null }
            m!!
        }
        runBlocking { alice.recall(stored) }
        val context = ApplicationProvider.getApplicationContext<Context>()
        val note = context.getString(R.string.talkwire_recalled_by, "Alice")
        rule.waitUntil(5_000) { rule.onAllNodesWithText(note).fetchSemanticsNodes().isNotEmpty() }
        assertTrue(rule.onAllNodesWithText("wrong chat").fetchSemanticsNodes().isEmpty())
        // On Alice's side the tombstone is hers; nobody counts it as unread.
        assertTrue(alice.messages(withBob).value.single { it.seq == stored.seq }.isRecalled)
        rule.waitUntil(5_000) { bob.conversations.value.firstOrNull { it.id == withAlice }?.lastMessage?.isRecalled == true }
        assertEquals(0, bob.conversations.value.first { it.id == withAlice }.unread)
        assertEquals(sent.localId, stored.localId)
    }
}
