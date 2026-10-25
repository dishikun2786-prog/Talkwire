package io.imkit.ui.chat

import android.widget.Toast
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.Mic
import androidx.compose.material.icons.outlined.AddCircleOutline
import androidx.compose.material.icons.outlined.EmojiEmotions
import androidx.compose.material.icons.outlined.Keyboard
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.SolidColor
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.TextRange
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.input.TextFieldValue
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import io.imkit.ui.R
import io.imkit.ui.theme.ImTheme

/** What the hold-to-talk button reports; see [holdToTalk]. */
public class VoiceInput(
    public val onPress: () -> Unit,
    public val onDrag: (Float) -> Unit,
    public val onRelease: () -> Unit,
)

/**
 * The input bar: voice/keyboard toggle, the text field (or the hold-to-talk
 * button), emoji, the attachment panel toggle and — only while there is
 * text — send. The text is hoisted ([value]) so the screen can keep the
 * draft and insert mentions.
 *
 * Typing notices are throttled: one when the field goes from empty to
 * non-empty (the moment the peer most wants to see it), then at most one per
 * [typingThrottleMs].
 */
@Composable
public fun MessageComposer(
    value: TextFieldValue,
    onValueChange: (TextFieldValue) -> Unit,
    onSend: (String) -> Unit,
    onToggleAttachments: () -> Unit,
    modifier: Modifier = Modifier,
    maxLength: Int = 20_000,
    typingThrottleMs: Long = 3_000,
    onTyping: () -> Unit = {},
    voice: VoiceInput? = null,
    replyTo: ReplyRef? = null,
    replyName: String = "",
    onCancelReply: () -> Unit = {},
    /** Called when "@" is typed at a word start; the screen opens its member picker. */
    onMention: (() -> Unit)? = null,
    clock: () -> Long = System::currentTimeMillis,
) {
    val colors = ImTheme.colors
    val context = LocalContext.current
    var showEmoji by remember { mutableStateOf(false) }
    var voiceMode by remember { mutableStateOf(false) }
    var lastTypingAt by remember { mutableLongStateOf(0L) }
    val text = value.text

    fun change(next: TextFieldValue) {
        val before = value.text
        if (onMention != null && next.text.length == before.length + 1 && next.selection.collapsed) {
            val at = next.selection.start - 1
            if (at >= 0 && next.text[at] == '@' && (at == 0 || next.text[at - 1].isWhitespace())) onMention()
        }
        onValueChange(next)
        if (next.text.isNotEmpty() && next.text != before) {
            val now = clock()
            if (before.isEmpty() || now - lastTypingAt >= typingThrottleMs) {
                lastTypingAt = now
                onTyping()
            }
        }
    }

    Column(modifier.fillMaxWidth().background(colors.surface)) {
        if (replyTo != null) {
            Row(Modifier.fillMaxWidth().padding(horizontal = 12.dp, vertical = 6.dp), verticalAlignment = Alignment.CenterVertically) {
                Box(Modifier.width(3.dp).height(32.dp).background(colors.accent))
                Spacer(Modifier.width(8.dp))
                Column(Modifier.weight(1f)) {
                    Text(stringResource(R.string.imkit_replying_to, replyName), fontSize = 11.sp, color = colors.accent)
                    Text(replyTo.text, fontSize = 13.sp, color = colors.textPrimary, maxLines = 1)
                }
                IconButton(onClick = onCancelReply, modifier = Modifier.size(32.dp)) {
                    Icon(Icons.Filled.Close, stringResource(R.string.imkit_cancel), tint = colors.textSecondary, modifier = Modifier.size(16.dp))
                }
            }
        }
        Row(Modifier.padding(horizontal = 8.dp, vertical = 4.dp), verticalAlignment = Alignment.CenterVertically) {
            if (voice != null) {
                IconButton(onClick = { voiceMode = !voiceMode; if (voiceMode) showEmoji = false }) {
                    Icon(
                        if (voiceMode) Icons.Outlined.Keyboard else Icons.Filled.Mic,
                        stringResource(if (voiceMode) R.string.imkit_keyboard else R.string.imkit_voice_input),
                        tint = colors.textSecondary,
                    )
                }
            }
            if (voiceMode && voice != null) {
                var pressed by remember { mutableStateOf(false) }
                Surface(
                    modifier = Modifier
                        .weight(1f)
                        .height(42.dp)
                        .testTag("imkit_hold_to_talk")
                        .holdToTalk(
                            onPress = { pressed = true; voice.onPress() },
                            onDrag = voice.onDrag,
                            onRelease = { pressed = false; voice.onRelease() },
                        ),
                    shape = RoundedCornerShape(20.dp),
                    color = if (pressed) colors.divider else colors.inputBackground,
                ) {
                    Box(contentAlignment = Alignment.Center) {
                        Text(stringResource(if (pressed) R.string.imkit_release_to_send else R.string.imkit_hold_to_talk), fontSize = 15.sp, color = colors.textPrimary)
                    }
                }
            } else {
                Surface(Modifier.weight(1f), shape = RoundedCornerShape(20.dp), color = colors.inputBackground) {
                    BasicTextField(
                        value = value,
                        onValueChange = ::change,
                        modifier = Modifier.padding(horizontal = 16.dp, vertical = 10.dp).testTag("imkit_input"),
                        textStyle = TextStyle(fontSize = 15.sp, color = colors.textPrimary),
                        cursorBrush = SolidColor(colors.accent),
                        maxLines = 4,
                        decorationBox = { inner ->
                            if (text.isEmpty()) Text(stringResource(R.string.imkit_type_message), fontSize = 15.sp, color = colors.textHint)
                            inner()
                        },
                    )
                }
            }
            IconButton(onClick = { showEmoji = !showEmoji }) {
                Icon(Icons.Outlined.EmojiEmotions, stringResource(R.string.imkit_emoji), tint = if (showEmoji) colors.accent else colors.textSecondary)
            }
            IconButton(onClick = { showEmoji = false; onToggleAttachments() }) {
                Icon(Icons.Outlined.AddCircleOutline, stringResource(R.string.imkit_attach), tint = colors.textSecondary)
            }
            if (!voiceMode && text.isNotBlank()) {
                val tooLong = text.length > maxLength
                Surface(
                    onClick = {
                        // Refused here, not hidden: a send button that disappears looks like a bug.
                        if (tooLong) {
                            Toast.makeText(context, context.getString(R.string.imkit_too_long, maxLength), Toast.LENGTH_SHORT).show()
                        } else {
                            onSend(text)
                        }
                    },
                    shape = RoundedCornerShape(6.dp),
                    color = if (tooLong) colors.divider else colors.accent,
                    modifier = Modifier.padding(start = 2.dp).testTag("imkit_send"),
                ) {
                    Text(stringResource(R.string.imkit_send), color = colors.onAccent, fontSize = 14.sp, modifier = Modifier.padding(horizontal = 12.dp, vertical = 7.dp))
                }
            }
        }
        if (!voiceMode && text.length > maxLength - maxLength / 10) {
            Text(
                "${text.length} / $maxLength",
                fontSize = 11.sp,
                color = if (text.length > maxLength) colors.error else colors.textSecondary,
                modifier = Modifier.align(Alignment.End).padding(end = 16.dp, bottom = 4.dp),
            )
        }
        AnimatedVisibility(visible = showEmoji) {
            EmojiPicker(onEmoji = { emoji ->
                val sel = value.selection
                val next = value.text.substring(0, sel.min) + emoji + value.text.substring(sel.max)
                change(TextFieldValue(next, TextRange(sel.min + emoji.length)))
            })
        }
    }
}
