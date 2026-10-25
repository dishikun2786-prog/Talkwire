package io.imkit.call

import android.Manifest
import android.annotation.SuppressLint
import android.content.Context
import android.media.AudioAttributes
import android.media.AudioManager
import android.media.MediaPlayer
import android.media.ToneGenerator
import android.os.VibrationEffect
import android.os.Vibrator
import android.provider.Settings
import android.util.Log

/** Plays the ring of an incoming call and the ringback of an outgoing one, until stopped. */
public interface CallRinger {
    public fun ring()
    public fun ringback()
    public fun stop()
}

public enum class RingTone { RING, RINGBACK }

/** The sound a call state wants: a ring while a call rings here, a ringback while the other side has not answered. */
public fun ringToneFor(state: CallState): RingTone? = when (state) {
    is CallState.Incoming -> RingTone.RING
    is CallState.Outgoing -> RingTone.RINGBACK
    else -> null
}

/** Which sound plays: another ringing call starts over; the server's ack of an outgoing call does not. */
internal fun ringKey(state: CallState): String? = when (state) {
    is CallState.Incoming -> "ring:${state.callSessionId}"
    is CallState.Outgoing -> "ringback"
    else -> null
}

/**
 * Keeps a [CallRinger] in step with the call state: starts a sound when the
 * wanted one changes (or another call rings), stops it when the call is
 * answered, refused or over. [CallSystem] uses one; a host without
 * CallSystem can feed it the manager's states itself.
 */
public class RingFollower(private val ringer: CallRinger) {
    private var playing: String? = null

    @Synchronized
    public fun onState(state: CallState) {
        val key = ringKey(state)
        if (key == playing) return
        if (playing != null) ringer.stop()
        playing = key
        when (ringToneFor(state)) {
            RingTone.RING -> ringer.ring()
            RingTone.RINGBACK -> ringer.ringback()
            null -> Unit
        }
    }

    @Synchronized
    public fun stop() {
        if (playing != null) ringer.stop()
        playing = null
    }
}

/**
 * Plays the system's sounds, so the kit ships no audio files: the phone's
 * ringtone, looped on the ringtone stream with a vibration pattern, and the
 * standard ringback tone (425 Hz, one second in five). It follows the ringer
 * mode: silent plays nothing, vibrate only vibrates. Vibrating needs VIBRATE
 * in the app's manifest; without it the ring only sounds.
 *
 * The notification channels stay silent (some vendor ROMs never play a
 * channel's sound), so this is what makes a call heard.
 */
public class SystemCallRinger(
    context: Context,
    private val vibrate: Boolean = true,
    /** The stream of the ringback; the voice-call stream, as phones play it. */
    private val ringbackStream: Int = AudioManager.STREAM_VOICE_CALL,
) : CallRinger {
    private val app = context.applicationContext
    private var player: MediaPlayer? = null
    private var tone: ToneGenerator? = null
    private var vibrating = false

    @Synchronized
    override fun ring() {
        stop()
        val mode = app.getSystemService(AudioManager::class.java)?.ringerMode ?: AudioManager.RINGER_MODE_NORMAL
        if (mode == AudioManager.RINGER_MODE_NORMAL) player = startRingtone()
        if (vibrate && mode != AudioManager.RINGER_MODE_SILENT) startVibrating()
    }

    @Synchronized
    override fun ringback() {
        stop()
        tone = try {
            ToneGenerator(ringbackStream, 80).also { it.startTone(ToneGenerator.TONE_SUP_RINGTONE) }
        } catch (e: RuntimeException) {
            // No tone generator free (the system has few): no ringback.
            Log.w(TAG, "ringback: ${e.message}")
            null
        }
    }

    @SuppressLint("MissingPermission") // only after vibrating, which checked the permission
    @Synchronized
    override fun stop() {
        player?.let {
            runCatching { it.stop() }
            it.release()
        }
        player = null
        tone?.let {
            runCatching { it.stopTone() }
            it.release()
        }
        tone = null
        if (vibrating) app.getSystemService(Vibrator::class.java)?.cancel()
        vibrating = false
    }

    private fun startRingtone(): MediaPlayer? {
        val p = MediaPlayer()
        return try {
            p.setAudioAttributes(
                AudioAttributes.Builder()
                    .setUsage(AudioAttributes.USAGE_NOTIFICATION_RINGTONE)
                    .setContentType(AudioAttributes.CONTENT_TYPE_SONIFICATION)
                    .build(),
            )
            p.setDataSource(app, Settings.System.DEFAULT_RINGTONE_URI)
            p.isLooping = true
            p.setOnPreparedListener { it.start() }
            p.prepareAsync()
            p
        } catch (e: Exception) {
            // No ringtone set, or one the app may not read: vibration only.
            Log.w(TAG, "ringtone: ${e.message}")
            p.release()
            null
        }
    }

    // VIBRATE is the app's to declare; the check skips vibrating when it did not.
    @SuppressLint("MissingPermission")
    private fun startVibrating() {
        if (app.checkSelfPermission(Manifest.permission.VIBRATE) != android.content.pm.PackageManager.PERMISSION_GRANTED) return
        val vibrator = app.getSystemService(Vibrator::class.java)?.takeIf { it.hasVibrator() } ?: return
        vibrator.vibrate(VibrationEffect.createWaveform(longArrayOf(0, 800, 1_200), 0))
        vibrating = true
    }

    private companion object {
        const val TAG = "SystemCallRinger"
    }
}
