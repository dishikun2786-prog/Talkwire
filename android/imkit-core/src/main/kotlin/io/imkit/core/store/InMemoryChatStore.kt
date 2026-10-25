package io.imkit.core.store

import io.imkit.core.model.ChatMessage
import io.imkit.core.model.ConversationId
import io.imkit.core.model.MessageKind
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import java.util.TreeMap

/** A [ChatStore] that forgets everything when the process ends; for tests and short-lived clients. */
public class InMemoryChatStore : ChatStore {
    private val mutex = Mutex()
    private var owner: String? = null
    private val stored = HashMap<ConversationId, TreeMap<Long, ChatMessage>>()
    private val pending = HashMap<ConversationId, LinkedHashMap<String, ChatMessage>>()
    private val recalled = HashMap<ConversationId, MutableSet<Long>>()
    private val clears = HashMap<ConversationId, ClearState>()
    private val cursors = HashMap<ConversationId, MutableMap<String, Long>>()
    private val conversations = LinkedHashMap<ConversationId, StoredConversation>()

    override suspend fun owner(): String? = mutex.withLock { owner }

    override suspend fun wipe(): Unit = mutex.withLock {
        owner = null
        stored.clear()
        pending.clear()
        recalled.clear()
        clears.clear()
        cursors.clear()
        conversations.clear()
    }

    override suspend fun setOwner(uid: String): Unit = mutex.withLock { owner = uid }

    override suspend fun putMessage(message: ChatMessage): Unit = mutex.withLock { putLocked(message) }

    private fun putLocked(message: ChatMessage) {
        val seq = message.seq
        if (seq != null) {
            stored.getOrPut(message.conversation) { TreeMap() }[seq] = message
        } else {
            val id = requireNotNull(message.localId) { "pending message without localId" }
            pending.getOrPut(message.conversation) { LinkedHashMap() }[id] = message
        }
    }

    override suspend fun promote(conversation: ConversationId, localId: String, stored: ChatMessage): Unit = mutex.withLock {
        require(stored.seq != null) { "promote needs a stored message" }
        pending[conversation]?.remove(localId)
        putLocked(stored)
    }

    override suspend fun message(conversation: ConversationId, seq: Long): ChatMessage? =
        mutex.withLock { stored[conversation]?.get(seq) }

    override suspend fun pendingMessage(conversation: ConversationId, localId: String): ChatMessage? =
        mutex.withLock { pending[conversation]?.get(localId) }

    override suspend fun latestMessages(conversation: ConversationId, limit: Int): List<ChatMessage> = mutex.withLock {
        val s = stored[conversation]?.descendingMap()?.values?.take(limit)?.reversed() ?: emptyList()
        (s + (pending[conversation]?.values?.toList() ?: emptyList())).sortedWith(ChatMessage.TIMELINE)
    }

    override suspend fun messagesBefore(conversation: ConversationId, beforeSeq: Long, limit: Int): List<ChatMessage> =
        mutex.withLock {
            stored[conversation]?.headMap(beforeSeq, false)?.descendingMap()?.values?.take(limit)?.reversed() ?: emptyList()
        }

    override suspend fun seqs(conversation: ConversationId): List<Long> =
        mutex.withLock { stored[conversation]?.keys?.toList() ?: emptyList() }

    override suspend fun pendingMessages(): List<ChatMessage> =
        mutex.withLock { pending.values.flatMap { it.values } }

    override suspend fun deleteMessage(conversation: ConversationId, seq: Long): Unit =
        mutex.withLock { stored[conversation]?.remove(seq); Unit }

    override suspend fun deletePending(conversation: ConversationId, localId: String): Unit =
        mutex.withLock { pending[conversation]?.remove(localId); Unit }

    override suspend fun deleteThrough(conversation: ConversationId, seq: Long): Unit =
        mutex.withLock { stored[conversation]?.headMap(seq, true)?.clear(); Unit }

    override suspend fun deleteAllMessages(conversation: ConversationId): Unit = mutex.withLock {
        stored.remove(conversation)
        pending.remove(conversation)
        recalled.remove(conversation)
        Unit
    }

    override suspend fun countUnread(conversation: ConversationId, afterSeq: Long, me: String): Int = mutex.withLock {
        stored[conversation]?.tailMap(afterSeq, false)?.values?.count { it.from != me && it.kind != MessageKind.SYSTEM } ?: 0
    }

    override suspend fun addRecalled(conversation: ConversationId, seq: Long): Unit =
        mutex.withLock { recalled.getOrPut(conversation) { HashSet() }.add(seq); Unit }

    override suspend fun recalledSeqs(conversation: ConversationId): List<Long> =
        mutex.withLock { recalled[conversation]?.sorted() ?: emptyList() }

    override suspend fun clearState(conversation: ConversationId): ClearState? = mutex.withLock { clears[conversation] }

    override suspend fun setClearState(conversation: ConversationId, state: ClearState): Unit =
        mutex.withLock { clears[conversation] = state }

    override suspend fun readCursors(conversation: ConversationId): Map<String, Long> =
        mutex.withLock { cursors[conversation]?.toMap() ?: emptyMap() }

    override suspend fun setReadCursor(conversation: ConversationId, uid: String, seq: Long): Unit =
        mutex.withLock { cursors.getOrPut(conversation) { HashMap() }[uid] = seq }

    override suspend fun conversations(): List<StoredConversation> = mutex.withLock { conversations.values.toList() }

    override suspend fun conversation(id: ConversationId): StoredConversation? = mutex.withLock { conversations[id] }

    override suspend fun putConversation(conversation: StoredConversation): Unit =
        mutex.withLock { conversations[conversation.id] = conversation }

    override suspend fun deleteConversation(id: ConversationId): Unit = mutex.withLock {
        conversations.remove(id)
        stored.remove(id)
        pending.remove(id)
        recalled.remove(id)
        clears.remove(id)
        cursors.remove(id)
        Unit
    }
}
