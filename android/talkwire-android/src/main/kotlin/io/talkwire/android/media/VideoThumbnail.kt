package io.talkwire.android.media

import android.content.Context
import android.graphics.Bitmap
import android.media.MediaMetadataRetriever
import android.net.Uri
import java.io.ByteArrayOutputStream
import kotlin.math.max
import kotlin.math.roundToInt

/**
 * The first frame of a video as a JPEG, with the video's size and length, so
 * the chat can show a real preview at the right aspect ratio before anyone
 * downloads the video. Whether a rotated (portrait) recording comes out
 * upright depends on the platform's retriever; that is only checked on a
 * device.
 */
public class VideoThumbnail(
    public val jpeg: ByteArray,
    public val width: Int,
    public val height: Int,
    public val durationSeconds: Int,
) {
    public companion object {
        /** Null when the video cannot be read; a send must not fail because of that. */
        public fun extract(context: Context, uri: Uri, maxSide: Int = 480, quality: Int = 80): VideoThumbnail? {
            val retriever = MediaMetadataRetriever()
            return try {
                retriever.setDataSource(context, uri)
                val frame = retriever.getFrameAtTime(0, MediaMetadataRetriever.OPTION_CLOSEST_SYNC) ?: return null
                val durationMs = retriever.extractMetadata(MediaMetadataRetriever.METADATA_KEY_DURATION)?.toLongOrNull() ?: 0L
                val (w, h) = scaledSize(frame.width, frame.height, maxSide)
                val scaled = if (w == frame.width && h == frame.height) frame else Bitmap.createScaledBitmap(frame, w, h, true)
                val bytes = ByteArrayOutputStream().also { scaled.compress(Bitmap.CompressFormat.JPEG, quality, it) }.toByteArray()
                if (scaled !== frame) scaled.recycle()
                frame.recycle()
                VideoThumbnail(bytes, w, h, durationSeconds(durationMs))
            } catch (e: Exception) {
                null
            } finally {
                runCatching { retriever.release() }
            }
        }

        /** Scales the longer side down to [maxSide], never up, keeping the ratio. */
        internal fun scaledSize(width: Int, height: Int, maxSide: Int): Pair<Int, Int> {
            val longer = max(width, height)
            if (longer <= maxSide) return width to height
            val f = maxSide.toDouble() / longer
            return (width * f).roundToInt().coerceAtLeast(1) to (height * f).roundToInt().coerceAtLeast(1)
        }

        /** Rounded up, so a 1.2 s clip says 2 s rather than 1 s; at least 1 for any non-empty clip. */
        internal fun durationSeconds(ms: Long): Int = if (ms <= 0) 0 else ((ms + 999) / 1000).toInt()
    }
}
