package io.imkit.videocompress

import android.content.Context
import android.net.Uri
import android.os.Looper
import androidx.test.core.app.ApplicationProvider
import io.imkit.android.media.VideoCompressor
import kotlinx.coroutines.DelicateCoroutinesApi
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.GlobalScope
import kotlinx.coroutines.async
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import org.junit.Assert.assertFalse
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.Shadows.shadowOf
import java.io.File
import java.time.Duration

/**
 * Only the failure path: re-encoding needs a device's codecs. What it
 * checks is the part that would otherwise go unseen, that a video the
 * Transformer cannot read ends as false with no file left behind, rather
 * than hanging or throwing.
 */
@OptIn(DelicateCoroutinesApi::class)
@RunWith(RobolectricTestRunner::class)
class Media3VideoCompressorTest {
    private val context = ApplicationProvider.getApplicationContext<Context>()

    @Test
    fun aFileThatIsNotAVideoIsFalseAndLeavesNothing() {
        val notAVideo = File(context.cacheDir, "notes.mp4").apply { writeText("not a video") }
        val out = File(context.cacheDir, "out.mp4")
        val compressor: VideoCompressor = Media3VideoCompressor(context, videoBitrate = 2_000_000)
        // Off the test thread, which is Robolectric's main thread: parts of Media3 post to the
        // main looper, and the player's threads wait on SystemClock, which under Robolectric
        // moves only when the test moves it. So this thread runs the looper and the clock.
        val result = GlobalScope.async(Dispatchers.Default) { withTimeout(20_000) { compressor.compress(Uri.fromFile(notAVideo), out) } }
        while (!result.isCompleted) {
            shadowOf(Looper.getMainLooper()).idleFor(Duration.ofMillis(10))
            Thread.sleep(5)
        }
        assertFalse(runBlocking { result.await() })
        assertFalse(out.exists())
    }
}
