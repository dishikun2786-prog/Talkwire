package io.imkit.sample

import io.imkit.android.media.MediaSender
import io.imkit.core.client.TokenProvider
import io.imkit.core.json.Json
import io.imkit.core.json.JsonObject
import io.imkit.core.model.ConversationId
import io.imkit.examples.redpacket.RedPacketSender
import io.imkit.ui.host.AttachmentAction
import io.imkit.ui.host.ImProfile
import io.imkit.ui.host.ImUiHost
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.withContext
import okhttp3.HttpUrl.Companion.toHttpUrl
import okhttp3.OkHttpClient
import okhttp3.Request

/**
 * The contact source and profile lookup: the part of [ImUiHost] an app
 * writes. Here both come from the host SDK's `/api/v1/chat` routes.
 */
class SampleHost(
    private val http: OkHttpClient,
    private val tokens: TokenProvider,
    override val media: MediaSender,
    private val redPackets: RedPacketSender,
    private val redPacketLabel: String,
) : ImUiHost {
    private val _contacts = MutableStateFlow<List<ImProfile>>(emptyList())
    override val contacts: StateFlow<List<ImProfile>> = _contacts

    override suspend fun refreshContacts() {
        val o = get("/api/v1/chat/contacts?status=accepted")
        _contacts.value = items(o).mapNotNull { c ->
            val uid = c.nonEmptyString("uid") ?: return@mapNotNull null
            ImProfile(uid, c.string("displayName") ?: "", resolveMediaUrl(c.string("avatarUrl")))
        }
    }

    override suspend fun profiles(uids: Collection<String>): Map<String, ImProfile> {
        if (uids.isEmpty()) return emptyMap()
        val o = get("/api/v1/chat/profiles?uids=" + uids.joinToString(","))
        return items(o).mapNotNull { p ->
            val uid = p.nonEmptyString("uid") ?: return@mapNotNull null
            ImProfile(uid, p.string("displayName") ?: "", resolveMediaUrl(p.string("avatarUrl")))
        }.associateBy { it.uid }
    }

    /** The host serves media under relative paths; make them absolute. */
    override fun resolveMediaUrl(url: String?): String? {
        if (url.isNullOrBlank()) return null
        return SampleConfig.HOST_URL.toHttpUrl().resolve(url)?.toString() ?: url
    }

    override fun attachmentActions(conversation: ConversationId): List<AttachmentAction> =
        listOfNotNull(redPackets.action(conversation, redPacketLabel))

    private fun items(o: JsonObject): List<JsonObject> = o.array("items")?.objects() ?: emptyList()

    private suspend fun get(path: String): JsonObject = withContext(Dispatchers.IO) {
        val token = tokens.accessToken(forceRefresh = false) ?: error("not signed in")
        val req = Request.Builder().url(SampleConfig.HOST_URL + path).header("Authorization", "Bearer $token").build()
        http.newCall(req).execute().use { resp ->
            check(resp.isSuccessful) { "HTTP ${resp.code}" }
            Json.parseObject(resp.body?.string() ?: "{}")
        }
    }

}
