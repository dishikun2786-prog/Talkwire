package io.imkit.core.store

import io.imkit.core.model.ChatMessage
import io.imkit.core.model.ConversationId
import io.imkit.core.model.MessageStatus

/** The clear watermark of a conversation (spec 11.3). */
public data class ClearState(val clearVersion: Long, val clearBeforeSeq: Long)

/** Per-conversation local state. */
public data class StoredConversation(
    val id: ConversationId,
    /** Group name; null for direct chats (the host names people). */
    val title: String? = null,
    /** My read position: messages up to and including this seq are read. */
    val lastReadSeq: Long = 0,
    /** Direct chats: how far the peer has read. */
    val peerReadSeq: Long = 0,
    val draft: String? = null,
    val pinned: Boolean = false,
    val muted: Boolean = false,
    /** A group I am no longer in (kicked, left, disbanded). */
    val left: Boolean = false,
    /** Last activity, for ordering the conversation list. */
    val updatedAt: Long = 0,
)

/**
 * Persistence for the chat engine. Implementations: [InMemoryChatStore] here,
 * a Room store in `imkit-storage-room`. The engine is the only writer and
 * keeps its own in-memory view; the store only has to remember.
 *
 * A message row is keyed by `(conversation, seq)` once stored on the server,
 * and by `(conversation, localId)` while pending. [promote] turns a pending
 * row into a stored one atomically, so a restart never finds both.
 */
public interface ChatStore {
    // ---- ownership (C4)

    /** The uid whose data this store holds; null when never claimed. */
    public suspend fun owner(): String?

    /** Deletes everything in every table, including the owner. */
    public suspend fun wipe()

    public suspend fun setOwner(uid: String)

    // ---- messages

    /** Inserts or replaces by key. */
    public suspend fun putMessage(message: ChatMessage)

    /** Atomically removes the pending row [localId] and stores [stored] (which has a seq). */
    public suspend fun promote(conversation: ConversationId, localId: String, stored: ChatMessage)

    public suspend fun message(conversation: ConversationId, seq: Long): ChatMessage?

    public suspend fun pendingMessage(conversation: ConversationId, localId: String): ChatMessage?

    /** The newest [limit] stored messages plus every pending one, in timeline order. */
    public suspend fun latestMessages(conversation: ConversationId, limit: Int): List<ChatMessage>

    /** Up to [limit] stored messages with seq below [beforeSeq], in timeline order. */
    public suspend fun messagesBefore(conversation: ConversationId, beforeSeq: Long, limit: Int): List<ChatMessage>

    /** Stored seqs in ascending order. */
    public suspend fun seqs(conversation: ConversationId): List<Long>

    /** Pending own messages in every conversation (SENDING or FAILED). */
    public suspend fun pendingMessages(): List<ChatMessage>

    public suspend fun deleteMessage(conversation: ConversationId, seq: Long)

    public suspend fun deletePending(conversation: ConversationId, localId: String)

    /** Deletes stored messages with seq ≤ [seq]; pending ones stay. */
    public suspend fun deleteThrough(conversation: ConversationId, seq: Long)

    /** Deletes every message, pending ones included. */
    public suspend fun deleteAllMessages(conversation: ConversationId)

    /** Stored messages after [afterSeq] not sent by [me] and not system messages. */
    public suspend fun countUnread(conversation: ConversationId, afterSeq: Long, me: String): Int

    // ---- recall tombstones (C3)

    public suspend fun addRecalled(conversation: ConversationId, seq: Long)

    public suspend fun recalledSeqs(conversation: ConversationId): List<Long>

    // ---- clear watermark

    public suspend fun clearState(conversation: ConversationId): ClearState?

    public suspend fun setClearState(conversation: ConversationId, state: ClearState)

    // ---- group read cursors

    public suspend fun readCursors(conversation: ConversationId): Map<String, Long>

    public suspend fun setReadCursor(conversation: ConversationId, uid: String, seq: Long)

    // ---- conversations

    public suspend fun conversations(): List<StoredConversation>

    public suspend fun conversation(id: ConversationId): StoredConversation?

    public suspend fun putConversation(conversation: StoredConversation)

    /** Deletes the conversation and everything stored for it. */
    public suspend fun deleteConversation(id: ConversationId)
}

internal fun ChatMessage.isPending(): Boolean = seq == null && (status == MessageStatus.SENDING || status == MessageStatus.FAILED)
