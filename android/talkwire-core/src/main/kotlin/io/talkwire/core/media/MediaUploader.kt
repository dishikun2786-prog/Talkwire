package io.talkwire.core.media

import io.talkwire.core.client.TokenProvider
import io.talkwire.core.json.Json
import io.talkwire.core.json.JsonException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrl
import okhttp3.MediaType.Companion.toMediaTypeOrNull
import okhttp3.MultipartBody
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.asRequestBody
import java.io.File
import java.io.IOException

public data class UploadedMedia(
    /** Absolute URL to put in the message's `fileUrl`. */
    val url: String,
    val name: String,
    val mime: String,
    val size: Long,
)

public class MediaUploadException(public val status: Int, message: String) : IOException(message)

/**
 * Uploads media to the host (the host SDK's `talkwireMediaRoutes`): media never
 * travels over the IM connection, only its URL does.
 *
 * Paths default to the Node host SDK's: `POST /api/v1/chat/upload`
 * (multipart, field `file`) and `DELETE /api/v1/chat/upload/:name` for an
 * upload that was never sent.
 */
public class MediaUploader(
    private val hostBaseUrl: String,
    private val tokens: TokenProvider,
    private val http: OkHttpClient = OkHttpClient(),
    private val uploadPath: String = "/api/v1/chat/upload",
) {
    private val base: HttpUrl = hostBaseUrl.toHttpUrl()

    public suspend fun upload(file: File, fileName: String = file.name, mime: String? = null): UploadedMedia = withContext(Dispatchers.IO) {
        val token = tokens.accessToken(forceRefresh = false) ?: throw MediaUploadException(401, "not signed in")
        val body = MultipartBody.Builder()
            .setType(MultipartBody.FORM)
            .addFormDataPart("file", fileName, file.asRequestBody(mime?.toMediaTypeOrNull()))
            .build()
        val req = Request.Builder()
            .url(base.resolve(uploadPath) ?: throw IllegalArgumentException("bad upload path"))
            .header("Authorization", "Bearer $token")
            .post(body)
            .build()
        http.newCall(req).execute().use { resp ->
            val text = resp.body?.string() ?: ""
            if (!resp.isSuccessful) throw MediaUploadException(resp.code, errorOf(text) ?: "upload failed")
            val o = try {
                Json.parseObject(text)
            } catch (e: JsonException) {
                throw MediaUploadException(resp.code, "upload reply is not JSON")
            }
            val url = o.nonEmptyString("url") ?: throw MediaUploadException(resp.code, "upload reply without url")
            UploadedMedia(
                url = base.resolve(url)?.toString() ?: url,
                name = o.string("name") ?: fileName,
                mime = o.string("mime") ?: mime ?: "application/octet-stream",
                size = o.long("size") ?: file.length(),
            )
        }
    }

    /**
     * Deletes an upload that was never sent. The host refuses when a message
     * uses it (409) or the upload is too old or not mine (404); both are
     * reported as false.
     */
    public suspend fun deleteUnsent(url: String): Boolean = withContext(Dispatchers.IO) {
        val name = url.substringAfterLast('/').takeIf { it.isNotEmpty() } ?: return@withContext false
        val token = tokens.accessToken(forceRefresh = false) ?: return@withContext false
        val target = base.resolve("$uploadPath/$name") ?: return@withContext false
        val req = Request.Builder().url(target).header("Authorization", "Bearer $token").delete().build()
        http.newCall(req).execute().use { it.isSuccessful }
    }

    private fun errorOf(text: String): String? = try {
        Json.parseObject(text).nonEmptyString("error")
    } catch (e: JsonException) {
        null
    }
}
