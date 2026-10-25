package io.imkit.android.notify

import android.Manifest
import android.annotation.SuppressLint
import android.content.Context
import android.content.pm.PackageManager
import android.media.AudioAttributes
import android.media.SoundPool
import android.os.VibrationEffect
import android.os.Vibrator
import androidx.annotation.RawRes

/** Plays the new-message alert. The channels are silent, so this is the only sound (C6). */
public fun interface AlertPlayer {
    public fun play()
}

/**
 * Plays a short sound from res/raw with [SoundPool] (on the notification
 * stream, so the ringer volume and silent mode apply), and optionally
 * vibrates. Vibrating needs the VIBRATE permission in the app's manifest;
 * without it the alert only plays the sound (the call would throw). A [play]
 * before the sound finished loading plays it as soon as it has.
 */
public class SoundPoolAlertPlayer(
    context: Context,
    @RawRes sound: Int,
    private val vibrateMs: Long = 0,
) : AlertPlayer {
    private val app = context.applicationContext
    private val pool = SoundPool.Builder()
        .setMaxStreams(1)
        .setAudioAttributes(
            AudioAttributes.Builder()
                .setUsage(AudioAttributes.USAGE_NOTIFICATION)
                .setContentType(AudioAttributes.CONTENT_TYPE_SONIFICATION)
                .build(),
        )
        .build()
    @Volatile private var soundId = 0
    @Volatile private var loaded = false
    @Volatile private var playWhenLoaded = false

    init {
        pool.setOnLoadCompleteListener { _, id, status ->
            if (id == soundId && status == 0) {
                loaded = true
                if (playWhenLoaded) {
                    playWhenLoaded = false
                    pool.play(soundId, 1f, 1f, 1, 0, 1f)
                }
            }
        }
        soundId = pool.load(app, sound, 1)
    }

    // VIBRATE is the app's to declare (the library's manifest stays empty); the
    // check below skips vibrating when it did not, which Lint cannot see for
    // a normal (install-time) permission.
    @SuppressLint("MissingPermission")
    override fun play() {
        if (loaded) pool.play(soundId, 1f, 1f, 1, 0, 1f) else playWhenLoaded = true
        if (vibrateMs > 0 && app.checkSelfPermission(Manifest.permission.VIBRATE) == PackageManager.PERMISSION_GRANTED) {
            app.getSystemService(Vibrator::class.java)?.takeIf { it.hasVibrator() }
                ?.vibrate(VibrationEffect.createOneShot(vibrateMs, VibrationEffect.DEFAULT_AMPLITUDE))
        }
    }

    public fun release() {
        pool.release()
    }
}
