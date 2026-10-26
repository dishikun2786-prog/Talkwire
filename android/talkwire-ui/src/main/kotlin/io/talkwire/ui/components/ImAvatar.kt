package io.talkwire.ui.components

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Shape
import androidx.compose.ui.graphics.luminance
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import coil.compose.AsyncImage
import io.talkwire.ui.R
import io.talkwire.ui.theme.ImTheme

/**
 * A round avatar: the image when there is one, else the name's first
 * character on a colour picked from the name, so the same person keeps the
 * same colour and different people can be told apart.
 */
@Composable
public fun ImAvatar(
    url: String?,
    name: String,
    modifier: Modifier = Modifier,
    size: Dp = 40.dp,
    shape: Shape = CircleShape,
) {
    val colors = ImTheme.colors
    val stroke = if (colors.isDark) Color.White.copy(alpha = 0.10f) else Color.Black.copy(alpha = 0.10f)
    if (!url.isNullOrBlank()) {
        AsyncImage(
            model = url,
            contentDescription = name.ifBlank { stringResource(R.string.talkwire_avatar) },
            contentScale = ContentScale.Crop,
            modifier = modifier.size(size).clip(shape).border(1.dp, stroke, shape),
        )
        return
    }
    val bg = avatarColor(name, colors.avatarPalette)
    Box(
        modifier.size(size).clip(shape).background(bg).border(1.dp, stroke, shape),
        contentAlignment = Alignment.Center,
    ) {
        Text(
            avatarInitial(name),
            color = if (bg.luminance() > 0.5f) Color(0xFF1A1A1A) else Color.White,
            fontSize = (size.value * 0.4f).sp,
            fontWeight = FontWeight.Medium,
            textAlign = TextAlign.Center,
        )
    }
}

/** Stable per name; an empty name takes the first colour. */
internal fun avatarColor(name: String, palette: List<Color>): Color {
    if (name.isEmpty() || palette.isEmpty()) return palette.firstOrNull() ?: Color.Gray
    return palette[Math.floorMod(name.hashCode(), palette.size)]
}

/** The first character (a whole emoji or surrogate pair, not half of one), upper-cased. */
internal fun avatarInitial(name: String): String {
    val t = name.trim()
    if (t.isEmpty()) return "?"
    val end = t.offsetByCodePoints(0, 1)
    return t.substring(0, end).uppercase()
}
