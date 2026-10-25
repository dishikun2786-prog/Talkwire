package io.imkit.core.client

import io.imkit.core.json.jsonObject
import io.imkit.core.testing.FakeServer
import io.imkit.core.testing.eventually
import io.imkit.core.testing.loginOk
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.async
import kotlinx.coroutines.cancel
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Test
import java.util.concurrent.CopyOnWriteArrayList
import java.util.concurrent.atomic.AtomicInteger
import kotlin.random.Random

class ImClientTest {
    private val scope = CoroutineScope(Dispatchers.Default + SupervisorJob())

    @After
    fun tearDown() = scope.cancel()

    private val me = "uid_K9gGyX8OAK8"

    private fun client(
        server: FakeServer,
        endpoints: List<String> = listOf("ws://a"),
        tokens: TokenProvider = TokenProvider { "t" },
        device: String? = null,
    ) = ImClient(
        ImClientConfig(
            endpoints = endpoints,
            tokenProvider = tokens,
            deviceName = device,
            transport = server,
            loginTimeoutMs = 500,
            requestTimeoutMs = 500,
            reconnectBaseDelayMs = 20,
            reconnectMaxDelayMs = 50,
            random = Random(1),
        ),
        scope,
    )

    /** A server that logs everyone in and answers sub with 200. */
    private fun friendlyServer() = FakeServer { c, f ->
        when (f.string("action")) {
            "login" -> c.push(loginOk(me))
            "sub" -> c.push(jsonObject { put("id", f.string("id")); put("action", "sub"); put("code", 200); put("topic", f.string("topic")) })
        }
    }

    @Test
    fun c1_connectedOnlyAfterTheLoginReply() {
        val server = FakeServer() // never answers the login
        val c = client(server)
        c.start()
        eventually { server.sent.any { it.string("action") == "login" } }
        assertTrue(c.state.value is ConnectionState.Connecting)
        assertNull(c.session)
        // The login reply arrives: now, and only now, connected.
        server.connections.last().push(loginOk(me))
        eventually { c.state.value is ConnectionState.Connected }
        assertEquals(me, c.session?.uid)
    }

    @Test
    fun c1_startIsSerialized() {
        val server = friendlyServer()
        val c = client(server)
        repeat(5) { c.start() }
        eventually { c.state.value is ConnectionState.Connected }
        Thread.sleep(100)
        assertEquals("one connect attempt", 1, server.connections.size)
    }

    @Test
    fun awaitingRightAfterARestartWaitsForTheNewLogin() = runBlocking {
        val c = client(friendlyServer())
        c.start()
        assertEquals(me, c.awaitConnected(2_000)?.uid)
        c.stop()
        c.start()
        assertTrue("not the old Stopped", c.state.value is ConnectionState.Connecting)
        assertEquals(me, c.awaitConnected(2_000)?.uid)
        c.stop()
    }

    @Test
    fun c1_identityClearedBeforeReconnectAndRequestsWaitForLogin() = runBlocking {
        val logins = AtomicInteger()
        val server = FakeServer()
        server.onFrame = { c, f ->
            if (f.string("action") == "login") {
                // The second connection's login reply is held back.
                if (logins.incrementAndGet() == 1) c.push(loginOk(me))
            }
        }
        val c = client(server)
        c.start()
        eventually { c.state.value is ConnectionState.Connected }
        server.connections.last().drop()
        eventually { logins.get() == 2 }
        // Socket open, login sent, no reply: no identity and no requests.
        assertNull(c.session)
        try {
            c.subscribe("uid_gbY32PzSxto")
            fail("request sent before login")
        } catch (e: ImException.NotConnected) {
        }
        assertFalse(server.connections.last().received.any { it.string("action") == "sub" })
        server.connections.last().push(loginOk(me))
        eventually { c.session != null }
    }

    @Test
    fun c1_lateReplyOfAReplacedSocketIsDropped() = runBlocking {
        val server = FakeServer()
        val subs = CopyOnWriteArrayList<io.imkit.core.testing.FakeServer.Conn>()
        server.onFrame = { conn, f ->
            when (f.string("action")) {
                "login" -> conn.push(loginOk(me))
                "sub" -> subs += conn // never answered on the first socket
            }
        }
        val c = client(server)
        c.start()
        eventually { c.state.value is ConnectionState.Connected }
        val first = server.connections.last()
        val pending = async { runCatching { c.subscribe("uid_gbY32PzSxto") } }
        eventually { subs.isNotEmpty() }
        first.drop()
        // The request in flight fails with ConnectionLost, not with a reply.
        val r = pending.await()
        assertTrue(r.exceptionOrNull() is ImException.ConnectionLost)
        eventually { c.state.value is ConnectionState.Connected && server.connections.size == 2 }
        // A reply on the dead socket (the fake still lets us push) changes nothing.
        val events = CopyOnWriteArrayList<ImEvent>()
        val job = scope.launch { c.events.collect { events += it } }
        first.open = true
        first.push("""{"id":"r1","action":"sub","code":200,"topic":"uid_gbY32PzSxto"}""")
        first.push("""{"action":"data","topic":"uid_gbY32PzSxto","from":"uid_gbY32PzSxto","seq":9,"ts":1,"content":"stale"}""")
        Thread.sleep(150)
        assertTrue("events from a replaced socket: $events", events.none { it is ImEvent.Message })
        job.cancel()
    }

    @Test
    fun c1_backoffIsJitteredAndCapped() {
        val c = client(FakeServer())
        val base = 20L
        for (round in 0..8) {
            val nominal = minOf(50L, base shl minOf(round, 5))
            repeat(20) {
                val d = c.backoffDelay(round)
                assertTrue("round $round delay $d", d >= (nominal * 0.8).toLong() && d <= (nominal * 1.2).toLong())
            }
        }
        val spread = (0 until 50).map { c.backoffDelay(0) }.toSet()
        assertTrue("jittered", spread.size > 1)
    }

    @Test
    fun c1_triesEveryEndpointAndPrefersTheOneThatWorked() {
        val server = friendlyServer()
        server.unreachable += "ws://first"
        val c = client(server, endpoints = listOf("ws://first", "ws://second"))
        c.start()
        eventually { c.state.value is ConnectionState.Connected }
        assertEquals("ws://second", c.session?.endpoint)
        server.connections.last().drop()
        eventually { server.connections.size >= 3 && c.state.value is ConnectionState.Connected }
        assertEquals("the working endpoint is tried first", "ws://second", server.connections[2].url)
    }

    @Test
    fun c1_replacedByAnotherLoginStopsReconnecting() {
        val server = friendlyServer()
        val c = client(server)
        c.start()
        eventually { c.state.value is ConnectionState.Connected }
        server.connections.last().push("""{"action":"login","code":401,"reason":"logged in from another device"}""")
        eventually { c.state.value == ConnectionState.Stopped(StopReason.REPLACED) }
        Thread.sleep(150)
        assertEquals("no reconnect", 1, server.connections.size)
    }

    @Test
    fun refusedTokenIsRefreshedThenGivenUp() {
        val asks = CopyOnWriteArrayList<Boolean>()
        val server = FakeServer { c, f -> if (f.string("action") == "login") c.push("""{"id":"${f.string("id")}","action":"login","code":401,"reason":"invalid token"}""") }
        val c = client(server, tokens = TokenProvider { force -> asks += force; "t" })
        c.start()
        eventually { c.state.value == ConnectionState.Stopped(StopReason.AUTH_REJECTED) }
        assertEquals(listOf(false, true, true), asks.toList())
    }

    @Test
    fun noTokenStopsWithNoCredentials() {
        val c = client(friendlyServer(), tokens = TokenProvider { null })
        c.start()
        eventually { c.state.value == ConnectionState.Stopped(StopReason.NO_CREDENTIALS) }
    }

    @Test
    fun loginCarriesTheDeviceName() {
        val server = friendlyServer()
        val c = client(server, device = "pixel-7")
        c.start()
        eventually { c.state.value is ConnectionState.Connected }
        assertEquals("pixel-7", server.sent.first { it.string("action") == "login" }.string("device"))
    }

    @Test
    fun repliesAreMatchedByIdAndActionOnly() = runBlocking {
        val server = FakeServer()
        server.onFrame = { c, f ->
            when (f.string("action")) {
                "login" -> c.push(loginOk(me, protocol = 1))
                "sub" -> {
                    // Noise first: an id-less reply, a reply of another action
                    // with the same id, and a replayed topic_cleared whose id
                    // is an offline record number.
                    c.push("""{"action":"sub","code":200,"topic":"wrong"}""")
                    c.push("""{"id":"${f.string("id")}","action":"get","code":200,"topic":"wrong"}""")
                    c.push("""{"id":"1","action":"topic_cleared","topic":"uid_x","from":"uid_x","clearVersion":1,"clearBeforeSeq":3}""")
                    c.push("""{"id":"${f.string("id")}","action":"sub","code":200,"topic":"${f.string("topic")}"}""")
                }
            }
        }
        val c = client(server)
        val events = CopyOnWriteArrayList<ImEvent>()
        scope.launch { c.events.collect { events += it } }
        c.start()
        c.awaitConnected(2_000)
        val reply = c.subscribe("uid_gbY32PzSxto")
        assertEquals("uid_gbY32PzSxto", reply.frame.string("topic"))
        eventually { events.any { it is ImEvent.TopicCleared } }
        val tc = events.filterIsInstance<ImEvent.TopicCleared>().single()
        assertEquals("1", tc.offlineId)
    }

    /**
     * From the source's ImResponseCorrelationTest: a reply that comes after its
     * request gave up goes to nobody, not to the next request of the same
     * action; and a reply of one action never completes a request of a longer
     * one (get vs get_members).
     */
    @Test
    fun aLateReplyIsNotHandedToTheNextRequest() = runBlocking {
        val held = CopyOnWriteArrayList<String>()
        val server = FakeServer { c, f ->
            val id = f.string("id")
            when (f.string("action")) {
                "login" -> c.push(loginOk(me, protocol = 1))
                "get" -> if (held.isEmpty()) {
                    held += id!!
                } else {
                    c.push(jsonObject { put("id", held[0]); put("action", "get"); put("code", 200); put("topic", "late") })
                    c.push(jsonObject { put("id", id); put("action", "get"); put("code", 200); put("topic", f.string("topic")) })
                }
                "get_members" -> {
                    c.push(jsonObject { put("id", id); put("action", "get"); put("code", 200); put("topic", "wrong") })
                    c.push(jsonObject { put("id", id); put("action", "get_members"); put("code", 200); put("topic", f.string("topic")) })
                }
            }
        }
        val c = client(server)
        c.start()
        c.awaitConnected(2_000)
        try {
            c.request(jsonObject { put("action", "get"); put("topic", "first") }, timeoutMs = 200)
            fail("the first get has no reply in time")
        } catch (e: ImException.Timeout) {
            assertTrue(e.connectionAlive)
        }
        assertEquals("second", c.request(jsonObject { put("action", "get"); put("topic", "second") }).frame.string("topic"))
        assertEquals("grp_x", c.request(jsonObject { put("action", "get_members"); put("topic", "grp_x") }).frame.string("topic"))
        c.stop()
    }

    @Test
    fun legacyPresenceSnapshotWithoutIdIsMatched() = runBlocking {
        val server = FakeServer { c, f ->
            when (f.string("action")) {
                "login" -> c.push(loginOk(me)) // no protocol: a legacy server
                "pres" -> c.push("""{"action":"pres","code":200,"what":"snapshot","states":[{"uid":"uid_gbY32PzSxto","online":true,"presRev":4}]}""")
            }
        }
        val c = client(server)
        c.start()
        c.awaitConnected(2_000)
        val states = c.queryPresence(listOf("uid_gbY32PzSxto"))
        assertEquals(listOf(PresenceSnapshot("uid_gbY32PzSxto", true, 4, 0)), states)
    }

    @Test
    fun strictPresenceSnapshotNeedsItsId() = runBlocking {
        val server = FakeServer { c, f ->
            when (f.string("action")) {
                "login" -> c.push(loginOk(me, protocol = 1))
                "pres" -> c.push("""{"action":"pres","code":200,"what":"snapshot","states":[]}""")
            }
        }
        val c = client(server)
        c.start()
        c.awaitConnected(2_000)
        try {
            c.queryPresence(emptyList())
            fail("an id-less snapshot must not be taken as the reply on a strict server")
        } catch (e: ImException.Timeout) {
            assertTrue(e.connectionAlive)
        }
    }

    @Test
    fun typedRepliesAreMatchedByReqId() = runBlocking {
        val server = FakeServer { c, f ->
            when (f.string("action")) {
                "login" -> c.push(loginOk(me))
                "forward_bundle" -> {
                    c.push("""{"type":"forward_bundle_result","reqId":"someone-else","ok":false,"reason":"not_member"}""")
                    c.push("""{"type":"forward_bundle_result","reqId":"${f.string("reqId")}","ok":true,"insertedSeqs":[7,8]}""")
                }
            }
        }
        val c = client(server)
        c.start()
        c.awaitConnected(2_000)
        val r = c.forwardBundle("grp_a", listOf(1, 2), "grp_b")
        assertEquals(ForwardBundleResult(true, listOf(7, 8), null), r)
    }

    @Test
    fun historyRefusesAPageForAnotherTopic() = runBlocking {
        val server = FakeServer { c, f ->
            when (f.string("action")) {
                "login" -> c.push(loginOk(me))
                "get" -> c.push("""{"id":"${f.string("id")}","action":"get","code":200,"topic":"uid_someoneelse","messages":[{"topic":"p2p_x","seq":1,"from":"uid_y","content":"x","ts":1}]}""")
            }
        }
        val c = client(server)
        c.start()
        c.awaitConnected(2_000)
        try {
            c.history("uid_gbY32PzSxto")
            fail("a page for another topic was accepted")
        } catch (e: ImException.BadReply) {
        }
    }

    @Test
    fun publishOutcomes() = runBlocking {
        val server = FakeServer { c, f ->
            when (f.string("action")) {
                "login" -> c.push(loginOk(me))
                "pub" -> when (f.string("content")) {
                    "ok" -> c.push("""{"id":"${f.string("id")}","action":"pub","code":200,"topic":"t","seq":5,"ts":99}""")
                    "long" -> c.push("""{"id":"${f.string("id")}","action":"pub","code":400,"reason":"message too long","errCode":"message_too_long"}""")
                    else -> Unit // no reply
                }
            }
        }
        val c = client(server)
        c.start()
        c.awaitConnected(2_000)
        assertEquals(PublishOutcome.Stored(5, 99), c.publish("uid_gbY32PzSxto", "ok", "text", "m1"))
        assertEquals(PublishOutcome.Refused(400, "message_too_long", "message too long"), c.publish("uid_gbY32PzSxto", "long", "text", "m2"))
        assertEquals(PublishOutcome.Pending, c.publish("uid_gbY32PzSxto", "silence", "text", "m3", timeoutMs = 200))
        c.stop()
        assertEquals(PublishOutcome.Lost, c.publish("uid_gbY32PzSxto", "ok", "text", "m4"))
    }

    @Test
    fun stopEndsEverything() = runBlocking {
        val server = friendlyServer()
        val c = client(server)
        c.start()
        c.awaitConnected(2_000)
        c.stop()
        assertEquals(ConnectionState.Stopped(StopReason.STOPPED), c.state.value)
        Thread.sleep(100)
        assertEquals("no reconnect after stop", 1, server.connections.size)
    }
}
