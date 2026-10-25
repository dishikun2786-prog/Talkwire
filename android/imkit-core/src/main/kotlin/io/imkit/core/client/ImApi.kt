package io.imkit.core.client

import io.imkit.core.json.JsonArray
import io.imkit.core.json.JsonNumber
import io.imkit.core.json.JsonObject
import io.imkit.core.json.JsonString
import io.imkit.core.json.JsonValue
import io.imkit.core.json.jsonObject
import io.imkit.core.model.GroupInfo
import io.imkit.core.model.GroupMember
import io.imkit.core.model.GroupRole
import io.imkit.core.protocol.Actions
import io.imkit.core.protocol.Reply
import io.imkit.core.protocol.WireMessage

// Typed requests on top of ImClient.request (spec 9–12, 17). Each function
// sends exactly the fields the spec lists and reads exactly the fields the
// reply carries; nothing here keeps state.

/** How a `pub` ended, in the terms the send queue needs (C2). */
public sealed interface PublishOutcome {
    /** The server stored the message (or had stored it: same `clientMsgId`). */
    public data class Stored(val seq: Long, val ts: Long) : PublishOutcome

    /** The server answered with an error. 4xx will not change on resend. */
    public data class Refused(val code: Int, val errCode: String?, val reason: String?) : PublishOutcome

    /**
     * No reply in time while the connection stayed up: the server most likely
     * has it. Keep the message pending; do not resend on your own.
     */
    public data object Pending : PublishOutcome

    /** Not sent, or the connection dropped before the reply. Resending with the same `clientMsgId` is safe. */
    public data object Lost : PublishOutcome
}

public suspend fun ImClient.publish(
    topic: String,
    content: String,
    contentType: String,
    clientMsgId: String,
    fileUrl: String? = null,
    fileName: String? = null,
    duration: Int = 0,
    extra: JsonValue? = null,
    timeoutMs: Long? = null,
): PublishOutcome {
    val frame = jsonObject {
        put("action", Actions.PUB)
        put("topic", topic)
        put("content", content)
        put("contentType", contentType)
        put("fileUrl", fileUrl)
        put("fileName", fileName)
        if (duration > 0) put("duration", duration)
        put("extra", extra)
        put("clientMsgId", clientMsgId)
    }
    val reply = try {
        request(frame, timeoutMs ?: publishTimeoutMs)
    } catch (e: ImException.Timeout) {
        return if (e.connectionAlive) PublishOutcome.Pending else PublishOutcome.Lost
    } catch (e: ImException.NotConnected) {
        return PublishOutcome.Lost
    } catch (e: ImException.ConnectionLost) {
        return PublishOutcome.Lost
    }
    if (!reply.ok) return PublishOutcome.Refused(reply.code, reply.errCode, reply.reason)
    val seq = reply.frame.long("seq") ?: return PublishOutcome.Refused(reply.code, "bad_reply", "no seq in reply")
    return PublishOutcome.Stored(seq, reply.frame.long("ts") ?: 0L)
}

public data class HistoryPage(
    val messages: List<WireMessage>,
    /** 0 when the conversation was never cleared. */
    val clearVersion: Long,
    val clearBeforeSeq: Long,
    /** Group read cursors (uid to seq), only when the host enabled read receipts. */
    val readCursors: Map<String, Long>?,
    /**
     * How far the user has read the conversation, on any of their devices
     * (strict profile, spec 9.3); null when the server does not know or is
     * a legacy one.
     */
    val readSeq: Long? = null,
)

/**
 * `get` (spec 9.3). The reply must name the topic that was asked for; a
 * mismatch throws instead of letting another conversation's page be filed
 * here.
 */
public suspend fun ImClient.history(
    topic: String,
    limit: Int? = null,
    beforeSeq: Long? = null,
    afterSeq: Long? = null,
): HistoryPage {
    val reply = request(
        jsonObject {
            put("action", Actions.GET)
            put("topic", topic)
            put("limit", limit)
            put("beforeSeq", beforeSeq?.takeIf { it > 0 })
            put("afterSeq", afterSeq?.takeIf { it > 0 })
        },
    )
    if (!reply.ok) throw ImException.Refused(reply)
    val replyTopic = reply.frame.string("topic")
    if (replyTopic != null && replyTopic != topic) throw ImException.BadReply("get for $topic answered for $replyTopic")
    val messages = reply.frame.array("messages")?.objects()?.mapNotNull { WireMessage.parse(it) } ?: emptyList()
    val cursors = reply.frame.array("readCursors")?.objects()?.mapNotNull { c ->
        val uid = c.nonEmptyString("uid") ?: return@mapNotNull null
        uid to (c.long("seq") ?: 0L)
    }?.toMap()
    return HistoryPage(
        messages = messages.sortedBy { it.seq },
        clearVersion = reply.frame.long("clearVersion") ?: 0L,
        clearBeforeSeq = reply.frame.long("clearBeforeSeq") ?: 0L,
        readCursors = cursors,
        readSeq = reply.frame.long("readSeq")?.takeIf { it > 0 },
    )
}

public suspend fun ImClient.subscribe(topic: String): Reply =
    request(jsonObject { put("action", Actions.SUB); put("topic", topic) })

/** Only cancels this connection's subscription; a direct chat must be left by its `p2p_…` topic (spec 9.4). */
public suspend fun ImClient.unsubscribe(topic: String): Reply =
    request(jsonObject { put("action", Actions.LEAVE); put("topic", topic) })

/** Recalls an own message (spec 9.5). */
public suspend fun ImClient.recall(topic: String, seq: Long): Reply =
    request(jsonObject { put("action", Actions.DEL); put("topic", topic); put("seq", seq) })

public data class ClearResult(val clearVersion: Long, val clearBeforeSeq: Long, val deletedMessages: Long, val deletedMedia: Long)

/** Clears a conversation for every member, permanently (spec 11.2). */
public suspend fun ImClient.clearConversation(topic: String): ClearResult {
    val reply = request(jsonObject { put("action", Actions.DEL_TOPIC); put("topic", topic) })
    if (!reply.ok) throw ImException.Refused(reply)
    val f = reply.frame
    return ClearResult(f.long("clearVersion") ?: 0, f.long("clearBeforeSeq") ?: 0, f.long("deletedMessages") ?: 0, f.long("deletedMedia") ?: 0)
}

/** "Typing" (spec 12.3); no reply. */
public fun ImClient.sendTyping(topic: String): Boolean =
    sendNoReply(jsonObject { put("action", Actions.NOTE); put("topic", topic); put("what", "kp") })

/** "Read up to [seq]" (spec 12.3); no reply. */
public fun ImClient.sendRead(topic: String, seq: Long): Boolean =
    sendNoReply(jsonObject { put("action", Actions.NOTE); put("topic", topic); put("what", "read"); put("seq", seq) })

public data class PresenceSnapshot(val uid: String, val online: Boolean, val rev: Long, val lastSeenAt: Long)

/** `pres` query (spec 12.2); [uids] empty asks for everyone sharing a conversation with me. */
public suspend fun ImClient.queryPresence(uids: List<String>): List<PresenceSnapshot> {
    val frame = jsonObject {
        put("action", Actions.PRES)
        when (uids.size) {
            0 -> put("topic", "*")
            1 -> put("topic", uids[0])
            else -> putStrings("topics", uids)
        }
    }
    val reply = request(frame)
    if (!reply.ok) throw ImException.Refused(reply)
    return reply.frame.array("states")?.objects()?.mapNotNull { s ->
        val uid = s.nonEmptyString("uid") ?: return@mapNotNull null
        PresenceSnapshot(uid, s.bool("online") ?: false, s.long("presRev") ?: 0, s.long("lastSeenAt") ?: 0)
    } ?: emptyList()
}

public suspend fun ImClient.groups(): List<GroupInfo> {
    val reply = request(jsonObject { put("action", Actions.GET_GROUPS) })
    if (!reply.ok) throw ImException.Refused(reply)
    return reply.frame.array("groups")?.objects()?.mapNotNull { g ->
        val topic = g.nonEmptyString("topic") ?: return@mapNotNull null
        GroupInfo(topic, g.string("name") ?: "", g.string("owner") ?: "", g.int("members") ?: 0, g.bool("encrypted") ?: false)
    } ?: emptyList()
}

public suspend fun ImClient.members(topic: String): List<GroupMember> {
    val reply = request(jsonObject { put("action", Actions.GET_MEMBERS); put("topic", topic) })
    if (!reply.ok) throw ImException.Refused(reply)
    return reply.frame.array("members")?.objects()?.mapNotNull { m ->
        val uid = m.nonEmptyString("uid") ?: return@mapNotNull null
        GroupMember(uid, GroupRole.of(m.string("role")), m.long("joinedAt") ?: 0)
    } ?: emptyList()
}

/** Returns the new group's topic (spec 10.2; success is 201). */
public suspend fun ImClient.createGroup(name: String, members: List<String>): String {
    val reply = request(jsonObject { put("action", Actions.CREATE_GRP); put("name", name); putStrings("members", members) })
    if (!reply.ok) throw ImException.Refused(reply)
    return reply.frame.nonEmptyString("topic") ?: throw ImException.BadReply("create_grp without topic")
}

public suspend fun ImClient.invite(topic: String, user: String): Reply =
    request(jsonObject { put("action", Actions.INVITE); put("topic", topic); put("user", user) })

public suspend fun ImClient.kick(topic: String, user: String): Reply =
    request(jsonObject { put("action", Actions.KICK); put("topic", topic); put("user", user) })

/** Groups only: never send this for a direct chat (spec 10.5, D2). */
public suspend fun ImClient.leaveGroup(topic: String): Reply {
    require(io.imkit.core.ImIds.isGroupTopic(topic)) { "leave_grp is for groups only" }
    return request(jsonObject { put("action", Actions.LEAVE_GRP); put("topic", topic) })
}

public suspend fun ImClient.renameGroup(topic: String, name: String): Reply =
    request(jsonObject { put("action", Actions.UPDATE_GRP); put("topic", topic); put("name", name) })

public suspend fun ImClient.setMemberRole(topic: String, user: String, role: GroupRole): Reply {
    require(role != GroupRole.OWNER) { "use transferOwner" }
    return request(
        jsonObject {
            put("action", Actions.SET_MEMBER_ROLE)
            put("topic", topic)
            put("user", user)
            put("memberRole", if (role == GroupRole.ADMIN) "admin" else "member")
        },
    )
}

public suspend fun ImClient.transferOwner(topic: String, user: String): Reply =
    request(jsonObject { put("action", Actions.TRANSFER_OWNER); put("topic", topic); put("user", user) })

public suspend fun ImClient.disbandGroup(topic: String): Reply =
    request(jsonObject { put("action", Actions.DISBAND_GRP); put("topic", topic) })

public data class ForwardBundleResult(val ok: Boolean, val insertedSeqs: List<Long>, val reason: String?)

/** Copies messages into another conversation (spec 9.6). */
public suspend fun ImClient.forwardBundle(sourceTopic: String, seqs: List<Long>, targetTopic: String): ForwardBundleResult {
    val f = requestTyped(
        jsonObject {
            put("action", Actions.FORWARD_BUNDLE)
            put("sourceTopic", sourceTopic)
            put("sourceMsgIds", JsonArray(seqs.map { JsonNumber(it.toString()) }))
            put("targetTopic", targetTopic)
        },
        timeoutMs = 20_000,
    )
    return ForwardBundleResult(
        ok = f.bool("ok") ?: false,
        insertedSeqs = f.array("insertedSeqs")?.items?.mapNotNull { (it as? JsonNumber)?.toLongOrNull() } ?: emptyList(),
        reason = f.nonEmptyString("reason"),
    )
}

public data class ForwardTargetResult(val topic: String, val ok: Boolean, val seq: Long?, val reason: String?)

/** Forwards one message to several conversations; targets must be canonical topics (spec 9.6). */
public suspend fun ImClient.forwardBroadcast(sourceTopic: String, seq: Long, targetTopics: List<String>): List<ForwardTargetResult> {
    val f = requestTyped(
        jsonObject {
            put("action", Actions.FORWARD_BROADCAST)
            put("sourceTopic", sourceTopic)
            put("sourceMsgId", seq)
            putStrings("targetTopics", targetTopics)
        },
        timeoutMs = 20_000,
    )
    return f.array("results")?.objects()?.map { r ->
        ForwardTargetResult(r.string("topic") ?: "", r.bool("ok") ?: false, r.long("seq"), r.nonEmptyString("reason"))
    } ?: emptyList()
}

/** Acknowledges a replayed `topic_cleared`; [offlineId] is the frame's `id` (spec 11.3). */
public fun ImClient.ackTopicCleared(offlineId: String): Boolean =
    sendNoReply(jsonObject { put("action", Actions.ACK_TOPIC_CLEARED); put("id", offlineId) })

/** Acknowledges a replayed group event (spec 10.10). */
public fun ImClient.ackGroupEvent(eventId: Long): Boolean =
    sendNoReply(jsonObject { put("action", Actions.ACK_GROUP_EVENT); put("eventId", eventId) })

/** Registers this device's push token (spec 17); call after every login. */
public fun ImClient.registerPushToken(token: String, provider: String): Boolean =
    sendNoReply(jsonObject { put("action", Actions.REG_TOKEN); put("token", token); put("provider", provider) })

/** Unregisters the push token; call before logging out. */
public fun ImClient.unregisterPushToken(token: String, provider: String): Boolean =
    sendNoReply(jsonObject { put("action", Actions.UNREG_TOKEN); put("token", token); put("provider", provider) })

internal fun JsonObject.stringList(key: String): List<String> =
    array(key)?.items?.mapNotNull { (it as? JsonString)?.value } ?: emptyList()
