package io.talkwire.storage.room.legacy

import android.database.Cursor
import android.database.sqlite.SQLiteDatabase
import io.talkwire.core.ImIds
import io.talkwire.core.json.Json
import io.talkwire.core.model.ChatMessage
import io.talkwire.core.model.ConversationId
import io.talkwire.core.model.MessageStatus
import io.talkwire.core.model.SendFailure
import io.talkwire.core.store.ChatStore
import io.talkwire.core.store.ClearState
import io.talkwire.core.store.StoredConversation
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import java.io.File

/**
 * Opens message text the source app sealed with its own content keys
 * (`content_keys`, wrapped by its Keystore key). Return null when it cannot be
 * opened; the stored text is then imported as it is, as the source app itself
 * did for rows written before it sealed anything.
 */
public fun interface LegacyContentDecryptor {
    public fun decrypt(topicId: String, stored: String): String?
}

/** What [LegacyChatImporter.importInto] did. When [imported] is false nothing was written and [reason] says why. */
public data class LegacyImportResult(
    val imported: Boolean,
    val reason: String?,
    val conversations: Int = 0,
    val messages: Int = 0,
    val pending: Int = 0,
    /** Topics with rows that were not imported: my own uid, or anything that is neither a user uid nor a grp_ topic. */
    val skippedTopics: Set<String> = emptySet(),
)

/**
 * Reads the source app's chat cache (`chat_cache.db`, Room schema version 18;
 * the exported schema is in src/test/resources/legacy/chat_cache-v18.json)
 * read-only and copies it into a [ChatStore], so an upgraded app starts with
 * the history it had. Run it once, after the user signed in and before the
 * chat engine is opened; record that it ran, and delete the old file with
 * `SQLiteDatabase.deleteDatabase` once it succeeded. Running it twice writes
 * the same rows again.
 *
 * Refuses (writes nothing) when the file is not version 18, when it belongs
 * to another account (`chat_cache_owner.userId` must be [appUserId]), when
 * the target store belongs to another account, or when the old text is sealed
 * and no [decryptor] was given.
 *
 * Mapping: direct chats are keyed by the peer's uid (`uid_…`, or the legacy
 * `usr…` form the server still accepts) and groups by their grp_ topic, as in
 * the source. Messages the server stored keep their seq; my own
 * unconfirmed ones (SENDING/FAILED) come back as failed, so the user decides
 * whether to send them again. Conversations the user deleted stay deleted. The
 * read position is derived from the old unread count.
 */
public class LegacyChatImporter(
    private val legacyDatabase: File,
    private val decryptor: LegacyContentDecryptor? = null,
) {
    public suspend fun importInto(store: ChatStore, appUserId: String): LegacyImportResult = withContext(Dispatchers.IO) {
        if (!legacyDatabase.isFile) return@withContext refuse("no legacy database")
        val db = SQLiteDatabase.openDatabase(legacyDatabase.path, null, SQLiteDatabase.OPEN_READONLY)
        try {
            run(db, store, appUserId)
        } finally {
            db.close()
        }
    }

    private fun refuse(reason: String) = LegacyImportResult(imported = false, reason = reason)

    private suspend fun run(db: SQLiteDatabase, store: ChatStore, appUserId: String): LegacyImportResult {
        if (db.version != VERSION) return refuse("unsupported legacy version ${db.version}")
        val owner = db.rawQuery("SELECT userId FROM chat_cache_owner WHERE id = 0", null).use { if (it.moveToFirst()) it.getString(0) else null }
        if (owner == null) return refuse("legacy database has no owner")
        if (owner != appUserId) return refuse("legacy database belongs to another account")
        val me = ImIds.uid(appUserId)
        val storeOwner = store.owner()
        if (storeOwner != null && storeOwner != me) return refuse("target store belongs to another account")
        val sealedTopics = strings(db, "SELECT topicId FROM content_keys").toSet()
        if (sealedTopics.isNotEmpty() && decryptor == null) return refuse("legacy content is sealed; a LegacyContentDecryptor is needed")
        if (storeOwner == null) store.setOwner(me)

        val deleted = strings(db, "SELECT topicId FROM deleted_conversations").toSet()
        val skipped = HashSet<String>()
        fun conversationOf(topic: String): ConversationId? {
            val c = when {
                ImIds.isGroupTopic(topic) -> ConversationId(topic)
                ImIds.isUserUid(topic) && topic != me -> ConversationId(topic)
                else -> null
            }
            if (c == null) skipped += topic
            return c?.takeIf { topic !in deleted }
        }

        val clears = HashMap<String, ClearState>()
        db.rawQuery("SELECT topicId, clearVersion, clearBeforeSeq FROM topic_clear_state", null).use { cur ->
            while (cur.moveToNext()) {
                val c = conversationOf(cur.getString(0)) ?: continue
                val state = ClearState(cur.getLong(1), cur.getLong(2))
                clears[c.id] = state
                store.setClearState(c, state)
            }
        }

        var messages = 0
        var pending = 0
        val touched = LinkedHashMap<ConversationId, Long>() // conversation -> latest ts
        db.rawQuery(
            "SELECT localId, serverSeq, topicId, fromUser, content, contentType, imageUrl, imageName, timestamp, status, " +
                "duration, extra, forwardFromMsgId, forwardFromTopic, senderKind FROM chat_messages",
            null,
        ).use { cur ->
            while (cur.moveToNext()) {
                val topic = cur.getString(2)
                val c = conversationOf(topic) ?: continue
                val seq = cur.longOrNull(1)
                val from = cur.getString(3)
                val status = cur.getString(9)
                val mine = from == me
                if (seq != null && seq <= (clears[c.id]?.clearBeforeSeq ?: 0)) continue
                val stored = cur.getString(4)
                val content = if (topic in sealedTopics && stored.isNotEmpty()) decryptor!!.decrypt(topic, stored) ?: stored else stored
                val localId = cur.getString(0).takeIf { it.isNotEmpty() }
                val base = ChatMessage(
                    conversation = c,
                    seq = seq,
                    localId = if (mine) localId else null,
                    from = from,
                    content = content,
                    contentType = cur.getString(5),
                    fileUrl = cur.stringOrNull(6),
                    fileName = cur.stringOrNull(7),
                    duration = cur.getInt(10),
                    extra = cur.stringOrNull(11)?.let { runCatching { Json.parse(it) }.getOrNull() },
                    senderKind = cur.stringOrNull(14) ?: ChatMessage.SENDER_USER,
                    forwardFromMsgId = cur.longOrNull(12),
                    forwardFromTopic = cur.stringOrNull(13),
                    ts = cur.getLong(8),
                    status = if (mine) MessageStatus.SENT else MessageStatus.RECEIVED,
                )
                when {
                    seq != null -> {
                        store.putMessage(base)
                        messages++
                    }
                    mine && localId != null && (status == "SENDING" || status == "FAILED") -> {
                        store.putMessage(base.copy(status = MessageStatus.FAILED, failure = SendFailure(0, null, "not confirmed before the upgrade")))
                        pending++
                    }
                    else -> continue
                }
                touched[c] = maxOf(touched[c] ?: 0, base.ts)
            }
        }

        db.rawQuery("SELECT topicId, seq FROM recalled_seqs", null).use { cur ->
            while (cur.moveToNext()) conversationOf(cur.getString(0))?.let { store.addRecalled(it, cur.getLong(1)) }
        }
        db.rawQuery("SELECT topicId, uid, lastReadSeq FROM read_cursors", null).use { cur ->
            while (cur.moveToNext()) {
                val c = conversationOf(cur.getString(0)) ?: continue
                if (c.isGroup) store.setReadCursor(c, cur.getString(1), cur.getLong(2))
            }
        }

        val conversations = HashSet<ConversationId>()
        db.rawQuery("SELECT topicId, peerName, lastTime, unreadCount, isGroup, isPinned, isMuted FROM cached_conversations", null).use { cur ->
            while (cur.moveToNext()) {
                val c = conversationOf(cur.getString(0)) ?: continue
                val existing = store.conversation(c)
                store.putConversation(
                    (existing ?: StoredConversation(c)).copy(
                        title = if (cur.getInt(4) != 0) cur.getString(1) else existing?.title,
                        lastReadSeq = maxOf(existing?.lastReadSeq ?: 0, readPosition(db, store, cur.getString(0), c, me, cur.getInt(3))),
                        pinned = cur.getInt(5) != 0,
                        muted = cur.getInt(6) != 0,
                        updatedAt = maxOf(existing?.updatedAt ?: 0, cur.getLong(2)),
                    ),
                )
                touched.remove(c)
                conversations += c
            }
        }
        // Topics the source knew without a list entry, and conversations with messages but no entry.
        for (topic in strings(db, "SELECT topicId FROM known_topics")) {
            conversationOf(topic)?.let { if (it !in touched) touched[it] = 0 }
        }
        for ((c, ts) in touched) {
            conversations += c
            if (store.conversation(c) != null) continue
            val max = store.seqs(c).lastOrNull() ?: 0
            store.putConversation(StoredConversation(c, lastReadSeq = max, updatedAt = ts))
        }

        return LegacyImportResult(true, null, conversations.size, messages, pending, skipped)
    }

    /**
     * The seq that leaves exactly [unread] messages from others unread: the
     * newest one past them, counted in the old table. System messages never
     * count as unread.
     */
    private suspend fun readPosition(db: SQLiteDatabase, store: ChatStore, topic: String, c: ConversationId, me: String, unread: Int): Long {
        if (unread <= 0) return store.seqs(c).lastOrNull() ?: 0
        return db.rawQuery(
            "SELECT serverSeq FROM chat_messages WHERE topicId = ? AND serverSeq IS NOT NULL AND fromUser != ? " +
                "AND lower(contentType) != 'system' ORDER BY serverSeq DESC LIMIT 1 OFFSET ?",
            arrayOf(topic, me, unread.toString()),
        ).use { if (it.moveToFirst()) it.getLong(0) else 0L }
    }

    private fun strings(db: SQLiteDatabase, sql: String): List<String> =
        db.rawQuery(sql, null).use { cur -> buildList { while (cur.moveToNext()) add(cur.getString(0)) } }

    private fun Cursor.longOrNull(i: Int): Long? = if (isNull(i)) null else getLong(i)

    private fun Cursor.stringOrNull(i: Int): String? = if (isNull(i)) null else getString(i)

    private companion object {
        const val VERSION = 18
    }
}
