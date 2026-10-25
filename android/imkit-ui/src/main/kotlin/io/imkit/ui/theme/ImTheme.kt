package io.imkit.ui.theme

import android.content.Context
import android.content.res.Configuration
import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.Immutable
import androidx.compose.runtime.remember
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.Shape
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import java.util.Locale

/**
 * The colour tokens of the chat UI. Every component reads these instead of a
 * hard-coded colour, so an app restyles the whole kit through [ImTheme]. The
 * defaults are the source app's chat palette.
 */
@Immutable
public data class ImColors(
    val isDark: Boolean,
    /** Brand colour: own bubbles, links, buttons, the send action. */
    val accent: Color,
    val onAccent: Color,
    /** Screens and bars. */
    val background: Color,
    /** Behind the message list. */
    val chatBackground: Color,
    /** Cards, sheets, the composer's input field. */
    val surface: Color,
    val inputBackground: Color,
    val bubbleMine: Color,
    val bubbleTheirs: Color,
    val onBubbleMine: Color,
    val onBubbleTheirs: Color,
    val textPrimary: Color,
    /** Secondary text; at least 4.5:1 on [background]. */
    val textSecondary: Color,
    val textHint: Color,
    val divider: Color,
    /** Online dot: a state colour, deliberately not the brand colour. */
    val online: Color,
    /** "Read" ticks. */
    val read: Color,
    /** Unread badges, failed sends, destructive actions. */
    val error: Color,
    val mentionOnMine: Color,
    val mentionOnTheirs: Color,
    /** The recording overlay's normal and cancel colours. */
    val recording: Color,
    val recordingCancel: Color,
    /** Backgrounds of avatars without an image; picked per name. */
    val avatarPalette: List<Color> = DefaultAvatarPalette,
) {
    public companion object {
        public val DefaultAccent: Color = Color(0xFFD67744)

        public val DefaultAvatarPalette: List<Color> = listOf(
            Color(0xFFD67744), Color(0xFFB8623A), Color(0xFFE8A27A), Color(0xFF5B8DB8), Color(0xFF4F7F6A),
        )

        public fun light(accent: Color = DefaultAccent): ImColors = ImColors(
            isDark = false,
            accent = accent,
            onAccent = Color.White,
            background = Color(0xFFF7F7F7),
            chatBackground = Color(0xFFEDEDED),
            surface = Color.White,
            inputBackground = Color(0xFFF5F5F5),
            bubbleMine = accent,
            bubbleTheirs = Color.White,
            onBubbleMine = Color.White,
            onBubbleTheirs = Color(0xFF191919),
            textPrimary = Color(0xFF191919),
            textSecondary = Color(0xFF757575),
            textHint = Color(0xFF9E9E9E),
            divider = Color(0xFFD9D9D9),
            online = Color(0xFF07C160),
            read = accent,
            error = Color(0xFFFF3B30),
            mentionOnMine = Color(0xFFA8D8FF),
            mentionOnTheirs = Color(0xFF1A6CD4),
            recording = Color(0xFF07C160),
            recordingCancel = Color(0xFFFF3B30),
        )

        public fun dark(accent: Color = Color(0xFFB8623A)): ImColors = ImColors(
            isDark = true,
            accent = accent,
            onAccent = Color.White,
            background = Color(0xFF111111),
            chatBackground = Color(0xFF1A1A1C),
            surface = Color(0xFF1E1E1E),
            inputBackground = Color(0xFF3A3A3C),
            bubbleMine = accent,
            bubbleTheirs = Color(0xFF2B2B2B),
            onBubbleMine = Color.White,
            onBubbleTheirs = Color.White,
            textPrimary = Color(0xFFF2F2F2),
            textSecondary = Color(0xFFB0B0B0),
            textHint = Color(0xFF8A8A8A),
            divider = Color(0xFF2E2E2E),
            online = Color(0xFF07C160),
            read = accent,
            error = Color(0xFFFF453A),
            mentionOnMine = Color(0xFFA8D8FF),
            mentionOnTheirs = Color(0xFF6CB4FF),
            recording = Color(0xFF07C160),
            recordingCancel = Color(0xFFFF453A),
        )
    }
}

/** Sizes and shapes the components share. */
@Immutable
public data class ImDimens(
    /** Media previews: a fixed width, the height from the aspect ratio clamped into a range. */
    val mediaWidth: Dp = 220.dp,
    val mediaMinHeight: Dp = 120.dp,
    val mediaMaxHeight: Dp = 260.dp,
    val mediaFallbackHeight: Dp = 180.dp,
    val avatarInList: Dp = 48.dp,
    val avatarInChat: Dp = 36.dp,
    /** The tail sits at the top, on the side of the avatar. */
    val bubbleMine: Shape = RoundedCornerShape(16.dp, 4.dp, 16.dp, 16.dp),
    val bubbleTheirs: Shape = RoundedCornerShape(4.dp, 16.dp, 16.dp, 16.dp),
    val media: Shape = RoundedCornerShape(8.dp),
)

public val LocalImColors: androidx.compose.runtime.ProvidableCompositionLocal<ImColors> =
    staticCompositionLocalOf { ImColors.light() }
public val LocalImDimens: androidx.compose.runtime.ProvidableCompositionLocal<ImDimens> =
    staticCompositionLocalOf { ImDimens() }

/** Shortcuts for components: `ImTheme.colors.accent`. */
public object ImTheme {
    public val colors: ImColors
        @Composable get() = LocalImColors.current
    public val dimens: ImDimens
        @Composable get() = LocalImDimens.current
}

/**
 * Wraps the chat UI. [locale] overrides the language of the kit's own
 * strings (null follows the app's context); sheets and dialogs keep it
 * through [LocalizedSheet] and [LocalizedDialog], which a plain
 * `ModalBottomSheet` does not (C8).
 */
@Composable
public fun ImTheme(
    darkTheme: Boolean = isSystemInDarkTheme(),
    colors: ImColors = if (darkTheme) ImColors.dark() else ImColors.light(),
    dimens: ImDimens = ImDimens(),
    locale: Locale? = null,
    content: @Composable () -> Unit,
) {
    val base = LocalContext.current
    val context = remember(base, locale) { if (locale == null) base else base.withLocale(locale) }
    val scheme = if (colors.isDark) {
        darkColorScheme(primary = colors.accent, onPrimary = colors.onAccent, background = colors.background, surface = colors.surface, error = colors.error)
    } else {
        lightColorScheme(primary = colors.accent, onPrimary = colors.onAccent, background = colors.background, surface = colors.surface, error = colors.error)
    }
    CompositionLocalProvider(
        LocalContext provides context,
        LocalConfiguration provides context.resources.configuration,
        LocalImColors provides colors,
        LocalImDimens provides dimens,
    ) {
        MaterialTheme(colorScheme = scheme, content = content)
    }
}

/** A context whose resources speak [locale]; keeps the original as base so `findActivity()` still works. */
internal fun Context.withLocale(locale: Locale): Context {
    val config = Configuration(resources.configuration)
    config.setLocale(locale)
    return LocaleContext(this, createConfigurationContext(config))
}

/**
 * Forwards everything to [base] (activity, services) except resources, which
 * come from [localized]. `createConfigurationContext` alone would return a
 * fresh context that is no longer a ContextWrapper of the activity.
 */
internal class LocaleContext(base: Context, private val localized: Context) : android.content.ContextWrapper(base) {
    override fun getResources(): android.content.res.Resources = localized.resources
    override fun getAssets(): android.content.res.AssetManager = localized.assets
}
