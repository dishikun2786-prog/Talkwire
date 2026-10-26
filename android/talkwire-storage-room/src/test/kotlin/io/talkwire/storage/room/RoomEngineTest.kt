package io.talkwire.storage.room

import android.content.Context
import androidx.test.core.app.ApplicationProvider
import io.talkwire.core.ImIds
import io.talkwire.core.client.ImClient
import io.talkwire.core.client.ImClientConfig
import io.talkwire.core.client.TokenProvider
import io.talkwire.core.engine.ChatEngine
import io.talkwire.core.engine.ChatEvent
import io.talkwire.core.engine.EngineConfig
import io.talkwire.core.engine.OutgoingMessage
import io.talkwire.core.model.ChatMessage
import io.talkwire.core.model.ConversationId
import io.talkwire.core.model.MessageStatus
import io.talkwire.core.store.StoredConversation
import io.talkwire.core.testing.MiniServer
import io.talkwire.core.testing.eventually
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import java.util.concurrent.CopyOnWriteArrayList
import kotlin.random.Random

/** The chat engine on the Room store: C4 on a real database, and history that outlives the process. */
@RunWith(RobolectricTestRunner::class)
class RoomEngineTest {
    private val ctx = ApplicationProvider.getApplicationContext<Context>()
    private val scope = CoroutineScope(Dispatchers.Default + SupervisorJob())
    private val server = MiniServer()
    private val alice = ImIds.uid("alice")
    private val bob = ImIds.uid("bob")
    private val withBob = ConversationId(bob)
    private val opened = mutableListOf<RoomChatStore>()
    private val clients = mutableListOf<ImClient>()

    @After
    fun tearDown() {
        clients.forEach { it.stop() }
        scope.cancel()
        opened.forEach { it.close() }
        ctx.deleteDatabase(FILE)
    }

    private fun store() = RoomChatStore.open(ctx, FILE, TestKeyWrapper()).also { opened += it }

    private fun engine(user: String, store: RoomChatStore): ChatEngine {
        val client = ImClient(
            ImClientConfig(
                endpoints = listOf("ws://mini"),
                tokenProvider = TokenProvider { user },
                transport = server.transport,
                loginTimeoutMs = 1_000,
                requestTimeoutMs = 1_000,
                publishTimeoutMs = 500,
                reconnectBaseDelayMs = 20,
                reconnectMaxDelayMs = 40,
                random = Random(3),
            ),
            scope,
        ).also { clients += it }
        return ChatEngine(client, store, user, scope, EngineConfig(pageSize = 3))
    }

    private fun ChatEngine.startAndSync(prepare: suspend ChatEngine.() -> Unit = {}) = runBlocking {
        val synced = CopyOnWriteArrayList<ChatEvent>()
        scope.launch(start = CoroutineStart.UNDISPATCHED) { events.collect { if (it is ChatEvent.Synced) synced += it } }
        open()
        prepare()
        client.start()
        eventually { synced.isNotEmpty() }
    }

    /** On the real database: another account's tables, keys included, are emptied; the same account's are kept. */
    @Test
    fun C4_SwitchingAccountClearsAllCaches() = runBlocking {
        val withAlice = ConversationId(alice)
        val store = store()
        store.setOwner(bob)
        store.putConversation(StoredConversation(withAlice, draft = "bob's draft"))
        store.putMessage(ChatMessage(withAlice, 1, null, alice, "bob's history", "text", ts = 1, status = MessageStatus.RECEIVED))
        store.putMessage(ChatMessage(withAlice, null, "p1", bob, "bob's pending", "text", ts = 2, status = MessageStatus.FAILED))
        store.addRecalled(withAlice, 3)
        store.setReadCursor(ConversationId("grp_gggggggggggg"), alice, 4)
        engine("alice", store).open()
        assertEquals(alice, store.owner())
        val db = store.database.openHelper.readableDatabase
        for (table in listOf("messages", "pending_messages", "conversations", "recalled_seqs", "clear_states", "read_cursors", "content_keys")) {
            assertEquals("$table is empty", 0, db.query("SELECT count(*) FROM $table").use { it.moveToFirst(); it.getInt(0) })
        }

        // Alice signs in again: her own data stays.
        store.putConversation(StoredConversation(withBob, draft = "mine"))
        store.putMessage(ChatMessage(withBob, 1, null, bob, "kept", "text", ts = 1, status = MessageStatus.RECEIVED))
        engine("alice", store).open()
        assertEquals(listOf(1L), store.seqs(withBob))
        assertEquals("mine", store.conversation(withBob)!!.draft)
    }

    @Test
    fun historySurvivesARestartAndSyncResumesWhereItStopped() {
        val topic = server.canonical(alice, bob)
        for (i in 1..2) server.inject(topic, bob, "m$i")
        val first = store()
        val a = engine("alice", first)
        a.startAndSync { ensureConversation(withBob) }
        runBlocking {
            a.send(withBob, OutgoingMessage("mine"))
            eventually { runBlocking { first.seqs(withBob) } == listOf(1L, 2L, 3L) }
            a.client.stop()
            a.close()
            first.close()
        }

        // While the app is gone: four more messages, more than one page.
        for (i in 4..7) server.inject(topic, bob, "m$i")
        server.requests.clear()
        val second = store()
        val again = engine("alice", second)
        again.startAndSync()
        runBlocking {
            assertEquals((1L..7L).toList(), second.seqs(withBob))
            assertEquals("mine", second.message(withBob, 3)!!.content)
            assertEquals(MessageStatus.SENT, second.message(withBob, 3)!!.status)
        }
        val pulls = server.requests.filter { it.string("action") == "get" && it.string("topic") == bob }
        assertEquals("resumed after the last held seq", 3L, pulls.first().long("afterSeq"))
    }

    private companion object {
        const val FILE = "engine-test.db"
    }
}
