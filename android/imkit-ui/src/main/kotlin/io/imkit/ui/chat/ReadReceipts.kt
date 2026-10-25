package io.imkit.ui.chat

import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.fadeIn
import androidx.compose.animation.scaleIn
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import io.imkit.core.model.ChatMessage
import io.imkit.ui.R
import io.imkit.ui.components.ImAlertDialog
import io.imkit.ui.components.ImAvatar
import io.imkit.ui.host.ImProfile
import io.imkit.ui.theme.ImTheme

/**
 * Who of [members] has read [message], from the conversation's read
 * positions; the sender never counts. A message without a seq (still
 * sending) has no readers yet.
 */
public fun readersOf(message: ChatMessage, cursors: Map<String, Long>, members: List<String>): List<String> {
    val seq = message.seq ?: return emptyList()
    if (cursors.isEmpty()) return emptyList()
    return members.filter { it != message.from && (cursors[it] ?: 0L) >= seq }
}

private val Slot = 20.dp
private val Inner = 18.dp
private val Step = 11.dp
private const val MaxShown = 3

/** Up to three overlapping avatars and "Read by N"; a tap opens the full list. */
@Composable
internal fun ReadReceiptRow(readers: List<ImProfile>, groupSize: Int, resolveUrl: (String?) -> String?) {
    var open by remember { mutableStateOf(false) }
    val colors = ImTheme.colors
    AnimatedVisibility(visible = readers.isNotEmpty(), enter = scaleIn(initialScale = 0.6f) + fadeIn()) {
        val shown = readers.take(MaxShown)
        val extra = readers.size - shown.size
        val slots = shown.size + if (extra > 0) 1 else 0
        Row(
            Modifier.padding(top = 1.dp).clip(RoundedCornerShape(8.dp)).clickable { open = true }.padding(vertical = 4.dp, horizontal = 2.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Box(Modifier.width(Step * (slots - 1) + Slot).height(Slot)) {
                shown.forEachIndexed { i, p ->
                    Box(Modifier.offset(x = Step * i).size(Slot).clip(CircleShape).background(colors.chatBackground), contentAlignment = Alignment.Center) {
                        ImAvatar(resolveUrl(p.avatarUrl), p.name, size = Inner)
                    }
                }
                if (extra > 0) {
                    Box(Modifier.offset(x = Step * shown.size).size(Slot).clip(CircleShape).background(colors.chatBackground), contentAlignment = Alignment.Center) {
                        Box(Modifier.size(Inner).clip(CircleShape).background(colors.divider), contentAlignment = Alignment.Center) {
                            Text("+$extra", fontSize = 8.sp, fontWeight = FontWeight.SemiBold, color = colors.textSecondary)
                        }
                    }
                }
            }
            Spacer(Modifier.width(4.dp))
            Text(stringResource(R.string.imkit_read_by_count, readers.size), fontSize = 11.sp, color = colors.textSecondary)
        }
    }
    if (open) ReadByListDialog(readers, (groupSize - 1).coerceAtLeast(0), resolveUrl) { open = false }
}

/** The full list. Rows are not clickable: group members cannot open each other's profiles. */
@Composable
internal fun ReadByListDialog(readers: List<ImProfile>, totalEligible: Int, resolveUrl: (String?) -> String?, onDismiss: () -> Unit) {
    val colors = ImTheme.colors
    val unread = (totalEligible - readers.size).coerceAtLeast(0)
    ImAlertDialog(
        onDismissRequest = onDismiss,
        title = stringResource(R.string.imkit_read_by_count, readers.size),
        confirmLabel = stringResource(R.string.imkit_ok),
        onConfirm = onDismiss,
        text = {
            LazyColumn(Modifier.heightIn(max = 320.dp)) {
                items(readers, key = { it.uid }) { p ->
                    Row(Modifier.padding(vertical = 8.dp, horizontal = 4.dp), verticalAlignment = Alignment.CenterVertically) {
                        ImAvatar(resolveUrl(p.avatarUrl), p.name, size = 40.dp)
                        Spacer(Modifier.width(12.dp))
                        Text(p.name, color = colors.textPrimary, fontSize = 15.sp)
                    }
                }
                if (totalEligible > 0) {
                    item {
                        Text(
                            if (unread > 0) stringResource(R.string.imkit_read_by_footer_partial, totalEligible, unread) else stringResource(R.string.imkit_read_by_footer_all, totalEligible),
                            color = colors.textHint,
                            fontSize = 12.sp,
                            modifier = Modifier.padding(top = 8.dp, start = 4.dp),
                        )
                    }
                }
            }
        },
    )
}
