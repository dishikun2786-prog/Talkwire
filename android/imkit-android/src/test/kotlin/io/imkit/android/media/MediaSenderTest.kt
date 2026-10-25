package io.imkit.android.media

import android.content.Context
import android.net.Uri
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
