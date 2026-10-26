package io.talkwire.storage.room

import android.content.Context
import androidx.room.Room
import androidx.room.RoomDatabase
import androidx.room.withTransaction
import androidx.sqlite.db.SupportSQLiteDatabase
import io.talkwire.core.json.Json
import io.talkwire.core.model.ChatMessage
import io.talkwire.core.model.ConversationId
import io.talkwire.core.model.MessageKind
import io.talkwire.core.model.MessageStatus
import io.talkwire.core.model.SendFailure
import io.talkwire.core.store.ChatStore
import io.talkwire.core.store.ClearState
import io.talkwire.core.store.StoredConversation
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import java.io.Closeable
import java.util.concurrent.ConcurrentHashMap
import javax.crypto.SecretKey
import javax.crypto.spec.SecretKeySpec

/**
 * [ChatStore] on Room. Open one per signed-in app, keep it open, and hand it
 * to the chat engine; the engine checks the owner (C4) and wipes another
 * user's data itself.
 *
 * With a [KeyWrapper], message text, `extra` and drafts are sealed with a
 * per-conversation data key, and deleting a conversation (or wiping the store)
 * destroys the key. A partial clear (`deleteThrough`) keeps it: the messages
 * after the watermark must stay readable. Sealing failures fall back to plain
 * text so that chat keeps working; text whose key is gone reads as empty.
 *
 * Deleted rows are overwritten (`secure_delete`), and [wipe] checkpoints the
 * write-ahead log so the old pages do not linger there either.
 */
public class RoomChatStore internal constructor(
    internal val database: TalkwireChatDatabase,
    private val keys: KeyWrapper?,
) : ChatStore, Closeable {
    private val dao = database.dao()
    private val dataKeys = ConcurrentHashMap<String, SecretKey>()
    private val keyLocks = ConcurrentHashMap<String, Mutex>()

    public companion object {
        public const val DEFAULT_NAME: String = "talkwire_chat.db"

        /** The store in the app's database directory. */
        public fun open(context: Context, name: String = DEFAULT_NAME, keys: KeyWrapper? = null): RoomChatStore =
            RoomChatStore(configure(Room.databaseBuilder(context.applicationContext, TalkwireChatDatabase::class.java, name)), keys)

        /** A store that lives in memory only (tests, previews). */
        public fun inMemory(context: Context, keys: KeyWrapper? = null): RoomChatStore =
            RoomChatStore(configure(Room.inMemoryDatabaseBuilder(context.applicationContext, TalkwireChatDatabase::class.java)), keys)

        private fun configure(builder: RoomDatabase.Builder<TalkwireChatDatabase>): TalkwireChatDatabase =
            builder.addCallback(
                object : RoomDatabase.Callback() {
                    override fun onOpen(db: SupportSQLiteDatabase) {
                        // Overwrite deleted content instead of only unlinking it.
                        // The setting belongs to one connection and is not saved
                        // in the file. Inside a transaction it reaches the primary
                        // connection, the one every write (and so every delete)
                        // runs on; read connections never delete. Android's own
                        // SQLite is built with it on anyway.
                        db.beginTransaction()
                        try {
                            db.query("PRAGMA secure_delete = ON").use { it.moveToFirst() }
                            db.setTransactionSuccessful()
                        } finally {
                            db.endTransaction()
                        }
                    }
                },
            ).build()
    }

    override fun close() {
        database.close()
    }

    // ---------------------------------------------------------------- ownership

    override suspend fun owner(): String? = dao.owner()

    override suspend fun wipe() {
        withContext(Dispatchers.IO) {
            // Deletes every row in one transaction, then checkpoints the WAL and vacuums.
            database.clearAllTables()
        }
        dataKeys.clear()
    }

    override suspend fun setOwner(uid: String): Unit = dao.setOwner(OwnerRow(uid = uid))

    // ---------------------------------------------------------------- messages

    override suspend fun putMessage(message: ChatMessage) {
        val c = message.conversation.id
        val seq = message.seq
        if (seq != null) {
            dao.putStored(StoredRow(c, seq, message.localId, body(message)))
        } else {
            val id = requireNotNull(message.localId) { "pending message without localId" }
            dao.putPending(PendingRow(c, id, body(message)))
        }
    }

    override suspend fun promote(conversation: ConversationId, localId: String, stored: ChatMessage) {
        val seq = requireNotNull(stored.seq) { "promote needs a stored message" }
        val row = StoredRow(conversation.id, seq, stored.localId, body(stored))
        database.withTransaction {
            dao.deletePending(conversation.id, localId)
            dao.putStored(row)
        }
    }

    override suspend fun message(conversation: ConversationId, seq: Long): ChatMessage? =
        dao.stored(conversation.id, seq)?.let { toMessage(it) }

    override suspend fun pendingMessage(conversation: ConversationId, localId: String): ChatMessage? =
        dao.pending(conversation.id, localId)?.let { toMessage(it) }

    override suspend fun latestMessages(conversation: ConversationId, limit: Int): List<ChatMessage> {
        val stored = dao.newest(conversation.id, limit).asReversed().map { toMessage(it) }
        val pending = dao.pendingIn(conversation.id).map { toMessage(it) }
        return (stored + pending).sortedWith(ChatMessage.TIMELINE)
    }

    override suspend fun messagesBefore(conversation: ConversationId, beforeSeq: Long, limit: Int): List<ChatMessage> =
        dao.before(conversation.id, beforeSeq, limit).asReversed().map { toMessage(it) }

    override suspend fun seqs(conversation: ConversationId): List<Long> = dao.seqs(conversation.id)

    override suspend fun pendingMessages(): List<ChatMessage> = dao.allPending().map { toMessage(it) }

    override suspend fun deleteMessage(conversation: ConversationId, seq: Long): Unit = dao.deleteStored(conversation.id, seq)

    override suspend fun deletePending(conversation: ConversationId, localId: String): Unit =
        dao.deletePending(conversation.id, localId)

    override suspend fun deleteThrough(conversation: ConversationId, seq: Long): Unit = dao.deleteThrough(conversation.id, seq)

    override suspend fun deleteAllMessages(conversation: ConversationId) {
        val c = conversation.id
        // The draft is sealed with the key that is about to go: carry it over.
        val draft = dao.conversation(c)?.let { openDraft(it) }
        database.withTransaction {
            dao.deleteStoredIn(c)
            dao.deletePendingIn(c)
            dao.deleteRecalledIn(c)
        }
        destroyKey(c)
        dao.conversation(c)?.let { row -> dao.putConversation(withDraft(row, draft)) }
    }

    override suspend fun countUnread(conversation: ConversationId, afterSeq: Long, me: String): Int =
        dao.unreadCandidates(conversation.id, afterSeq, me).count { MessageKind.of(it.contentType, it.fileUrl) != MessageKind.SYSTEM }

    // ---------------------------------------------------------------- recall tombstones, clear watermark, cursors

    override suspend fun addRecalled(conversation: ConversationId, seq: Long): Unit = dao.addRecalled(RecalledRow(conversation.id, seq))

    override suspend fun recalledSeqs(conversation: ConversationId): List<Long> = dao.recalled(conversation.id)

    override suspend fun clearState(conversation: ConversationId): ClearState? =
        dao.clearState(conversation.id)?.let { ClearState(it.clearVersion, it.clearBeforeSeq) }

    override suspend fun setClearState(conversation: ConversationId, state: ClearState): Unit =
        dao.putClearState(ClearRow(conversation.id, state.clearVersion, state.clearBeforeSeq))

    override suspend fun readCursors(conversation: ConversationId): Map<String, Long> =
        dao.cursors(conversation.id).associate { it.uid to it.seq }

    override suspend fun setReadCursor(conversation: ConversationId, uid: String, seq: Long): Unit =
        dao.putCursor(CursorRow(conversation.id, uid, seq))

    // ---------------------------------------------------------------- conversations

    override suspend fun conversations(): List<StoredConversation> = dao.conversations().map { toConversation(it) }

    override suspend fun conversation(id: ConversationId): StoredConversation? = dao.conversation(id.id)?.let { toConversation(it) }

    override suspend fun putConversation(conversation: StoredConversation) {
        val c = conversation.id.id
        val (draft, sealed) = seal(c, conversation.draft)
        dao.putConversation(
            ConversationRow(
                id = c,
                title = conversation.title,
                lastReadSeq = conversation.lastReadSeq,
                peerReadSeq = conversation.peerReadSeq,
                draft = draft,
                draftSealed = sealed,
                pinned = conversation.pinned,
                muted = conversation.muted,
                left = conversation.left,
                updatedAt = conversation.updatedAt,
            ),
        )
    }

    override suspend fun deleteConversation(id: ConversationId) {
        val c = id.id
        database.withTransaction {
            dao.deleteConversation(c)
            dao.deleteStoredIn(c)
            dao.deletePendingIn(c)
            dao.deleteRecalledIn(c)
            dao.deleteClearState(c)
            dao.deleteCursorsIn(c)
        }
        destroyKey(c)
    }

    // ---------------------------------------------------------------- sealing

    /** Not inside a transaction: [dataKey] takes the same lock and then writes. */
    private suspend fun destroyKey(c: String) {
        if (keys == null) return
        keyLocks.getOrPut(c) { Mutex() }.withLock {
            dao.deleteContentKey(c)
            dataKeys.remove(c)
        }
    }

    private suspend fun dataKey(c: String, create: Boolean): SecretKey? {
        dataKeys[c]?.let { return it }
        val wrapper = keys ?: return null
        return keyLocks.getOrPut(c) { Mutex() }.withLock {
            dataKeys[c]?.let { return@withLock it }
            var row = dao.contentKey(c)
            if (row == null && create) {
                val fresh = ContentCrypto.newDataKey()
                val wrapped = runCatching { wrapper.wrap(fresh) }.getOrNull() ?: return@withLock null
                dao.insertContentKey(ContentKeyRow(c, wrapped.iv, wrapped.ciphertext, System.currentTimeMillis()))
                // Another store on the same file may have won the insert: use what is there.
                row = dao.contentKey(c)
            }
            val raw = row?.let { r -> runCatching { wrapper.unwrap(WrappedKey(r.iv, r.ciphertext)) }.getOrNull() } ?: return@withLock null
            SecretKeySpec(raw, "AES").also { dataKeys[c] = it }
        }
    }

    private suspend fun seal(c: String, text: String?): Pair<String?, Boolean> {
        if (text.isNullOrEmpty() || keys == null) return text to false
        val key = dataKey(c, create = true) ?: return text to false
        return runCatching { ContentCrypto.seal(key, text) to true }.getOrElse { text to false }
    }

    /** Null when the text was sealed and cannot be opened any more. */
    private suspend fun open(c: String, text: String?, sealed: Boolean): String? {
        if (!sealed || text == null) return text
        val key = dataKey(c, create = false) ?: return null
        return ContentCrypto.open(key, text)
    }

    private suspend fun openDraft(row: ConversationRow): String? = open(row.id, row.draft, row.draftSealed)

    private suspend fun withDraft(row: ConversationRow, draft: String?): ConversationRow {
        val (text, sealed) = seal(row.id, draft)
        return row.copy(draft = text, draftSealed = sealed)
    }

    // ---------------------------------------------------------------- mapping

    private suspend fun body(m: ChatMessage): MessageBody {
        val extraJson = m.extra?.let { Json.stringify(it) }
        // One key, one decision: content and extra are sealed together or not at all.
        val key = if (keys != null && (m.content.isNotEmpty() || extraJson != null)) dataKey(m.conversation.id, create = true) else null
        val sealedFields = key?.let { k ->
            runCatching {
                Pair(
                    if (m.content.isEmpty()) "" else ContentCrypto.seal(k, m.content),
                    extraJson?.let { ContentCrypto.seal(k, it) },
                )
            }.getOrNull()
        }
        return MessageBody(
            from = m.from,
            content = sealedFields?.first ?: m.content,
            contentType = m.contentType,
            fileUrl = m.fileUrl,
            fileName = m.fileName,
            duration = m.duration,
            extra = if (sealedFields != null) sealedFields.second else extraJson,
            senderKind = m.senderKind,
            forwardFromMsgId = m.forwardFromMsgId,
            forwardFromTopic = m.forwardFromTopic,
            ts = m.ts,
            status = m.status.name,
            failureCode = m.failure?.code,
            failureErrCode = m.failure?.errCode,
            failureReason = m.failure?.reason,
            sealed = sealedFields != null,
        )
    }

    private suspend fun toMessage(row: StoredRow): ChatMessage = toMessage(row.conversation, row.seq, row.localId, row.body)

    private suspend fun toMessage(row: PendingRow): ChatMessage = toMessage(row.conversation, null, row.localId, row.body)

    private suspend fun toMessage(conversation: String, seq: Long?, localId: String?, b: MessageBody): ChatMessage {
        // An empty field was never sealed (seal skips empty text).
        val content = if (b.content.isEmpty()) "" else open(conversation, b.content, b.sealed) ?: ""
        val extraText = b.extra?.let { open(conversation, it, b.sealed) }
        return ChatMessage(
            conversation = ConversationId(conversation),
            seq = seq,
            localId = localId,
            from = b.from,
            content = content,
            contentType = b.contentType,
            fileUrl = b.fileUrl,
            fileName = b.fileName,
            duration = b.duration,
            extra = extraText?.let { runCatching { Json.parse(it) }.getOrNull() },
            senderKind = b.senderKind,
            forwardFromMsgId = b.forwardFromMsgId,
            forwardFromTopic = b.forwardFromTopic,
            ts = b.ts,
            status = runCatching { MessageStatus.valueOf(b.status) }.getOrDefault(MessageStatus.RECEIVED),
            failure = b.failureCode?.let { SendFailure(it, b.failureErrCode, b.failureReason) },
        )
    }

    private suspend fun toConversation(row: ConversationRow): StoredConversation = StoredConversation(
        id = ConversationId(row.id),
        title = row.title,
        lastReadSeq = row.lastReadSeq,
        peerReadSeq = row.peerReadSeq,
        draft = openDraft(row),
        pinned = row.pinned,
        muted = row.muted,
        left = row.left,
        updatedAt = row.updatedAt,
    )
}
