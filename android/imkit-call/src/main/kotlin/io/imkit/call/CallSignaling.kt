package io.imkit.call

import io.imkit.core.client.ImClient
import io.imkit.core.client.ImEvent
import io.imkit.core.json.JsonObject
import io.imkit.core.json.jsonObject
import io.imkit.core.protocol.Reply
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.mapNotNull

public enum class CallType(public val wire: String) {
    VOICE("voice"),
    VIDEO("video"),
    ;

    public companion object {
        /** Anything but "video" is a voice call. */
        public fun of(wire: String?): CallType = if (wire == "video") VIDEO else VOICE
    }
}

/**
 * Why a call ended. Clients must tolerate reasons not listed here (spec
 * 16.4): the host may end calls for reasons of its own.
 */
public object CallEndReason {
    public const val USER_HANGUP: String = "user_hangup"
    public const val NO_ANSWER: String = "no_answer"
    public const val REJECTED: String = "rejected"
    public const val CANCELLED: String = "cancelled"
    public const val DISCONNECTED: String = "disconnected"
    public const val FAILED: String = "failed"
    public const val ANSWERED_ELSEWHERE: String = "answered_elsewhere"
    public const val CREDIT_EXHAUSTED: String = "credit_exhausted"
    public const val MAX_DURATION_REACHED: String = "max_duration_reached"
}

/** One side's credentials for the RTC channel; [rtcToken] is never sent to the other side. */
public data class RtcCredentials(
    val channelId: String,
    val rtcToken: String,
    val appId: String?,
    val videoQuality: String?,
    val denoiseEnabled: Boolean,
) {
    public companion object {
        /** The credentials in a `call_accept` reply or a `call_answered` push. */
        public fun of(f: JsonObject): RtcCredentials = RtcCredentials(
            channelId = f.string("channelId") ?: "",
            rtcToken = f.string("rtcToken") ?: "",
            appId = f.nonEmptyString("appId"),
            videoQuality = f.nonEmptyString("videoQuality"),
            denoiseEnabled = f.bool("denoiseEnabled") == true,
        )
    }
}

/** The call pushes (spec 16), one to one. */
public sealed interface CallSignal {
    public val callSessionId: String

    public data class Ring(
        override val callSessionId: String,
        val callType: CallType,
        val callerName: String?,
        val callerAvatar: String?,
        val ringTimeoutSeconds: Int,
    ) : CallSignal

    /** To the caller: the callee answered; the caller's own credentials. */
    public data class Answered(override val callSessionId: String, val credentials: RtcCredentials) : CallSignal

    public data class Ended(override val callSessionId: String, val reason: String) : CallSignal

    public data class UpgradeRequest(override val callSessionId: String) : CallSignal

    public data class Upgraded(override val callSessionId: String, val videoQuality: String?) : CallSignal

    public data class UpgradeDeclined(override val callSessionId: String) : CallSignal

    public companion object {
        /** A call push as a signal; null for other frames. */
        public fun parse(f: JsonObject): CallSignal? {
            val id = f.nonEmptyString("callSessionId") ?: return null
            return when (f.string("type")) {
                "call_ring" -> Ring(
                    id,
                    CallType.of(f.string("callType")),
                    f.nonEmptyString("callerName"),
                    f.nonEmptyString("callerAvatar"),
                    f.int("ringTimeoutSeconds")?.takeIf { it > 0 } ?: 45,
                )
                "call_answered" -> Answered(id, RtcCredentials.of(f))
                "call_ended" -> Ended(id, f.nonEmptyString("reason") ?: CallEndReason.USER_HANGUP)
                "call_upgrade_request" -> UpgradeRequest(id)
                "call_upgraded" -> Upgraded(id, f.nonEmptyString("videoQuality"))
                "call_upgrade_declined" -> UpgradeDeclined(id)
                else -> null
            }
        }
    }
}

/**
 * The call actions and pushes of spec 16, one method per action, sending
 * exactly the fields the spec names (a replayed golden transcript checks
 * every request). Replies come back as they are, error codes included;
 * [CallManager] is the stateful layer on top.
 */
public class CallSignaling(public val client: ImClient) {
    /** The call pushes, for as long as it is collected. */
    public val signals: Flow<CallSignal> = client.events.mapNotNull { e -> (e as? ImEvent.Module)?.frame?.let(CallSignal::parse) }

    /** [peer] is the callee's uid. The reply carries `callSessionId`; the callee rings first (F49). */
    public suspend fun invite(peer: String, callType: String, clientCallId: String? = null): Reply = client.request(
        jsonObject {
            put("action", "call_invite")
            put("topic", peer)
            put("callType", callType)
            put("clientCallId", clientCallId)
        },
    )

    /** The reply carries this side's credentials ([RtcCredentials.of]). */
    public suspend fun accept(callSessionId: String, acceptAsVoice: Boolean = false): Reply = client.request(
        jsonObject {
            put("action", "call_accept")
            put("callSessionId", callSessionId)
            if (acceptAsVoice) put("acceptAsVoice", true)
        },
    )

    public suspend fun reject(callSessionId: String): Reply = simple("call_reject", callSessionId)

    public suspend fun cancel(callSessionId: String): Reply = simple("call_cancel", callSessionId)

    public suspend fun hangup(callSessionId: String): Reply = simple("call_hangup", callSessionId)

    public suspend fun upgrade(callSessionId: String): Reply = client.request(
        jsonObject {
            put("action", "call_upgrade")
            put("callSessionId", callSessionId)
            put("toType", "video")
        },
    )

    public suspend fun upgradeAccept(callSessionId: String): Reply = simple("call_upgrade_accept", callSessionId)

    public suspend fun upgradeReject(callSessionId: String): Reply = simple("call_upgrade_reject", callSessionId)

    private suspend fun simple(action: String, callSessionId: String): Reply =
        client.request(jsonObject { put("action", action); put("callSessionId", callSessionId) })
}
