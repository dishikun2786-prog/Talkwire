package io.imkit.android.notify

import android.Manifest
import android.app.Application
import android.content.Context
import android.os.Vibrator
import androidx.test.core.app.ApplicationProvider
import org.junit.Assert.assertEquals
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.Shadows.shadowOf

/** Robolectric's SoundPool does not open the resource, so any id does. */
private const val ANY_SOUND = 1

@RunWith(RobolectricTestRunner::class)
class AlertPlayerTest {
    private val app = ApplicationProvider.getApplicationContext<Application>()
    private val vibrator = shadowOf(app.getSystemService(Context.VIBRATOR_SERVICE) as Vibrator)

    @Test
    fun withoutTheVibratePermissionItOnlyPlaysTheSound() {
        shadowOf(app).denyPermissions(Manifest.permission.VIBRATE)
        SoundPoolAlertPlayer(app, ANY_SOUND, vibrateMs = 200).play() // would throw if it vibrated
        assertEquals(0L, vibrator.milliseconds)
    }

    @Test
    fun withThePermissionItVibrates() {
        shadowOf(app).grantPermissions(Manifest.permission.VIBRATE)
        SoundPoolAlertPlayer(app, ANY_SOUND, vibrateMs = 200).play()
        assertEquals(200L, vibrator.milliseconds)
    }
}
