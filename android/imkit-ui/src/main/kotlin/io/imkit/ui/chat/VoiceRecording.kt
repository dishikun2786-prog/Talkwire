package io.imkit.ui.chat

import android.Manifest
import android.content.Context
import android.content.pm.PackageManager
import android.media.MediaRecorder
import android.os.Build
import android.util.Log
import androidx.compose.animation.animateColorAsState
import androidx.compose.animation.core.Animatable
import androidx.compose.animation.core.FastOutSlowInEasing
import androidx.compose.animation.core.tween
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.Mic
import androidx.compose.material3.Icon
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.hapticfeedback.HapticFeedbackType
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.LocalHapticFeedback
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.IntOffset
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import io.imkit.ui.R
import io.imkit.ui.theme.ImTheme
import kotlinx.coroutines.delay
import java.io.File
import kotlin.math.ln
import kotlin.math.roundToInt

/**
 * Records a voice clip (AAC in MP4, mono). Every MediaRecorder failure — the
 * microphone held by another app, a rejected source, an encoder that will
 * not start — ends as a `null`, never as an exception: these happen inside a
 * gesture handler, where an exception would take the screen down.
 */
public class VoiceRecorder(private val context: Context) {
    private var recorder: MediaRecorder? = null
    private var file: File? = null

    public val isRecording: Boolean @Synchronized get() = recorder != null

    @Synchronized
    public fun start(): File? {
        if (context.checkSelfPermission(Manifest.permission.RECORD_AUDIO) != PackageManager.PERMISSION_GRANTED) return null
        if (recorder != null) return null
        val out = File(context.cacheDir, "imkit-voice-${System.nanoTime()}.m4a")
        val r = try {
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) MediaRecorder(context) else @Suppress("DEPRECATION") MediaRecorder()
        } catch (e: Exception) {
            Log.w(TAG, "cannot create recorder", e)
            return null
        }
        return try {
            r.setAudioSource(MediaRecorder.AudioSource.MIC)
            r.setOutputFormat(MediaRecorder.OutputFormat.MPEG_4)
            r.setAudioEncoder(MediaRecorder.AudioEncoder.AAC)
            runCatching { r.setAudioSamplingRate(44_100) }
            runCatching { r.setAudioEncodingBitRate(64_000) }
            runCatching { r.setAudioChannels(1) }
            r.setOutputFile(out)
            r.prepare()
            r.start()
            recorder = r
            file = out
            out
        } catch (e: Exception) {
            Log.w(TAG, "cannot start recorder", e)
            runCatching { r.reset() }
            runCatching { r.release() }
            out.delete()
            null
        }
    }

    public fun amplitude(): Int = try {
        synchronized(this) { recorder?.maxAmplitude } ?: 0
    } catch (e: Exception) {
        0
    }

    /** The clip, or null when nothing usable was recorded. */
    @Synchronized
    public fun stop(): File? {
        val r = recorder ?: return null
        val out = file
        recorder = null
        file = null
        return try {
            r.stop()
            runCatching { r.release() }
            out?.takeIf { it.exists() && it.length() > 0 }
        } catch (e: Exception) {
            // stop() throws when almost nothing was recorded; that clip is unusable.
            runCatching { r.reset() }
            runCatching { r.release() }
            out?.delete()
            null
        }
    }

    @Synchronized
    public fun cancel() {
        val r = recorder
        val out = file
        recorder = null
        file = null
        runCatching { r?.stop() }
        runCatching { r?.release() }
        out?.delete()
    }

    private companion object {
        const val TAG = "ImKitVoice"
    }
}

/** Seconds a clip may last; the overlay counts up to it and the recording stops there. */
public const val MAX_VOICE_SECONDS: Int = 60

/** How far up (in dp) the finger must slide to cancel. */
public const val VOICE_CANCEL_DP: Float = 100f

/** How far the record button follows the finger up, so it never reaches the timer. */
private const val MAX_BUTTON_LIFT_DP: Int = 24

private const val BARS = 14

/**
 * The full-screen recording overlay. Purely visual: the press, slide and
 * release are one gesture on the composer's button ([holdToTalk]), and the
 * screen drives this from that gesture's state.
 */
@Composable
public fun VoiceRecordOverlay(
    elapsedSeconds: Int,
    dragY: Float,
    cancelling: Boolean,
    amplitude: () -> Int,
    modifier: Modifier = Modifier,
) {
    val colors = ImTheme.colors
    val haptic = LocalHapticFeedback.current
    LaunchedEffect(cancelling) { if (cancelling) haptic.performHapticFeedback(HapticFeedbackType.LongPress) }
    val bars = remember { List(BARS) { Animatable(0.15f) } }
    LaunchedEffect(Unit) {
        var i = 0
        while (true) {
            val amp = amplitude()
            val level = if (amp <= 0) 0.1f else (ln(amp + 1f) / ln(32_768f)).coerceIn(0.1f, 1f)
            bars.forEachIndexed { n, bar ->
                // A fixed ripple instead of random jitter: deterministic, still alive.
                val wobble = 0.85f + 0.3f * (((n * 7 + i) % 5) / 4f)
                bar.animateTo((level * wobble).coerceIn(0.1f, 1f), tween(140, easing = FastOutSlowInEasing))
            }
            i++
            delay(100)
        }
    }
    val accent by animateColorAsState(if (cancelling) colors.recordingCancel else colors.recording, tween(180), label = "voiceAccent")
    Box(modifier.fillMaxSize().background(Color.Black.copy(alpha = 0.88f))) {
        if (dragY < 0) {
            Column(Modifier.fillMaxWidth().padding(top = 48.dp), horizontalAlignment = Alignment.CenterHorizontally) {
                Surface(Modifier.size(56.dp), CircleShape, color = colors.recordingCancel.copy(alpha = 0.2f)) {
                    Box(contentAlignment = Alignment.Center) { Icon(Icons.Filled.Close, null, tint = colors.recordingCancel) }
                }
                Spacer(Modifier.height(8.dp))
                Text(stringResource(R.string.imkit_release_to_cancel), fontSize = 14.sp, color = colors.recordingCancel, fontWeight = FontWeight.SemiBold)
            }
        }
        Column(Modifier.fillMaxSize().padding(horizontal = 32.dp), horizontalAlignment = Alignment.CenterHorizontally, verticalArrangement = Arrangement.Center) {
            Text(
                // Cancelling is said once, by the label at the top. The timer below shows
                // the time, so the limit is worth a word only near the end.
                if (elapsedSeconds >= MAX_VOICE_SECONDS - 10) {
                    stringResource(R.string.imkit_recording_elapsed, elapsedSeconds, MAX_VOICE_SECONDS)
                } else {
                    stringResource(R.string.imkit_recording)
                },
                fontSize = 16.sp,
                color = Color.White.copy(alpha = 0.75f),
            )
            Spacer(Modifier.height(36.dp))
            Row(Modifier.height(72.dp), horizontalArrangement = Arrangement.spacedBy(3.dp), verticalAlignment = Alignment.CenterVertically) {
                bars.forEach { bar -> Box(Modifier.width(4.dp).fillMaxHeight(bar.value).clip(RoundedCornerShape(2.dp)).background(accent)) }
            }
            Spacer(Modifier.height(28.dp))
            Surface(color = Color.White.copy(alpha = 0.08f), shape = RoundedCornerShape(14.dp)) {
                Text(
                    "%02d:%02d".format(elapsedSeconds / 60, elapsedSeconds % 60),
                    Modifier.padding(horizontal = 28.dp, vertical = 10.dp),
                    fontSize = 36.sp,
                    fontWeight = FontWeight.Bold,
                    color = Color.White,
                    style = TextStyle(fontFeatureSettings = "tnum"),
                )
            }
            Spacer(Modifier.height(44.dp))
            // The button only nudges after the finger (on a phone it followed all the way into
            // the timer); the colour, the icon and the label at the top say what releasing does.
            val maxLift = with(LocalDensity.current) { MAX_BUTTON_LIFT_DP.dp.roundToPx() }
            Surface(
                Modifier.size(80.dp).offset { IntOffset(0, dragY.roundToInt().coerceIn(-maxLift, 0)) },
                CircleShape,
                color = accent,
            ) {
                Box(contentAlignment = Alignment.Center) {
                    Icon(if (cancelling) Icons.Filled.Close else Icons.Filled.Mic, stringResource(R.string.imkit_recording), tint = Color.White, modifier = Modifier.size(36.dp))
                }
            }
            Spacer(Modifier.height(8.dp))
            Text(
                if (dragY < 0) "" else stringResource(R.string.imkit_swipe_up_cancel),
                fontSize = 13.sp,
                color = Color.White.copy(alpha = 0.45f),
            )
        }
    }
}
