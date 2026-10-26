package io.talkwire.ui.chat

import android.content.ContentValues
import android.content.Context
import android.content.Intent
import android.os.Build
import android.os.Environment
import android.provider.MediaStore
import androidx.core.content.FileProvider
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import java.io.File
import java.io.InputStream
import java.net.HttpURLConnection
import java.net.URL

/** Gives shared and captured files a content URI under the app's own authority. */
public class ImFileProvider : FileProvider()

/** Saving and sharing the media of a message. */
internal object MediaActions {
    fun authority(context: Context): String = context.packageName + ".talkwire.files"

    /** Saving needs MediaStore's pending-write support (Android 10+); older systems would need a storage permission. */
    val canSave: Boolean get() = Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q

    /** Photos and videos go to the gallery, everything else to Downloads. False on any failure. */
    suspend fun save(context: Context, url: String, mime: String, name: String): Boolean = withContext(Dispatchers.IO) {
        if (!canSave) return@withContext false
        val resolver = context.contentResolver
        val collection = when {
            mime.startsWith("image/") -> MediaStore.Images.Media.EXTERNAL_CONTENT_URI
            mime.startsWith("video/") -> MediaStore.Video.Media.EXTERNAL_CONTENT_URI
            else -> MediaStore.Downloads.EXTERNAL_CONTENT_URI
        }
        val dir = when {
            mime.startsWith("image/") -> Environment.DIRECTORY_PICTURES
            mime.startsWith("video/") -> Environment.DIRECTORY_MOVIES
            else -> Environment.DIRECTORY_DOWNLOADS
        }
        val values = ContentValues().apply {
            put(MediaStore.MediaColumns.DISPLAY_NAME, safeName(name))
            put(MediaStore.MediaColumns.MIME_TYPE, mime)
            put(MediaStore.MediaColumns.RELATIVE_PATH, dir)
            put(MediaStore.MediaColumns.IS_PENDING, 1)
        }
        val item = runCatching { resolver.insert(collection, values) }.getOrNull() ?: return@withContext false
        val ok = runCatching {
            open(url).use { input -> resolver.openOutputStream(item)!!.use { input.copyTo(it) } }
        }.isSuccess
        if (ok) {
            runCatching { resolver.update(item, ContentValues().apply { put(MediaStore.MediaColumns.IS_PENDING, 0) }, null, null) }
        } else {
            runCatching { resolver.delete(item, null, null) }
        }
        ok
    }

    /** Downloads into the share cache and opens the system share sheet. */
    suspend fun share(context: Context, url: String, mime: String, name: String): Boolean {
        val file = withContext(Dispatchers.IO) {
            runCatching {
                val dir = File(context.cacheDir, "talkwire-share").apply { mkdirs() }
                // Only the newest shared file is kept.
                dir.listFiles()?.forEach { it.delete() }
                File(dir, safeName(name)).also { f -> open(url).use { input -> f.outputStream().use { input.copyTo(it) } } }
            }.getOrNull()
        } ?: return false
        val uri = FileProvider.getUriForFile(context, authority(context), file)
        val send = Intent(Intent.ACTION_SEND).apply {
            type = mime
            putExtra(Intent.EXTRA_STREAM, uri)
            addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
        }
        return runCatching {
            context.startActivity(Intent.createChooser(send, null).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
        }.isSuccess
    }

    /** A file for the camera to write into, and its content URI. */
    fun cameraTarget(context: Context): Pair<File, android.net.Uri> {
        val dir = File(context.cacheDir, "talkwire-camera").apply { mkdirs() }
        val f = File(dir, "photo-${System.currentTimeMillis()}.jpg")
        return f to FileProvider.getUriForFile(context, authority(context), f)
    }

    private fun open(url: String): InputStream {
        val conn = URL(url).openConnection() as HttpURLConnection
        conn.connectTimeout = 15_000
        conn.readTimeout = 60_000
        if (conn.responseCode !in 200..299) {
            conn.disconnect()
            throw java.io.IOException("HTTP ${conn.responseCode}")
        }
        return conn.inputStream
    }

    internal fun safeName(name: String): String =
        name.substringAfterLast('/').replace(Regex("[\\\\/:*?\"<>|\\p{Cntrl}]"), "_").take(100).ifBlank { "file" }
}
