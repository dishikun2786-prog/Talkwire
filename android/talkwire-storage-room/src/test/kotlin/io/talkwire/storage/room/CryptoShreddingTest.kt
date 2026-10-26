package io.talkwire.storage.room

import android.content.Context
import androidx.test.core.app.ApplicationProvider
import io.talkwire.core.json.Json
import io.talkwire.core.model.ChatMessage
import io.talkwire.core.model.ConversationId
import io.talkwire.core.model.MessageStatus
import io.talkwire.core.store.StoredConversation
import kotlinx.coroutines.runBlocking
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner

@RunWith(RobolectricTestRunner::class)
class CryptoShreddingTest {
    private val ctx = ApplicationProvider.getApplicationContext<Context>()
    private val a = ConversationId("uid_aaaaaaaaaaa")
    private val g = ConversationId("grp_gggggggggggg")
    private val opened = mutableListOf<RoomChatStore>()

    @After
    fun tearDown() {
        opened.forEach { it.close() }
        ctx.deleteDatabase(FILE)
    }

    private fun memory(wrapper: KeyWrapper? = TestKeyWrapper()) = RoomChatStore.inMemory(ctx, wrapper).also { opened += it }

    private fun file(wrapper: KeyWrapper = TestKeyWrapper()) = RoomChatStore.open(ctx, FILE, wrapper).also { opened += it }

    private fun msg(c: ConversationId, seq: Long, text: String, extra: String? = null) =
        ChatMessage(c, seq, null, "uid_peeeeeeeeee", text, "text", extra = extra?.let { Json.parse(it) }, ts = seq, status = MessageStatus.RECEIVED)

    private fun RoomChatStore.column(sql: String): List<String?> =
        database.openHelper.readableDatabase.query(sql).use { c ->
            buildList { while (c.moveToNext()) add(if (c.isNull(0)) null else c.getString(0)) }
        }

    private fun RoomChatStore.keyRows(c: ConversationId): List<String?> =
        column("SELECT hex(ciphertext) FROM content_keys WHERE conversation = '${c.id}'")

    @Test
    fun textExtraAndDraftAreSealedAtRest() = runBlocking {
        val store = memory()
        store.putMessage(msg(a, 1, "top secret", """{"amount":5}"""))
        store.putConversation(StoredConversation(a, draft = "unsent draft"))
        val content = store.column("SELECT content FROM messages").single()!!
        val extra = store.column("SELECT extra FROM messages").single()!!
        val draft = store.column("SELECT draft FROM conversations").single()!!
        for (raw in listOf(content, extra, draft)) assertTrue(raw, raw.startsWith("v1:"))
        assertFalse(content.contains("secret"))
        assertFalse(draft.contains("draft"))
        assertEquals("top secret", store.message(a, 1)!!.content)
        assertEquals(Json.parse("""{"amount":5}"""), store.message(a, 1)!!.extra)
        assertEquals("unsent draft", store.conversation(a)!!.draft)
        assertEquals(1, store.keyRows(a).size)
    }

    @Test
    fun deletingAConversationDestroysOnlyItsKey() = runBlocking {
        val store = memory()
        store.putMessage(msg(a, 1, "for a"))
        store.putMessage(msg(g, 1, "for g"))
        store.deleteConversation(a)
        assertTrue(store.keyRows(a).isEmpty())
        assertEquals(1, store.keyRows(g).size)
        assertEquals("for g", store.message(g, 1)!!.content)
    }

    @Test
    fun deletingAllMessagesDestroysTheKeyAndKeepsTheDraft() = runBlocking {
        val store = memory()
        store.putMessage(msg(a, 1, "old"))
        store.putConversation(StoredConversation(a, draft = "keep me"))
        val before = store.keyRows(a).single()
        store.deleteAllMessages(a)
        assertTrue(store.seqs(a).isEmpty())
        val after = store.keyRows(a).single()
        assertNotEquals("a new key seals the carried-over draft", before, after)
        assertEquals("keep me", store.conversation(a)!!.draft)
    }

    @Test
    fun aPartialClearKeepsTheKey() = runBlocking {
        val store = memory()
        for (s in 1L..3L) store.putMessage(msg(a, s, "m$s"))
        val key = store.keyRows(a).single()
        store.deleteThrough(a, 2)
        assertEquals(key, store.keyRows(a).single())
        assertEquals("m3", store.message(a, 3)!!.content)
    }

    @Test
    fun wipeDestroysEveryKey() = runBlocking {
        val store = memory()
        store.putMessage(msg(a, 1, "x"))
        store.putMessage(msg(g, 1, "y"))
        store.wipe()
        assertTrue(store.column("SELECT conversation FROM content_keys").isEmpty())
        store.putMessage(msg(a, 1, "after the wipe"))
        assertEquals("after the wipe", store.message(a, 1)!!.content)
    }

    @Test
    fun withTheKeyGoneTheTextReadsEmptyNotAsCiphertext() = runBlocking {
        file().apply {
            putMessage(msg(a, 1, "gone for good", """{"k":1}"""))
            close()
        }
        file().apply {
            database.openHelper.writableDatabase.execSQL("DELETE FROM content_keys")
            close()
        }
        val reopened = file()
        val m = reopened.message(a, 1)!!
        assertEquals("", m.content)
        assertEquals(null, m.extra)
    }

    @Test
    fun aFailingWrapperFallsBackToPlainText() = runBlocking {
        val store = memory(TestKeyWrapper(failWrap = true))
        store.putMessage(msg(a, 1, "still delivered"))
        assertEquals("still delivered", store.column("SELECT content FROM messages").single())
        assertEquals("still delivered", store.message(a, 1)!!.content)
    }

    @Test
    fun withoutAWrapperNothingIsSealed() = runBlocking {
        val store = memory(null)
        store.putMessage(msg(a, 1, "plain"))
        assertEquals("plain", store.column("SELECT content FROM messages").single())
        assertTrue(store.column("SELECT conversation FROM content_keys").isEmpty())
    }

    @Test
    fun deletesOverwriteOnTheWritingConnection() = runBlocking {
        val store = file()
        store.putMessage(msg(a, 1, "x")) // opens the database
        val db = store.database.openHelper.writableDatabase
        db.beginTransaction() // pins the primary connection, where deletes run
        try {
            assertEquals(1, db.query("PRAGMA secure_delete").use { it.moveToFirst(); it.getInt(0) })
        } finally {
            db.endTransaction()
        }
    }

    private companion object {
        const val FILE = "shredding-test.db"
    }
}
