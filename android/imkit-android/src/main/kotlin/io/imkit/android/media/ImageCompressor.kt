package io.imkit.android.media

import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.graphics.Canvas
import android.graphics.Color
import android.graphics.Matrix
import android.media.ExifInterface
import java.io.ByteArrayOutputStream
import java.io.InputStream
import kotlin.math.max
import kotlin.math.roundToInt

/**
 * Prepares a photo for upload: decodes it no larger than needed, scales the
 * longer side down to [maxSide] (never up), turns it upright by its EXIF
 * orientation (camera photos are often stored sideways), puts transparent
 * areas on white, and encodes JPEG.
 */
public object ImageCompressor {
    public class Compressed(public val bytes: ByteArray, public val width: Int, public val height: Int) {
        public val mime: String get() = "image/jpeg"
    }

    /** [open] is called up to three times; each call returns the image from the start. */
    public fun compress(open: () -> InputStream, maxSide: Int = 1920, quality: Int = 85): Compressed {
        require(maxSide > 0) { "maxSide must be positive" }
        require(quality in 1..100) { "quality must be 1..100" }
        val bounds = BitmapFactory.Options().apply { inJustDecodeBounds = true }
        open().use { BitmapFactory.decodeStream(it, null, bounds) }
        require(bounds.outWidth > 0 && bounds.outHeight > 0) { "not a decodable image" }
        val decoded = open().use {
            BitmapFactory.decodeStream(it, null, BitmapFactory.Options().apply { inSampleSize = sampleSize(bounds.outWidth, bounds.outHeight, maxSide) })
        } ?: throw IllegalArgumentException("not a decodable image")
        val orientation = open().use { runCatching { ExifInterface(it).getAttributeInt(ExifInterface.TAG_ORIENTATION, ExifInterface.ORIENTATION_NORMAL) }.getOrNull() }
            ?: ExifInterface.ORIENTATION_NORMAL

        val (w, h) = targetSize(decoded.width, decoded.height, maxSide)
        val matrix = Matrix().apply {
            postScale(w / decoded.width.toFloat(), h / decoded.height.toFloat())
            orient(this, orientation)
        }
        val transformed = Bitmap.createBitmap(decoded, 0, 0, decoded.width, decoded.height, matrix, true)
        val opaque = Bitmap.createBitmap(transformed.width, transformed.height, Bitmap.Config.ARGB_8888)
        Canvas(opaque).apply {
            drawColor(Color.WHITE)
            drawBitmap(transformed, 0f, 0f, null)
        }
        val bytes = ByteArrayOutputStream().also { opaque.compress(Bitmap.CompressFormat.JPEG, quality, it) }.toByteArray()
        val result = Compressed(bytes, opaque.width, opaque.height)
        if (transformed !== decoded) transformed.recycle()
        decoded.recycle()
        opaque.recycle()
        return result
    }

    /** The largest power of two that still decodes the longer side at least [maxSide] wide. */
    internal fun sampleSize(width: Int, height: Int, maxSide: Int): Int {
        var sample = 1
        while (max(width, height) / (sample * 2) >= maxSide) sample *= 2
        return sample
    }

    /** The size with the longer side at most [maxSide], keeping the aspect ratio. */
    internal fun targetSize(width: Int, height: Int, maxSide: Int): Pair<Int, Int> {
        val longer = max(width, height)
        if (longer <= maxSide) return width to height
        val scale = maxSide.toDouble() / longer
        return max(1, (width * scale).roundToInt()) to max(1, (height * scale).roundToInt())
    }

    private fun orient(m: Matrix, orientation: Int) {
        when (orientation) {
            ExifInterface.ORIENTATION_FLIP_HORIZONTAL -> m.postScale(-1f, 1f)
            ExifInterface.ORIENTATION_ROTATE_180 -> m.postRotate(180f)
            ExifInterface.ORIENTATION_FLIP_VERTICAL -> m.postScale(1f, -1f)
            ExifInterface.ORIENTATION_TRANSPOSE -> {
                m.postRotate(90f)
                m.postScale(-1f, 1f)
            }
            ExifInterface.ORIENTATION_ROTATE_90 -> m.postRotate(90f)
            ExifInterface.ORIENTATION_TRANSVERSE -> {
                m.postRotate(-90f)
                m.postScale(-1f, 1f)
            }
            ExifInterface.ORIENTATION_ROTATE_270 -> m.postRotate(-90f)
        }
    }
}
