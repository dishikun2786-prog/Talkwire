package io.imkit.storage.room.legacy

import android.content.ContentValues
import android.content.Context
import android.database.sqlite.SQLiteDatabase
import androidx.test.core.app.ApplicationProvider
import io.imkit.core.ImIds
import io.imkit.core.json.Json
import io.imkit.core.json.JsonObject
import io.imkit.core.model.ChatMessage
import io.imkit.core.model.ConversationId
import io.imkit.core.model.MessageStatus
import io.imkit.core.store.ClearState
import io.imkit.storage.room.RoomChatStore
import io.imkit.storage.room.TestKeyWrapper
import kotlinx.coroutines.runBlocking
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import java.io.File

/**
 * The importer against a database built from the source app's exported v18
 * schema (src/test/resources/legacy/chat_cache-v18.json), so the column names
 * it reads are the real ones.
 */
@RunWith(RobolectricTestRunner::class)
class LegacyChatImporterTest {
    private val ctx = ApplicationProvider.getApplicationContext<Context>()
    private val appUserId = "alice-app"
    private val me = ImIds.uid(appUserId)
    private val peer = ImIds.uid("bob-app")
    private val carol = ImIds.uid("carol-app") // deleted by the user
    private val dave = ImIds.uid("dave-app") // only in known_topics
    private val grp = "grp_abcdefghijkl"
    private val usr = "usrAbCdEfGhIjK" // the legacy uid form: still a direct chat
    private val p2p = "p2p_abcdefghijk" // a canonical p2p topic: not how the source keyed chats
    private lateinit var file: File
    private lateinit var store: RoomChatStore

    /** The source app's sealed format is its own; the test seals as "ENC(text)". */
    private val decryptor = LegacyContentDecryptor { _, stored ->
        if (stored.startsWith("ENC(") && stored.endsWith(")")) stored.substring(4, stored.length - 1) else null
    }

    @Before
    fun setUp() {
        file = File(ctx.cacheDir, "chat_cache.db").also { it.delete() }
        store = RoomChatStore.inMemory(ctx, TestKeyWrapper())
    }

    @After
    fun tearDown() {
        store.close()
        SQLiteDatabase.deleteDatabase(file)
    }

    private fun createV18(version: Int = 18, owner: String? = appUserId, sealed: Boolean = true, fill: SQLiteDatabase.() -> Unit = { sample(sealed) }) {
        val schema = Json.parseObject(javaClass.getResourceAsStream("/legacy/chat_cache-v18.json")!!.reader().readText())
        val db = SQLiteDatabase.openOrCreateDatabase(file, null)
        try {
            for (e in schema.obj("database")!!.array("entities")!!.items.map { it as JsonObject }) {
                val table = e.string("tableName")!!
                db.execSQL(e.string("createSql")!!.replace("\${TABLE_NAME}", table))
                e.array("indices")?.items?.forEach { idx -> db.execSQL((idx as JsonObject).string("createSql")!!.replace("\${TABLE_NAME}", table)) }
            }
            db.version = version
            owner?.let { db.insertOrThrow("chat_cache_owner", null, ContentValues().apply { put("id", 0); put("userId", it); put("updatedAt", 1L) }) }
            db.fill()
        } finally {
            db.close()
        }
    }

    private fun SQLiteDatabase.message(
        topic: String,
        seq: Long?,
        from: String,
        content: String,
        status: String,
        localId: String = "",
        contentType: String = "text",
        extra: String? = null,
        ts: Long = 1_000 + (seq ?: 500),
        configure: ContentValues.() -> Unit = {},
    ) {
        insertOrThrow(
            "chat_messages",
            null,
            ContentValues().apply {
                put("stableKey", "$topic-${seq ?: localId.ifEmpty { "x$ts" }}")
                put("localId", localId)
                if (seq != null) put("serverSeq", seq) else putNull("serverSeq")
                put("topicId", topic)
                put("fromUser", from)
                put("content", content)
                put("contentType", contentType)
                put("timestamp", ts)
                put("status", status)
                put("duration", 0)
                put("extra", extra)
                put("senderKind", "user")
                configure()
            },
        )
    }

    private fun SQLiteDatabase.sample(sealed: Boolean) {
        if (sealed) insertOrThrow("content_keys", null, ContentValues().apply { put("topicId", peer); put("wrappedIv", byteArrayOf(1)); put("wrappedCiphertext", byteArrayOf(2)); put("createdAt", 1L) })
        fun s(text: String) = if (sealed) "ENC($text)" else text
        message(peer, 1, peer, s("hello"), "RECEIVED")
        message(peer, 2, me, s("hi"), "SENT", localId = "cid-2")
        message(peer, null, me, s("retry me"), "FAILED", localId = "cid-3")
        message(peer, null, me, s("no id"), "SENDING", localId = "")
        message(peer, null, peer, s("odd"), "RECEIVED", localId = "x")
        message(grp, 3, peer, "below the watermark", "RECEIVED")
        message(grp, 5, peer, "group hello", "RECEIVED", contentType = "image", extra = """{"w":10}""") {
            put("imageUrl", "https://m/a.png")
            put("imageName", "a.png")
            put("duration", 7)
            put("forwardFromMsgId", 2L)
            put("forwardFromTopic", "grp_zzzzzzzzzzzz")
            put("senderKind", "ai_assistant")
        }
        message(grp, 6, peer, "someone joined", "RECEIVED", contentType = "system")
        message(grp, 7, peer, "latest", "RECEIVED")
        message(usr, 1, usr, "legacy uid", "RECEIVED")
        message(p2p, 1, peer, "unmappable", "RECEIVED")
        message(carol, 1, carol, "deleted chat", "RECEIVED")
        message(me, 1, peer, "addressed to my uid", "RECEIVED")
        insertOrThrow("topic_clear_state", null, ContentValues().apply { put("topicId", grp); put("clearVersion", 2); put("clearBeforeSeq", 4); put("updatedAt", 1L) })
        insertOrThrow("recalled_seqs", null, ContentValues().apply { put("topicId", grp); put("seq", 2) })
        insertOrThrow("recalled_seqs", null, ContentValues().apply { put("topicId", peer); put("seq", 9) })
        insertOrThrow("read_cursors", null, ContentValues().apply { put("topicId", grp); put("uid", peer); put("lastReadSeq", 5); put("updatedAt", 1L) })
        fun conv(topic: String, name: String, unread: Int, group: Boolean, pinned: Boolean = false, muted: Boolean = false) =
            insertOrThrow(
                "cached_conversations",
                null,
                ContentValues().apply {
                    put("topicId", topic); put("peerName", name); put("peerAvatarUrl", null as String?); put("lastMessage", "")
                    put("lastTime", 9_000L); put("unreadCount", unread); put("isGroup", group); put("isPinned", pinned); put("isMuted", muted); put("updatedAt", 1L)
                },
            )
        conv(peer, "Bob", unread = 1, group = false, pinned = true)
        conv(grp, "Team", unread = 1, group = true, muted = true)
        conv(carol, "Carol", unread = 0, group = false)
        insertOrThrow("known_topics", null, ContentValues().apply { put("topicId", dave); put("updatedAt", 1L) })
        insertOrThrow("deleted_conversations", null, ContentValues().apply { put("topicId", carol); put("deletedAt", 1L) })
    }

    private fun import(decrypt: LegacyContentDecryptor? = decryptor) = runBlocking { LegacyChatImporter(file, decrypt).importInto(store, appUserId) }

    @Test
    fun importsTheHistoryItCanMap() = runBlocking {
        createV18()
        val r = import()
        assertTrue(r.reason, r.imported)
        assertEquals(6, r.messages)
        assertEquals(1, r.pending)
        assertEquals(setOf(p2p, me), r.skippedTopics)
        assertEquals(listOf(1L), store.seqs(ConversationId(usr)))
        assertEquals(me, store.owner())

        val direct = ConversationId(peer)
        assertEquals(listOf(1L, 2L), store.seqs(direct))
        store.message(direct, 1)!!.let {
            assertEquals("hello", it.content)
            assertEquals(MessageStatus.RECEIVED, it.status)
            assertNull(it.localId)
        }
        store.message(direct, 2)!!.let {
            assertEquals("hi", it.content)
            assertEquals(MessageStatus.SENT, it.status)
            assertEquals("cid-2", it.localId)
        }
        val failed = store.pendingMessages().single()
        assertEquals("cid-3", failed.localId)
        assertEquals(MessageStatus.FAILED, failed.status)
        assertEquals("retry me", failed.content)

        val group = ConversationId(grp)
        assertEquals("below the watermark is gone", listOf(5L, 6L, 7L), store.seqs(group))
        val five = store.message(group, 5)!!
        assertEquals(
            ChatMessage(
                group, 5, null, peer, "group hello", "image", "https://m/a.png", "a.png", 7, Json.parse("""{"w":10}"""),
                ChatMessage.SENDER_AI, 2, "grp_zzzzzzzzzzzz", 1_005, MessageStatus.RECEIVED,
            ),
            five,
        )
        assertEquals(ClearState(2, 4), store.clearState(group))
        assertEquals(listOf(2L), store.recalledSeqs(group))
        assertEquals(listOf(9L), store.recalledSeqs(direct))
        assertEquals(mapOf(peer to 5L), store.readCursors(group))

        val bob = store.conversation(direct)!!
        assertTrue(bob.pinned)
        assertNull(bob.title)
        assertEquals("one unread kept", 1, store.countUnread(direct, bob.lastReadSeq, me))
        val team = store.conversation(group)!!
        assertEquals("Team", team.title)
        assertTrue(team.muted)
        assertEquals("one unread kept, the system message not counted", 1, store.countUnread(group, team.lastReadSeq, me))
        assertTrue("known topic becomes a conversation", store.conversation(ConversationId(dave)) != null)
        assertNull("deleted stays deleted", store.conversation(ConversationId(carol)))
        assertTrue(store.seqs(ConversationId(carol)).isEmpty())
    }

    @Test
    fun runningItTwiceWritesTheSameRows() = runBlocking {
        createV18()
        val first = import()
        val second = import()
        assertEquals(first, second)
        assertEquals(listOf(1L, 2L), store.seqs(ConversationId(peer)))
        assertEquals(1, store.pendingMessages().size)
    }

    @Test
    fun plainLegacyTextNeedsNoDecryptor() = runBlocking {
        createV18(sealed = false)
        assertTrue(import(decrypt = null).imported)
        assertEquals("hello", store.message(ConversationId(peer), 1)!!.content)
    }

    @Test
    fun refusesSealedTextWithoutADecryptor() {
        createV18()
        val r = import(decrypt = null)
        assertFalse(r.imported)
        assertTrue(r.reason!!.contains("sealed"))
        assertNull(runBlocking { store.owner() })
    }

    @Test
    fun refusesAnotherAccountsDatabase() {
        createV18(owner = "mallory-app")
        val r = import()
        assertFalse(r.imported)
        assertTrue(runBlocking { store.conversations().isEmpty() })
    }

    @Test
    fun refusesADatabaseWithoutOwner() {
        createV18(owner = null)
        assertFalse(import().imported)
    }

    @Test
    fun refusesOtherVersions() {
        createV18(version = 17)
        val r = import()
        assertFalse(r.imported)
        assertTrue(r.reason!!.contains("17"))
    }

    @Test
    fun refusesAStoreThatBelongsToSomeoneElse() {
        createV18()
        runBlocking { store.setOwner(ImIds.uid("someone-else")) }
        assertFalse(import().imported)
        assertEquals(0, runBlocking { store.conversations().size })
    }

    @Test
    fun noFileNoImport() {
        val r = import()
        assertFalse(r.imported)
        assertEquals("no legacy database", r.reason)
    }
}
