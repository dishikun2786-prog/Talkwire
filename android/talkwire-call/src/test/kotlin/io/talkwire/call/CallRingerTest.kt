package io.talkwire.call

import android.Manifest
import android.app.Application
import android.media.AudioManager
import android.os.Vibrator
import androidx.test.core.app.ApplicationProvider
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.Shadows.shadowOf

/** When a call rings, and the system ringer under Robolectric. */
@RunWith(RobolectricTestRunner::class)
class CallRingerTest {
    private class LogRinger : CallRinger {
        val log = mutableListOf<String>()
        override fun ring() {
            log += "ring"
        }
        override fun ringback() {
            log += "ringback"
        }
        override fun stop() {
            log += "stop"
        }
    }

    private fun incoming(id: String, name: String? = null) = CallState.Incoming(id, CallType.VOICE, name, null, 45, 1)
    private fun active(id: String, connecting: Boolean) = CallState.Active(id, CallType.VOICE, null, null, connecting, false, false, false, null, 2)

    @Test
    fun ringsWhileACallRingsRingbacksWhileCallingAndIsQuietOtherwise() {
        val ringer = LogRinger()
        val rings = RingFollower(ringer)
        rings.onState(CallState.Idle)
        assertEquals(emptyList<String>(), ringer.log)

        rings.onState(incoming("a"))
        rings.onState(incoming("a", "Bob"))
        assertEquals("the same call does not start over", listOf("ring"), ringer.log)
        rings.onState(incoming("b"))
        assertEquals("another call rings anew", listOf("ring", "stop", "ring"), ringer.log)
        rings.onState(active("b", connecting = true))
        rings.onState(active("b", connecting = false))
        assertEquals(listOf("ring", "stop", "ring", "stop"), ringer.log)

        rings.onState(CallState.Outgoing("uid_bob", CallType.VIDEO, null, 3))
        rings.onState(CallState.Outgoing("uid_bob", CallType.VIDEO, "c", 3))
        assertEquals("the server's ack does not restart it", listOf("ringback"), ringer.log.drop(4))
        rings.onState(CallState.Ended("c", CallEndReason.NO_ANSWER, 4))
        assertEquals(listOf("ringback", "stop"), ringer.log.drop(4))

        rings.onState(incoming("d"))
        rings.stop()
        rings.stop()
        assertEquals("stopping stops the sound once", listOf("ring", "stop"), ringer.log.drop(6))
        assertNull(ringToneFor(CallState.Idle))
        assertEquals(RingTone.RING, ringToneFor(incoming("x")))
    }

    @Test
    fun theSystemRingerVibratesByTheRingerModeAndStops() {
        val app = ApplicationProvider.getApplicationContext<Application>()
        val vibrator = shadowOf(app.getSystemService(Vibrator::class.java))
        val audio = app.getSystemService(AudioManager::class.java)
        val ringer = SystemCallRinger(app)

        // Without VIBRATE in the manifest: no vibration, no crash.
        ringer.ring()
        assertFalse(vibrator.isVibrating)
        ringer.stop()

        shadowOf(app).grantPermissions(Manifest.permission.VIBRATE)
        ringer.ring()
        assertTrue(vibrator.isVibrating)
        ringer.stop()
        assertFalse(vibrator.isVibrating)

        audio.ringerMode = AudioManager.RINGER_MODE_SILENT
        ringer.ring()
        assertFalse("silent means silent", vibrator.isVibrating)
        ringer.stop()

        audio.ringerMode = AudioManager.RINGER_MODE_VIBRATE
        ringer.ring()
        assertTrue(vibrator.isVibrating)
        ringer.ringback()
        assertFalse("the ringback stops the ring first", vibrator.isVibrating)
        ringer.stop()
        ringer.stop()
    }
}
