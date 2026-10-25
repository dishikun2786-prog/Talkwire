// SPDX-License-Identifier: AGPL-3.0-only
package io.imkit.e2ee

import io.imkit.core.client.ImClient
import io.imkit.core.client.ImEvent
import io.imkit.core.client.ImException
import io.imkit.core.json.JsonObject
import io.imkit.core.json.JsonArray
import io.imkit.core.json.jsonObject
import io.imkit.core.protocol.Reply
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.mapNotNull

/** A one-time prekey as published: its id and its public key in base64. */
public data class WireOneTimeKey(val keyId: Int, val preKey: String)

/**
 * A key bundle as the wire carries it (section 13.1): binary fields are
 * base64 strings, passed through untouched. As published it carries a batch
 * of [oneTimePreKeys]; as fetched, at most the one the server handed out.
 */
public data class WireBundle(
    val registrationId: Int,
    val identityKey: String,
    val signedPreKeyId: Int,
    val signedPreKey: String,
    val signedPreKeySignature: String,
    val kyberPreKeyId: Int,
    val kyberPreKey: String,
    val kyberPreKeySignature: String,
    val oneTimePreKeys: List<WireOneTimeKey> = emptyList(),
    /** Always 1: one set of keys per account (F42). */
    val deviceId: Int = 1,
)

/** Where libsignal's keys come from and go to: [E2eeSignaling] on a real connection. */
public interface E2eeKeyServer {
    /** Replaces this account's published keys and adds [WireBundle.oneTimePreKeys] to its pool. */
    public suspend fun publish(bundle: WireBundle): E2eeResult<Unit>

    /** The peer's keys, taking one of their one-time prekeys; [E2eeFailure.PEER_KEYS_UNAVAILABLE] when there are none we may have. */
    public suspend fun fetch(uid: String): E2eeResult<WireBundle>

    /** How many of my one-time prekeys the server still has. */
    public suspend fun oneTimeKeysLeft(): E2eeResult<Int>
}

/** What the server pushes about encryption. */
public sealed interface E2eeEvent {
    /** An envelope: `msg` or `sender_key`, as the sender wrote it (section 13.2). */
    public data class Deliver(
        val topic: String,
        val from: String,
        val kind: String,
        val msgType: Int?,
        val content: String,
        val ts: Long,
    ) : E2eeEvent

    /** The peer cleared their messages and asks me to clear mine; the session stays. */
    public data class Cleared(val topic: String, val from: String) : E2eeEvent

    /** The peer ended the encrypted session. */
    public data class Terminated(val topic: String, val from: String) : E2eeEvent

    /** A manager cleared the encrypted group's messages. */
    public data class GroupCleared(val topic: String, val from: String) : E2eeEvent

    /**
     * The group is encrypted: `owner_enabled` when it was turned on (or on
     * again), `member_removed` or `member_left` when everyone left must make
     * a new sender key (section 13.3).
     */
    public data class GroupEncrypted(val topic: String, val from: String, val reason: String) : E2eeEvent
}

/**
 * The end-to-end encryption protocol (section 13), one method per action,
 * sending exactly the fields the spec names: a replayed golden transcript
 * checks every request. Binary fields stay base64 strings here; what they hold
 * is the business of [E2eeCrypto]. Nothing here links to libsignal.
 *
 * Every refusal and transport failure comes back as an [E2eeFailure] class,
 * never as an exception (cancellation aside).
 */
public class E2eeSignaling(public val client: ImClient) : E2eeKeyServer {
    /** The encryption pushes, for as long as it is collected; queued envelopes come right after login. */
    public val events: Flow<E2eeEvent> = client.events.mapNotNull { e -> (e as? ImEvent.Module)?.frame?.let(::parseEvent) }

    override suspend fun publish(bundle: WireBundle): E2eeResult<Unit> = typed(
        jsonObject {
            put("action", "e2e_publish_bundle")
            put(
                "e2eBundle",
                jsonObject {
                    put("registrationId", bundle.registrationId)
                    put("identityKey", bundle.identityKey)
                    put("signedPreKeyId", bundle.signedPreKeyId)
                    put("signedPreKey", bundle.signedPreKey)
                    put("signedPreKeySignature", bundle.signedPreKeySignature)
                    put("kyberPreKeyId", bundle.kyberPreKeyId)
                    put("kyberPreKey", bundle.kyberPreKey)
                    put("kyberPreKeySignature", bundle.kyberPreKeySignature)
                    if (bundle.oneTimePreKeys.isNotEmpty()) {
                        put(
                            "oneTimePreKeys",
                            JsonArray(bundle.oneTimePreKeys.map { k -> jsonObject { put("keyId", k.keyId); put("preKey", k.preKey) } }),
                        )
                    }
                },
            )
        },
    ) { r -> okOrReason(r) }

    override suspend fun fetch(uid: String): E2eeResult<WireBundle> = typed(
        jsonObject {
            put("action", "e2e_fetch_bundle")
            put("user", uid)
        },
    ) { r ->
        if (r.bool("found") != true) return@typed E2eeResult.Failed(E2eeFailure.PEER_KEYS_UNAVAILABLE)
        parseBundle(r)?.let { E2eeResult.Ok(it) } ?: E2eeResult.Failed(E2eeFailure.SERVER, "incomplete bundle")
    }

    override suspend fun oneTimeKeysLeft(): E2eeResult<Int> = typed(jsonObject { put("action", "e2e_otpk_count") }) { r ->
        r.int("count")?.let { E2eeResult.Ok(it) } ?: E2eeResult.Failed(E2eeFailure.SERVER, "no count")
    }

    /**
     * Relays an envelope (section 13.2). For a direct chat [user] is the peer
     * and [topic] only a label; in a group the server fans it out to every
     * other member.
     */
    public suspend fun send(topic: String, kind: String, content: String?, msgType: Int? = null, user: String? = null): E2eeResult<Unit> = action(
        jsonObject {
            put("action", "e2e_send")
            put("topic", topic)
            put("user", user)
            put("e2eKind", kind)
            put("e2eMsgType", msgType)
            put("content", content)
        },
    )

    /** Turns encryption on for a group; owner only, and it cannot be turned off (section 13.3). */
    public suspend fun setGroupEncrypted(topic: String): E2eeResult<Unit> = typed(
        jsonObject {
            put("action", "e2e_set_group_encrypted")
            put("topic", topic)
        },
    ) { r -> okOrReason(r) }

    /** Asks the peer, if online, to clear their messages too; the session stays. */
    public suspend fun clear(topic: String, user: String): E2eeResult<Unit> = action(
        jsonObject {
            put("action", "e2e_clear")
            put("topic", topic)
            put("user", user)
        },
    )

    /** Ends the session; the peer, if online, is told. */
    public suspend fun terminate(topic: String, user: String): E2eeResult<Unit> = action(
        jsonObject {
            put("action", "e2e_terminate")
            put("topic", topic)
            put("user", user)
        },
    )

    /** Clears an encrypted group's messages for every online member; managers only. */
    public suspend fun groupClear(topic: String): E2eeResult<Unit> = action(
        jsonObject {
            put("action", "e2e_group_clear")
            put("topic", topic)
        },
    )

    private suspend fun action(frame: JsonObject): E2eeResult<Unit> = guard {
        val reply = client.request(frame)
        if (reply.ok) E2eeResult.Ok(Unit) else E2eeResult.Failed(classify(reply.code, reply.reason), reply.toString())
    }

    private suspend fun <T> typed(frame: JsonObject, read: (JsonObject) -> E2eeResult<T>): E2eeResult<T> = guard {
        read(client.requestTyped(frame))
    }

    private suspend fun <T> guard(block: suspend () -> E2eeResult<T>): E2eeResult<T> = try {
        block()
    } catch (e: CancellationException) {
        throw e
    } catch (e: ImException.Refused) {
        E2eeResult.Failed(classify(e.reply.code, e.reply.reason), e.message)
    } catch (e: ImException) {
        E2eeResult.Failed(E2eeFailure.OFFLINE, e.message)
    }

    public companion object {
        /** The class of a refusal, from its code and reason (sections 13.1 to 13.5). */
        public fun classify(code: Int, reason: String?): E2eeFailure = when {
            reason == "e2ee_disabled" || reason == "group_e2ee_disabled" -> E2eeFailure.DISABLED
            reason == "group_not_encrypted" -> E2eeFailure.GROUP_NOT_ENCRYPTED
            reason == "not_owner" -> E2eeFailure.NOT_ALLOWED
            code == 401 || code == 403 -> E2eeFailure.NOT_ALLOWED
            code == 501 -> E2eeFailure.DISABLED
            else -> E2eeFailure.SERVER
        }

        private fun okOrReason(r: JsonObject): E2eeResult<Unit> =
            if (r.bool("ok") == true) E2eeResult.Ok(Unit) else E2eeResult.Failed(classify(0, r.nonEmptyString("reason")), r.toString())

        internal fun parseBundle(r: JsonObject): WireBundle? {
            val otpkId = r.int("oneTimePreKeyId")
            val otpk = r.nonEmptyString("oneTimePreKey")
            return WireBundle(
                registrationId = r.int("registrationId") ?: return null,
                identityKey = r.nonEmptyString("identityKey") ?: return null,
                signedPreKeyId = r.int("signedPreKeyId") ?: return null,
                signedPreKey = r.nonEmptyString("signedPreKey") ?: return null,
                signedPreKeySignature = r.nonEmptyString("signedPreKeySignature") ?: return null,
                kyberPreKeyId = r.int("kyberPreKeyId") ?: return null,
                kyberPreKey = r.nonEmptyString("kyberPreKey") ?: return null,
                kyberPreKeySignature = r.nonEmptyString("kyberPreKeySignature") ?: return null,
                oneTimePreKeys = if (otpkId != null && otpk != null) listOf(WireOneTimeKey(otpkId, otpk)) else emptyList(),
                deviceId = r.int("deviceId") ?: 1,
            )
        }

        /** The push in [frame], or null when it is not an encryption push. */
        public fun parseEvent(frame: JsonObject): E2eeEvent? {
            val topic = frame.nonEmptyString("topic") ?: return null
            val from = frame.nonEmptyString("from") ?: return null
            return when (frame.string("action")) {
                "e2e_deliver" -> E2eeEvent.Deliver(
                    topic,
                    from,
                    frame.nonEmptyString("e2eKind") ?: return null,
                    frame.int("e2eMsgType"),
                    frame.string("content") ?: "",
                    frame.long("ts") ?: 0,
                )
                "e2e_cleared" -> E2eeEvent.Cleared(topic, from)
                "e2e_terminated" -> E2eeEvent.Terminated(topic, from)
                "e2e_group_cleared" -> E2eeEvent.GroupCleared(topic, from)
                "e2e_set_group_encrypted" -> E2eeEvent.GroupEncrypted(topic, from, frame.string("reason") ?: "")
                else -> null
            }
        }
    }
}
