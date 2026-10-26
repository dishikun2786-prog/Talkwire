package io.talkwire.ui.components

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import io.talkwire.ui.theme.ImTheme
import io.talkwire.ui.theme.LocalizedDialog

/**
 * The kit's alert dialog. Material's `AlertDialog` opens its own window and
 * loses the screen's language (see [io.talkwire.ui.theme.LocalizedSheet]); this
 * one is drawn inside [LocalizedDialog] so it keeps it.
 */
@Composable
public fun ImAlertDialog(
    onDismissRequest: () -> Unit,
    title: String?,
    confirmLabel: String,
    onConfirm: () -> Unit,
    dismissLabel: String? = null,
    destructive: Boolean = false,
    confirmEnabled: Boolean = true,
    text: (@Composable () -> Unit)? = null,
) {
    val colors = ImTheme.colors
    LocalizedDialog(onDismissRequest = onDismissRequest) {
        Surface(shape = RoundedCornerShape(24.dp), color = colors.surface, modifier = Modifier.widthIn(min = 280.dp, max = 560.dp)) {
            Column(Modifier.padding(start = 24.dp, end = 24.dp, top = 24.dp, bottom = 12.dp)) {
                if (title != null) {
                    Text(title, fontSize = 20.sp, fontWeight = FontWeight.Medium, color = colors.textPrimary)
                    Spacer(Modifier.height(16.dp))
                }
                if (text != null) {
                    Box(Modifier.fillMaxWidth()) { text() }
                    Spacer(Modifier.height(12.dp))
                }
                Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.End) {
                    if (dismissLabel != null) {
                        TextButton(onClick = onDismissRequest) { Text(dismissLabel, color = colors.textSecondary) }
                    }
                    TextButton(onClick = onConfirm, enabled = confirmEnabled) {
                        Text(confirmLabel, color = if (!confirmEnabled) colors.textHint else if (destructive) colors.error else colors.accent)
                    }
                }
            }
        }
    }
}

/** A yes/no question; [message] below the title, or as the only text. */
@Composable
public fun ImConfirmDialog(
    message: String,
    confirmLabel: String,
    dismissLabel: String,
    onConfirm: () -> Unit,
    onDismiss: () -> Unit,
    title: String? = null,
    destructive: Boolean = false,
) {
    ImAlertDialog(
        onDismissRequest = onDismiss,
        title = title,
        confirmLabel = confirmLabel,
        onConfirm = onConfirm,
        dismissLabel = dismissLabel,
        destructive = destructive,
        text = { Text(message, fontSize = 15.sp, color = ImTheme.colors.textPrimary) },
    )
}
