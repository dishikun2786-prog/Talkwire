package io.imkit.core.model

import io.imkit.core.ImIds
import io.imkit.core.json.JsonValue

/**
 * A conversation as the client files it. Groups use their `grp_…` topic; a
 * direct chat uses the **peer's uid**, which is also what requests should
 * name (protocol spec 5.5). The canonical `p2p_…` topic only appears in
 * history replies and is mapped back to the peer before anything is stored.
 */
public data class ConversationId(val id: String) {
    init {
        require(id.isNotEmpty()) { "empty conversation id" }
    }

    public val isGroup: Boolean get() = ImIds.isGroupTopic(id)

    /** What a request's `topic` field carries for this conversation. */
    public val wireTopic: String get() = id

    override fun toString(): String = id

    public companion object {
        public fun group(topic: String): ConversationId {
            require(ImIds.isGroupTopic(topic)) { "not a group topic: $topic" }
            return ConversationId(topic)
        }

        public fun direct(peerUid: String): ConversationId {
            require(ImIds.isUserUid(peerUid)) { "not a user uid: $peerUid" }
            return ConversationId(peerUid)
        }
    }
}

public enum class MessageStatus {
    /** Own message, published, no reply yet (or the reply timed out on a live connection). */
    SENDING,

    /** Own message the server stored: it has a seq. */
    SENT,

    /** Own message that did not reach the server, or that the server refused. */
    FAILED,

    /** Someone else's message. */
    RECEIVED,
}

/**
 * What a message is, decided by its `contentType` alone. The text in
 * `content` never decides the kind: a media message carries a fallback text
 * there only for clients that do not know the type.
 */
public enum class MessageKind {
    TEXT, IMAGE, VIDEO, VOICE, FILE, SYSTEM, CUSTOM;

    public companion object {
        public fun of(contentType: String, fileUrl: String?): MessageKind {
            val t = contentType.lowercase()
            return when {
                t.isEmpty() || t == "text" || t.startsWith("text/") -> TEXT
                t == "system" -> SYSTEM
                t == "image" || t.startsWith("image/") -> IMAGE
                t == "video" || t.startsWith("video/") -> VIDEO
                t == "voice" || t == "audio" || t.startsWith("audio/") -> VOICE
                t == "file" || (t.startsWith("application/") && fileUrl != null) -> FILE
                else -> CUSTOM
            }
        }
    }
}

/** Why a send failed. [code] is the server's code; 0 means there was no server reply. */
public data class SendFailure(val code: Int, val errCode: String?, val reason: String?) {
    /** The server decided; sending the same request again will not change it. */
    public val isDeterministic: Boolean get() = code in 400..499
}

public data class ChatMessage(
    val conversation: ConversationId,
    /** Server sequence number; `null` until the server stored the message. */
    val seq: Long?,
    /** The `clientMsgId` of an own message; stays the same across retries. */
    val localId: String?,
    val from: String,
    val content: String,
    val contentType: String,
    val fileUrl: String? = null,
    val fileName: String? = null,
    val duration: Int = 0,
    val extra: JsonValue? = null,
    val senderKind: String = SENDER_USER,
    val forwardFromMsgId: Long? = null,
    val forwardFromTopic: String? = null,
    /** Server time for stored messages; local creation time for pending ones. */
    val ts: Long,
    val status: MessageStatus,
    val failure: SendFailure? = null,
) {
    /** Stable across the SENDING → SENT transition only through [localId]; use [key] for lists. */
    val key: String get() = seq?.let { "s:$it" } ?: "l:$localId"

    val kind: MessageKind get() = MessageKind.of(contentType, fileUrl)

    public companion object {
        public const val SENDER_USER: String = "user"
        public const val SENDER_AI: String = "ai_assistant"

        /**
         * Timeline order: stored messages by seq (the order the server stored
         * them in), pending ones after all stored ones, by creation time.
         */
        public val TIMELINE: Comparator<ChatMessage> = Comparator { a, b ->
            val sa = a.seq
            val sb = b.seq
            when {
                sa != null && sb != null -> sa.compareTo(sb)
                sa != null -> -1
                sb != null -> 1
                else -> compareValues(a.ts, b.ts).takeIf { it != 0 } ?: compareValues(a.localId, b.localId)
            }
        }
    }
}

public enum class PresenceState { ONLINE, OFFLINE, UNKNOWN }

/** The latest presence of one user. [rev] is the server's `presRev`; 0 when unknown. */
public data class PresenceRecord(
    val online: Boolean,
    val rev: Long,
    val receivedAt: Long,
    val lastSeenAt: Long,
)

/** A group the user belongs to, as `get_groups` reports it. */
public data class GroupInfo(
    val topic: String,
    val name: String,
    val owner: String,
    val memberCount: Int,
    val encrypted: Boolean,
)

public enum class GroupRole {
    OWNER, ADMIN, MEMBER;

    public val canManage: Boolean get() = this != MEMBER

    public companion object {
        /** Unknown or missing roles fall back to the least privileged one. */
        public fun of(wire: String?): GroupRole = when (wire) {
            "owner" -> OWNER
            "admin" -> ADMIN
            else -> MEMBER
        }
    }
}

public data class GroupMember(val uid: String, val role: GroupRole, val joinedAt: Long)
