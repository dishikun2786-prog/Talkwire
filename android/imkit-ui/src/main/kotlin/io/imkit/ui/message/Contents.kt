@file:androidx.annotation.OptIn(androidx.media3.common.util.UnstableApi::class)

package io.imkit.ui.message

import android.content.Context
import android.media.AudioAttributes
import android.media.AudioFocusRequest
import android.media.AudioManager
import android.net.Uri
import android.os.Build
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.animation.core.tween
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.material.icons.filled.Stop
import androidx.compose.material.icons.outlined.Article
import androidx.compose.material.icons.outlined.Description
import androidx.compose.material.icons.outlined.FolderZip
import androidx.compose.material.icons.outlined.InsertDriveFile
import androidx.compose.material.icons.outlined.TableChart
import androidx.compose.material.icons.outlined.TextSnippet
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextDecoration
import androidx.compose.ui.text.withStyle
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.compose.ui.window.DialogProperties
import androidx.media3.common.MediaItem
import androidx.media3.common.PlaybackException
import androidx.media3.common.PlaybackParameters
import androidx.media3.common.Player
import androidx.media3.datasource.DefaultDataSource
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.exoplayer.source.DefaultMediaSourceFactory
import androidx.media3.ui.AspectRatioFrameLayout
import androidx.media3.ui.PlayerView
import coil.compose.AsyncImage
import io.imkit.android.media.MediaCache
import io.imkit.core.model.ChatMessage
import io.imkit.ui.R
import io.imkit.ui.theme.ImTheme
import io.imkit.ui.theme.LocalizedDialog
import kotlinx.coroutines.delay

private val MENTION = Regex("@(\\S+)")
private val URL = Regex("""https?://[^\s]+""")

/** Text with @mentions and links highlighted. */
@Composable
internal fun TextContent(text: String, isMine: Boolean, unsupported: Boolean = false) {
    val colors = ImTheme.colors
    val color = if (isMine) colors.onBubbleMine else colors.onBubbleTheirs
    if (unsupported) {
        Text(stringResource(R.string.imkit_unsupported_message), color = color.copy(alpha = 0.7f), fontSize = 15.sp, fontStyle = FontStyle.Italic)
        return
    }
    val mention = if (isMine) colors.mentionOnMine else colors.mentionOnTheirs
    val annotated = remember(text, mention) {
        buildAnnotatedString {
            var last = 0
            val spans = (MENTION.findAll(text).map { it.range to false } + URL.findAll(text).map { it.range to true })
                .sortedBy { it.first.first }
            for ((range, isUrl) in spans) {
                if (range.first < last) continue
                append(text.substring(last, range.first))
                val style = if (isUrl) SpanStyle(color = mention, textDecoration = TextDecoration.Underline) else SpanStyle(color = mention, fontWeight = FontWeight.Medium)
                withStyle(style) { append(text.substring(range.first, range.last + 1)) }
                last = range.last + 1
            }
            append(text.substring(last))
        }
    }
    Text(annotated, color = color, fontSize = 15.sp, lineHeight = 22.sp)
}

@Composable
internal fun SystemContent(text: String) {
    Box(Modifier.fillMaxWidth().padding(vertical = 4.dp), contentAlignment = Alignment.Center) {
        Text(
            text,
            fontSize = 12.sp,
            color = ImTheme.colors.textSecondary,
            textAlign = TextAlign.Center,
            modifier = Modifier.clip(RoundedCornerShape(6.dp)).background(ImTheme.colors.textSecondary.copy(alpha = 0.08f)).padding(horizontal = 10.dp, vertical = 4.dp),
        )
    }
}

/**
 * Height of a media preview that keeps `width:height`, clamped into the
 * shared range; the fallback height when the sender gave no size. Without
 * a bounded height a camera photo's raw pixel height becomes the bubble's
 * height (the blank-space bug of the source app).
 */
@Composable
internal fun mediaHeight(width: Int, height: Int): Dp {
    val d = ImTheme.dimens
    return if (width > 0 && height > 0) (d.mediaWidth * height / width).coerceIn(d.mediaMinHeight, d.mediaMaxHeight) else d.mediaFallbackHeight
}

@OptIn(ExperimentalFoundationApi::class)
@Composable
internal fun ImageContent(message: ChatMessage, scope: MessageScope) {
    val url = scope.resolveUrl(message.fileUrl)
    AsyncImage(
        model = url,
        contentDescription = stringResource(R.string.imkit_image),
        contentScale = ContentScale.Crop,
        modifier = Modifier
            .width(ImTheme.dimens.mediaWidth)
            .height(mediaHeight(message.extraInt("imageWidth"), message.extraInt("imageHeight")))
            .clip(ImTheme.dimens.media)
            .background(ImTheme.colors.divider)
            .combinedClickable(onLongClick = scope.onLongPress) { url?.let(scope.openImage) },
    )
}

@OptIn(ExperimentalFoundationApi::class)
@Composable
internal fun VideoContent(message: ChatMessage, scope: MessageScope) {
    var fullscreen by remember { mutableStateOf(false) }
    val thumb = scope.resolveUrl(message.extraString("thumbUrl"))
    val seconds = message.durationSeconds
    val play = stringResource(R.string.imkit_play_video)
    Box(
        Modifier
            .width(ImTheme.dimens.mediaWidth)
            .height(mediaHeight(message.extraInt("thumbWidth"), message.extraInt("thumbHeight")))
            .clip(ImTheme.dimens.media)
            .background(Color(0xFF333333))
            .semantics { contentDescription = play }
            .combinedClickable(onLongClick = scope.onLongPress) { fullscreen = true },
    ) {
        if (thumb != null) {
            AsyncImage(model = thumb, contentDescription = null, contentScale = ContentScale.Crop, modifier = Modifier.fillMaxSize())
            Box(Modifier.fillMaxSize().background(Brush.verticalGradient(0.55f to Color.Transparent, 1f to Color.Black.copy(alpha = 0.45f))))
        }
        Box(
            Modifier.align(Alignment.Center).size(52.dp).clip(CircleShape).background(Color.Black.copy(alpha = 0.35f)),
            contentAlignment = Alignment.Center,
        ) {
            Icon(Icons.Filled.PlayArrow, null, tint = Color.White, modifier = Modifier.size(28.dp))
        }
        if (seconds > 0) {
            Text(
                "%d:%02d".format(seconds / 60, seconds % 60),
                color = Color.White,
                fontSize = 11.sp,
                modifier = Modifier.align(Alignment.BottomEnd).padding(6.dp)
                    .background(Color.Black.copy(alpha = 0.5f), RoundedCornerShape(4.dp)).padding(horizontal = 5.dp, vertical = 2.dp),
            )
        }
    }
    if (fullscreen) {
        scope.resolveUrl(message.fileUrl)?.let { FullscreenVideoPlayer(it, onDismiss = { fullscreen = false }) }
    }
}

/** A player that reads through [MediaCache], so the second play does not download again (C7). */
internal fun cachedPlayer(context: Context): ExoPlayer =
    ExoPlayer.Builder(context)
        .setMediaSourceFactory(DefaultMediaSourceFactory(MediaCache.get(context).dataSourceFactory(DefaultDataSource.Factory(context))))
        .build()

@Composable
internal fun FullscreenVideoPlayer(url: String, onDismiss: () -> Unit) {
    val context = LocalContext.current
    var failure by remember { mutableStateOf<String?>(null) }
    var state by remember { mutableIntStateOf(Player.STATE_IDLE) }
    val player = remember {
        cachedPlayer(context).apply {
            setMediaItem(MediaItem.fromUri(Uri.parse(url)))
            prepare()
            playWhenReady = true
        }
    }
    DisposableEffect(player) {
        val listener = object : Player.Listener {
            override fun onPlayerError(error: PlaybackException) {
                failure = error.errorCodeName
            }

            override fun onPlaybackStateChanged(playbackState: Int) {
                state = playbackState
            }
        }
        player.addListener(listener)
        state = player.playbackState
        onDispose {
            player.removeListener(listener)
            player.release()
        }
    }
    LocalizedDialog(onDismissRequest = onDismiss, properties = DialogProperties(usePlatformDefaultWidth = false)) {
        Box(Modifier.fillMaxSize().background(Color.Black)) {
            AndroidView(
                factory = { ctx ->
                    PlayerView(ctx).apply {
                        this.player = player
                        resizeMode = AspectRatioFrameLayout.RESIZE_MODE_FIT
                        useController = true
                    }
                },
                modifier = Modifier.fillMaxSize(),
            )
            IconButton(onClick = onDismiss, modifier = Modifier.align(Alignment.TopStart).padding(12.dp)) {
                Icon(Icons.Filled.Close, stringResource(R.string.imkit_close), tint = Color.White)
            }
            // A black screen alone does not tell a slow network from a file that will never play.
            val hint = when {
                failure != null -> stringResource(R.string.imkit_video_failed, failure ?: "")
                state == Player.STATE_BUFFERING || state == Player.STATE_IDLE -> stringResource(R.string.imkit_loading)
                else -> null
            }
            hint?.let { Text(it, color = Color.White, modifier = Modifier.align(Alignment.Center).padding(24.dp)) }
        }
    }
}

private val WAVE = listOf(6, 12, 18, 8, 14, 16, 10, 18, 6, 12, 8, 14, 11, 16, 7, 13, 9, 17)

/**
 * Bars in a voice bubble: about 0.42 a second, 4 to 26. The bubble wraps its
 * bars, so a short clip stays compact and a long one is wide (C7).
 */
public fun voiceBarCount(seconds: Int): Int = (4 + seconds.coerceIn(1, 60) * 0.42f).toInt().coerceIn(4, 26)

@OptIn(ExperimentalFoundationApi::class)
@Composable
internal fun VoiceContent(message: ChatMessage, scope: MessageScope) {
    val context = LocalContext.current
    val colors = ImTheme.colors
    val seconds = message.durationSeconds
    var player by remember { mutableStateOf<ExoPlayer?>(null) }
    var playing by remember { mutableStateOf(false) }
    var loading by remember { mutableStateOf(false) }
    var speed by remember { mutableFloatStateOf(1f) }
    var posMs by remember { mutableIntStateOf(0) }
    var totalMs by remember { mutableIntStateOf(0) }
    val bars = remember(seconds) { List(voiceBarCount(seconds)) { WAVE[it % WAVE.size] } }
    val bg = if (scope.isMine) colors.bubbleMine else colors.bubbleTheirs
    val fg = if (scope.isMine) colors.onBubbleMine.copy(alpha = 0.75f) else colors.accent
    val textColor = if (scope.isMine) colors.onBubbleMine.copy(alpha = 0.75f) else colors.textSecondary
    val shape = if (scope.isMine) ImTheme.dimens.bubbleMine else ImTheme.dimens.bubbleTheirs

    LaunchedEffect(playing) {
        while (playing) {
            player?.let { p ->
                posMs = p.currentPosition.toInt()
                if (p.duration > 0) totalMs = p.duration.toInt()
            }
            delay(150)
        }
    }
    val progress by animateFloatAsState(if (totalMs > 0) posMs.toFloat() / totalMs else 0f, tween(150), label = "voice")

    fun stop() {
        runCatching { player?.release() }
        player = null
        playing = false
        loading = false
        posMs = 0
        abandonFocus(context)
    }
    val focusListener = remember { AudioManager.OnAudioFocusChangeListener { if (it == AudioManager.AUDIOFOCUS_LOSS || it == AudioManager.AUDIOFOCUS_LOSS_TRANSIENT) stop() } }
    DisposableEffect(Unit) { onDispose { stop() } }

    fun start() {
        val url = scope.resolveUrl(message.fileUrl) ?: return
        if (!requestFocus(context, focusListener)) return
        try {
            val exo = cachedPlayer(context)
            exo.setAudioAttributes(
                androidx.media3.common.AudioAttributes.Builder().setContentType(androidx.media3.common.C.AUDIO_CONTENT_TYPE_SPEECH).build(),
                false,
            )
            exo.addListener(object : Player.Listener {
                override fun onPlaybackStateChanged(state: Int) {
                    when (state) {
                        // Only once Ready are position and duration meaningful.
                        Player.STATE_READY -> if (loading) {
                            exo.playbackParameters = PlaybackParameters(speed)
                            loading = false
                            playing = true
                        }
                        Player.STATE_ENDED -> stop()
                    }
                }

                override fun onPlayerError(error: PlaybackException) = stop()
            })
            loading = true
            exo.setMediaItem(MediaItem.fromUri(Uri.parse(url)))
            exo.prepare()
            exo.playWhenReady = true
            player = exo
        } catch (e: Exception) {
            stop()
        }
    }

    val label = stringResource(R.string.imkit_voice_seconds, seconds)
    Column(Modifier.widthIn(min = 84.dp, max = 240.dp)) {
        Row(
            Modifier.clip(shape).background(bg)
                .semantics { contentDescription = label }
                .combinedClickable(onLongClick = scope.onLongPress) { if (playing || loading) stop() else start() }
                .padding(12.dp, 8.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Icon(if (playing || loading) Icons.Filled.Stop else Icons.Filled.PlayArrow, null, tint = fg, modifier = Modifier.size(22.dp))
            Spacer(Modifier.width(6.dp))
            // Wraps its content: the bar count is what makes long clips wider.
            Column {
                Row(Modifier.height(18.dp), verticalAlignment = Alignment.CenterVertically) {
                    bars.forEach { h ->
                        Spacer(Modifier.width(2.dp))
                        Box(Modifier.width(2.dp).height(h.dp).clip(RoundedCornerShape(1.dp)).background(fg))
                    }
                }
                if (playing && totalMs > 0) {
                    Spacer(Modifier.height(4.dp))
                    // The bars' own width, not fillMaxWidth: that would stretch the bubble while playing.
                    Box(Modifier.width((bars.size * 4).dp).height(2.dp).clip(RoundedCornerShape(1.dp)).background(fg.copy(alpha = 0.2f))) {
                        Box(Modifier.fillMaxWidth(progress).height(2.dp).background(fg))
                    }
                }
            }
            Spacer(Modifier.width(6.dp))
            Text(
                if (playing && totalMs > 0) "${fmt(posMs)}/${fmt(totalMs)}" else "${seconds}\"",
                fontSize = 11.sp,
                color = textColor,
            )
        }
        if (playing) {
            val speedLabel = when (speed) { 1.5f -> "1.5x"; 2f -> "2x"; else -> "1x" }
            Text(
                stringResource(R.string.imkit_playback_speed, speedLabel),
                fontSize = 11.sp,
                color = colors.textSecondary,
                modifier = Modifier.padding(start = 4.dp, top = 4.dp).clickable {
                    speed = when (speed) { 1f -> 1.5f; 1.5f -> 2f; else -> 1f }
                    player?.playbackParameters = PlaybackParameters(speed)
                },
            )
        }
    }
}

private fun fmt(ms: Int) = "%d:%02d".format(ms / 60000, (ms / 1000) % 60)

private fun requestFocus(context: Context, listener: AudioManager.OnAudioFocusChangeListener): Boolean {
    val am = context.getSystemService(Context.AUDIO_SERVICE) as? AudioManager ?: return true
    return runCatching {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            val attr = AudioAttributes.Builder().setContentType(AudioAttributes.CONTENT_TYPE_SPEECH).build()
            am.requestAudioFocus(
                AudioFocusRequest.Builder(AudioManager.AUDIOFOCUS_GAIN_TRANSIENT).setAudioAttributes(attr).setOnAudioFocusChangeListener(listener).build(),
            ) == AudioManager.AUDIOFOCUS_REQUEST_GRANTED
        } else {
            @Suppress("DEPRECATION")
            am.requestAudioFocus(listener, AudioManager.STREAM_MUSIC, AudioManager.AUDIOFOCUS_GAIN_TRANSIENT) == AudioManager.AUDIOFOCUS_REQUEST_GRANTED
        }
    }.getOrDefault(true)
}

private fun abandonFocus(context: Context) {
    val am = context.getSystemService(Context.AUDIO_SERVICE) as? AudioManager ?: return
    @Suppress("DEPRECATION")
    runCatching { am.abandonAudioFocus(null) }
}

private fun fileIcon(contentType: String): ImageVector {
    val t = contentType.lowercase()
    return when {
        "pdf" in t -> Icons.Outlined.Description
        listOf("zip", "rar", "tar", "7z").any { it in t } -> Icons.Outlined.FolderZip
        listOf("word", "document", "msword").any { it in t } -> Icons.Outlined.Article
        listOf("excel", "sheet", "spreadsheet").any { it in t } -> Icons.Outlined.TableChart
        "text" in t -> Icons.Outlined.TextSnippet
        else -> Icons.Outlined.InsertDriveFile
    }
}

@Composable
internal fun FileContent(message: ChatMessage, scope: MessageScope) {
    val color = if (scope.isMine) ImTheme.colors.onBubbleMine else ImTheme.colors.onBubbleTheirs
    Row(Modifier.widthIn(max = 220.dp), verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.Start) {
        Icon(fileIcon(message.contentType), null, tint = color, modifier = Modifier.size(28.dp))
        Spacer(Modifier.width(8.dp))
        Column(Modifier.weight(1f, fill = false)) {
            Text(message.fileName ?: stringResource(R.string.imkit_file), fontSize = 14.sp, color = color, fontWeight = FontWeight.Medium, maxLines = 1)
            Text(message.contentType, fontSize = 11.sp, color = color.copy(alpha = 0.6f), maxLines = 1)
        }
    }
}
