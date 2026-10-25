package io.imkit.android.media

import android.content.ContentProvider
import android.content.ContentValues
import android.content.Context
import android.database.Cursor
import android.database.MatrixCursor
import android.net.Uri
import android.provider.OpenableColumns
import android.webkit.MimeTypeMap
import androidx.test.core.app.ApplicationProvider
import io.imkit.core.client.ImClient
import io.imkit.core.client.ImClientConfig
import io.imkit.core.client.TokenProvider
import io.imkit.core.engine.ChatEngine
import io.imkit.core.engine.EngineConfig
import io.imkit.core.json.JsonObject
import io.imkit.core.media.MediaUploader
import io.imkit.core.model.ConversationId
import io.imkit.core.store.InMemoryChatStore
import io.imkit.core.testing.MiniServer
import io.imkit.core.testing.eventually
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.runBlocking
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.Robolectric
import org.robolectric.RobolectricTestRunner
import org.robolectric.Shadows.shadowOf
import java.io.File
import java.net.InetAddress
import java.net.ServerSocket
import kotlin.concurrent.thread
import java.util.concurrent.CopyOnWriteArrayList
import kotlin.random.Random

/** MediaSender against a real HTTP upload endpoint and the in-memory IM server. */
@RunWith(RobolectricTestRunner::class)
class MediaSenderTest {
    private val ctx = ApplicationProvider.getApplicationContext<Context>()
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private val server = MiniServer()
    private val uploads = CopyOnWriteArrayList<Pair<String, Int>>()
    private lateinit var http: ServerSocket
    private lateinit var engine: ChatEngine
    private lateinit var sender: MediaSender
    private val peer = ConversationId(MiniServer().uidOf("bob"))

    @Before
    fun setUp() = runBlocking {
        http = ServerSocket(0, 50, InetAddress.getLoopbackAddress())
        // A minimal HTTP/1.1 upload endpoint: the Android test classpath has no JDK HTTP server.
        thread(isDaemon = true) {
            while (!http.isClosed) {
                val sock = runCatching { http.accept() }.getOrNull() ?: break
                sock.use { s ->
                    val input = s.getInputStream().buffered()
                    val head = StringBuilder()
                    while (!head.endsWith(CRLF + CRLF)) head.append(input.read().takeIf { it >= 0 }?.toChar() ?: break)
                    val len = Regex("(?i)content-length: ([0-9]+)").find(head)?.groupValues?.get(1)?.toInt() ?: 0
                    val body = ByteArray(len).also { b -> var o = 0; while (o < len) { val r = input.read(b, o, len - o); if (r < 0) break; o += r } }
                    val n = uploads.size + 1
                    val type = Regex("Content-Type: ([^" + CRLF + "]+)").findAll(String(body, Charsets.ISO_8859_1)).lastOrNull()?.groupValues?.get(1) ?: "application/octet-stream"
                    uploads += type to body.size
                    val reply = """{"url":"/media/f$n","name":"f$n","mime":"$type","size":${body.size}}""".toByteArray()
                    val out = s.getOutputStream()
                    val headers = listOf("HTTP/1.1 200 OK", "Content-Type: application/json", "Content-Length: ${reply.size}", "Connection: close", "", "")
                    out.write(headers.joinToString(CRLF).toByteArray())
                    out.write(reply)
                    out.flush()
                }
            }
        }
        val client = ImClient(
            ImClientConfig(endpoints = listOf("ws://mini"), tokenProvider = TokenProvider { "alice" }, transport = server.transport, random = Random(1)),
            scope,
        )
        engine = ChatEngine(client, InMemoryChatStore(), "alice", scope, EngineConfig())
        engine.open()
        engine.start()
        client.awaitConnected(5_000)
        val uploader = MediaUploader("http://127.0.0.1:${http.localPort}", TokenProvider { "alice" })
        sender = MediaSender(ctx, engine, uploader, maxBytes = 64 * 1024)
    }

    @After
    fun tearDown() {
        engine.client.stop()
        scope.cancel()
        http.close()
    }

    private fun lastPub(): JsonObject = server.requests.last { it.string("action") == "pub" }

    @Test
    fun aVoiceClipIsUploadedThenSentWithItsLength() = runBlocking {
        val clip = File(ctx.cacheDir, "clip.m4a").apply { writeBytes(ByteArray(2_000) { 1 }) }
        val r = sender.sendVoice(peer, clip, seconds = 7)
        assertTrue(r is MediaSender.Result.Sent)
        eventually { server.requests.any { it.string("action") == "pub" } }
        val pub = lastPub()
        assertEquals("audio/mp4", pub.string("contentType"))
        assertEquals(7L, pub.long("duration"))
        assertEquals("[Voice]", pub.string("content"))
        assertTrue(pub.string("fileUrl")!!.startsWith("http://127.0.0.1:"))
        assertFalse("the recording is deleted afterwards", clip.exists())
    }

    @Test
    fun aPickedFileIsSentAsAFileEvenWhenItsTypeLooksLikeText() = runBlocking {
        shadowOf(MimeTypeMap.getSingleton()).addExtensionMimeTypeMapping("txt", "text/plain")
        val f = File(ctx.cacheDir, "notes.txt").apply { writeText("hello") }
        val r = sender.sendPicked(peer, Uri.fromFile(f))
        assertTrue(r is MediaSender.Result.Sent)
        eventually { server.requests.any { it.string("action") == "pub" } }
        val pub = lastPub()
        // A file:// pick has no provider type: it goes as a generic file, never as a text message.
        assertEquals("application/octet-stream", pub.string("contentType"))
        assertEquals("[File]", pub.string("content"))
        assertEquals(1, uploads.size)
    }

    @Test
    fun aFileOverTheLimitIsRefusedBeforeUpload() = runBlocking {
        val f = File(ctx.cacheDir, "big.bin").apply { writeBytes(ByteArray(100 * 1024)) }
        val r = sender.sendPicked(peer, Uri.fromFile(f))
        assertEquals(MediaSender.Result.TooLarge(64 * 1024), r)
        assertTrue(uploads.isEmpty())
        assertTrue(server.requests.none { it.string("action") == "pub" })
    }

    @Test
    fun anUnreadableUriFailsWithoutSending() = runBlocking {
        val r = sender.sendPicked(peer, Uri.fromFile(File(ctx.cacheDir, "missing.bin")))
        assertTrue(r is MediaSender.Result.Failed)
        assertTrue(server.requests.none { it.string("action") == "pub" })
    }

    /** A video provider: its type, and a size it reports or keeps to itself; the bytes come from [video]. */
    class Videos : ContentProvider() {
        override fun onCreate() = true
        override fun getType(uri: Uri) = types[uri.lastPathSegment]
        override fun query(uri: Uri, projection: Array<out String>?, selection: String?, args: Array<out String>?, sort: String?): Cursor {
            val f = files.getValue(uri.lastPathSegment!!)
            return MatrixCursor(arrayOf(OpenableColumns.DISPLAY_NAME, OpenableColumns.SIZE)).apply {
                addRow(arrayOf<Any?>(f.name, if (uri.lastPathSegment in hiddenSizes) null else f.length()))
            }
        }
        override fun insert(uri: Uri, values: ContentValues?): Uri? = null
        override fun update(uri: Uri, values: ContentValues?, selection: String?, args: Array<out String>?) = 0
        override fun delete(uri: Uri, selection: String?, args: Array<out String>?) = 0

        companion object {
            val files = mutableMapOf<String, File>()
            val types = mutableMapOf<String, String>()
            val hiddenSizes = mutableSetOf<String>()
        }
    }

    private fun video(name: String, bytes: Int, mime: String = "video/quicktime", sizeKnown: Boolean = true): Uri {
        Robolectric.setupContentProvider(Videos::class.java, "io.imkit.test.videos")
        Videos.files[name] = File(ctx.cacheDir, name).apply { writeBytes(ByteArray(bytes) { 7 }) }
        Videos.types[name] = mime
        if (!sizeKnown) Videos.hiddenSizes += name
        val uri = Uri.parse("content://io.imkit.test.videos/$name")
        // Not through the provider's openFile: Robolectric cannot open a ParcelFileDescriptor on JDK 17.
        shadowOf(ctx.contentResolver).registerInputStreamSupplier(uri) { Videos.files.getValue(name).inputStream() }
        return uri
    }

    /** Writes [outBytes] bytes, or fails when null; records what it was given. */
    private class FakeCompressor(private val outBytes: Int?) : VideoCompressor {
        val sources = CopyOnWriteArrayList<Uri>()
        override suspend fun compress(source: Uri, out: File): Boolean {
            sources += source
            ctx().contentResolver.openInputStream(source)!!.use { it.readBytes() } // it can read what it is given
            outBytes ?: return false
            out.writeBytes(ByteArray(outBytes))
            return true
        }

        private fun ctx() = ApplicationProvider.getApplicationContext<Context>()
    }

    /** One video went up, of this type; the recorded size is the multipart body, a little over the file. */
    private fun assertVideoUpload(type: String, fileBytes: Int) {
        val videos = uploads.filter { it.first.startsWith("video/") }
        assertEquals(1, videos.size)
        assertEquals(type, videos[0].first)
        assertTrue("${videos[0].second} bytes for a $fileBytes-byte file", videos[0].second in fileBytes..fileBytes + 1024)
    }

    private fun senderWith(compressor: VideoCompressor?, compressAbove: Long = 50L * 1024 * 1024) =
        MediaSender(ctx, engine, MediaUploader("http://127.0.0.1:${http.localPort}", TokenProvider { "alice" }), maxBytes = 64 * 1024, videoCompressor = compressor, compressVideosAboveBytes = compressAbove)

    @Test
    fun aVideoOverTheLimitIsReencodedAndSentAsMp4() = runBlocking {
        val compressor = FakeCompressor(outBytes = 10_000)
        val uri = video("trip.mov", 100 * 1024)
        val r = senderWith(compressor).sendPicked(peer, uri)
        assertTrue(r.toString(), r is MediaSender.Result.Sent)
        assertEquals(listOf(uri), compressor.sources.toList())
        assertVideoUpload("video/mp4", 10_000)
        eventually { server.requests.any { it.string("action") == "pub" } }
        assertEquals("video/mp4", lastPub().string("contentType"))
        assertEquals("[Video]", lastPub().string("content"))
    }

    @Test
    fun aVideoStillTooLargeOrNotReencodedIsRefused() = runBlocking {
        val failing = senderWith(FakeCompressor(outBytes = null)).sendPicked(peer, video("a.mov", 100 * 1024))
        assertEquals(MediaSender.Result.TooLarge(64 * 1024), failing)
        val tooBig = senderWith(FakeCompressor(outBytes = 80 * 1024)).sendPicked(peer, video("b.mov", 100 * 1024))
        assertEquals(MediaSender.Result.TooLarge(64 * 1024), tooBig)
        val noCompressor = senderWith(null).sendPicked(peer, video("c.mov", 100 * 1024))
        assertEquals(MediaSender.Result.TooLarge(64 * 1024), noCompressor)
        assertTrue(uploads.isEmpty())
        assertTrue(server.requests.none { it.string("action") == "pub" })
    }

    @Test
    fun aReencodeThatIsNoSmallerSendsTheOriginal() = runBlocking {
        val compressor = FakeCompressor(outBytes = 50 * 1024)
        val r = senderWith(compressor, compressAbove = 32 * 1024).sendPicked(peer, video("d.mov", 40 * 1024))
        assertTrue(r.toString(), r is MediaSender.Result.Sent)
        assertEquals(1, compressor.sources.size)
        assertVideoUpload("video/quicktime", 40 * 1024)
    }

    @Test
    fun aSmallVideoIsNotTouched() = runBlocking {
        val compressor = FakeCompressor(outBytes = 1_000)
        val r = senderWith(compressor).sendPicked(peer, video("e.mov", 20 * 1024))
        assertTrue(r.toString(), r is MediaSender.Result.Sent)
        assertTrue(compressor.sources.isEmpty())
        assertVideoUpload("video/quicktime", 20 * 1024)
    }

    @Test
    fun aVideoOfUnknownSizeIsReadFirstThenReencoded() = runBlocking {
        val compressor = FakeCompressor(outBytes = 10_000)
        val r = senderWith(compressor).sendPicked(peer, video("f.mov", 100 * 1024, sizeKnown = false))
        assertTrue(r.toString(), r is MediaSender.Result.Sent)
        assertEquals("file", compressor.sources.single().scheme)
        assertVideoUpload("video/mp4", 10_000)
    }

    @Test
    fun helpers() {
        assertEquals(240 to 480, VideoThumbnail.scaledSize(1080, 2160, 480))
        assertEquals(320 to 240, VideoThumbnail.scaledSize(320, 240, 480))
        assertEquals(2, VideoThumbnail.durationSeconds(1_200))
        assertEquals(1, VideoThumbnail.durationSeconds(1))
        assertEquals(0, VideoThumbnail.durationSeconds(0))
        shadowOf(MimeTypeMap.getSingleton()).addExtensionMimeTypeMapping("jpg", "image/jpeg")
        assertEquals("photo.jpg", MediaSender.withExtension("photo", "image/jpeg"))
        assertEquals("a.png", MediaSender.withExtension("a.png", "image/jpeg"))
        assertEquals("[Image]", MediaSender.defaultFallback(io.imkit.core.model.MessageKind.IMAGE))
    }
}

private const val CRLF = "\r\n"
