package io.talkwire.core.testing

import io.talkwire.core.json.Json
import io.talkwire.core.json.JsonObject
import io.talkwire.core.transport.ImSocket
import io.talkwire.core.transport.ImSocketListener
import io.talkwire.core.transport.ImTransport
import kotlinx.coroutines.delay
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import java.util.concurrent.CopyOnWriteArrayList
import java.util.concurrent.Executors
import java.util.concurrent.atomic.AtomicInteger

/**
 * A scripted server behind [ImTransport]. Every frame a client sends goes to
 * [onFrame], which answers through [Conn.push]. Frames are delivered on one
 * background thread, in order, like a real socket.
 */
class FakeServer(var onFrame: (Conn, JsonObject) -> Unit = { _, _ -> }) : ImTransport {
    val connections = CopyOnWriteArrayList<Conn>()
    val sent = CopyOnWriteArrayList<JsonObject>()
    private val ids = AtomicInteger()
    private val io = Executors.newSingleThreadExecutor { r -> Thread(r, "fake-server").apply { isDaemon = true } }

    /** Endpoints that refuse to connect. */
    val unreachable = HashSet<String>()

    inner class Conn(val url: String, private val listener: ImSocketListener) : ImSocket {
        val n = ids.incrementAndGet()

        @Volatile var open = true
        val received = CopyOnWriteArrayList<JsonObject>()

        fun push(frame: String) {
            io.execute { if (open) listener.onText(frame) }
        }

        fun push(frame: JsonObject) = push(frame.toString())

        /** The server drops the connection. */
        fun drop() {
            io.execute {
                if (open) {
                    open = false
                    listener.onClosed(1006, "dropped")
                }
            }
        }

        override fun send(text: String): Boolean {
            if (!open) return false
            val f = Json.parseObject(text)
            received += f
            sent += f
            io.execute { onFrame(this, f) }
            return true
        }

        override fun close(code: Int, reason: String) {
            io.execute {
                if (open) {
                    open = false
                    listener.onClosed(code, reason)
                }
            }
        }

        override fun cancel() {
            io.execute {
                if (open) {
                    open = false
                    listener.onFailure(java.io.IOException("cancelled"))
                }
            }
        }
    }

    override fun open(url: String, listener: ImSocketListener): ImSocket {
        val c = Conn(url, listener)
        connections += c
        io.execute {
            if (url in unreachable) {
                c.open = false
                listener.onFailure(java.io.IOException("unreachable"))
            } else {
                listener.onOpen()
            }
        }
        return c
    }

    fun sentActions(): List<String> = sent.mapNotNull { it.string("action") }
}

/** Polls [check] until it holds or [timeoutMs] passes. */
fun eventually(timeoutMs: Long = 5_000, check: () -> Boolean) = runBlocking {
    withTimeout(timeoutMs) {
        while (!check()) delay(5)
    }
}

/** A login reply the way the reference server sends it (no id). */
fun loginOk(uid: String, protocol: Int? = null): String =
    if (protocol == null) """{"action":"login","code":200,"uid":"$uid"}"""
    else """{"action":"login","code":200,"uid":"$uid","protocol":$protocol,"capabilities":["errcode","reply-id","multi-device"]}"""
