// SPDX-License-Identifier: AGPL-3.0-only
package io.imkit.e2ee

import android.content.Context
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.ArrowBack
import androidx.compose.material.icons.outlined.Send
import androidx.compose.material.icons.outlined.Lock
import androidx.compose.material.icons.outlined.MoreVert
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.Text
import androidx.compose.material3.TextField
import androidx.compose.material3.TextFieldDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontStyle
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import io.imkit.ui.components.ImAlertDialog
import io.imkit.ui.components.ImConfirmDialog
import io.imkit.ui.theme.ImTheme
import kotlinx.coroutines.launch

/**
 * The text for a failure class (red line C10): what happened and what to do,
 * never a blanket "try again later". [peerName] names the peer of a direct
 * chat in [E2eeFailure.PEER_KEYS_UNAVAILABLE], which reads that class as
 * "not set up yet": only right where the peer is known to be a contact.
 */
public fun e2eeFailureText(context: Context, failure: E2eeFailure, peerName: String): String = when (failure) {
    E2eeFailure.PEER_KEYS_UNAVAILABLE -> context.getString(R.string.imkit_e2ee_failed_peer_keys, peerName)
    E2eeFailure.NOT_ALLOWED -> context.getString(R.string.imkit_e2ee_failed_not_allowed)
    E2eeFailure.GROUP_NOT_ENCRYPTED -> context.getString(R.string.imkit_e2ee_failed_group_not_encrypted)
    E2eeFailure.DISABLED -> context.getString(R.string.imkit_e2ee_failed_disabled)
    E2eeFailure.OFFLINE -> context.getString(R.string.imkit_e2ee_failed_offline)
    E2eeFailure.CRYPTO -> context.getString(R.string.imkit_e2ee_failed_crypto)
    E2eeFailure.SERVER -> context.getString(R.string.imkit_e2ee_failed_server)
}

/** A line about [notice] for a banner or a notification; it never contains message text. */
public fun e2eeNoticeText(context: Context, notice: E2eeNotice, nameOf: (String) -> String): String = when (notice) {
    is E2eeNotice.Incoming -> context.getString(R.string.imkit_e2ee_notice_incoming)
    is E2eeNotice.Ended -> context.getString(R.string.imkit_e2ee_notice_ended, nameOf(notice.peer))
    is E2eeNotice.Cleared -> context.getString(R.string.imkit_e2ee_notice_cleared, nameOf(notice.from))
    is E2eeNotice.GroupEncryptionOn -> context.getString(R.string.imkit_e2ee_notice_group_on)
}

/** The safety number in groups of five digits, four groups a line, as Signal shows it. */
internal fun safetyNumberLines(number: String): List<String> =
    number.chunked(5).chunked(4).map { it.joinToString(" ") }

/**
 * An encrypted conversation. For a direct chat give [peer]; for a group
 * give [members], which is asked before each send (whoever lacks my key gets
 * it first). [title] is what the top bar shows, and the peer's name in texts.
 */
@Composable
public fun EncryptedChatScreen(
    chats: E2eeChats,
    topic: String,
    title: String,
    modifier: Modifier = Modifier,
    peer: String? = null,
    members: (suspend () -> Collection<String>)? = null,
    nameOf: (String) -> String = { it },
    onBack: (() -> Unit)? = null,
) {
    val colors = ImTheme.colors
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    val messages by remember(topic) { chats.messages(topic) }.collectAsState()
    var draft by remember(topic) { mutableStateOf("") }
    var banner by remember(topic) { mutableStateOf<String?>(null) }
    var menu by remember { mutableStateOf(false) }
    var dialog by remember { mutableStateOf<Dialog?>(null) }
    val list = rememberLazyListState()

    LaunchedEffect(messages.size) { if (messages.isNotEmpty()) list.animateScrollToItem(messages.size - 1) }

    fun report(failure: E2eeFailure?) {
        banner = failure?.let { e2eeFailureText(context, it, title) }
    }

    fun send() {
        val text = draft.trim()
        if (text.isEmpty()) return
        draft = ""
        scope.launch {
            if (peer != null) {
                when (val r = chats.sendDirect(peer, text)) {
                    is E2eeResult.Ok -> report(null)
                    is E2eeResult.Failed -> {
                        report(r.failure)
                        // Nothing was kept when encrypting failed: give the text back.
                        if (r.failure == E2eeFailure.PEER_KEYS_UNAVAILABLE || r.failure == E2eeFailure.CRYPTO) draft = text
                    }
                }
            } else {
                when (val r = chats.sendGroup(topic, members?.invoke() ?: emptyList(), text)) {
                    is E2eeResult.Ok -> banner = if (r.value.withoutKey.isEmpty()) null else context.getString(R.string.imkit_e2ee_without_key)
                    is E2eeResult.Failed -> report(r.failure)
                }
            }
        }
    }

    Column(modifier.fillMaxSize().background(colors.chatBackground).imePadding()) {
        Row(Modifier.fillMaxWidth().background(colors.surface).padding(horizontal = 4.dp, vertical = 4.dp), verticalAlignment = Alignment.CenterVertically) {
            if (onBack != null) {
                IconButton(onClick = onBack) { Icon(Icons.Outlined.ArrowBack, stringResource(R.string.imkit_e2ee_back), tint = colors.textPrimary) }
            } else {
                Spacer(Modifier.width(12.dp))
            }
            Column(Modifier.weight(1f)) {
                Text(title, color = colors.textPrimary, fontSize = 17.sp, maxLines = 1)
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Icon(Icons.Outlined.Lock, null, tint = colors.online, modifier = Modifier.size(12.dp))
                    Spacer(Modifier.width(4.dp))
                    Text(stringResource(R.string.imkit_e2ee_encrypted), color = colors.textSecondary, fontSize = 12.sp)
                }
            }
            Box {
                IconButton(onClick = { menu = true }) { Icon(Icons.Outlined.MoreVert, stringResource(R.string.imkit_e2ee_more), tint = colors.textPrimary) }
                DropdownMenu(expanded = menu, onDismissRequest = { menu = false }) {
                    if (peer != null) {
                        DropdownMenuItem(text = { Text(stringResource(R.string.imkit_e2ee_safety_number)) }, onClick = {
                            menu = false
                            scope.launch { dialog = Dialog.Safety(chats.safetyNumber(peer)) }
                        })
                    }
                    DropdownMenuItem(text = { Text(stringResource(R.string.imkit_e2ee_clear)) }, onClick = { menu = false; dialog = Dialog.Clear })
                    if (peer != null) {
                        DropdownMenuItem(text = { Text(stringResource(R.string.imkit_e2ee_end)) }, onClick = { menu = false; dialog = Dialog.End })
                    }
                }
            }
        }
        banner?.let {
            Text(
                it,
                color = colors.error,
                fontSize = 13.sp,
                modifier = Modifier.fillMaxWidth().background(colors.surface).padding(horizontal = 16.dp, vertical = 8.dp),
            )
        }
        Box(Modifier.weight(1f).fillMaxWidth()) {
            if (messages.isEmpty()) {
                Text(
                    stringResource(R.string.imkit_e2ee_empty),
                    color = colors.textSecondary,
                    fontSize = 13.sp,
                    textAlign = TextAlign.Center,
                    modifier = Modifier.align(Alignment.Center).padding(32.dp),
                )
            }
            LazyColumn(Modifier.fillMaxSize(), state = list, verticalArrangement = Arrangement.spacedBy(6.dp)) {
                item { Spacer(Modifier.size(4.dp)) }
                items(messages, key = { it.id }) { m ->
                    Bubble(m, showName = peer == null && !m.mine, name = nameOf(m.from)) {
                        scope.launch {
                            when (val r = chats.resend(m.id, members?.invoke() ?: emptyList())) {
                                is E2eeResult.Ok -> report(null)
                                is E2eeResult.Failed -> report(r.failure)
                            }
                        }
                    }
                }
                item { Spacer(Modifier.size(4.dp)) }
            }
        }
        Row(Modifier.fillMaxWidth().background(colors.surface).padding(8.dp), verticalAlignment = Alignment.CenterVertically) {
            TextField(
                value = draft,
                onValueChange = { draft = it },
                placeholder = { Text(stringResource(R.string.imkit_e2ee_hint), color = colors.textHint) },
                modifier = Modifier.weight(1f),
                shape = RoundedCornerShape(20.dp),
                colors = TextFieldDefaults.colors(
                    focusedContainerColor = colors.inputBackground,
                    unfocusedContainerColor = colors.inputBackground,
                    focusedIndicatorColor = colors.inputBackground,
                    unfocusedIndicatorColor = colors.inputBackground,
                    focusedTextColor = colors.textPrimary,
                    unfocusedTextColor = colors.textPrimary,
                ),
                maxLines = 5,
            )
            IconButton(onClick = ::send, enabled = draft.isNotBlank()) {
                Icon(Icons.Outlined.Send, stringResource(R.string.imkit_e2ee_send), tint = if (draft.isNotBlank()) colors.accent else colors.textHint)
            }
        }
    }

    when (val d = dialog) {
        null -> Unit
        is Dialog.Safety -> ImAlertDialog(
            onDismissRequest = { dialog = null },
            title = stringResource(R.string.imkit_e2ee_safety_number),
            confirmLabel = stringResource(R.string.imkit_e2ee_close),
            onConfirm = { dialog = null },
        ) {
            Column {
                if (d.number == null) {
                    Text(stringResource(R.string.imkit_e2ee_safety_number_none), color = colors.textSecondary)
                } else {
                    Text(stringResource(R.string.imkit_e2ee_safety_number_explain, title), color = colors.textSecondary, fontSize = 13.sp)
                    Spacer(Modifier.size(12.dp))
                    safetyNumberLines(d.number).forEach { line ->
                        Text(line, color = colors.textPrimary, fontFamily = FontFamily.Monospace, fontSize = 16.sp)
                    }
                }
            }
        }
        Dialog.Clear -> ImConfirmDialog(
            message = if (peer != null) stringResource(R.string.imkit_e2ee_clear_confirm, title) else stringResource(R.string.imkit_e2ee_clear_group_confirm),
            confirmLabel = stringResource(R.string.imkit_e2ee_clear),
            dismissLabel = stringResource(R.string.imkit_e2ee_cancel),
            destructive = true,
            onConfirm = {
                dialog = null
                scope.launch {
                    val r = if (peer != null) chats.clearDirect(peer) else chats.clearGroup(topic)
                    // The local copy is cleared either way; only a group refusal is worth saying.
                    report((r as? E2eeResult.Failed)?.failure?.takeIf { peer == null && it == E2eeFailure.NOT_ALLOWED })
                }
            },
            onDismiss = { dialog = null },
        )
        Dialog.End -> ImConfirmDialog(
            message = stringResource(R.string.imkit_e2ee_end_confirm),
            confirmLabel = stringResource(R.string.imkit_e2ee_end),
            dismissLabel = stringResource(R.string.imkit_e2ee_cancel),
            destructive = true,
            onConfirm = {
                dialog = null
                scope.launch {
                    if (peer != null) chats.endDirect(peer)
                    onBack?.invoke()
                }
            },
            onDismiss = { dialog = null },
        )
    }
}

private sealed interface Dialog {
    class Safety(val number: String?) : Dialog

    object Clear : Dialog

    object End : Dialog
}

@Composable
private fun Bubble(m: E2eeMessage, showName: Boolean, name: String, onResend: () -> Unit) {
    val colors = ImTheme.colors
    val dimens = ImTheme.dimens
    Row(Modifier.fillMaxWidth().padding(horizontal = 12.dp), horizontalArrangement = if (m.mine) Arrangement.End else Arrangement.Start) {
        Column(horizontalAlignment = if (m.mine) Alignment.End else Alignment.Start) {
            if (showName) Text(name, color = colors.textSecondary, fontSize = 12.sp, modifier = Modifier.padding(bottom = 2.dp))
            val undecryptable = m.status == E2eeMessage.Status.UNDECRYPTABLE
            Text(
                if (undecryptable) stringResource(R.string.imkit_e2ee_undecryptable) else m.text,
                color = if (m.mine) colors.onBubbleMine else colors.onBubbleTheirs,
                fontStyle = if (undecryptable) FontStyle.Italic else FontStyle.Normal,
                fontSize = 16.sp,
                modifier = Modifier
                    .widthIn(max = 280.dp)
                    .background(if (m.mine) colors.bubbleMine else colors.bubbleTheirs, if (m.mine) dimens.bubbleMine else dimens.bubbleTheirs)
                    .padding(horizontal = 12.dp, vertical = 8.dp),
            )
            if (m.status == E2eeMessage.Status.FAILED) {
                Text(
                    stringResource(R.string.imkit_e2ee_not_sent),
                    color = colors.error,
                    fontSize = 12.sp,
                    modifier = Modifier.padding(top = 2.dp).clickable(onClick = onResend),
                )
            }
        }
    }
}
