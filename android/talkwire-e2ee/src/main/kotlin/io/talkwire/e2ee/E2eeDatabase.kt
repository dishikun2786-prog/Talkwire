// SPDX-License-Identifier: AGPL-3.0-only
package io.talkwire.e2ee

import android.content.Context
import androidx.room.Dao
import androidx.room.Database
import androidx.room.Entity
import androidx.room.Index
import androidx.room.Insert
import androidx.room.OnConflictStrategy
import androidx.room.PrimaryKey
import androidx.room.Query
import androidx.room.Room
import androidx.room.RoomDatabase
import androidx.sqlite.db.SupportSQLiteDatabase

/*
 * A database file of its own (talkwire-e2ee.db), separate from the chat store:
 * it holds the identity's private key, the ratchets and the decrypted text,
 * none of which may leave the device, so an app can exclude this one file
 * from backups. The libsignal half has blocking DAOs because libsignal calls
 * its store synchronously from native code; callers stay off the main thread.
 */

/** The account's identity: one row. [owner] is the uid it belongs to. */
@Entity(tableName = "identity")
internal class IdentityRow(@PrimaryKey val id: Int, val owner: String, val keyPair: ByteArray, val registrationId: Int)

@Entity(tableName = "pre_keys")
internal class PreKeyRow(@PrimaryKey val keyId: Int, val record: ByteArray)

@Entity(tableName = "signed_pre_keys")
internal class SignedPreKeyRow(@PrimaryKey val keyId: Int, val record: ByteArray)

@Entity(tableName = "kyber_pre_keys")
internal class KyberPreKeyRow(@PrimaryKey val keyId: Int, val record: ByteArray)

@Entity(tableName = "sessions", primaryKeys = ["address", "deviceId"])
internal class SessionRow(val address: String, val deviceId: Int, val record: ByteArray)

/** A peer's identity key, remembered the first time it is seen (trust on first use). */
@Entity(tableName = "remote_identities", primaryKeys = ["address", "deviceId"])
internal class RemoteIdentityRow(val address: String, val deviceId: Int, val identityKey: ByteArray, val firstSeenAt: Long)

@Entity(tableName = "sender_keys", primaryKeys = ["sender", "deviceId", "distributionId"])
internal class SenderKeyRow(val sender: String, val deviceId: Int, val distributionId: String, val record: ByteArray)

/**
 * My current sender key in a group: its distribution id and the distribution
 * message `GroupSessionBuilder.create` returned, kept so it is made once and
 * handed to every member as the same bytes.
 */
@Entity(tableName = "group_sender_state")
internal class GroupSenderRow(@PrimaryKey val topic: String, val distributionId: String, val distribution: ByteArray)

/** Members who already have my current sender key in a group. */
@Entity(tableName = "group_key_sent", primaryKeys = ["topic", "member"])
internal class GroupKeySentRow(val topic: String, val member: String)

/** An encrypted conversation; [preview] is sealed like message text. */
@Entity(tableName = "threads")
internal class ThreadRow(
    @PrimaryKey val topic: String,
    /** The peer of a direct chat; null for a group. */
    val peer: String?,
    val updatedAt: Long,
    val preview: String,
)

/**
 * A message of an encrypted conversation. [text] is sealed when there is a
 * key wrapper. A message of mine that did not go out keeps its envelope
 * ([envelopeType], [envelope]) so a retry sends the same ciphertext, which
 * the receiver drops if it did arrive after all.
 */
@Entity(tableName = "messages", indices = [Index("topic")])
internal class MessageRow(
    @PrimaryKey(autoGenerate = true) val id: Long,
    val topic: String,
    val from: String,
    val text: String,
    val ts: Long,
    val status: String,
    val envelopeType: Int?,
    val envelope: String?,
)

/** A conversation's data key, wrapped; deleting it shreds what is left of its text on disk. */
@Entity(tableName = "content_keys")
internal class ContentKeyRow(@PrimaryKey val topic: String, val iv: ByteArray, val ciphertext: ByteArray)

@Dao
internal interface SignalDao {
    @Query("SELECT * FROM identity WHERE id = 0")
    fun identity(): IdentityRow?

    @Insert(onConflict = OnConflictStrategy.REPLACE)
    fun putIdentity(row: IdentityRow)

    @Query("SELECT record FROM pre_keys WHERE keyId = :id")
    fun preKey(id: Int): ByteArray?

    @Insert(onConflict = OnConflictStrategy.REPLACE)
    fun putPreKey(row: PreKeyRow)

    @Query("DELETE FROM pre_keys WHERE keyId = :id")
    fun deletePreKey(id: Int)

    @Query("SELECT MAX(keyId) FROM pre_keys")
    fun maxPreKeyId(): Int?

    @Query("SELECT record FROM signed_pre_keys WHERE keyId = :id")
    fun signedPreKey(id: Int): ByteArray?

    @Query("SELECT record FROM signed_pre_keys")
    fun signedPreKeys(): List<ByteArray>

    @Insert(onConflict = OnConflictStrategy.REPLACE)
    fun putSignedPreKey(row: SignedPreKeyRow)

    @Query("DELETE FROM signed_pre_keys WHERE keyId = :id")
    fun deleteSignedPreKey(id: Int)

    @Query("SELECT record FROM kyber_pre_keys WHERE keyId = :id")
    fun kyberPreKey(id: Int): ByteArray?

    @Query("SELECT record FROM kyber_pre_keys")
    fun kyberPreKeys(): List<ByteArray>

    @Insert(onConflict = OnConflictStrategy.REPLACE)
    fun putKyberPreKey(row: KyberPreKeyRow)

    @Query("SELECT record FROM sessions WHERE address = :address AND deviceId = :deviceId")
    fun session(address: String, deviceId: Int): ByteArray?

    @Query("SELECT deviceId FROM sessions WHERE address = :address")
    fun sessionDevices(address: String): List<Int>

    @Insert(onConflict = OnConflictStrategy.REPLACE)
    fun putSession(row: SessionRow)

    @Query("DELETE FROM sessions WHERE address = :address AND deviceId = :deviceId")
    fun deleteSession(address: String, deviceId: Int)

    @Query("DELETE FROM sessions WHERE address = :address")
    fun deleteSessions(address: String)

    @Query("SELECT identityKey FROM remote_identities WHERE address = :address AND deviceId = :deviceId")
    fun remoteIdentity(address: String, deviceId: Int): ByteArray?

    @Insert(onConflict = OnConflictStrategy.REPLACE)
    fun putRemoteIdentity(row: RemoteIdentityRow)

    @Query("SELECT record FROM sender_keys WHERE sender = :sender AND deviceId = :deviceId AND distributionId = :distributionId")
    fun senderKey(sender: String, deviceId: Int, distributionId: String): ByteArray?

    @Insert(onConflict = OnConflictStrategy.REPLACE)
    fun putSenderKey(row: SenderKeyRow)

    @Query("SELECT * FROM group_sender_state WHERE topic = :topic")
    fun groupSender(topic: String): GroupSenderRow?

    @Insert(onConflict = OnConflictStrategy.REPLACE)
    fun putGroupSender(row: GroupSenderRow)

    @Query("DELETE FROM group_sender_state WHERE topic = :topic")
    fun deleteGroupSender(topic: String)

    @Query("SELECT member FROM group_key_sent WHERE topic = :topic")
    fun groupKeySentTo(topic: String): List<String>

    @Insert(onConflict = OnConflictStrategy.IGNORE)
    fun putGroupKeySent(row: GroupKeySentRow)

    @Query("DELETE FROM group_key_sent WHERE topic = :topic")
    fun deleteGroupKeySent(topic: String)
}

@Dao
internal interface ChatDao {
    @Query("SELECT * FROM threads ORDER BY updatedAt DESC")
    suspend fun threads(): List<ThreadRow>

    @Query("SELECT * FROM threads WHERE topic = :topic")
    suspend fun thread(topic: String): ThreadRow?

    @Insert(onConflict = OnConflictStrategy.REPLACE)
    suspend fun putThread(row: ThreadRow)

    @Query("DELETE FROM threads WHERE topic = :topic")
    suspend fun deleteThread(topic: String)

    @Query("SELECT * FROM messages WHERE topic = :topic ORDER BY ts, id")
    suspend fun messages(topic: String): List<MessageRow>

    @Query("SELECT * FROM messages WHERE id = :id")
    suspend fun message(id: Long): MessageRow?

    @Insert
    suspend fun insertMessage(row: MessageRow): Long

    @Query("UPDATE messages SET status = :status, envelopeType = :envelopeType, envelope = :envelope WHERE id = :id")
    suspend fun setStatus(id: Long, status: String, envelopeType: Int?, envelope: String?)

    @Query("DELETE FROM messages WHERE topic = :topic")
    suspend fun deleteMessages(topic: String)

    @Query("SELECT * FROM content_keys WHERE topic = :topic")
    suspend fun contentKey(topic: String): ContentKeyRow?

    @Insert(onConflict = OnConflictStrategy.REPLACE)
    suspend fun putContentKey(row: ContentKeyRow)

    @Query("DELETE FROM content_keys WHERE topic = :topic")
    suspend fun deleteContentKey(topic: String)
}

@Database(
    entities = [
        IdentityRow::class, PreKeyRow::class, SignedPreKeyRow::class, KyberPreKeyRow::class,
        SessionRow::class, RemoteIdentityRow::class, SenderKeyRow::class,
        GroupSenderRow::class, GroupKeySentRow::class,
        ThreadRow::class, MessageRow::class, ContentKeyRow::class,
    ],
    version = 1,
)
internal abstract class E2eeRoom : RoomDatabase() {
    abstract fun signal(): SignalDao

    abstract fun chat(): ChatDao
}

/**
 * This device's encryption state: one per signed-in account. [open] gives the
 * file database, [inMemory] one for tests.
 *
 * On sign-out or an account switch call [wipe]: the next account on this
 * device must never see the previous one's keys or messages.
 */
public class E2eeDatabase private constructor(internal val room: E2eeRoom) {
    /** Deletes everything and folds the write-ahead log back, so the deleted pages do not linger in it. */
    public fun wipe() {
        room.clearAllTables()
        checkpoint()
    }

    public fun close(): Unit = room.close()

    internal fun checkpoint() {
        runCatching { room.openHelper.writableDatabase.query("PRAGMA wal_checkpoint(TRUNCATE)").close() }
    }

    public companion object {
        public const val FILE_NAME: String = "talkwire-e2ee.db"

        public fun open(context: Context, name: String = FILE_NAME): E2eeDatabase = E2eeDatabase(
            Room.databaseBuilder(context.applicationContext, E2eeRoom::class.java, name)
                .addCallback(SecureDelete)
                .build(),
        )

        public fun inMemory(context: Context): E2eeDatabase = E2eeDatabase(
            Room.inMemoryDatabaseBuilder(context.applicationContext, E2eeRoom::class.java).build(),
        )
    }

    /** Deleted rows are zeroed in place, not left in free pages. */
    private object SecureDelete : RoomDatabase.Callback() {
        override fun onOpen(db: SupportSQLiteDatabase) {
            runCatching { db.query("PRAGMA secure_delete = ON").close() }
        }
    }
}
