package io.talkwire.storage.room

import androidx.room.ColumnInfo
import androidx.room.Dao
import androidx.room.Database
import androidx.room.Embedded
import androidx.room.Entity
import androidx.room.Insert
import androidx.room.OnConflictStrategy
import androidx.room.PrimaryKey
import androidx.room.Query
import androidx.room.RoomDatabase

// Schema version 1 of the store. Stored and pending messages live in two
// tables because they have different keys: (conversation, seq) once the server
// stored the message, (conversation, local_id) before. A message is in exactly
// one of them; promote moves it in one transaction.

/** The columns both message tables share. */
internal data class MessageBody(
    @ColumnInfo(name = "from_uid") val from: String,
    /** Sealed with the conversation's data key when [sealed]. */
    val content: String,
    @ColumnInfo(name = "content_type") val contentType: String,
    @ColumnInfo(name = "file_url") val fileUrl: String?,
    @ColumnInfo(name = "file_name") val fileName: String?,
    val duration: Int,
    /** JSON text; sealed like [content]. */
    val extra: String?,
    @ColumnInfo(name = "sender_kind") val senderKind: String,
    @ColumnInfo(name = "forward_from_msg_id") val forwardFromMsgId: Long?,
    @ColumnInfo(name = "forward_from_topic") val forwardFromTopic: String?,
    val ts: Long,
    val status: String,
    @ColumnInfo(name = "failure_code") val failureCode: Int?,
    @ColumnInfo(name = "failure_err_code") val failureErrCode: String?,
    @ColumnInfo(name = "failure_reason") val failureReason: String?,
    val sealed: Boolean,
)

@Entity(tableName = "messages", primaryKeys = ["conversation", "seq"])
internal data class StoredRow(
    val conversation: String,
    val seq: Long,
    @ColumnInfo(name = "local_id") val localId: String?,
    @Embedded val body: MessageBody,
)

@Entity(tableName = "pending_messages", primaryKeys = ["conversation", "local_id"])
internal data class PendingRow(
    val conversation: String,
    @ColumnInfo(name = "local_id") val localId: String,
    @Embedded val body: MessageBody,
)

@Entity(tableName = "conversations")
internal data class ConversationRow(
    @PrimaryKey val id: String,
    val title: String?,
    @ColumnInfo(name = "last_read_seq") val lastReadSeq: Long,
    @ColumnInfo(name = "peer_read_seq") val peerReadSeq: Long,
    /** Sealed with the conversation's data key when [draftSealed]. */
    val draft: String?,
    @ColumnInfo(name = "draft_sealed") val draftSealed: Boolean,
    val pinned: Boolean,
    val muted: Boolean,
    @ColumnInfo(name = "has_left") val left: Boolean,
    @ColumnInfo(name = "updated_at") val updatedAt: Long,
)

@Entity(tableName = "recalled_seqs", primaryKeys = ["conversation", "seq"])
internal data class RecalledRow(val conversation: String, val seq: Long)

@Entity(tableName = "clear_states")
internal data class ClearRow(
    @PrimaryKey val conversation: String,
    @ColumnInfo(name = "clear_version") val clearVersion: Long,
    @ColumnInfo(name = "clear_before_seq") val clearBeforeSeq: Long,
)

@Entity(tableName = "read_cursors", primaryKeys = ["conversation", "uid"])
internal data class CursorRow(val conversation: String, val uid: String, val seq: Long)

/** One row: whose data this database holds (C4). */
@Entity(tableName = "owner")
internal data class OwnerRow(@PrimaryKey val id: Int = 0, val uid: String)

/**
 * A conversation's data key, wrapped by the app's [KeyWrapper]. Deleting the
 * row makes everything sealed with it unreadable, wherever its bytes remain.
 */
@Entity(tableName = "content_keys")
internal class ContentKeyRow(
    @PrimaryKey val conversation: String,
    val iv: ByteArray,
    val ciphertext: ByteArray,
    @ColumnInfo(name = "created_at") val createdAt: Long,
)

internal data class UnreadCandidate(val fromUid: String, val contentType: String, val fileUrl: String?)

@Dao
internal interface StoreDao {
    @Query("SELECT uid FROM owner WHERE id = 0")
    suspend fun owner(): String?

    @Insert(onConflict = OnConflictStrategy.REPLACE)
    suspend fun setOwner(row: OwnerRow)

    @Insert(onConflict = OnConflictStrategy.REPLACE)
    suspend fun putStored(row: StoredRow)

    @Insert(onConflict = OnConflictStrategy.REPLACE)
    suspend fun putPending(row: PendingRow)

    @Query("SELECT * FROM messages WHERE conversation = :c AND seq = :seq")
    suspend fun stored(c: String, seq: Long): StoredRow?

    @Query("SELECT * FROM pending_messages WHERE conversation = :c AND local_id = :localId")
    suspend fun pending(c: String, localId: String): PendingRow?

    @Query("SELECT * FROM messages WHERE conversation = :c ORDER BY seq DESC LIMIT :limit")
    suspend fun newest(c: String, limit: Int): List<StoredRow>

    @Query("SELECT * FROM messages WHERE conversation = :c AND seq < :beforeSeq ORDER BY seq DESC LIMIT :limit")
    suspend fun before(c: String, beforeSeq: Long, limit: Int): List<StoredRow>

    @Query("SELECT * FROM pending_messages WHERE conversation = :c")
    suspend fun pendingIn(c: String): List<PendingRow>

    @Query("SELECT * FROM pending_messages")
    suspend fun allPending(): List<PendingRow>

    @Query("SELECT seq FROM messages WHERE conversation = :c ORDER BY seq")
    suspend fun seqs(c: String): List<Long>

    @Query("DELETE FROM messages WHERE conversation = :c AND seq = :seq")
    suspend fun deleteStored(c: String, seq: Long)

    @Query("DELETE FROM pending_messages WHERE conversation = :c AND local_id = :localId")
    suspend fun deletePending(c: String, localId: String)

    @Query("DELETE FROM messages WHERE conversation = :c AND seq <= :seq")
    suspend fun deleteThrough(c: String, seq: Long)

    @Query("DELETE FROM messages WHERE conversation = :c")
    suspend fun deleteStoredIn(c: String)

    @Query("DELETE FROM pending_messages WHERE conversation = :c")
    suspend fun deletePendingIn(c: String)

    @Query(
        "SELECT from_uid AS fromUid, content_type AS contentType, file_url AS fileUrl FROM messages " +
            "WHERE conversation = :c AND seq > :afterSeq AND from_uid != :me",
    )
    suspend fun unreadCandidates(c: String, afterSeq: Long, me: String): List<UnreadCandidate>

    @Insert(onConflict = OnConflictStrategy.IGNORE)
    suspend fun addRecalled(row: RecalledRow)

    @Query("SELECT seq FROM recalled_seqs WHERE conversation = :c ORDER BY seq")
    suspend fun recalled(c: String): List<Long>

    @Query("DELETE FROM recalled_seqs WHERE conversation = :c")
    suspend fun deleteRecalledIn(c: String)

    @Query("SELECT * FROM clear_states WHERE conversation = :c")
    suspend fun clearState(c: String): ClearRow?

    @Insert(onConflict = OnConflictStrategy.REPLACE)
    suspend fun putClearState(row: ClearRow)

    @Query("DELETE FROM clear_states WHERE conversation = :c")
    suspend fun deleteClearState(c: String)

    @Query("SELECT * FROM read_cursors WHERE conversation = :c")
    suspend fun cursors(c: String): List<CursorRow>

    @Insert(onConflict = OnConflictStrategy.REPLACE)
    suspend fun putCursor(row: CursorRow)

    @Query("DELETE FROM read_cursors WHERE conversation = :c")
    suspend fun deleteCursorsIn(c: String)

    @Query("SELECT * FROM conversations")
    suspend fun conversations(): List<ConversationRow>

    @Query("SELECT * FROM conversations WHERE id = :id")
    suspend fun conversation(id: String): ConversationRow?

    @Insert(onConflict = OnConflictStrategy.REPLACE)
    suspend fun putConversation(row: ConversationRow)

    @Query("DELETE FROM conversations WHERE id = :id")
    suspend fun deleteConversation(id: String)

    @Query("SELECT * FROM content_keys WHERE conversation = :c")
    suspend fun contentKey(c: String): ContentKeyRow?

    /** Never replaces: a key that exists stays the key of everything sealed with it. */
    @Insert(onConflict = OnConflictStrategy.IGNORE)
    suspend fun insertContentKey(row: ContentKeyRow): Long

    @Query("DELETE FROM content_keys WHERE conversation = :c")
    suspend fun deleteContentKey(c: String)
}

@Database(
    entities = [
        StoredRow::class, PendingRow::class, ConversationRow::class, RecalledRow::class,
        ClearRow::class, CursorRow::class, OwnerRow::class, ContentKeyRow::class,
    ],
    version = 1,
    exportSchema = true,
)
internal abstract class TalkwireChatDatabase : RoomDatabase() {
    abstract fun dao(): StoreDao
}
