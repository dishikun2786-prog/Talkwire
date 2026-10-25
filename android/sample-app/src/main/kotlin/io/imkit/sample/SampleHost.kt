package io.imkit.sample

import android.widget.Toast
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.RowScope
import androidx.compose.runtime.Composable
import androidx.compose.ui.platform.LocalContext
import io.imkit.android.media.MediaSender
import io.imkit.call.CallButtons
import io.imkit.call.CallManager
import io.imkit.core.client.TokenProvider
import io.imkit.core.engine.ChatEngine
import io.imkit.core.json.Json
import io.imkit.core.json.JsonObject
import io.imkit.core.model.ConversationId
import io.imkit.core.model.GroupRole
import io.imkit.examples.redpacket.RedPacketSender
import io.imkit.groupjoin.GroupJoinClient
import io.imkit.groupjoin.GroupQrCard
import io.imkit.groupjoin.JoinApprovalSetting
import io.imkit.groupjoin.JoinRequestsPanel
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
 * writes. Here both come from the host SDK's `/api/v1/chat` routes. The
 * optional modules come in through the host too: call buttons in a direct
 * chat's top bar, the group QR code and join requests on the group info.
 */
class SampleHost(
    private val http: OkHttpClient,
    private val tokens: TokenProvider,
    override val media: MediaSender,
    private val redPackets: RedPacketSender,
    private val redPacketLabel: String,
    private val calls: CallManager,
    private val groupJoin: GroupJoinClient,
    private val engine: ChatEngine,
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

    override val topBarExtras: (@Composable RowScope.(ConversationId) -> Unit) = { id ->
        if (!id.isGroup) {
            val context = LocalContext.current
            CallButtons(calls, peer = id.id, onRefused = { Toast.makeText(context, it, Toast.LENGTH_SHORT).show() })
        }
    }

    override val groupInfoExtras: (@Composable (String, GroupRole) -> Unit) = { topic, role ->
        Column {
            GroupQrCard(groupJoin, topic, canRefresh = role.canManage, linkFor = ::joinLink)
            JoinApprovalSetting(groupJoin, topic, editable = role == GroupRole.OWNER)
            if (role.canManage) JoinRequestsPanel(groupJoin, engine, topic)
        }
    }

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

/**
 * What a group QR code encodes: the web sample's join page, so the same code
 * works from a browser. This app opens it too (pasted, or as
 * `imkitsample://join/<token>`); see [qrTokenOf].
 */
fun joinLink(qrToken: String): String = SampleConfig.WEB_URL + "#/join/" + android.net.Uri.encode(qrToken)

/** The QR token in a join link, an `imkitsample://join/` link or a bare token; null when there is none. */
fun qrTokenOf(text: String): String? {
    val t = text.trim()
    if (t.isEmpty()) return null
    val marker = listOf("#/join/", "imkitsample://join/").firstOrNull { t.contains(it) }
    val raw = if (marker != null) t.substringAfter(marker) else t
    return android.net.Uri.decode(raw.substringBefore('?').trimEnd('/')).takeIf { it.isNotBlank() && !it.contains(' ') && !it.contains("://") }
}
