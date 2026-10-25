package io.imkit.ui.chat

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.foundation.gestures.detectTransformGestures
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.outlined.Checklist
import androidx.compose.material.icons.outlined.ContentCopy
import androidx.compose.material.icons.outlined.Delete
import androidx.compose.material.icons.outlined.Download
import androidx.compose.material.icons.outlined.Forward
import androidx.compose.material.icons.outlined.Refresh
import androidx.compose.material.icons.outlined.Reply
import androidx.compose.material.icons.outlined.Share
import androidx.compose.material.icons.outlined.Undo
import androidx.compose.material3.Checkbox
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.compose.ui.window.DialogProperties
import coil.compose.AsyncImage
import io.imkit.core.model.ConversationId
import io.imkit.ui.R
import io.imkit.ui.components.ImAlertDialog
import io.imkit.ui.components.ImAvatar
import io.imkit.ui.message.MessageAction
import io.imkit.ui.theme.ImTheme
import io.imkit.ui.theme.LocalizedDialog
import io.imkit.ui.theme.LocalizedSheet

/** What a long press may do with a failed own message, besides the type's own actions. */
internal enum class FailedAction { RETRY, DISCARD }

/**
 * The long-press sheet. [actions] already reflects the message type and
 * whether the message is mine; [failed] adds resend/delete for a message
 * that did not go out.
 */
@Composable
internal fun MessageActionsSheet(
    actions: List<MessageAction>,
    failed: Boolean,
    onAction: (MessageAction) -> Unit,
    onFailedAction: (FailedAction) -> Unit,
    onDismiss: () -> Unit,
) {
    LocalizedSheet(onDismissRequest = onDismiss) {
        Column(Modifier.padding(bottom = 32.dp).testTag("imkit_actions")) {
            if (failed) {
                ActionRow(Icons.Outlined.Refresh, stringResource(R.string.imkit_action_retry)) { onFailedAction(FailedAction.RETRY); onDismiss() }
                ActionRow(Icons.Outlined.Delete, stringResource(R.string.imkit_action_discard), destructive = true) { onFailedAction(FailedAction.DISCARD); onDismiss() }
            }
            actions.forEach { a ->
                ActionRow(a.icon(), stringResource(a.label()), destructive = a == MessageAction.RECALL) { onAction(a); onDismiss() }
            }
            if (actions.isEmpty() && !failed) Spacer(Modifier.height(8.dp))
        }
    }
}

@Composable
private fun ActionRow(icon: ImageVector, label: String, destructive: Boolean = false, onClick: () -> Unit) {
    val colors = ImTheme.colors
    Row(Modifier.fillMaxWidth().clickable(onClick = onClick).padding(horizontal = 20.dp, vertical = 14.dp), verticalAlignment = Alignment.CenterVertically) {
        Icon(icon, null, tint = if (destructive) colors.error else colors.textSecondary, modifier = Modifier.size(24.dp))
        Spacer(Modifier.width(16.dp))
        Text(label, fontSize = 16.sp, color = if (destructive) colors.error else colors.textPrimary)
    }
}

private fun MessageAction.icon(): ImageVector = when (this) {
    MessageAction.COPY -> Icons.Outlined.ContentCopy
    MessageAction.SAVE -> Icons.Outlined.Download
    MessageAction.SHARE -> Icons.Outlined.Share
    MessageAction.REPLY -> Icons.Outlined.Reply
    MessageAction.FORWARD -> Icons.Outlined.Forward
    MessageAction.SELECT -> Icons.Outlined.Checklist
    MessageAction.RECALL -> Icons.Outlined.Undo
}

private fun MessageAction.label(): Int = when (this) {
    MessageAction.COPY -> R.string.imkit_action_copy
    MessageAction.SAVE -> R.string.imkit_action_save
    MessageAction.SHARE -> R.string.imkit_action_share
    MessageAction.REPLY -> R.string.imkit_action_reply
    MessageAction.FORWARD -> R.string.imkit_action_forward
    MessageAction.SELECT -> R.string.imkit_action_select
    MessageAction.RECALL -> R.string.imkit_action_recall
}

/**
 * A full-screen image: pinch to zoom, double tap to reset, tap anywhere or
 * the close button to leave, long press to save. It is a real dialog window,
 * so it sits above the screen whatever order things were composed in.
 */
@Composable
internal fun ImageViewerDialog(url: String, onSave: (() -> Unit)?, onDismiss: () -> Unit) {
    LocalizedDialog(onDismissRequest = onDismiss, properties = DialogProperties(usePlatformDefaultWidth = false)) {
        var scale by remember { mutableFloatStateOf(1f) }
        var dx by remember { mutableFloatStateOf(0f) }
        var dy by remember { mutableFloatStateOf(0f) }
        Box(
            Modifier
                .fillMaxSize()
                .background(Color.Black.copy(alpha = 0.95f))
                .pointerInput(Unit) {
                    detectTapGestures(
                        onTap = { onDismiss() },
                        onDoubleTap = { scale = 1f; dx = 0f; dy = 0f },
                        onLongPress = { onSave?.invoke() },
                    )
                }
                .pointerInput(Unit) {
                    detectTransformGestures { _, pan, zoom, _ ->
                        scale = (scale * zoom).coerceIn(0.5f, 5f)
                        dx += pan.x
                        dy += pan.y
                    }
                },
        ) {
            AsyncImage(
                model = url,
                contentDescription = stringResource(R.string.imkit_image),
                contentScale = ContentScale.Fit,
                modifier = Modifier.fillMaxSize().graphicsLayer { scaleX = scale; scaleY = scale; translationX = dx; translationY = dy },
            )
            IconButton(
                onClick = onDismiss,
                modifier = Modifier.align(Alignment.TopEnd).statusBarsPadding().padding(16.dp).background(Color.Black.copy(alpha = 0.45f), CircleShape),
            ) {
                Icon(Icons.Filled.Close, stringResource(R.string.imkit_close), tint = Color.White)
            }
        }
    }
}

/** A conversation as the forward picker lists it. */
internal data class ForwardTarget(val id: ConversationId, val title: String, val avatarUrl: String?)

/**
 * Picks where to forward. With [multiple], several targets and a send
 * button (one message to many chats); otherwise a tap picks the one target
 * (several messages bundled into one chat).
 */
@Composable
internal fun ForwardDialog(targets: List<ForwardTarget>, multiple: Boolean, onConfirm: (List<ConversationId>) -> Unit, onDismiss: () -> Unit) {
    val colors = ImTheme.colors
    var picked by remember { mutableStateOf(emptySet<ConversationId>()) }
    ImAlertDialog(
        onDismissRequest = onDismiss,
        title = stringResource(R.string.imkit_forward_to),
        confirmLabel = if (multiple) stringResource(R.string.imkit_send_count, picked.size) else stringResource(R.string.imkit_cancel),
        onConfirm = { if (multiple) onConfirm(targets.filter { it.id in picked }.map { it.id }) else onDismiss() },
        confirmEnabled = !multiple || picked.isNotEmpty(),
        dismissLabel = if (multiple) stringResource(R.string.imkit_cancel) else null,
        text = {
            if (targets.isEmpty()) {
                Box(Modifier.fillMaxWidth().height(120.dp), contentAlignment = Alignment.Center) {
                    Text(stringResource(R.string.imkit_no_chats), color = colors.textHint, fontSize = 14.sp)
                }
            } else {
                LazyColumn(Modifier.heightIn(max = 360.dp)) {
                    items(targets, key = { it.id.id }) { t ->
                        Row(
                            Modifier.fillMaxWidth().clickable {
                                if (multiple) picked = if (t.id in picked) picked - t.id else picked + t.id else onConfirm(listOf(t.id))
                            }.padding(vertical = 8.dp, horizontal = 4.dp),
                            verticalAlignment = Alignment.CenterVertically,
                        ) {
                            if (multiple) {
                                Checkbox(checked = t.id in picked, onCheckedChange = { on -> picked = if (on) picked + t.id else picked - t.id })
                                Spacer(Modifier.width(4.dp))
                            }
                            ImAvatar(t.avatarUrl, t.title, size = 40.dp)
                            Spacer(Modifier.width(12.dp))
                            Text(t.title, color = colors.textPrimary, fontSize = 15.sp, maxLines = 1)
                        }
                    }
                }
            }
        },
    )
}
