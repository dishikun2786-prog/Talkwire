// SPDX-License-Identifier: AGPL-3.0-only
package io.imkit.e2ee

import io.imkit.core.ImIds
import io.imkit.core.json.JsonArray
import io.imkit.core.json.JsonObject
import io.imkit.core.json.jsonObject
import io.imkit.core.testing.MiniServer
import io.imkit.storage.room.KeyWrapper
import io.imkit.storage.room.WrappedKey
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.CopyOnWriteArrayList
import java.util.concurrent.atomic.AtomicInteger
import javax.crypto.Cipher
import javax.crypto.spec.GCMParameterSpec
import javax.crypto.spec.SecretKeySpec

/** An `e2e_deliver` push as the server sends it. */
internal fun e2eDeliver(topic: String, from: String, kind: String, type: Int?, content: String) = jsonObject {
    put("action", "e2e_deliver"); put("topic", topic); put("from", from); put("e2eKind", kind)
    put("e2eMsgType", type); put("content", content); put("ts", System.currentTimeMillis())
}

/** The server's E2EE relay (section 13), on top of the in-memory server. */
class TestRelay(private val server: MiniServer) {
    val bundles = ConcurrentHashMap<String, JsonObject>()
    private val pools = ConcurrentHashMap<String, ArrayDeque<JsonObject>>()
    private val encrypted = ConcurrentHashMap.newKeySet<String>()
    val publishes = AtomicInteger()
    val senderKeyTopics = CopyOnWriteArrayList<String>()
    val held = CopyOnWriteArrayList<Pair<String, JsonObject>>()

    @Volatile var refuseSends = false

    @Volatile var holdSenderKeys = false

    init {
        server.overrideAs = ::handle
    }

    fun release() {
        holdSenderKeys = false
        held.forEach { (to, f) -> server.pushTo(to, f) }
        held.clear()
    }

    fun rotate(topic: String, from: String, reason: String) {
        for (m in server.groups.getValue(topic).members.keys) server.pushTo(m, groupEncrypted(topic, from, reason))
    }

    private fun groupEncrypted(topic: String, from: String, reason: String) = jsonObject {
        put("action", "e2e_set_group_encrypted"); put("topic", topic); put("from", from); put("e2eGroupEncrypted", true); put("reason", reason)
    }

    private fun typed(f: JsonObject, type: String, body: io.imkit.core.json.JsonObjectBuilder.() -> Unit) = jsonObject {
        put("type", type); put("reqId", f.string("reqId")); body()
    }

    private fun handle(me: String, f: JsonObject): JsonObject? = when (f.string("action")) {
        "e2e_publish_bundle" -> {
            val b = f["e2eBundle"] as JsonObject
            publishes.incrementAndGet()
            bundles[me] = JsonObject(b.fields - "oneTimePreKeys")
            (b["oneTimePreKeys"] as? JsonArray)?.items?.forEach { pools.getOrPut(me) { ArrayDeque() }.addLast(it as JsonObject) }
            typed(f, "e2e_publish_bundle_result") { put("ok", true) }
        }
        "e2e_otpk_count" -> typed(f, "e2e_otpk_count_result") { put("count", pools[me]?.size ?: 0) }
        "e2e_fetch_bundle" -> {
            val user = f.string("user")!!
            val b = bundles[user]
            if (b == null) {
                typed(f, "e2e_bundle") { put("user", user); put("found", false) }
            } else {
                val otpk = synchronized(this) { pools[user]?.removeFirstOrNull() }
                JsonObject(b.fields + typed(f, "e2e_bundle") {
                    put("user", user); put("found", true); put("deviceId", 1)
                    otpk?.let { put("oneTimePreKeyId", it.long("keyId")); put("oneTimePreKey", it.string("preKey")) }
                }.fields)
            }
        }
        "e2e_send" -> send(me, f)
        "e2e_clear", "e2e_terminate" -> {
            val user = f.string("user")!!
            server.pushTo(user, jsonObject { put("action", if (f.string("action") == "e2e_clear") "e2e_cleared" else "e2e_terminated"); put("topic", f.string("topic")); put("from", me) })
            jsonObject { put("action", f.string("action")); put("code", 200); put("topic", f.string("topic")) }
        }
        "e2e_group_clear" -> {
            val topic = f.string("topic")!!
            for (m in server.groups.getValue(topic).members.keys - me) server.pushTo(m, jsonObject { put("action", "e2e_group_cleared"); put("topic", topic); put("from", me) })
            jsonObject { put("action", "e2e_group_clear"); put("code", 200); put("topic", topic) }
        }
        "e2e_set_group_encrypted" -> {
            val topic = f.string("topic")!!
            val g = server.groups[topic]
            if (g?.members?.get(me) != "owner") {
                typed(f, "e2e_set_group_encrypted_result") { put("ok", false); put("reason", "not_owner") }
            } else {
                encrypted += topic
                rotate(topic, me, "owner_enabled")
                typed(f, "e2e_set_group_encrypted_result") { put("ok", true) }
            }
        }
        else -> null
    }

    private fun send(me: String, f: JsonObject): JsonObject {
        val topic = f.string("topic")!!
        val frame = e2eDeliver(topic, me, f.string("e2eKind")!!, f.int("e2eMsgType"), f.string("content") ?: "")
        if (ImIds.isGroupTopic(topic)) {
            if (topic !in encrypted) return jsonObject { put("action", "e2e_send"); put("code", 409); put("reason", "group_not_encrypted") }
            for (m in server.groups.getValue(topic).members.keys - me) server.pushTo(m, frame)
        } else {
            if (refuseSends) return jsonObject { put("action", "e2e_send"); put("code", 401); put("reason", "not friends") }
            val user = f.string("user")!!
            if (f.string("e2eKind") == "sender_key") {
                senderKeyTopics += topic
                if (holdSenderKeys) {
                    held += user to frame
                    return jsonObject { put("action", "e2e_send"); put("code", 200); put("topic", topic) }
                }
            }
            server.pushTo(user, frame)
        }
        return jsonObject { put("action", "e2e_send"); put("code", 200); put("topic", topic) }
    }
}

/** A software key standing in for the Keystore one. */
class TestWrapper : KeyWrapper {
    private val key = SecretKeySpec(ByteArray(32) { it.toByte() }, "AES")

    override fun wrap(key: ByteArray): WrappedKey {
        val c = Cipher.getInstance("AES/GCM/NoPadding")
        c.init(Cipher.ENCRYPT_MODE, this.key)
        return WrappedKey(c.iv, c.doFinal(key))
    }

    override fun unwrap(wrapped: WrappedKey): ByteArray {
        val c = Cipher.getInstance("AES/GCM/NoPadding")
        c.init(Cipher.DECRYPT_MODE, key, GCMParameterSpec(128, wrapped.iv))
        return c.doFinal(wrapped.ciphertext)
    }
}
