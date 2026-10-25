package io.imkit.android.media

import android.content.Context
import android.net.Uri
import android.provider.OpenableColumns
import android.webkit.MimeTypeMap
import io.imkit.core.engine.ChatEngine
import io.imkit.core.engine.OutgoingMessage
import io.imkit.core.json.jsonObject
import io.imkit.core.media.MediaUploader
import io.imkit.core.model.ChatMessage
import io.imkit.core.model.ConversationId
import io.imkit.core.model.MessageKind
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import java.io.File

/**
 * Sends a picked photo, video or file, or a recorded voice clip: prepares
 * it, uploads it to the host and sends the message that points at it.
 *
 * - Photos are compressed ([ImageCompressor]) and carry `extra.imageWidth`
 *   and `imageHeight`, so the bubble has its final size before the image loads.
 * - Videos are sent as they are, with a first-frame thumbnail
 *   (`extra.thumbUrl`, `thumbWidth`, `thumbHeight`) and their length; a
 *   thumbnail that cannot be made or uploaded does not stop the send.
 * - The message's `content` is a short fallback text ([fallbackText]) for
 *   clients that do not know the type; ImKit's own UI never shows it.
 */
public class MediaSender(
    private val context: Context,
    private val engine: ChatEngine,
    private val uploader: MediaUploader,
    /** Larger files are refused before they are read; the Node host SDK's default limit is 50 MB. */
    public val maxBytes: Long = 50L * 1024 * 1024,
    private val fallbackText: (MessageKind) -> String = ::defaultFallback,
) {
    public sealed interface Result {
        public data class Sent(val message: ChatMessage) : Result

        public data class TooLarge(val limitBytes: Long) : Result

        /** Reading, preparing or uploading failed; nothing was sent. */
        public data class Failed(val error: Throwable) : Result
    }

    /** A photo, video or other file the user picked. */
    public suspend fun sendPicked(conversation: ConversationId, uri: Uri): Result = guarded {
        val resolver = context.contentResolver
        val mime = resolver.getType(uri) ?: "application/octet-stream"
        val (displayName, size) = withContext(Dispatchers.IO) { nameAndSize(uri) }
        if (size != null && size > maxBytes) return@guarded Result.TooLarge(maxBytes)
        val name = withExtension(displayName ?: uri.lastPathSegment ?: "file", mime)
        when {
            mime.startsWith("image/") && mime != "image/gif" -> {
                val compressed = withContext(Dispatchers.IO) {
                    ImageCompressor.compress({ resolver.openInputStream(uri) ?: error("cannot open $uri") })
                }
                val up = uploadBytes(compressed.bytes, name.substringBeforeLast('.') + ".jpg", compressed.mime)
                send(conversation, MessageKind.IMAGE, up.mime, up.url, up.name, extra = jsonObject {
                    put("imageWidth", compressed.width)
                    put("imageHeight", compressed.height)
                })
            }
            mime.startsWith("video/") -> {
                val thumb = withContext(Dispatchers.IO) { VideoThumbnail.extract(context, uri) }
                val up = uploadUri(uri, name, mime)
                val extra = thumb?.let { t ->
                    val thumbUrl = runCatching { uploadBytes(t.jpeg, "$name-thumb.jpg", "image/jpeg").url }.getOrNull()
                    thumbUrl?.let { url ->
                        jsonObject {
                            put("thumbUrl", url)
                            put("thumbWidth", t.width)
                            put("thumbHeight", t.height)
                        }
                    }
                }
                send(conversation, MessageKind.VIDEO, up.mime, up.url, up.name, duration = thumb?.durationSeconds ?: 0, extra = extra)
            }
            else -> {
                val up = uploadUri(uri, name, mime)
                // A picked .txt or .mp3 is a file, not a text or voice message.
                val type = if (MessageKind.of(up.mime, up.url) == MessageKind.FILE) up.mime else "application/octet-stream"
                send(conversation, MessageKind.FILE, type, up.url, up.name)
            }
        }
    }

    /** A voice clip; the file is deleted afterwards either way. */
    public suspend fun sendVoice(conversation: ConversationId, file: File, seconds: Int): Result = guarded {
        try {
            val up = uploader.upload(file, "voice.m4a", "audio/mp4")
            send(conversation, MessageKind.VOICE, up.mime.takeIf { it.startsWith("audio/") } ?: "audio/mp4", up.url, up.name, duration = seconds.coerceAtLeast(1))
        } finally {
            file.delete()
        }
    }

    private suspend fun send(
        conversation: ConversationId,
        kind: MessageKind,
        mime: String,
        url: String,
        name: String,
        duration: Int = 0,
        extra: io.imkit.core.json.JsonObject? = null,
    ): Result = Result.Sent(
        engine.send(conversation, OutgoingMessage(content = fallbackText(kind), contentType = mime, fileUrl = url, fileName = name, duration = duration, extra = extra)),
    )

    private suspend fun uploadBytes(bytes: ByteArray, name: String, mime: String) = withTempFile(name) { f ->
        f.writeBytes(bytes)
        uploader.upload(f, name, mime)
    }

    private suspend fun uploadUri(uri: Uri, name: String, mime: String) = withTempFile(name) { f ->
        withContext(Dispatchers.IO) {
            (context.contentResolver.openInputStream(uri) ?: error("cannot open $uri")).use { input -> f.outputStream().use { input.copyTo(it) } }
        }
        if (f.length() > maxBytes) throw TooLargeException()
        uploader.upload(f, name, mime)
    }

    private suspend fun <T> withTempFile(name: String, block: suspend (File) -> T): T {
        val dir = File(context.cacheDir, "imkit-upload").apply { mkdirs() }
        val f = File.createTempFile("imkit-up", "-" + name.replace(Regex("[^A-Za-z0-9._-]"), "_").takeLast(60), dir)
        try {
            return block(f)
        } finally {
            f.delete()
        }
    }

    private fun nameAndSize(uri: Uri): Pair<String?, Long?> =
        runCatching {
            context.contentResolver.query(uri, arrayOf(OpenableColumns.DISPLAY_NAME, OpenableColumns.SIZE), null, null, null)?.use { c ->
                if (!c.moveToFirst()) return@use null to null
                val name = c.getColumnIndex(OpenableColumns.DISPLAY_NAME).takeIf { it >= 0 }?.let { c.getString(it) }
                val size = c.getColumnIndex(OpenableColumns.SIZE).takeIf { it >= 0 && !c.isNull(it) }?.let { c.getLong(it) }
                name to size
            }
        }.getOrNull() ?: (null to null)

    private class TooLargeException : Exception("file too large")

    private suspend fun guarded(block: suspend () -> Result): Result = try {
        block()
    } catch (e: CancellationException) {
        throw e
    } catch (e: TooLargeException) {
        Result.TooLarge(maxBytes)
    } catch (e: Throwable) {
        // OutOfMemoryError included: a huge pick must end as a message to the user, not a crash.
        Result.Failed(e)
    }

    public companion object {
        /** A name with an extension: the host derives the allowed type from it. */
        internal fun withExtension(name: String, mime: String): String {
            if (name.substringAfterLast('/').contains('.')) return name
            val ext = MimeTypeMap.getSingleton().getExtensionFromMimeType(mime) ?: return name
            return "$name.$ext"
        }

        public fun defaultFallback(kind: MessageKind): String = when (kind) {
            MessageKind.IMAGE -> "[Image]"
            MessageKind.VIDEO -> "[Video]"
            MessageKind.VOICE -> "[Voice]"
            else -> "[File]"
        }
    }
}
