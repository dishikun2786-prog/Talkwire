package io.imkit.ui.components

import androidx.compose.foundation.layout.size
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.Check
import androidx.compose.material.icons.rounded.DoneAll
import androidx.compose.material.icons.rounded.ErrorOutline
import androidx.compose.material.icons.rounded.Schedule
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import io.imkit.core.model.MessageStatus
import io.imkit.ui.R
import io.imkit.ui.theme.ImTheme

/**
 * The state of an own message: a clock while sending, a tick once stored,
 * two ticks once the peer read it ([read]), and a red mark that resends on
 * tap. The failed mark has a full 48 dp target: that is the one people need
 * to hit.
 */
@Composable
public fun MessageStatusIcon(status: MessageStatus, read: Boolean, modifier: Modifier = Modifier, onRetry: (() -> Unit)? = null) {
    val colors = ImTheme.colors
    when {
        status == MessageStatus.FAILED -> if (onRetry != null) {
            IconButton(onClick = onRetry, modifier = modifier) {
                Icon(Icons.Rounded.ErrorOutline, stringResource(R.string.imkit_send_failed_retry), tint = colors.error, modifier = Modifier.size(16.dp))
            }
        } else {
            Icon(Icons.Rounded.ErrorOutline, stringResource(R.string.imkit_send_failed), tint = colors.error, modifier = modifier.size(16.dp))
        }
        status == MessageStatus.SENDING ->
            Icon(Icons.Rounded.Schedule, stringResource(R.string.imkit_sending), tint = colors.textSecondary, modifier = modifier.size(12.dp))
        read ->
            Icon(Icons.Rounded.DoneAll, stringResource(R.string.imkit_read), tint = colors.read, modifier = modifier.size(12.dp))
        else ->
            Icon(Icons.Rounded.Check, stringResource(R.string.imkit_sent), tint = colors.textSecondary, modifier = modifier.size(12.dp))
    }
}
