package io.imkit.ui.theme

import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties

/**
 * A sheet that keeps the language and theme of the screen that opened it.
 *
 * A bottom sheet, like a dialog, is its own window: its composition provides
 * `LocalContext` and `LocalConfiguration` again from the window, which drops
 * a language set through [ImTheme]'s `locale` (or any other context override
 * of the app). The sheet would then speak the system language while the
 * screen behind it speaks the app's (C8). Re-providing them inside fixes it.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
public fun LocalizedSheet(
    onDismissRequest: () -> Unit,
    modifier: Modifier = Modifier,
    skipPartiallyExpanded: Boolean = true,
    content: @Composable ColumnScope.() -> Unit,
) {
    // Created here so callers do not need Material's experimental sheet API.
    val sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = skipPartiallyExpanded)
    val context = LocalContext.current
    val configuration = LocalConfiguration.current
    val colors = LocalImColors.current
    val dimens = LocalImDimens.current
    ModalBottomSheet(
        onDismissRequest = onDismissRequest,
        modifier = modifier,
        sheetState = sheetState,
        containerColor = colors.surface,
    ) {
        CompositionLocalProvider(
            LocalContext provides context,
            LocalConfiguration provides configuration,
            LocalImColors provides colors,
            LocalImDimens provides dimens,
        ) {
            content()
        }
    }
}

/** A dialog that keeps the screen's language and theme; see [LocalizedSheet]. */
@Composable
public fun LocalizedDialog(
    onDismissRequest: () -> Unit,
    properties: DialogProperties = DialogProperties(),
    content: @Composable () -> Unit,
) {
    val context = LocalContext.current
    val configuration = LocalConfiguration.current
    val colors = LocalImColors.current
    val dimens = LocalImDimens.current
    Dialog(onDismissRequest = onDismissRequest, properties = properties) {
        CompositionLocalProvider(
            LocalContext provides context,
            LocalConfiguration provides configuration,
            LocalImColors provides colors,
            LocalImDimens provides dimens,
            content = content,
        )
    }
}
