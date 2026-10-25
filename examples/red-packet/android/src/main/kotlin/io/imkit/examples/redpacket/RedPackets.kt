package io.imkit.examples.redpacket

import io.imkit.core.client.ImClient
import io.imkit.core.client.ImEvent
import io.imkit.core.client.TokenProvider
import io.imkit.core.json.Json
import io.imkit.core.json.JsonObject
import io.imkit.core.json.JsonValue
import io.imkit.core.json.jsonObject
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import okhttp3.HttpUrl.Companion.toHttpUrl
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import java.io.IOException

/** What every member may see of a red packet; [myAmount] only in the viewer's own answers. */
data class RedPacketCard(
    val id: String,
    val topic: String,
    val senderId: String,
    val total: Int,
    val count: Int,
    val claimed: Int,
    val remaining: Int,
    val myAmount: Int?,
) {
    companion object {
        fun parse(o: JsonObject): RedPacketCard? {
            val id = o.nonEmptyString("id") ?: return null
            return RedPacketCard(
                id = id,
                topic = o.string("topic") ?: "",
                senderId = o.string("senderId") ?: "",
                total = o.int("total") ?: 0,
                count = o.int("count") ?: 0,
                claimed = o.int("claimed") ?: 0,
                remaining = o.int("remaining") ?: 0,
                myAmount = o.int("myAmount"),
            )
        }
    }
}

/** The id a red packet message carries in `extra.redPacketId`. */
fun redPacketIdOf(extra: JsonValue?): String? = (extra as? JsonObject)?.nonEmptyString("redPacketId")

class RedPacketException(val status: Int, message: String, val card: RedPacketCard? = null) : IOException(message)

/** The host's red packet routes (examples/red-packet/src/red-packets.ts). */
class RedPacketApi(private val hostBaseUrl: String, private val tokens: TokenProvider, private val http: OkHttpClient = OkHttpClient()) {
    /** Creates a packet; returns its card and the message to publish next. */
    suspend fun create(topic: String, total: Int, count: Int): Pair<RedPacketCard, JsonObject> {
        val o = call("POST", "/api/v1/red-packets", jsonObject { put("topic", topic); put("total", total); put("count", count) })
        val card = (o.get("packet") as? JsonObject)?.let(RedPacketCard::parse) ?: throw RedPacketException(200, "reply without packet")
        val message = o.get("message") as? JsonObject ?: throw RedPacketException(200, "reply without message")
        return card to message
    }

    suspend fun get(id: String): RedPacketCard = RedPacketCard.parse(call("GET", "/api/v1/red-packets/$id", null)) ?: throw RedPacketException(200, "bad card")

    /** The claimer's card; a 409 (already claimed, all gone) carries the current card too. */
    suspend fun claim(id: String): RedPacketCard = RedPacketCard.parse(call("POST", "/api/v1/red-packets/$id/claim", jsonObject {})) ?: throw RedPacketException(200, "bad card")

    private suspend fun call(method: String, path: String, body: JsonObject?): JsonObject = withContext(Dispatchers.IO) {
        val token = tokens.accessToken(forceRefresh = false) ?: throw RedPacketException(401, "not signed in")
        val url = hostBaseUrl.toHttpUrl().resolve(path) ?: throw IllegalArgumentException(path)
        val req = Request.Builder().url(url).header("Authorization", "Bearer $token")
            .method(method, body?.toString()?.toRequestBody("application/json".toMediaType()))
            .build()
        http.newCall(req).execute().use { resp ->
            val text = resp.body?.string() ?: ""
            val o = runCatching { Json.parseObject(text) }.getOrNull()
            if (!resp.isSuccessful) {
                throw RedPacketException(resp.code, o?.string("error") ?: "HTTP ${resp.code}", (o?.get("card") as? JsonObject)?.let(RedPacketCard::parse))
            }
            o ?: throw RedPacketException(resp.code, "reply is not JSON")
        }
    }
}

/**
 * Card states, kept current by the host's `custom_update` pushes. A push
 * carries no one's amount, so the viewer's own [RedPacketCard.myAmount]
 * from an earlier answer is kept.
 */
class RedPacketStore(private val api: RedPacketApi, client: ImClient, private val scope: CoroutineScope) {
    private val _cards = MutableStateFlow<Map<String, RedPacketCard>>(emptyMap())
    val cards: StateFlow<Map<String, RedPacketCard>> = _cards.asStateFlow()

    init {
        scope.launch {
            client.events.collect { e ->
                if (e is ImEvent.Custom && e.what == WHAT) (e.extra as? JsonObject)?.let(RedPacketCard::parse)?.let(::merge)
            }
        }
    }

    fun merge(card: RedPacketCard) {
        _cards.update { all -> all + (card.id to card.copy(myAmount = card.myAmount ?: all[card.id]?.myAmount)) }
    }

    /** Loads the current state; pushes are not queued for clients that were offline. */
    fun refresh(id: String) {
        scope.launch { runCatching { api.get(id) }.onSuccess(::merge) }
    }

    suspend fun claim(id: String): Result<RedPacketCard> = runCatching { api.claim(id) }
        .onSuccess(::merge)
        .onFailure { e -> (e as? RedPacketException)?.card?.let(::merge) }

    companion object {
        /** The host's custom message type and update kind. */
        const val WHAT: String = "red_packet"
    }
}
