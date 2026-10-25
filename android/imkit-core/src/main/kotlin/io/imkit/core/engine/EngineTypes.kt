package io.imkit.core.engine

import io.imkit.core.json.JsonValue
import io.imkit.core.model.ChatMessage
import io.imkit.core.model.ConversationId

public data class EngineConfig(
    /** Page size of history requests. */
    val pageSize: Int = 50,
    /** At most this many messages per conversation in one sync. */
    val offlinePullMax: Int = 500,
    /** Run the full sync (spec 8) after every login. */
    val autoSync: Boolean = true,
    /** Automatic resends of a message whose connection dropped. */
    val maxAutoRetries: Int = 5,
    val retryBaseDelayMs: Long = 2_000,
    val retryMaxDelayMs: Long = 60_000,
    /** A message still "sending" after this long, with nothing in flight, becomes failed. */
    val staleSendingMs: Long = 5 * 60_000,
    val typingTtlMs: Long = 10_000,
    /** How long "the assistant is typing" lasts in a group without its reply. */
    val aiTypingTtlMs: Long = 15_000,
    val presenceTtlMs: Long = 180_000,
    /** `contentType` of plain text. The reference app uses `text`. */
    val textContentType: String = "text",
    /** Messages kept in memory per open conversation before older ones are paged in. */
    val windowSize: Int = 50,
    val clock: () -> Long = System::currentTimeMillis,
)

/** A message to send. Media is uploaded first; only its URL goes over the IM connection. */
public data class OutgoingMessage(
    val content: String = "",
    /** Null means plain text ([EngineConfig.textContentType]). */
    val contentType: String? = null,
    val fileUrl: String? = null,
    val fileName: String? = null,
    /** Seconds, for audio and video. */
    val duration: Int = 0,
    val extra: JsonValue? = null,
)

public data class ConversationSummary(
    val id: ConversationId,
    val title: String?,
    val lastMessage: ChatMessage?,
    val unread: Int,
    val draft: String?,
    val pinned: Boolean,
    val muted: Boolean,
    val left: Boolean,
    val updatedAt: Long,
    /** My read position: messages up to this seq are read. */
    val lastReadSeq: Long = 0,
    /** Direct chats: how far the peer has read my messages. */
    val peerReadSeq: Long = 0,
)

/** What the engine tells the app; protocol-level pushes stay on [io.imkit.core.client.ImClient.events]. */
public sealed interface ChatEvent {
    /** A new message from someone else arrived live (not during a sync). */
    public data class MessageArrived(val message: ChatMessage, val groupName: String?) : ChatEvent

    /** A send failed for good: refused by the server, or out of automatic retries. */
    public data class SendFailed(val message: ChatMessage) : ChatEvent

    /** I am no longer in this group (kicked, disbanded, or missing from the group list). */
    public data class RemovedFromGroup(val conversation: ConversationId) : ChatEvent

    /** Someone cleared the conversation for everyone. */
    public data class ConversationCleared(val conversation: ConversationId, val by: String) : ChatEvent

    /** The server hinted that a group's name or roster changed; fetch the roster again. */
    public data class GroupChanged(val conversation: ConversationId) : ChatEvent

    /** The host changed a friendship (`contact_sync`). */
    public data class ContactChanged(val what: String, val peerUid: String) : ChatEvent

    /**
     * A sync after login finished. [conversations] are up to date; [pulled]
     * of them needed a `get` (with `sync_summary`, only those that changed,
     * spec 8.1).
     */
    public data class Synced(val conversations: Int, val failed: Int, val pulled: Int = conversations) : ChatEvent

    /** The server logged in a different user than this engine belongs to; the client was stopped. */
    public data class WrongAccount(val expected: String, val actual: String) : ChatEvent
}
