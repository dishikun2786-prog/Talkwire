@file:androidx.annotation.OptIn(androidx.media3.common.util.UnstableApi::class)

package io.talkwire.android.media

import android.content.Context
import android.net.Uri
import androidx.media3.database.StandaloneDatabaseProvider
import androidx.media3.datasource.DataSource
import androidx.media3.datasource.DataSpec
import androidx.media3.datasource.DefaultHttpDataSource
import androidx.test.core.app.ApplicationProvider
import org.junit.After
import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import java.io.File
import java.io.Closeable
import java.io.IOException
import java.net.InetAddress
import java.net.ServerSocket
import java.net.Socket
import java.util.concurrent.atomic.AtomicInteger
import kotlin.concurrent.thread
import kotlin.random.Random

/**
 * C7: the second play of a voice or video message reads from disk. Reads use
 * the flag ExoPlayer's progressive loader sets (do not cache unknown lengths),
 * so the test also pins down why the server must send Content-Length.
 */
@RunWith(RobolectricTestRunner::class)
class MediaCacheTest {
    private val ctx = ApplicationProvider.getApplicationContext<Context>()
    private val body = Random(7).nextBytes(64 * 1024)
    private val hits = AtomicInteger()
    private lateinit var server: TinyHttp
    private lateinit var dir: File
    private lateinit var cache: MediaCache

    @Before
    fun setUp() {
        server = TinyHttp(body, hits)
        dir = File(ctx.cacheDir, "media-test").also { it.deleteRecursively() }
        cache = MediaCache.create(dir, MediaCache.DEFAULT_MAX_BYTES, StandaloneDatabaseProvider(ctx))
    }

    @After
    fun tearDown() {
        cache.cache.release()
        server.close()
        dir.deleteRecursively()
    }

    private fun play(factory: DataSource.Factory, path: String): ByteArray {
        val source = factory.createDataSource()
        val spec = DataSpec.Builder()
            .setUri(Uri.parse("http://127.0.0.1:${server.port}$path"))
            .setFlags(DataSpec.FLAG_DONT_CACHE_IF_LENGTH_UNKNOWN)
            .build()
        source.open(spec)
        val out = java.io.ByteArrayOutputStream()
        val buf = ByteArray(8192)
        while (true) {
            val n = source.read(buf, 0, buf.size)
            if (n < 0) break
            out.write(buf, 0, n)
        }
        source.close()
        return out.toByteArray()
    }

    @Test
    fun C7_MediaCacheHitsSecondPlay() {
        val factory = cache.dataSourceFactory(DefaultHttpDataSource.Factory())
        assertArrayEquals(body, play(factory, "/sized.amr"))
        assertArrayEquals(body, play(factory, "/sized.amr"))
        assertEquals("downloaded once", 1, hits.get())
        assertEquals(body.size.toLong(), cache.cache.cacheSpace)
    }

    @Test
    fun withoutContentLengthEveryPlayDownloadsAgain() {
        val factory = cache.dataSourceFactory(DefaultHttpDataSource.Factory())
        assertArrayEquals(body, play(factory, "/chunked.amr"))
        assertArrayEquals(body, play(factory, "/chunked.amr"))
        assertEquals("the server must send Content-Length", 2, hits.get())
        assertEquals(0L, cache.cache.cacheSpace)
    }
}

/**
 * Just enough HTTP/1.1 for the test (android.jar hides the JDK's HttpServer):
 * "/sized…" answers with Content-Length, anything else chunked without one.
 */
private class TinyHttp(private val body: ByteArray, private val hits: AtomicInteger) : Closeable {
    private val socket = ServerSocket(0, 50, InetAddress.getByName("127.0.0.1"))
    val port: Int get() = socket.localPort

    init {
        thread(isDaemon = true) {
            while (true) {
                val c = try {
                    socket.accept()
                } catch (e: IOException) {
                    break
                }
                thread(isDaemon = true) { serve(c) }
            }
        }
    }

    private fun serve(c: Socket) = c.use {
        val reader = c.getInputStream().bufferedReader(Charsets.ISO_8859_1)
        val path = reader.readLine()?.split(" ")?.getOrNull(1) ?: return@use
        while (!reader.readLine().isNullOrEmpty()) Unit
        hits.incrementAndGet()
        val out = c.getOutputStream()
        fun line(s: String) = out.write((s + CRLF).toByteArray(Charsets.ISO_8859_1))
        line("HTTP/1.1 200 OK")
        line("Content-Type: audio/amr")
        line("Connection: close")
        if (path.startsWith("/sized")) {
            line("Content-Length: ${body.size}")
            line("")
            out.write(body)
        } else {
            line("Transfer-Encoding: chunked")
            line("")
            for (chunk in body.toList().chunked(8192)) {
                line(Integer.toHexString(chunk.size))
                out.write(chunk.toByteArray())
                line("")
            }
            line("0")
            line("")
        }
        out.flush()
    }

    override fun close() = socket.close()

    private companion object {
        const val CRLF = "\r\n"
    }
}
