package io.imkit.groupjoin

import io.imkit.core.client.ImClient
import io.imkit.core.client.ImEvent
import io.imkit.core.client.ImException
import io.imkit.core.engine.ChatEngine
import io.imkit.core.json.JsonObject
import io.imkit.core.json.jsonObject
import io.imkit.core.model.ConversationId
import io.imkit.core.protocol.Reply
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.mapNotNull

/** A group's QR token; [expiresAt] is in epoch milliseconds. */
public data class QrToken(val topic: String, val qrToken: String, val expiresAt: Long)

/** What a QR token leads to, before joining. */
public data class QrPreview(
    val topic: String,
    val name: String,
    val memberCount: Int,
    val alreadyMember: Boolean,
    val isFull: Boolean,
    val requireApproval: Boolean,
)

public enum class JoinStatus { JOINED, PENDING_APPROVAL }

public data class JoinResult(val topic: String, val status: JoinStatus)

public data class JoinRequest(
    val id: Long,
    val requesterUid: String,
    /** The host's name for the applicant, as the reviewer sees them; their uid when unknown (F46). */
    val requesterName: String,
    val message: String?,
    /** Epoch milliseconds. */
    val createdAt: Long,
)

public sealed interface GroupJoinEvent {
    /** Someone asked to join a group I manage. */
    public data class Request(val topic: String, val name: String, val from: String) : GroupJoinEvent

    /** A manager decided on my request; on approval a `grp_sync` follows. */
    public data class Reviewed(val topic: String, val name: String, val approved: Boolean) : GroupJoinEvent
}

/**
 * Scan-to-join (protocol section 14): a group's QR token, joining with it,
 * and reviewing join requests.
 *
 * One method per protocol action, sending exactly the fields the spec names
 * (a replayed golden transcript checks every request), so [join] does not
 * subscribe to the group: on the legacy server the joiner is not subscribed
 * and nobody is told (D7), and the caller follows a [JoinStatus.JOINED] with
 * [afterJoined]. The screens in this module do.
 *
 * A refused request throws [ImException.Refused]; its `reply.code` is 401 for
 * a non-member or non-manager, 404 for a token that no longer works, 409 for
 * a full group or a request already handled.
 */
public class GroupJoinClient(public val client: ImClient) {
    /**
     * Join requests and review results, for as long as it is collected.
     * Replayed ones (strict servers, section 10.10) are acknowledged by the
     * [ChatEngine], which every app on the kit runs; this flow does not
     * acknowledge them again.
     */
    public val events: Flow<GroupJoinEvent> = client.events.mapNotNull { e -> (e as? ImEvent.Module)?.frame?.let(::parseEvent) }

    /**
     * The group's current QR token; the same one until a manager refreshes it.
     * [refresh] (managers only) replaces it, and the old one stops working at
     * once. Members only.
     */
    public suspend fun qrToken(topic: String, refresh: Boolean = false): QrToken {
        val f = ok(
            jsonObject {
                put("action", "group_qr_token")
                put("topic", topic)
                if (refresh) put("refreshQr", true)
            },
        )
        return QrToken(f.string("topic") ?: topic, f.string("qrToken") ?: throw ImException.BadReply("no qrToken"), f.long("expiresAt") ?: 0L)
    }

    /** The group behind a token; null when the token is unknown, expired or replaced (404). */
    public suspend fun resolve(qrToken: String): QrPreview? {
        val reply = client.request(jsonObject { put("action", "group_qr_resolve"); put("qrToken", qrToken) })
        if (reply.code == 404) return null
        val f = checked(reply)
        return QrPreview(
            topic = f.string("topic") ?: "",
            name = f.string("name") ?: "",
            memberCount = f.int("memberCount") ?: 0,
            alreadyMember = f.bool("alreadyMember") == true,
            isFull = f.bool("isFull") == true,
            requireApproval = f.bool("requireApproval") == true,
        )
    }

    /**
     * Joins with a token, no friendship needed. [JoinStatus.JOINED] also when
     * already a member; [JoinStatus.PENDING_APPROVAL] when a manager must
     * approve (asking twice does not tell them twice). The server cuts
     * [message] to [JOIN_MESSAGE_MAX] code points.
     */
    public suspend fun join(qrToken: String, message: String? = null): JoinResult {
        val f = ok(
            jsonObject {
                put("action", "group_join")
                put("qrToken", qrToken)
                put("joinMessage", message?.takeIf { it.isNotEmpty() })
            },
        )
        val status = if (f.string("joinStatus") == "pending_approval") JoinStatus.PENDING_APPROVAL else JoinStatus.JOINED
        return JoinResult(f.string("topic") ?: "", status)
    }

    /** Pending requests of a group, oldest first as the server lists them. Managers only. */
    public suspend fun requests(topic: String): List<JoinRequest> {
        val f = ok(jsonObject { put("action", "group_join_requests_list"); put("topic", topic) })
        return f.array("joinRequests")?.objects().orEmpty().mapNotNull { o ->
            val id = o.long("id") ?: return@mapNotNull null
            val uid = o.string("requesterUid") ?: ""
            JoinRequest(id, uid, o.nonEmptyString("requesterName") ?: uid, o.nonEmptyString("message"), o.long("createdAt") ?: 0L)
        }
    }

    /**
     * Approves or rejects one request; true when approved. [topic] must be the
     * request's own group, from the push or the list. Already handled: a
     * refusal with 409 (on a strict server, `errCode` `group_full` means the
     * request is still pending and the group is full).
     */
    public suspend fun review(topic: String, joinRequestId: Long, approve: Boolean): Boolean {
        val f = ok(
            jsonObject {
                put("action", "group_join_review")
                put("topic", topic)
                put("joinRequestId", joinRequestId)
                put("approve", approve)
            },
        )
        return f.string("joinStatus") == "approved"
    }

    /** Whether joining by QR needs a manager's approval. Members only. */
    public suspend fun requireApproval(topic: String): Boolean =
        ok(jsonObject { put("action", "get_group_join_settings"); put("topic", topic) }).bool("requireApproval") == true

    /** Turns approval on or off; the owner only. Returns the new value. */
    public suspend fun setRequireApproval(topic: String, requireApproval: Boolean): Boolean =
        ok(
            jsonObject {
                put("action", "update_group_join_settings")
                put("topic", topic)
                put("requireApproval", requireApproval)
            },
        ).bool("requireApproval") == true

    private suspend fun ok(frame: JsonObject): JsonObject = checked(client.request(frame))

    private fun checked(reply: Reply): JsonObject {
        if (!reply.ok) throw ImException.Refused(reply)
        return reply.frame
    }

    public companion object {
        /** `joinMessage` is cut to this many code points by the server; the screens limit input to it. */
        public const val JOIN_MESSAGE_MAX: Int = 60

        /** A module push as an event; null for other frames. */
        public fun parseEvent(f: JsonObject): GroupJoinEvent? = when (f.string("action")) {
            "group_join_request" -> GroupJoinEvent.Request(f.string("topic") ?: "", f.string("name") ?: "", f.string("from") ?: "")
            "group_join_reviewed" -> GroupJoinEvent.Reviewed(f.string("topic") ?: "", f.string("name") ?: "", f.string("joinStatus") == "approved")
            else -> null
        }
    }
}

/**
 * After [JoinStatus.JOINED]: lists the conversation, subscribes to the group
 * and refreshes the group list. The legacy server neither subscribes the
 * joiner nor tells anyone (D7); on a strict server this is harmless.
 */
public suspend fun ChatEngine.afterJoined(topic: String, name: String? = null) {
    val id = ConversationId.group(topic)
    ensureConversation(id, name)
    try {
        subscribe(id)
    } catch (e: CancellationException) {
        throw e
    } catch (e: Exception) {
        // Offline now: the conversation is stored, so the engine's next full
        // sync covers it, and its `get` subscribes the connection (spec 7).
    }
    refreshGroups()
}

/** The reply code of a refused request, or 0 for anything else (offline, timeout). */
internal fun Throwable.refusedCode(): Int = (this as? ImException.Refused)?.reply?.code ?: 0
