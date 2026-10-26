package io.talkwire.videocompress

import android.content.Context
import android.net.Uri
import android.os.Handler
import android.os.HandlerThread
import android.util.Log
import androidx.annotation.OptIn
import androidx.media3.common.MediaItem
import androidx.media3.common.MimeTypes
import androidx.media3.common.util.UnstableApi
import androidx.media3.transformer.Composition
import androidx.media3.transformer.DefaultEncoderFactory
import androidx.media3.transformer.EditedMediaItem
import androidx.media3.transformer.ExportException
import androidx.media3.transformer.ExportResult
import androidx.media3.transformer.Transformer
import androidx.media3.transformer.VideoEncoderSettings
import io.talkwire.android.media.VideoCompressor
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.android.asCoroutineDispatcher
import kotlinx.coroutines.withContext
import java.io.File

/**
 * Re-encodes a video to H.264 video and AAC audio in an MP4 with Media3
 * Transformer, as the reference deployment did before uploading videos of
 * 50 MB and more. Pass it to `MediaSender(videoCompressor = ...)`.
 *
 * The resolution stays as it is. With [videoBitrate] null the encoder picks
 * its own bitrate (what the reference deployment shipped); a set bitrate
 * makes the result's size predictable: about `bitrate × seconds / 8` bytes
 * of video.
 *
 * Each export runs on a thread of its own, not the main thread: Transformer
 * is driven from the looper it was built on.
 *
 * Not verified in this repository: the encoder needs a device, and whether a
 * rotated (portrait) recording keeps its orientation has to be checked on one.
 */
@OptIn(UnstableApi::class)
public class Media3VideoCompressor(
    context: Context,
    private val videoBitrate: Int? = null,
) : VideoCompressor {
    private val context = context.applicationContext

    override suspend fun compress(source: Uri, out: File): Boolean {
        val thread = HandlerThread("talkwire-video-compress").apply { start() }
        val ok = try {
            withContext(Handler(thread.looper).asCoroutineDispatcher()) { export(source, out, thread) }
        } finally {
            thread.quitSafely()
        }
        if (!ok) withContext(Dispatchers.IO + NonCancellable) { out.delete() }
        return ok
    }

    /** On [thread]'s looper, the only thread allowed to touch the Transformer. */
    private suspend fun export(source: Uri, out: File, thread: HandlerThread): Boolean {
        val done = CompletableDeferred<Boolean>()
        val builder = Transformer.Builder(context)
            .setLooper(thread.looper)
            .setVideoMimeType(MimeTypes.VIDEO_H264)
            .setAudioMimeType(MimeTypes.AUDIO_AAC)
            .addListener(object : Transformer.Listener {
                override fun onCompleted(composition: Composition, exportResult: ExportResult) {
                    done.complete(true)
                }

                override fun onError(composition: Composition, exportResult: ExportResult, exportException: ExportException) {
                    Log.w(TAG, "export failed: $exportException")
                    done.complete(false)
                }
            })
        videoBitrate?.let { bitrate ->
            val settings = VideoEncoderSettings.Builder().setBitrate(bitrate).build()
            builder.setEncoderFactory(DefaultEncoderFactory.Builder(context).setRequestedVideoEncoderSettings(settings).build())
        }
        val transformer = builder.build()
        return try {
            transformer.start(EditedMediaItem.Builder(MediaItem.fromUri(source)).build(), out.absolutePath)
            done.await()
        } catch (cancel: CancellationException) {
            // Still on the looper thread here; the caller deletes the half-written file.
            runCatching { transformer.cancel() }
            runCatching { out.delete() }
            throw cancel
        } catch (e: Exception) {
            Log.w(TAG, "export failed to start: ${e.message}")
            false
        }
    }

    private companion object {
        const val TAG = "Media3VideoCompressor"
    }
}
