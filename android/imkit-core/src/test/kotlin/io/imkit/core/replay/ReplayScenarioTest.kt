package io.imkit.core.replay

import io.imkit.core.ImIds
import io.imkit.core.client.ConnectionState
import io.imkit.core.client.ImClient
import io.imkit.core.client.ImClientConfig
import io.imkit.core.client.ImEvent
import io.imkit.core.client.PublishOutcome
import io.imkit.core.client.TokenProvider
import io.imkit.core.client.createGroup
import io.imkit.core.client.history
import io.imkit.core.client.kick
import io.imkit.core.client.publish
import io.imkit.core.client.queryPresence
import io.imkit.core.client.setMemberRole
import io.imkit.core.client.subscribe
import io.imkit.core.engine.ChatEngine
import io.imkit.core.engine.ChatEvent
import io.imkit.core.engine.EngineConfig
import io.imkit.core.engine.OutgoingMessage
import io.imkit.core.model.ConversationId
import io.imkit.core.model.GroupRole
import io.imkit.core.model.MessageStatus
import io.imkit.core.model.PresenceState
import io.imkit.core.json.jsonObject
import io.imkit.core.store.ChatStore
import io.imkit.core.store.ClearState
import io.imkit.core.store.InMemoryChatStore
import io.imkit.core.testing.RecordingTransport
import io.imkit.core.testing.Replay
import io.imkit.core.transport.ImTransport
import io.imkit.core.transport.OkHttpTransport
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.util.concurrent.CopyOnWriteArrayList

/**
 * The client against the golden transcripts: `replay` plays the server and
 * fails unless the client sends exactly the scripted requests. Each test
 * covers one of the client red lines (C1–C9 in contract/client/redlines.json)
 * that a fake server could get wrong in the same way as the client.
 *
 * Scenario user X is app user "conf-X" (tools/conformance/harness), so its uid
 * is ImIds.uid("conf-X"). The transcripts come from the legacy-profile
 * reference server: the login reply has no `protocol`, and presence snapshots
 * have no `id`.
 */
class ReplayScenarioTest {
    private val scope = CoroutineScope(Dispatchers.Default + SupervisorJob())
    private val clients = CopyOnWriteArrayList<ImClient>()

    @After
    fun tearDown() {
        clients.forEach { it.stop() }
        scope.cancel()
    }

    private fun client(
        replay: Replay,
        user: String,
        transport: ImTransport = OkHttpTransport(),
        requestTimeoutMs: Long = 5_000,
        publishTimeoutMs: Long = 20_000,
        reconnectDelayMs: Long = 100,
    ) = ImClient(
        ImClientConfig(
            endpoints = listOf(replay.url),
            tokenProvider = TokenProvider { "token-of-$user" }, // the replay does not check tokens
            transport = transport,
            requestTimeoutMs = requestTimeoutMs,
            publishTimeoutMs = publishTimeoutMs,
            reconnectBaseDelayMs = reconnectDelayMs,
            reconnectMaxDelayMs = reconnectDelayMs * 2,
        ),
        scope,
    ).also { clients += it }

    private fun engine(client: ImClient, user: String, pageSize: Int = 50, store: ChatStore = InMemoryChatStore()) = ChatEngine(
        client,
        store,
        "conf-$user",
        scope,
        // No automatic sync: the transcript scripts every request.
        EngineConfig(autoSync = false, textContentType = "text/plain", pageSize = pageSize),
    )

    private suspend fun until(timeoutMs: Long = 10_000, what: String = "condition", check: () -> Boolean) {
        try {
            withTimeout(timeoutMs) { while (!check()) delay(10) }
        } catch (e: kotlinx.coroutines.TimeoutCancellationException) {
            throw AssertionError("timed out waiting for $what")
        }
    }

    /** Runs [block] against a replay, then requires the replay to pass; failures carry its output. */
    private fun replaying(scenario: String, user: String, faults: String? = null, vararg extra: String, block: suspend (Replay) -> Unit) =
        runBlocking {
            Replay.start(scenario, user, faults, *extra).use { replay ->
                try {
                    block(replay)
                } catch (e: Throwable) {
                    throw AssertionError("$e\n--- replay output so far ---\n${replay.output}", e)
                }
                replay.assertPassed()
            }
        }

    private fun ChatEngine.seqs(c: ConversationId) = messages(c).value.mapNotNull { it.seq }

    @Test(timeout = 60_000)
    fun bindingsUseTheHarnessUserIds() {
        val b = Replay.bindings("core/p2p")
        assertEquals(ImIds.uid("conf-A"), b["uid:A"])
        assertEquals(ImIds.p2pTopic(ImIds.uid("conf-A"), ImIds.uid("conf-B")), b["p2p:A,B"])
    }

    /**
     * R19: the legacy server answers the handshake login and the presence
     * snapshot without an id, and the client takes them by action; every
     * other reply goes by id, so the refused second login (step 3, with an
     * id) reaches the request that sent it.
     */
    @Test(timeout = 60_000)
    fun R19_CorrelateLegacyRepliesByAction() {
        val uidA = Replay.bindings("core/login").getValue("uid:A")
        replaying("core/login", "A") { replay ->
            // Superseded at step 5 by a plain close; it must not come back within the test.
            val first = client(replay, "A", reconnectDelayMs = 60_000)
            first.start()
            val session = first.awaitConnected(10_000) // step 2: no id
            assertEquals(uidA, session?.uid)
            assertEquals(0, session?.protocol)

            val again = first.request(jsonObject { put("action", "login"); put("token", "token-of-A") }) // step 3
            assertEquals(400, again.code)
            assertEquals("still logged in", uidA, first.session?.uid)

            val second = client(replay, "A")
            second.start()
            assertEquals(uidA, second.awaitConnected(10_000)?.uid) // steps 4-5
            until(what = "the first connection closed") { first.state.value !is ConnectionState.Connected }

            assertTrue("the snapshot lists nobody", second.queryPresence(emptyList()).isEmpty()) // step 6: no id
        }
    }

    /**
     * R7: B is offline while A clears the chat. On B's next login the pending
     * clear comes after the login reply; B empties the conversation and acks
     * the clear by its id. The next login brings nothing.
     */
    @Test(timeout = 60_000)
    fun R7_ClearOfflineReplay() {
        val withA = ConversationId(Replay.bindings("core/clear-offline").getValue("uid:A"))
        replaying("core/clear-offline", "B") { replay ->
            val transport = RecordingTransport(OkHttpTransport())
            val client = client(replay, "B", transport)
            val store = InMemoryChatStore()
            val engine = engine(client, "B", store = store)
            val cleared = CopyOnWriteArrayList<ChatEvent.ConversationCleared>()
            scope.launch(start = CoroutineStart.UNDISPATCHED) {
                engine.events.collect { if (it is ChatEvent.ConversationCleared) cleared += it }
            }
            engine.open()
            val window = engine.messages(withA)
            client.start()
            assertNotNull(client.awaitConnected(10_000))
            until(what = "the message") { window.value.map { it.content } == listOf("secret") } // step 6

            client.stop() // step 7: B goes offline; A clears (steps 8-9)
            client.start() // steps 10-11
            until(what = "the pending clear") { cleared.isNotEmpty() }
            assertTrue(window.value.isEmpty())
            assertEquals(ClearState(clearVersion = 1, clearBeforeSeq = 1), store.clearState(withA))
            until(what = "the ack") { transport.sentActions("ack_topic_cleared").isNotEmpty() } // step 12
            assertEquals("1", transport.sentActions("ack_topic_cleared").single().string("id"))

            client.stop() // step 13
            client.start() // steps 14-15
            assertNotNull(client.awaitConnected(10_000))
            delay(500)
            assertEquals("not delivered again", 1, cleared.size)
            assertEquals(1, transport.sentActions("ack_topic_cleared").size)
        }
    }

    /** C1: an injected disconnect is noticed, the client logs in again by itself and carries on. C5: presence follows pushes. */
    @Test(timeout = 60_000)
    fun C1_ReconnectAfterInjectedDisconnect() {
        val b = Replay.bindings("core/p2p")
        val uidA = b.getValue("uid:A")
        val uidB = b.getValue("uid:B")
        replaying("core/p2p", "A", """[{"kind":"disconnect","step":9}]""") { replay ->
            val client = client(replay, "A")
            val engine = engine(client, "A")
            val log = CopyOnWriteArrayList<ImEvent>()
            scope.launch(start = CoroutineStart.UNDISPATCHED) { client.events.collect { log += it } }
            engine.open()
            client.start()
            val first = client.awaitConnected(10_000)
            assertEquals(uidA, first?.uid)
            assertEquals("legacy login reply carries no protocol", 0, first?.protocol)

            assertEquals(200, client.subscribe(uidB).code) // step 7
            until(what = "B online") { engine.presence.state(uidB) == PresenceState.ONLINE }

            // Before step 9 the replay closes the socket and waits for a new login.
            until(what = "second login") { log.count { it is ImEvent.Connected } == 2 }
            assertTrue(log.any { it is ImEvent.Disconnected && it.willReconnect })

            val stored = client.publish(uidB, "hey", "text/plain", clientMsgId = "m-1") // step 9
            assertEquals(1L, (stored as PublishOutcome.Stored).seq)

            // Step 11: the legacy snapshot has no id and is matched by action.
            assertTrue(client.queryPresence(emptyList()).single { it.uid == uidB }.online)

            until(what = "B offline") { engine.presence.state(uidB) == PresenceState.OFFLINE } // step 14
            val last = client.queryPresence(listOf(uidB)).single() // step 15
            assertFalse(last.online)
            assertTrue(last.lastSeenAt > 0)
        }
    }

    /** C1: every frame duplicated, replies put last: duplicates of replies, logins and pushes are ignored. */
    @Test(timeout = 60_000)
    fun C1_IgnoresDuplicatedFrames() {
        val b = Replay.bindings("core/p2p")
        val uidB = b.getValue("uid:B")
        replaying("core/p2p", "A", """[{"kind":"duplicate","step":0}]""", "-order", "last") { replay ->
            val client = client(replay, "A")
            val engine = engine(client, "A")
            engine.open()
            client.start()
            assertNotNull(client.awaitConnected(10_000))
            assertEquals(200, client.subscribe(uidB).code)
            assertEquals(1L, (client.publish(uidB, "hey", "text/plain", clientMsgId = "m-1") as PublishOutcome.Stored).seq)
            assertTrue(client.queryPresence(emptyList()).single { it.uid == uidB }.online)
            until(what = "B offline") { engine.presence.state(uidB) == PresenceState.OFFLINE }
            assertFalse(client.queryPresence(listOf(uidB)).single().online)
            assertTrue("still the first connection", client.state.value is ConnectionState.Connected)
        }
    }

    /**
     * C2: the reply to a publish is lost while the connection stays up. The
     * message stays "sending" and is not sent again on its own; a retry uses
     * the same clientMsgId (the replay checks the mapping) and the server's
     * de-duplication answers with the same seq.
     */
    @Test(timeout = 60_000)
    fun C2_LostPublishReplyStaysSending() {
        val b = Replay.bindings("core/group-messaging")
        val uidB = b.getValue("uid:B")
        replaying("core/group-messaging", "A", """[{"kind":"drop","step":8}]""") { replay ->
            val transport = RecordingTransport(OkHttpTransport())
            val client = client(replay, "A", transport, publishTimeoutMs = 1_000)
            val engine = engine(client, "A")
            engine.open()
            client.start()
            assertNotNull(client.awaitConnected(10_000))

            val topic = client.createGroup("team", listOf(uidB)) // step 5
            assertEquals(b["grp"], topic)
            val group = ConversationId(topic)
            val window = engine.messages(group) // open it before anything is stored
            assertEquals(200, engine.subscribe(group).code) // step 6

            engine.send(group, OutgoingMessage("hello")) // step 8; the reply is dropped
            delay(2_500) // the publish timeout passes, then some more
            assertEquals("no resend on a live connection", 1, transport.sentActions("pub").size)
            val pending = window.value.single()
            assertEquals(MessageStatus.SENDING, pending.status)
            assertNull(pending.seq)

            engine.retry(pending) // step 9
            until(what = "stored as seq 1") { window.value.any { it.seq == 1L && it.status == MessageStatus.SENT } }
            val ids = transport.sentActions("pub").map { it.string("clientMsgId") }
            assertEquals(2, ids.size)
            assertEquals(pending.localId, ids[0])
            assertEquals(ids[0], ids[1])

            until(what = "B's message") { engine.seqs(group) == listOf(1L, 2L) } // step 10
            assertTrue(engine.sendTyping(group)) // step 12
            assertEquals(listOf(1L, 2L), client.history(topic, limit = 20).messages.map { it.seq }) // step 13
            assertEquals("team", engine.refreshGroups()!!.single().name) // step 14
            assertEquals(GroupRole.OWNER, engine.myRole(topic)) // step 15
            assertEquals("stored once", 2, window.value.size)
        }
    }

    /**
     * C3: a message seen live after a gap must not move the resume point:
     * with 1 and 3 held, the pull asks for everything after 1 (the scripted
     * request), then fills in 2.
     */
    @Test(timeout = 60_000)
    fun C3_ResumeFromContiguousEnd() {
        val b = Replay.bindings("core/resubscribe")
        val uidA = b.getValue("uid:A")
        val group = ConversationId(b.getValue("grp"))
        replaying("core/resubscribe", "B") { replay ->
            val client = client(replay, "B")
            val engine = engine(client, "B", pageSize = 10)
            engine.open()
            client.start()
            assertNotNull(client.awaitConnected(10_000))
            until(what = "g1") { engine.seqs(group) == listOf(1L) } // steps 5-6

            client.stop() // step 7
            client.start() // steps 8-9: a new connection
            withTimeout(10_000) { client.state.first { it is ConnectionState.Connected } }
            until(what = "p1") { engine.messages(ConversationId(uidA)).value.map { it.content } == listOf("p1") } // step 11

            assertEquals(200, engine.subscribe(group).code) // step 12
            until(what = "g3 live") { engine.seqs(group) == listOf(1L, 3L) } // step 13: g2 was missed
            engine.syncConversation(group) // step 14: afterSeq 1, not 3
            assertEquals(listOf(1L, 2L, 3L), engine.seqs(group))
        }
    }

    /** C9: the role comes from the roster; grp_sync marks it stale, so the next look fetches it again. */
    @Test(timeout = 60_000)
    fun C9_RoleFromRoster() {
        val b = Replay.bindings("core/group-guards")
        val (uidA, uidB, uidC, uidD) = listOf("A", "B", "C", "D").map { b.getValue("uid:$it") }
        replaying("core/group-guards", "A") { replay ->
            val client = client(replay, "A")
            val engine = engine(client, "A")
            engine.open()
            client.start()
            assertNotNull(client.awaitConnected(10_000))

            val topic = client.createGroup("guards", listOf(uidB, uidC, uidD)) // step 9
            assertEquals(200, client.setMemberRole(topic, uidB, GroupRole.ADMIN).code) // step 10
            assertEquals(200, client.setMemberRole(topic, uidC, GroupRole.ADMIN).code) // step 11
            assertEquals(400, client.kick(topic, uidA).code) // step 12: not even the owner kicks himself
            until(what = "two grp_sync") { engine.rosterEpoch(topic) == 2L }

            assertEquals(GroupRole.OWNER, engine.myRole(topic)) // step 18: first fetch
            assertEquals(
                mapOf(uidA to GroupRole.OWNER, uidB to GroupRole.ADMIN, uidC to GroupRole.ADMIN, uidD to GroupRole.MEMBER),
                engine.members(topic)!!.associate { it.uid to it.role }, // cached: no request
            )

            assertEquals(200, client.kick(topic, uidC).code) // step 19, with a grp_sync
            until(what = "grp_sync after the kick") { engine.rosterEpoch(topic) == 3L }
            assertEquals(true, engine.rosterStale(topic))
            assertEquals(GroupRole.OWNER, engine.myRole(topic)) // step 20: fetched again
            assertEquals(setOf(uidA, uidB, uidD), engine.members(topic)!!.map { it.uid }.toSet())
        }
    }

    /** C9: when the roster cannot be fetched, the user is treated as a plain member. */
    @Test(timeout = 60_000)
    fun C9_UnknownRosterMeansMember() {
        val b = Replay.bindings("core/group-guards")
        val (uidA, uidB, uidC, uidD) = listOf("A", "B", "C", "D").map { b.getValue("uid:$it") }
        replaying("core/group-guards", "A", """[{"kind":"drop","step":18}]""") { replay ->
            val client = client(replay, "A", requestTimeoutMs = 1_000)
            val engine = engine(client, "A")
            engine.open()
            client.start()
            assertNotNull(client.awaitConnected(10_000))

            val topic = client.createGroup("guards", listOf(uidB, uidC, uidD))
            client.setMemberRole(topic, uidB, GroupRole.ADMIN)
            client.setMemberRole(topic, uidC, GroupRole.ADMIN)
            client.kick(topic, uidA)
            until(what = "two grp_sync") { engine.rosterEpoch(topic) == 2L }

            assertEquals("no roster: fail closed", GroupRole.MEMBER, engine.myRole(topic)) // step 18, reply dropped
            assertNull(engine.rosterStale(topic))
            assertEquals(200, client.kick(topic, uidC).code) // step 19
            assertEquals(GroupRole.OWNER, engine.myRole(topic)) // step 20
        }
    }
}
