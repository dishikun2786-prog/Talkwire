package io.talkwire.core.m2

import io.talkwire.core.ImIds
import io.talkwire.core.TestFiles
import io.talkwire.core.client.ImClient
import io.talkwire.core.client.ImClientConfig
import io.talkwire.core.client.ImEvent
import io.talkwire.core.client.TokenProvider
import io.talkwire.core.client.createGroup
import io.talkwire.core.engine.ChatEngine
import io.talkwire.core.engine.ChatEvent
import io.talkwire.core.engine.EngineConfig
import io.talkwire.core.engine.OutgoingMessage
import io.talkwire.core.json.Json
import io.talkwire.core.json.JsonObject
import io.talkwire.core.json.jsonObject
import io.talkwire.core.m1.RealServer
import io.talkwire.core.media.MediaUploader
import io.talkwire.core.model.ConversationId
import io.talkwire.core.model.MessageStatus
import io.talkwire.core.store.InMemoryChatStore
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import org.junit.After
import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assume.assumeTrue
import org.junit.Test
import java.io.File
import java.net.ServerSocket
import java.security.SecureRandom
import java.util.concurrent.CopyOnWriteArrayList
import java.util.concurrent.TimeUnit

/**
 * The stack the sample app runs on, on this machine: the real IM server in
 * host mode, pointed at `examples/sample-host` (Node), with two users that
 * sign in through the sample host's dev login. Covers what the phone run
 * covers below the UI: sign-in and the automatic friendship, a direct
 * message, media through the host, a group, and the red packet's live update.
 *
 * Needs TALKWIRE_TEST_DSN, `server/bin/talkwire-server(.exe)`, Node on the PATH and
 * `npm install` done in examples/sample-host; skipped without them.
 */
class SampleStackTest {
    private val scope = CoroutineScope(Dispatchers.Default + SupervisorJob())
    private val clients = CopyOnWriteArrayList<ImClient>()
    private val processes = CopyOnWriteArrayList<Process>()
    private val http = OkHttpClient.Builder().readTimeout(10, TimeUnit.SECONDS).build()

    @After
    fun tearDown() {
        clients.forEach { it.stop() }
        scope.cancel()
        processes.forEach { p -> p.destroy(); if (!p.waitFor(5, TimeUnit.SECONDS)) p.destroyForcibly() }
    }

    private fun freePort() = ServerSocket(0).use { it.localPort }

    private fun spawn(dir: File, env: Map<String, String>, vararg cmd: String): Pair<Process, StringBuffer> {
        val pb = ProcessBuilder(*cmd).directory(dir).redirectErrorStream(true)
        pb.environment().putAll(env)
        val p = pb.start().also { processes += it }
        val log = StringBuffer()
        Thread { p.inputStream.bufferedReader().forEachLine { log.append(it).append('\n') } }.apply { isDaemon = true; start() }
        return p to log
    }

    private fun waitHttp(url: String, what: String, log: StringBuffer, p: Process) {
        val deadline = System.currentTimeMillis() + 60_000
        while (true) {
            check(p.isAlive) { "$what exited:\n${log.toString().takeLast(3000)}" }
            if (runCatching { http.newCall(Request.Builder().url(url).build()).execute().use { true } }.getOrDefault(false)) return
            check(System.currentTimeMillis() < deadline) { "$what not up in 60s:\n${log.toString().takeLast(3000)}" }
            Thread.sleep(200)
        }
    }

    private fun post(url: String, body: JsonObject, token: String? = null): Pair<Int, JsonObject?> {
        val b = Request.Builder().url(url).post(body.toString().toRequestBody("application/json".toMediaType()))
        if (token != null) b.header("Authorization", "Bearer $token")
        return http.newCall(b.build()).execute().use { r -> r.code to runCatching { Json.parseObject(r.body!!.string()) }.getOrNull() }
    }

    private suspend fun until(what: String, timeoutMs: Long = 15_000, check: suspend () -> Boolean) {
        try {
            withTimeout(timeoutMs) { while (!check()) delay(25) }
        } catch (e: kotlinx.coroutines.TimeoutCancellationException) {
            throw AssertionError("timed out waiting for $what")
        }
    }

    private class Device(val engine: ChatEngine, val customs: CopyOnWriteArrayList<ImEvent.Custom>, val events: CopyOnWriteArrayList<ChatEvent>)

    private suspend fun device(wsUrl: String, userId: String, token: String): Device {
        val client = ImClient(ImClientConfig(endpoints = listOf(wsUrl), tokenProvider = TokenProvider { token }, reconnectBaseDelayMs = 200, reconnectMaxDelayMs = 400), scope)
            .also { clients += it }
        val engine = ChatEngine(client, InMemoryChatStore(), userId, scope, EngineConfig())
        val customs = CopyOnWriteArrayList<ImEvent.Custom>()
        val events = CopyOnWriteArrayList<ChatEvent>()
        scope.launch(start = CoroutineStart.UNDISPATCHED) { client.events.collect { if (it is ImEvent.Custom) customs += it } }
        scope.launch(start = CoroutineStart.UNDISPATCHED) { engine.events.collect { events += it } }
        engine.open()
        return Device(engine, customs, events)
    }

    @Test(timeout = 240_000)
    fun theSampleAppsStack() = runBlocking {
        val dsn = System.getenv("TALKWIRE_TEST_DSN")
        assumeTrue("set TALKWIRE_TEST_DSN", !dsn.isNullOrBlank())
        val serverBin = File(TestFiles.repoRoot, "server/bin/" + if (System.getProperty("os.name").startsWith("Windows")) "talkwire-server.exe" else "talkwire-server")
        assumeTrue("build the server", serverBin.isFile)
        val hostDir = File(TestFiles.repoRoot, "examples/sample-host")
        assumeTrue("npm install in examples/sample-host", File(hostDir, "node_modules/tsx").isDirectory)
        assumeTrue("node on the PATH", runCatching { ProcessBuilder("node", "--version").start().waitFor() == 0 }.getOrDefault(false))

        val secret = ByteArray(32).also { SecureRandom().nextBytes(it) }.joinToString("") { "%02x".format(it) }
        val hostPort = freePort()
        val imPort = freePort()
        val hostUrl = "http://127.0.0.1:$hostPort"
        val media = File.createTempFile("talkwire-media", "").apply { delete(); mkdirs() }
        val (hostProc, hostLog) = spawn(
            hostDir,
            mapOf("JWT_SECRET" to secret, "PORT" to "$hostPort", "IM_BASE_URL" to "http://127.0.0.1:$imPort", "MEDIA_DIR" to media.absolutePath),
            "node", "--import", "tsx", "src/main.ts",
        )
        val db = RealServer.parseDsn(dsn!!)
        val (imProc, imLog) = spawn(
            serverBin.parentFile.parentFile,
            mapOf(
                "IM_PORT" to "$imPort",
                "DB_HOST" to (db["host"] ?: "127.0.0.1"), "DB_PORT" to (db["port"] ?: "5432"), "DB_USER" to (db["user"] ?: "postgres"),
                "DB_PASSWORD" to (db["password"] ?: ""), "DB_NAME" to (db["dbname"] ?: error("no dbname")), "DB_SSLMODE" to (db["sslmode"] ?: "disable"),
                "JWT_SECRET" to secret,
                "MAIN_API_BASE_URL" to hostUrl,
                "TALKWIRE_HOST_MODE" to "http",
                "TALKWIRE_PROTOCOL_PROFILE" to "strict",
            ),
            serverBin.absolutePath,
        )
        try {
            waitHttp("$hostUrl/media/none", "sample host", hostLog, hostProc)
            waitHttp("http://127.0.0.1:$imPort/api/health", "talkwire-server", imLog, imProc)
            flow(hostUrl, "ws://127.0.0.1:$imPort/ws/im")
        } catch (e: Throwable) {
            throw AssertionError("$e\n--- sample host ---\n${hostLog.toString().takeLast(4000)}\n--- talkwire-server ---\n${imLog.toString().takeLast(4000)}", e)
        } finally {
            media.deleteRecursively()
        }
    }

    private suspend fun flow(hostUrl: String, wsUrl: String) {
        val run = ByteArray(4).also { SecureRandom().nextBytes(it) }.joinToString("") { "%02x".format(it) }
        // Dev sign-in; the second user becomes the first one's contact.
        val (s1, ann) = post("$hostUrl/dev/login", jsonObject { put("name", "ann_$run") })
        val (s2, bo) = post("$hostUrl/dev/login", jsonObject { put("name", "bo_$run") })
        assertEquals(200, s1)
        assertEquals(200, s2)
        val annId = ann!!.string("userId")!!
        val boId = bo!!.string("userId")!!
        val annToken = ann.string("accessToken")!!
        val boToken = bo.string("accessToken")!!
        val withBo = ConversationId(ImIds.uid(boId))
        val withAnn = ConversationId(ImIds.uid(annId))

        // The sample app's contact source and profile lookup, as SampleHost asks for them.
        fun items(path: String, token: String) = http.newCall(Request.Builder().url(hostUrl + path).header("Authorization", "Bearer $token").build())
            .execute().use { r -> assertEquals(path, 200, r.code); Json.parseObject(r.body!!.string()).array("items")!!.objects() }
        assertEquals(listOf(withBo.id), items("/api/v1/chat/contacts?status=accepted", annToken).map { it.string("uid") })
        assertEquals(listOf(withAnn.id), items("/api/v1/chat/contacts?status=accepted", boToken).map { it.string("uid") })
        val profile = items("/api/v1/chat/profiles?uids=${withAnn.id}", boToken).single()
        assertEquals("ann_$run", profile.string("displayName"))

        val a = device(wsUrl, annId, annToken)
        val b = device(wsUrl, boId, boToken)
        a.engine.ensureConversation(withBo)
        b.engine.ensureConversation(withAnn)
        val bWin = b.engine.messages(withAnn)
        a.engine.start()
        b.engine.start()
        until("both synced") { a.events.any { it is ChatEvent.Synced } && b.events.any { it is ChatEvent.Synced } }

        // A direct message: the IM server asks the sample host whether they are friends.
        a.engine.send(withBo, OutgoingMessage("hi bo"))
        until("bo receives text") { bWin.value.any { it.content == "hi bo" && it.status == MessageStatus.RECEIVED } }

        // Media goes to the host; the message carries its URL.
        val bytes = ByteArray(3_000) { (it % 251).toByte() }
        val clip = File.createTempFile("clip", ".m4a").apply { writeBytes(bytes) }
        val up = MediaUploader(hostUrl, TokenProvider { annToken }).upload(clip, "clip.m4a", "audio/mp4")
        clip.delete()
        a.engine.send(withBo, OutgoingMessage("[Voice]", contentType = up.mime, fileUrl = up.url, fileName = up.name, duration = 3))
        until("bo receives voice") { bWin.value.any { it.fileUrl == up.url } }
        val got = bWin.value.first { it.fileUrl == up.url }
        assertEquals(3, got.duration)
        val served = http.newCall(Request.Builder().url(up.url).build()).execute().use { it.body!!.bytes() }
        assertArrayEquals(bytes, served)

        // A group.
        val topic = a.engine.client.createGroup("sample $run", listOf(withBo.id))
        val group = ConversationId(topic)
        until("bo learns of the group") { b.engine.conversations.value.any { it.id == group } }
        val bGroup = b.engine.messages(group)
        a.engine.send(group, OutgoingMessage("hello group"))
        until("bo receives group text") { bGroup.value.any { it.content == "hello group" } }

        // The red packet: created at the host, sent as a message, claimed, pushed live.
        val (cs, created) = post("$hostUrl/api/v1/red-packets", jsonObject { put("topic", topic); put("total", 30); put("count", 2) }, annToken)
        assertEquals(200, cs)
        val message = created!!.get("message") as JsonObject
        val id = (message.get("extra") as JsonObject).string("redPacketId")!!
        a.engine.send(group, OutgoingMessage(message.string("content")!!, contentType = message.string("contentType"), extra = message.get("extra")))
        until("bo receives the card") { bGroup.value.any { it.contentType == "red_packet" } }
        val (ks, claimed) = post("$hostUrl/api/v1/red-packets/$id/claim", jsonObject {}, boToken)
        assertEquals(200, ks)
        assertEquals(15, claimed!!.int("myAmount"))
        until("ann sees the update") { a.customs.any { it.what == "red_packet" && (it.extra as? JsonObject)?.int("claimed") == 1 } }
        val pushed = a.customs.last { it.what == "red_packet" }.extra as JsonObject
        assertEquals("a push carries nobody's amount", null, pushed.int("myAmount"))
    }
}
