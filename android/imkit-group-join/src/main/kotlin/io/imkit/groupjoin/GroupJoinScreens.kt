package io.imkit.groupjoin

import android.content.Context
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.Divider
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Surface
import androidx.compose.material3.Switch
import androidx.compose.material3.SwitchDefaults
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.platform.LocalClipboardManager
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import io.imkit.core.client.ImException
import io.imkit.core.engine.ChatEngine
import io.imkit.ui.R as UiR
import io.imkit.ui.components.ImConfirmDialog
import io.imkit.ui.theme.ImTheme
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import java.text.DateFormat
import java.util.Date

// ------------------------------------------------------------------ QR card

/**
 * A group's current QR code and link, for members to share. [linkFor] turns
 * the token into what the code encodes, usually a link to the app's join
 * page (default: the token itself). [qrCode] draws it; without it only the
 * link is shown, since the module ships no QR encoder. Managers
 * ([canRefresh]) may reset the code, which stops the old one at once.
 */
@Composable
public fun GroupQrCard(
    client: GroupJoinClient,
    topic: String,
    modifier: Modifier = Modifier,
    canRefresh: Boolean = false,
    linkFor: (qrToken: String) -> String = { it },
    qrCode: (@Composable (value: String) -> Unit)? = null,
) {
    val colors = ImTheme.colors
    val scope = rememberCoroutineScope()
    val clipboard = LocalClipboardManager.current
    val locale = LocalConfiguration.current.locales[0]
    var token by remember(client, topic) { mutableStateOf<QrToken?>(null) }
    var error by remember(client, topic) { mutableStateOf<Int?>(null) }
    var busy by remember { mutableStateOf(false) }
    var copied by remember { mutableStateOf(false) }
    var confirming by remember { mutableStateOf(false) }

    suspend fun load(refresh: Boolean) {
        busy = true
        error = null
        try {
            token = client.qrToken(topic, refresh)
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            error = if (e.refusedCode() == 401) R.string.imkit_gj_not_allowed else R.string.imkit_gj_failed
        } finally {
            busy = false
        }
    }
    LaunchedEffect(client, topic) { load(refresh = false) }
    LaunchedEffect(copied) {
        if (copied) {
            delay(1_500)
            copied = false
        }
    }

    val title = stringResource(R.string.imkit_gj_qr_title)
    Section(modifier.semantics { contentDescription = title }) {
        Heading(title)
        error?.let { ErrorText(stringResource(it)) }
        val t = token
        if (t == null) {
            if (error == null) Muted(stringResource(R.string.imkit_gj_loading))
        } else {
            val link = linkFor(t.qrToken)
            qrCode?.invoke(link)
            Surface(shape = RoundedCornerShape(8.dp), color = colors.inputBackground, modifier = Modifier.fillMaxWidth()) {
                Text(link, fontSize = 12.sp, fontFamily = FontFamily.Monospace, color = colors.textPrimary, modifier = Modifier.padding(8.dp))
            }
            val until = DateFormat.getDateTimeInstance(DateFormat.MEDIUM, DateFormat.SHORT, locale).format(Date(t.expiresAt))
            Muted(stringResource(R.string.imkit_gj_qr_expires, until))
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.CenterVertically) {
                Primary(stringResource(if (copied) R.string.imkit_gj_copied else R.string.imkit_gj_copy_link)) {
                    clipboard.setText(AnnotatedString(link))
                    copied = true
                }
                if (canRefresh) Secondary(stringResource(R.string.imkit_gj_qr_refresh), enabled = !busy) { confirming = true }
            }
        }
    }
    if (confirming) {
        ImConfirmDialog(
            message = stringResource(R.string.imkit_gj_qr_refresh_confirm),
            confirmLabel = stringResource(R.string.imkit_gj_qr_refresh),
            dismissLabel = stringResource(UiR.string.imkit_cancel),
            destructive = true,
            onConfirm = {
                confirming = false
                scope.launch { load(refresh = true) }
            },
            onDismiss = { confirming = false },
        )
    }
}

// ------------------------------------------------------------------ joining

private sealed interface JoinPhase {
    data object Loading : JoinPhase

    data object Invalid : JoinPhase

    /** A phase that shows the group. */
    sealed interface Shown : JoinPhase {
        val preview: QrPreview
    }

    data class Preview(override val preview: QrPreview, val error: Int? = null) : Shown

    data class Joining(override val preview: QrPreview) : Shown

    /** [approved]: joined by an approved request, said instead of "already in this group". */
    data class Member(override val preview: QrPreview, val approved: Boolean = false) : Shown

    data class Pending(override val preview: QrPreview) : Shown

    data class Rejected(override val preview: QrPreview) : Shown
}

/** The preview once I joined: the count it showed was taken before. */
private fun QrPreview.joinedOne(): QrPreview = copy(memberCount = memberCount + 1, alreadyMember = true)

/**
 * Where a scanned or opened code leads: the group, then joining or asking to
 * join. After joining it lists and subscribes the group ([afterJoined]).
 * [onOpen] opens the group's chat; the button shows once the user is a member.
 */
@Composable
public fun JoinGroupScreen(
    client: GroupJoinClient,
    engine: ChatEngine,
    qrToken: String,
    modifier: Modifier = Modifier,
    onOpen: ((topic: String) -> Unit)? = null,
) {
    val colors = ImTheme.colors
    val scope = rememberCoroutineScope()
    var phase by remember(client, qrToken) { mutableStateOf<JoinPhase>(JoinPhase.Loading) }
    var message by remember(qrToken) { mutableStateOf("") }

    LaunchedEffect(client, qrToken) {
        phase = try {
            val p = client.resolve(qrToken)
            when {
                p == null -> JoinPhase.Invalid
                p.alreadyMember -> JoinPhase.Member(p)
                else -> JoinPhase.Preview(p)
            }
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            JoinPhase.Invalid
        }
    }

    // While the request waits, the manager's decision arrives as a push: follow it here.
    val pendingTopic = (phase as? JoinPhase.Pending)?.preview?.topic
    LaunchedEffect(client, pendingTopic) {
        if (pendingTopic == null) return@LaunchedEffect
        client.events.collect { e ->
            if (e !is GroupJoinEvent.Reviewed || e.topic != pendingTopic) return@collect
            val p = phase as? JoinPhase.Pending ?: return@collect
            if (e.approved) {
                try {
                    engine.afterJoined(pendingTopic, e.name.ifEmpty { p.preview.name })
                } catch (c: CancellationException) {
                    throw c
                } catch (_: Exception) {
                    // The server already added and subscribed me; the list catches up on the next sync.
                }
                phase = JoinPhase.Member(p.preview.joinedOne(), approved = true)
            } else {
                phase = JoinPhase.Rejected(p.preview)
            }
        }
    }

    fun join(preview: QrPreview) {
        phase = JoinPhase.Joining(preview)
        scope.launch {
            phase = try {
                val r = client.join(qrToken, if (preview.requireApproval) message.trim().ifEmpty { null } else null)
                if (r.status == JoinStatus.PENDING_APPROVAL) {
                    JoinPhase.Pending(preview)
                } else {
                    engine.afterJoined(r.topic.ifEmpty { preview.topic }, preview.name)
                    JoinPhase.Member(preview.joinedOne())
                }
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                when (e.refusedCode()) {
                    404 -> JoinPhase.Invalid
                    409 -> JoinPhase.Preview(preview.copy(isFull = true), R.string.imkit_gj_group_full)
                    else -> JoinPhase.Preview(preview, R.string.imkit_gj_failed)
                }
            }
        }
    }

    when (val p = phase) {
        JoinPhase.Loading -> Section(modifier) { Muted(stringResource(R.string.imkit_gj_loading)) }
        JoinPhase.Invalid -> Section(modifier) { ErrorText(stringResource(R.string.imkit_gj_invalid_code)) }
        is JoinPhase.Shown -> {
            val preview = p.preview
            Section(modifier.semantics { contentDescription = preview.name }) {
                Heading(preview.name)
                // The kit's context, so the count follows ImTheme's locale.
                Muted(LocalContext.current.resources.getQuantityString(R.plurals.imkit_gj_member_count, preview.memberCount, preview.memberCount))
                when (p) {
                    is JoinPhase.Member -> {
                        Body(if (p.approved) stringResource(R.string.imkit_gj_approved_notice, preview.name) else stringResource(R.string.imkit_gj_already_member))
                        if (onOpen != null) Primary(stringResource(R.string.imkit_gj_open_chat)) { onOpen(preview.topic) }
                    }
                    is JoinPhase.Pending -> Body(stringResource(R.string.imkit_gj_pending))
                    is JoinPhase.Rejected -> ErrorText(stringResource(R.string.imkit_gj_rejected_notice, preview.name))
                    else -> {
                        if (preview.isFull) ErrorText(stringResource(R.string.imkit_gj_group_full))
                        if (preview.requireApproval && !preview.isFull) {
                            Muted(stringResource(R.string.imkit_gj_needs_approval))
                            val hint = stringResource(R.string.imkit_gj_join_message)
                            Surface(shape = RoundedCornerShape(10.dp), color = colors.inputBackground, modifier = Modifier.fillMaxWidth()) {
                                BasicTextField(
                                    value = message,
                                    onValueChange = { message = limitCodePoints(it, GroupJoinClient.JOIN_MESSAGE_MAX) },
                                    textStyle = TextStyle(fontSize = 15.sp, color = colors.textPrimary),
                                    cursorBrush = SolidColor(colors.accent),
                                    modifier = Modifier.fillMaxWidth().heightIn(min = 64.dp).padding(10.dp).semantics { contentDescription = hint },
                                    decorationBox = { inner ->
                                        if (message.isEmpty()) Text(hint, fontSize = 15.sp, color = colors.textHint)
                                        inner()
                                    },
                                )
                            }
                        }
                        val err = (p as? JoinPhase.Preview)?.error
                        if (err != null && !preview.isFull) ErrorText(stringResource(err))
                        if (!preview.isFull) {
                            val label = stringResource(if (preview.requireApproval) R.string.imkit_gj_ask_to_join else R.string.imkit_gj_join)
                            Primary(label, enabled = p !is JoinPhase.Joining) { join(preview) }
                        }
                    }
                }
            }
        }
    }
}

// ------------------------------------------------------------------ managing

/** A group's pending join requests, with approve and decline. Managers only. */
@Composable
public fun JoinRequestsPanel(
    client: GroupJoinClient,
    engine: ChatEngine,
    topic: String,
    modifier: Modifier = Modifier,
    nameOf: (JoinRequest) -> String = { it.requesterName },
) {
    val colors = ImTheme.colors
    val scope = rememberCoroutineScope()
    var requests by remember(client, topic) { mutableStateOf<List<JoinRequest>?>(null) }
    var error by remember(client, topic) { mutableStateOf<Int?>(null) }
    var busy by remember { mutableStateOf<Long?>(null) }

    suspend fun load() {
        try {
            requests = client.requests(topic)
            error = null
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            error = if (e.refusedCode() == 401) R.string.imkit_gj_not_allowed else R.string.imkit_gj_failed
        }
    }
    LaunchedEffect(client, topic) { load() }
    LaunchedEffect(client, topic) {
        client.events.collect { e -> if (e is GroupJoinEvent.Request && e.topic == topic) load() }
    }

    fun review(r: JoinRequest, approve: Boolean) {
        busy = r.id
        scope.launch {
            try {
                client.review(topic, r.id, approve)
                requests = requests?.filter { it.id != r.id }
                error = null
                // The server tells only the requester (F45); the member list here would stay old.
                if (approve) engine.noteGroupChanged(topic)
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                val reply = (e as? ImException.Refused)?.reply
                when {
                    // strict servers check first: the request stays pending.
                    reply?.code == 409 && reply.errCode == "group_full" -> error = R.string.imkit_gj_group_full
                    reply?.code == 409 -> {
                        // Handled by someone else; on a legacy server also a failed
                        // check after the request was used up (D8). Either way it is
                        // no longer pending: show the server's list, then say why.
                        load()
                        error = R.string.imkit_gj_handled_elsewhere
                        engine.noteGroupChanged(topic)
                    }
                    reply?.code == 401 -> error = R.string.imkit_gj_not_allowed
                    else -> error = R.string.imkit_gj_failed
                }
            } finally {
                busy = null
            }
        }
    }

    val title = stringResource(R.string.imkit_gj_requests_title)
    Section(modifier.semantics { contentDescription = title }) {
        Heading(title)
        error?.let { ErrorText(stringResource(it)) }
        val list = requests
        when {
            list == null -> if (error == null) Muted(stringResource(R.string.imkit_gj_loading))
            list.isEmpty() -> Muted(stringResource(R.string.imkit_gj_no_requests))
            else -> list.forEach { r ->
                Row(Modifier.fillMaxWidth().padding(vertical = 6.dp), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    Column(Modifier.weight(1f)) {
                        Text(nameOf(r), fontSize = 15.sp, fontWeight = FontWeight.Medium, color = colors.textPrimary)
                        r.message?.let { Text(it, fontSize = 13.sp, color = colors.textSecondary) }
                    }
                    Secondary(stringResource(R.string.imkit_gj_reject), enabled = busy != r.id) { review(r, approve = false) }
                    Primary(stringResource(R.string.imkit_gj_approve), enabled = busy != r.id) { review(r, approve = true) }
                }
                Divider(color = colors.divider, thickness = 0.5.dp)
            }
        }
    }
}

/** The "admins approve people who join by QR code" switch; only the owner may change it ([editable]). */
@Composable
public fun JoinApprovalSetting(
    client: GroupJoinClient,
    topic: String,
    modifier: Modifier = Modifier,
    editable: Boolean = false,
) {
    val colors = ImTheme.colors
    val scope = rememberCoroutineScope()
    var on by remember(client, topic) { mutableStateOf<Boolean?>(null) }
    var error by remember(client, topic) { mutableStateOf<Int?>(null) }

    LaunchedEffect(client, topic) {
        try {
            on = client.requireApproval(topic)
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            error = R.string.imkit_gj_failed
        }
    }

    fun toggle(next: Boolean) {
        error = null
        // The switch follows the tap at once; the server's answer settles it.
        on = next
        scope.launch {
            try {
                on = client.setRequireApproval(topic, next)
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                on = !next
                error = if (e.refusedCode() == 401) R.string.imkit_gj_not_allowed else R.string.imkit_gj_failed
            }
        }
    }

    Column(modifier.fillMaxWidth().background(colors.surface).padding(horizontal = 16.dp, vertical = 6.dp)) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text(stringResource(R.string.imkit_gj_require_approval), fontSize = 15.sp, color = colors.textPrimary, modifier = Modifier.weight(1f))
            Switch(
                checked = on ?: false,
                onCheckedChange = ::toggle,
                enabled = editable && on != null,
                colors = SwitchDefaults.colors(checkedTrackColor = colors.accent),
            )
        }
        error?.let { ErrorText(stringResource(it)) }
    }
}

/** The notice for an event, for a banner or a notification, in [context]'s language. */
public fun groupJoinNotice(context: Context, event: GroupJoinEvent, nameOf: (uid: String) -> String = { it }): String = when (event) {
    is GroupJoinEvent.Request -> context.getString(R.string.imkit_gj_request_notice, nameOf(event.from), event.name)
    is GroupJoinEvent.Reviewed -> context.getString(if (event.approved) R.string.imkit_gj_approved_notice else R.string.imkit_gj_rejected_notice, event.name)
}

/** [s] cut to its first [max] code points, so a surrogate pair is never split. */
internal fun limitCodePoints(s: String, max: Int): String =
    if (s.codePointCount(0, s.length) <= max) s else s.substring(0, s.offsetByCodePoints(0, max))

// ------------------------------------------------------------------ pieces

@Composable
private fun Section(modifier: Modifier, content: @Composable ColumnScope.() -> Unit) {
    Column(
        modifier.fillMaxWidth().background(ImTheme.colors.surface, RoundedCornerShape(12.dp)).padding(16.dp),
        verticalArrangement = Arrangement.spacedBy(12.dp),
        content = content,
    )
}

@Composable
private fun Heading(text: String) = Text(text, fontSize = 17.sp, fontWeight = FontWeight.Medium, color = ImTheme.colors.textPrimary)

@Composable
private fun Body(text: String) = Text(text, fontSize = 15.sp, color = ImTheme.colors.textPrimary)

@Composable
private fun Muted(text: String) = Text(text, fontSize = 13.sp, color = ImTheme.colors.textSecondary)

@Composable
private fun ErrorText(text: String) = Text(text, fontSize = 13.sp, color = ImTheme.colors.error)

@Composable
private fun Primary(label: String, enabled: Boolean = true, onClick: () -> Unit) {
    val colors = ImTheme.colors
    Button(onClick = onClick, enabled = enabled, colors = ButtonDefaults.buttonColors(containerColor = colors.accent, contentColor = colors.onAccent)) {
        Text(label)
    }
}

@Composable
private fun Secondary(label: String, enabled: Boolean = true, onClick: () -> Unit) {
    OutlinedButton(onClick = onClick, enabled = enabled) { Text(label, color = ImTheme.colors.textPrimary) }
}
