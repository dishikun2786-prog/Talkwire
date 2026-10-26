package io.talkwire.core.protocol

import io.talkwire.core.json.JsonObject
import io.talkwire.core.json.JsonValue

/** Action names of the protocol (spec appendix A). */
public object Actions {
    public const val LOGIN: String = "login"
    public const val SUB: String = "sub"
    public const val LEAVE: String = "leave"
    public const val PUB: String = "pub"
    public const val GET: String = "get"
    public const val DEL: String = "del"
    public const val DEL_TOPIC: String = "del_topic"
    public const val NOTE: String = "note"
    public const val PRES: String = "pres"
    public const val CREATE_GRP: String = "create_grp"
    public const val INVITE: String = "invite"
    public const val KICK: String = "kick"
    public const val LEAVE_GRP: String = "leave_grp"
    public const val UPDATE_GRP: String = "update_grp"
    public const val SET_MEMBER_ROLE: String = "set_member_role"
    public const val TRANSFER_OWNER: String = "transfer_owner"
    public const val DISBAND_GRP: String = "disband_grp"
    public const val GET_GROUPS: String = "get_groups"
    public const val GET_MEMBERS: String = "get_members"

    /** Strict profile only, offered as the `sync-summary` capability (spec 8.1). */
    public const val SYNC_SUMMARY: String = "sync_summary"
    public const val FORWARD_BUNDLE: String = "forward_bundle"
    public const val FORWARD_BROADCAST: String = "forward_broadcast"
    public const val ACK_TOPIC_CLEARED: String = "ack_topic_cleared"
    public const val ACK_GROUP_EVENT: String = "ack_group_event"
    public const val REG_TOKEN: String = "reg_token"
    public const val UNREG_TOKEN: String = "unreg_token"

    // pushes
    public const val DATA: String = "data"
    public const val RECALLED: String = "recalled"
    public const val INFO: String = "info"
    public const val TOPIC_CLEARED: String = "topic_cleared"
    public const val GRP_SYNC: String = "grp_sync"
    public const val GRP_DISBAND: String = "grp_disband"
    public const val GROUP_JOIN_REQUEST: String = "group_join_request"
    public const val GROUP_JOIN_REVIEWED: String = "group_join_reviewed"
    public const val CONTACT_SYNC: String = "contact_sync"
    public const val CUSTOM_UPDATE: String = "custom_update"
    public const val RED_PACKET_UPDATE: String = "red_packet_update"
    public const val ERROR: String = "error"
}

/** The reply to an action-family request (spec 4.1). */
public class Reply(public val frame: JsonObject) {
    public val code: Int get() = frame.int("code") ?: 0

    public val ok: Boolean get() = code in 200..299

    public val reason: String? get() = frame.nonEmptyString("reason")

    /** strict servers only (spec 4.3). */
    public val errCode: String? get() = frame.nonEmptyString("errCode")

    override fun toString(): String = frame.toString()
}

/** A message as `data` pushes and `get` replies carry it (spec 9.2, 9.3). */
public data class WireMessage(
    val topic: String,
    val seq: Long,
    val from: String,
    val ts: Long,
    val content: String,
    val contentType: String,
    val fileUrl: String?,
    val fileName: String?,
    val duration: Int,
    val extra: JsonValue?,
    val senderKind: String,
    val clientMsgId: String?,
    val forwardFromMsgId: Long?,
    val forwardFromTopic: String?,
    /** Current group name; only on group `data` pushes. */
    val groupName: String?,
) {
    public companion object {
        public fun parse(f: JsonObject): WireMessage? {
            val seq = f.long("seq") ?: return null
            if (seq <= 0) return null
            return WireMessage(
                topic = f.string("topic") ?: "",
                seq = seq,
                from = f.string("from") ?: "",
                ts = f.long("ts") ?: 0L,
                content = f.string("content") ?: "",
                contentType = f.string("contentType") ?: "",
                fileUrl = f.nonEmptyString("fileUrl"),
                fileName = f.nonEmptyString("fileName"),
                duration = f.int("duration") ?: 0,
                extra = f["extra"]?.takeUnless { it is io.talkwire.core.json.JsonNull },
                senderKind = f.nonEmptyString("senderKind") ?: "user",
                clientMsgId = f.nonEmptyString("clientMsgId"),
                forwardFromMsgId = f.long("forwardFromMsgId"),
                forwardFromTopic = f.nonEmptyString("forwardFromTopic"),
                groupName = f.nonEmptyString("name"),
            )
        }
    }
}
