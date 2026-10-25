package io.imkit.ui.chat

import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Checkbox
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.Immutable
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import io.imkit.core.json.JsonObject
import io.imkit.core.json.jsonObject
import io.imkit.core.model.ChatMessage
import io.imkit.core.model.MessageKind
import io.imkit.ui.R
import io.imkit.ui.components.ImAvatar
import io.imkit.ui.components.ImTime
import io.imkit.ui.components.MessageStatusIcon
import io.imkit.ui.host.ImProfile
import io.imkit.ui.message.MessageScope
import io.imkit.ui.message.MessageType
import io.imkit.ui.message.extraObject
import io.imkit.ui.theme.ImTheme

/** The message a reply quotes, carried in `extra.replyTo` of the reply. */
@Immutable
public data class ReplyRef(val seq: Long, val from: String, val text: String)

public fun ChatMessage.replyRef(): ReplyRef? {
    val r = extraObject()?.get("replyTo") as? JsonObject ?: return null
    val seq = r.long("seq") ?: return null
    return ReplyRef(seq, r.string("from") ?: "", r.string("text") ?: "")
}

/** The `extra` of a reply to [ref]; the quoted text is cut to keep messages small. */
public fun replyExtra(ref: ReplyRef): JsonObject = jsonObject {
    put("replyTo", jsonObject {
        put("seq", ref.seq)
        put("from", ref.from)
        put("text", ref.text.take(120))
    })
}

/**
 * One message: avatar, optional sender name, the bubble (or card) drawn by
 * the message's [type], then time, status and read receipts. Rows of others
 * are on the left, own rows on the right.
 */
@OptIn(ExperimentalFoundationApi::class)
@Composable
internal fun MessageRow(
    message: ChatMessage,
    type: MessageType,
    isMine: Boolean,
    sender: ImProfile?,
    senderName: String,
    showAvatar: Boolean,
    showSenderName: Boolean,
    read: Boolean,
    readers: List<ImProfile>,
    groupSize: Int,
    scope: MessageScope,
    replyName: (String) -> String,
    onRetry: () -> Unit,
    onAvatarClick: (() -> Unit)?,
    selecting: Boolean,
    selected: Boolean,
    onToggleSelect: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val colors = ImTheme.colors
    val dimens = ImTheme.dimens
    val maxWidth = (LocalConfiguration.current.screenWidthDp.dp * 0.65f).coerceAtMost(300.dp)
    if (message.kind == MessageKind.SYSTEM) {
        // System notes span the row and have no avatar, time or status.
        Box(modifier.fillMaxWidth().padding(horizontal = 24.dp, vertical = 2.dp)) { type.Content(message, scope) }
        return
    }
    Row(
        modifier
            .fillMaxWidth()
            .then(if (selecting) Modifier.clickable(onClick = onToggleSelect) else Modifier)
            .padding(horizontal = 12.dp, vertical = 3.dp),
        horizontalArrangement = if (isMine) Arrangement.End else Arrangement.Start,
        verticalAlignment = Alignment.Top,
    ) {
        if (selecting) {
            Checkbox(checked = selected, onCheckedChange = { onToggleSelect() }, modifier = Modifier.padding(end = 4.dp, top = 4.dp).size(40.dp))
        }
        if (!isMine) {
            Box(Modifier.size(dimens.avatarInChat)) {
                if (showAvatar) {
                    ImAvatar(
                        url = scope.resolveUrl(sender?.avatarUrl),
                        name = senderName,
                        size = dimens.avatarInChat,
                        modifier = if (onAvatarClick != null && !selecting) Modifier.clickable(onClick = onAvatarClick) else Modifier,
                    )
                }
            }
            Spacer(Modifier.width(8.dp))
        } else {
            Spacer(Modifier.width(if (selecting) 14.dp else 60.dp))
        }
        Column(Modifier.widthIn(max = maxWidth), horizontalAlignment = if (isMine) Alignment.End else Alignment.Start) {
            if (showSenderName && !isMine) {
                Text(senderName, fontSize = 12.sp, color = colors.textSecondary, fontWeight = FontWeight.Medium, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.padding(start = 4.dp, bottom = 2.dp))
            }
            if (message.forwardFromMsgId != null) {
                Text(stringResource(R.string.imkit_forwarded), fontSize = 11.sp, color = colors.textSecondary, modifier = Modifier.padding(bottom = 2.dp, start = 4.dp))
            }
            Box {
                if (type.framed) {
                    val shape = if (isMine) dimens.bubbleMine else dimens.bubbleTheirs
                    val failed = message.status == io.imkit.core.model.MessageStatus.FAILED
                    Column(
                        Modifier
                            .clip(shape)
                            .then(if (failed) Modifier.border(1.dp, colors.error, shape) else Modifier)
                            .background(if (isMine) colors.bubbleMine else colors.bubbleTheirs, shape)
                            .combinedClickable(onClick = {}, onLongClick = scope.onLongPress)
                            .padding(horizontal = 12.dp, vertical = 8.dp),
                    ) {
                        message.replyRef()?.let { ReplyQuote(it, replyName(it.from), isMine) }
                        type.Content(message, scope)
                    }
                } else {
                    type.Content(message, scope)
                }
                if (selecting) {
                    // Swallows taps so a selection tap does not also open the image or play the clip.
                    Box(Modifier.matchParentSize().clickable(remember { MutableInteractionSource() }, null, onClick = onToggleSelect))
                }
            }
            Row(verticalAlignment = Alignment.CenterVertically, modifier = Modifier.padding(top = 1.dp)) {
                Text(ImTime.messageTime(LocalContext.current, message.ts), fontSize = 11.sp, color = colors.textSecondary)
                if (isMine) {
                    Spacer(Modifier.width(4.dp))
                    MessageStatusIcon(message.status, read, onRetry = onRetry)
                }
            }
            if (isMine && readers.isNotEmpty()) ReadReceiptRow(readers, groupSize, scope.resolveUrl)
        }
        if (!isMine) Spacer(Modifier.width(60.dp))
    }
}

@Composable
private fun ReplyQuote(ref: ReplyRef, name: String, isMine: Boolean) {
    val colors = ImTheme.colors
    val fg = if (isMine) colors.onBubbleMine else colors.onBubbleTheirs
    Row(Modifier.padding(bottom = 6.dp).background(fg.copy(alpha = 0.08f), RoundedCornerShape(4.dp))) {
        Box(Modifier.width(3.dp).height(36.dp).background(fg.copy(alpha = 0.5f)))
        Column(Modifier.padding(horizontal = 8.dp, vertical = 3.dp)) {
            Text(name, fontSize = 11.sp, color = fg.copy(alpha = 0.8f), fontWeight = FontWeight.Medium, maxLines = 1)
            Text(ref.text, fontSize = 12.sp, color = fg.copy(alpha = 0.7f), maxLines = 1, overflow = TextOverflow.Ellipsis)
        }
    }
}
