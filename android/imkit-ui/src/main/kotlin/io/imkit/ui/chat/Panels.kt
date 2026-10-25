package io.imkit.ui.chat

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.AttachFile
import androidx.compose.material.icons.outlined.PhotoCamera
import androidx.compose.material.icons.outlined.PhotoLibrary
import androidx.compose.material3.Icon
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import io.imkit.ui.R
import io.imkit.ui.host.AttachmentAction
import io.imkit.ui.theme.ImTheme

private val EMOJI = listOf(
    "😀", "😃", "😄", "😁", "😅", "😂", "🤣", "😊", "😇", "🙂", "😉", "😌", "😍", "🥰", "😘", "😗",
    "😋", "😛", "😜", "🤪", "😝", "🤑", "🤗", "🤭", "🤫", "🤔", "🤐", "🤨", "😐", "😑", "😶", "😏",
    "😒", "🙄", "😬", "🤥", "😔", "😪", "🤤", "😴", "😷", "🤒", "🤕", "🤢", "🤮", "🤧", "🥵", "🥶",
    "😵", "🤯", "🤠", "🥳", "🥸", "😎", "🤓", "🧐", "😕", "😟", "🙁", "😮", "😯", "😲", "😳", "🥺",
    "😦", "😧", "😨", "😰", "😥", "😢", "😭", "😱", "😖", "😣", "😞", "😓", "😩", "😫", "🥱", "😤",
    "😡", "😠", "🤬", "😈", "👿", "💀", "☠️", "💩", "🤡", "👹", "👺", "👻", "👽", "👾", "🤖", "🎃",
    "👍", "👎", "👏", "🙌", "🤝", "💪", "🤞", "✌️", "🤟", "👌", "🤌", "🤏", "👆", "👇", "👉", "👈",
    "❤️", "🧡", "💛", "💚", "💙", "💜", "🖤", "🤍", "🤎", "💔", "❣️", "💕", "💞", "💓", "💗", "💖",
    "🎉", "🎊", "🎂", "🔥", "⭐", "🌟", "✨", "💯", "✅", "❌", "❓", "❗", "💤", "💢", "💦", "💨",
)

@Composable
internal fun EmojiPicker(onEmoji: (String) -> Unit, modifier: Modifier = Modifier) {
    LazyVerticalGrid(
        columns = GridCells.Fixed(8),
        contentPadding = PaddingValues(8.dp),
        horizontalArrangement = Arrangement.spacedBy(4.dp),
        verticalArrangement = Arrangement.spacedBy(4.dp),
        modifier = modifier.fillMaxWidth().height(220.dp).background(ImTheme.colors.inputBackground),
    ) {
        items(EMOJI) { e ->
            Box(Modifier.size(36.dp).clip(RoundedCornerShape(6.dp)).clickable { onEmoji(e) }, contentAlignment = Alignment.Center) {
                Text(e, fontSize = 20.sp)
            }
        }
    }
}

/**
 * The attachment grid: photos, camera and file, then the app's own entries
 * ([extra]), four to a row.
 */
@Composable
internal fun AttachmentPanel(
    onPickMedia: () -> Unit,
    onTakePhoto: () -> Unit,
    onPickFile: () -> Unit,
    extra: List<Pair<AttachmentAction, () -> Unit>>,
    modifier: Modifier = Modifier,
) {
    val entries = buildList {
        add(Triple(stringResource(R.string.imkit_attach_photo), Icons.Outlined.PhotoLibrary, onPickMedia))
        add(Triple(stringResource(R.string.imkit_attach_camera), Icons.Outlined.PhotoCamera, onTakePhoto))
        add(Triple(stringResource(R.string.imkit_attach_file), Icons.Outlined.AttachFile, onPickFile))
        extra.forEach { (a, click) -> add(Triple(a.label, a.icon, click)) }
    }
    LazyVerticalGrid(
        columns = GridCells.Fixed(4),
        contentPadding = PaddingValues(start = 12.dp, end = 12.dp, top = 16.dp, bottom = 20.dp),
        verticalArrangement = Arrangement.spacedBy(12.dp),
        modifier = modifier.fillMaxWidth().height(if (entries.size > 4) 220.dp else 120.dp).background(ImTheme.colors.inputBackground),
    ) {
        items(entries) { (label, icon, click) -> PanelItem(label, icon, click) }
    }
}

@Composable
private fun PanelItem(label: String, icon: ImageVector, onClick: () -> Unit) {
    val colors = ImTheme.colors
    Column(
        Modifier.clip(RoundedCornerShape(8.dp)).clickable(onClick = onClick).padding(vertical = 6.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Box(Modifier.size(52.dp).clip(RoundedCornerShape(12.dp)).background(colors.surface), contentAlignment = Alignment.Center) {
            Icon(icon, contentDescription = null, tint = colors.textSecondary, modifier = Modifier.size(24.dp))
        }
        Spacer(Modifier.height(6.dp))
        Text(label, fontSize = 11.sp, color = colors.textSecondary, maxLines = 1)
    }
}
