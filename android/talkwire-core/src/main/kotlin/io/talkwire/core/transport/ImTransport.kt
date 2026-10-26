package io.talkwire.core.transport

import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import java.util.concurrent.TimeUnit

/**
 * Opens WebSocket connections. The client never talks to OkHttp directly, so
 * tests can drive it with a scripted transport and inject faults.
 */
public interface ImTransport {
    public fun open(url: String, listener: ImSocketListener): ImSocket
}

public interface ImSocket {
    /** Queues one text frame; false when the socket is already closing or gone. */
    public fun send(text: String): Boolean

    /** Graceful close. */
    public fun close(code: Int, reason: String)

    /** Drops the connection immediately. */
    public fun cancel()
}

/**
 * Callbacks of one socket. They may arrive on any thread; the client tags each
 * socket with a generation and ignores callbacks from sockets it replaced.
 */
public interface ImSocketListener {
    public fun onOpen()

    public fun onText(text: String)

    public fun onClosed(code: Int, reason: String)

    public fun onFailure(error: Throwable)
}

/**
 * The production transport. [pingIntervalMs] makes OkHttp send pings and
 * notice a half-dead connection; the server pings too (protocol spec 2) and
 * OkHttp answers those automatically.
 */
public class OkHttpTransport(
    base: OkHttpClient = OkHttpClient(),
    pingIntervalMs: Long = 30_000L,
    connectTimeoutMs: Long = 10_000L,
) : ImTransport {
    private val client: OkHttpClient = base.newBuilder()
        .pingInterval(pingIntervalMs, TimeUnit.MILLISECONDS)
        .connectTimeout(connectTimeoutMs, TimeUnit.MILLISECONDS)
        .readTimeout(0, TimeUnit.MILLISECONDS)
        .build()

    override fun open(url: String, listener: ImSocketListener): ImSocket {
        val ws = client.newWebSocket(
            Request.Builder().url(url).build(),
            object : WebSocketListener() {
                override fun onOpen(webSocket: WebSocket, response: Response) = listener.onOpen()

                override fun onMessage(webSocket: WebSocket, text: String) = listener.onText(text)

                override fun onClosing(webSocket: WebSocket, code: Int, reason: String) {
                    webSocket.close(code, null)
                }

                override fun onClosed(webSocket: WebSocket, code: Int, reason: String) = listener.onClosed(code, reason)

                override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) = listener.onFailure(t)
            },
        )
        return object : ImSocket {
            override fun send(text: String): Boolean = ws.send(text)

            override fun close(code: Int, reason: String) {
                ws.close(code, reason)
            }

            override fun cancel() = ws.cancel()
        }
    }
}
