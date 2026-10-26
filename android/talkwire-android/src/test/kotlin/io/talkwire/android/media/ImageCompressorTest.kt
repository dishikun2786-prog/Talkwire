package io.talkwire.android.media

import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.graphics.Color
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.GraphicsMode
import java.io.ByteArrayInputStream
import java.io.ByteArrayOutputStream

@RunWith(RobolectricTestRunner::class)
@GraphicsMode(GraphicsMode.Mode.NATIVE)
class ImageCompressorTest {
    @Test
    fun sampleSizeDecodesNoSmallerThanNeeded() {
        assertEquals(1, ImageCompressor.sampleSize(1000, 800, 1920))
        assertEquals(1, ImageCompressor.sampleSize(3839, 100, 1920))
        assertEquals(2, ImageCompressor.sampleSize(3840, 100, 1920))
        assertEquals(2, ImageCompressor.sampleSize(4000, 3000, 1920))
        assertEquals(4, ImageCompressor.sampleSize(8000, 6000, 1920))
    }

    @Test
    fun targetSizeKeepsTheAspectRatioAndNeverUpscales() {
        assertEquals(800 to 600, ImageCompressor.targetSize(800, 600, 1920))
        assertEquals(1920 to 1440, ImageCompressor.targetSize(4000, 3000, 1920))
        assertEquals(1080 to 1920, ImageCompressor.targetSize(2250, 4000, 1920))
        assertEquals(1 to 1920, ImageCompressor.targetSize(1, 5000, 1920))
    }

    private fun png(w: Int, h: Int, color: Int): ByteArray {
        val b = Bitmap.createBitmap(w, h, Bitmap.Config.ARGB_8888).apply { eraseColor(color) }
        return ByteArrayOutputStream().also { b.compress(Bitmap.CompressFormat.PNG, 100, it) }.toByteArray()
    }

    @Test
    fun scalesDownAndEncodesJpegOnWhite() {
        val src = png(4000, 2000, Color.TRANSPARENT)
        val out = ImageCompressor.compress({ ByteArrayInputStream(src) }, maxSide = 1000, quality = 80)
        assertEquals(1000, out.width)
        assertEquals(500, out.height)
        assertEquals("image/jpeg", out.mime)
        val decoded = BitmapFactory.decodeByteArray(out.bytes, 0, out.bytes.size)
        assertEquals(1000, decoded.width)
        val p = decoded.getPixel(500, 250)
        assertTrue("transparent became white: ${Integer.toHexString(p)}", Color.red(p) > 240 && Color.green(p) > 240 && Color.blue(p) > 240)
    }

    @Test
    fun smallImagesKeepTheirSize() {
        val src = png(300, 200, Color.RED)
        val out = ImageCompressor.compress({ ByteArrayInputStream(src) })
        assertEquals(300, out.width)
        assertEquals(200, out.height)
    }

    @Test(expected = IllegalArgumentException::class)
    fun rejectsWhatIsNotAnImage() {
        ImageCompressor.compress({ ByteArrayInputStream("not an image".toByteArray()) })
    }
}
