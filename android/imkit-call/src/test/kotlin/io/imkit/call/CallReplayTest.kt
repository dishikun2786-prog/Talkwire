package io.imkit.call

import io.imkit.core.client.ImClient
import io.imkit.core.client.ImClientConfig
import io.imkit.core.client.TokenProvider
import io.imkit.core.testing.Replay
import io.imkit.core.transport.OkHttpTransport
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.util.concurrent.CopyOnWriteArrayList

/**
 * The call manager against the golden transcript `module/call`: the replay
 * plays the server and fails unless the client sends exactly the scripted
 * requests. A is the caller, B the callee; the step numbers are the
 * transcript's. Requests the manager would never send (a second answer, a
 * hang-up after the end) go through its signaling directly. Skipped when the
 * replay binary is not built.
 */
class CallReplayTest {
    private val scope = CoroutineScope(Dispatchers.Default + SupervisorJob())
    private val clients = CopyOnWriteArrayList<ImClient>()

    @After
    fun tearDown() {
        clients.forEach { it.stop() }
        scope.cancel()
    }

    private suspend fun connect(replay: Replay, user: String, me: String): Pair<CallManager, NoMediaEngine> {
        val client = ImClient(
            ImClientConfig(
                endpoints = listOf(replay.url),
                tokenProvider = TokenProvider { "token-of-$user" },
                transport = OkHttpTransport(),
                requestTimeoutMs = 5_000,
            ),
            scope,
        ).also { clients += it }
        val media = NoMediaEngine()
        val manager = CallManager(client, me, media, scope, endedHoldMs = 20)
        manager.start()
        client.start()
        assertNotNull("not connected", client.awaitConnected(10_000))
        return manager to media
    }

    private suspend fun until(what: String, check: () -> Boolean) {
        try {
            withTimeout(10_000) { while (!check()) delay(10) }
        } catch (e: kotlinx.coroutines.TimeoutCancellationException) {
            throw AssertionError("timed out waiting for $what")
        }
    }

    private fun replaying(scenario: String, user: String, block: suspend (Replay) -> Unit) = runBlocking {
        Replay.start(scenario, user).use { replay ->
            try {
                block(replay)
            } catch (e: Throwable) {
                throw AssertionError("$e\n--- replay output so far ---\n${replay.output}", e)
            }
            replay.assertPassed()
        }
    }

    /** The media log without the loudspeaker routing, which the transcript does not see. */
    private fun NoMediaEngine.calls() = log.filterNot { it.startsWith("speaker") }

    @Test
    fun theCaller() {
        val b = Replay.bindings("module/call")
        val (cs1, cs2, cs3) = listOf(b.getValue("cs1"), b.getValue("cs2"), b.getValue("cs3"))
        replaying("module/call", "A") { replay ->
            val (manager, media) = connect(replay, "A", b.getValue("uid:A"))
            assertEquals(InviteResult.Refused(409, "offline", null), manager.invite(b.getValue("uid:D"), CallType.VOICE)) // 7
            val notFriends = manager.invite(b.getValue("uid:C"), CallType.VOICE) as InviteResult.Refused // 8
            assertEquals(401 to "not_friends", notFriends.code to notFriends.reason)
            assertEquals(400, (manager.invite(b.getValue("uid:A"), CallType.VOICE) as InviteResult.Refused).code) // 9
            assertEquals(400, manager.signaling.invite(b.getValue("uid:B"), "fax").code) // 10
            assertEquals(InviteResult.Ok(cs1), manager.invite(b.getValue("uid:B"), CallType.VOICE, clientCallId = "cc-1")) // 11

            until("answered") { (manager.state.value as? CallState.Active)?.connecting == false } // 13
            assertEquals(listOf("join voice fake-channel"), media.calls())
            val active = manager.state.value as CallState.Active
            assertEquals(cs1, active.callSessionId)
            assertEquals(b.getValue("uid:B"), active.peer)

            assertTrue(manager.requestVideo()) // 15
            until("declined") { (manager.state.value as? CallState.Active)?.upgrade == Upgrade.DECLINED } // 16
            assertTrue(manager.requestVideo()) // 17
            until("video") { (manager.state.value as? CallState.Active)?.callType == CallType.VIDEO } // 18
            until("camera on") { "video true" in media.log }
            assertEquals(409, manager.signaling.upgrade(cs1).code) // 19: already video
            manager.hangup() // 20
            assertEquals(CallEndReason.USER_HANGUP, (manager.state.value as CallState.Ended).reason)

            assertEquals(InviteResult.Ok(cs2), manager.invite(b.getValue("uid:B"), CallType.VIDEO)) // 22
            manager.cancel() // 23
            assertEquals(InviteResult.Ok(cs3), manager.invite(b.getValue("uid:B"), CallType.VIDEO)) // 24
            until("rejected") { (manager.state.value as? CallState.Ended)?.reason == CallEndReason.REJECTED } // 26
        }
    }

    @Test
    fun theCallee() {
        val b = Replay.bindings("module/call")
        val (cs1, cs2, cs3) = listOf(b.getValue("cs1"), b.getValue("cs2"), b.getValue("cs3"))
        replaying("module/call", "B") { replay ->
            val (manager, media) = connect(replay, "B", b.getValue("uid:B"))
            // Pushes arrive faster than a test can look: rings and ends from the start.
            val seen = CopyOnWriteArrayList<String>()
            scope.launch(start = CoroutineStart.UNDISPATCHED) {
                manager.signaling.signals.collect { s ->
                    when (s) {
                        is CallSignal.Ring -> seen += "ring ${s.callSessionId}"
                        is CallSignal.Ended -> seen += "ended ${s.reason}"
                        else -> Unit
                    }
                }
            }
            until("ringing") { manager.state.value is CallState.Incoming } // 11
            val ring = manager.state.value as CallState.Incoming
            assertEquals(cs1, ring.callSessionId)
            assertEquals(CallType.VOICE, ring.callType)
            assertEquals("Alice", ring.callerName)
            assertEquals("https://example.test/a.png", ring.callerAvatar)
            assertEquals(45, ring.ringTimeoutSeconds)
            assertTrue(manager.accept()) // 13
            assertEquals(listOf("join voice fake-channel"), media.calls())
            assertEquals(409, manager.signaling.accept(cs1).code) // 14: already answered

            until("asked for video") { (manager.state.value as? CallState.Active)?.upgrade == Upgrade.THEIRS } // 15
            manager.declineVideo() // 16
            until("asked again") { (manager.state.value as? CallState.Active)?.upgrade == Upgrade.THEIRS } // 17
            manager.acceptVideo() // 18
            // The replay sends A's hang-up (20) right behind call_upgraded: the camera went on in between.
            until("camera on") { "video true" in media.log }

            until("hung up") { (manager.state.value as? CallState.Ended)?.reason == CallEndReason.USER_HANGUP } // 20
            until("left") { media.log.lastOrNull() == "leave" }
            assertTrue(manager.signaling.hangup(cs1).code >= 400) // 21: already ended

            // 22–24 arrive back to back: a ring, its cancel, another ring.
            until("rings a third time") { (manager.state.value as? CallState.Incoming)?.callSessionId == cs3 } // 24
            val after = seen.drop(seen.indexOf("ended user_hangup") + 1)
            assertEquals(listOf("ring $cs2", "ended cancelled", "ring $cs3"), after)
            manager.reject() // 26
            assertEquals(404, manager.signaling.accept("nonexistent-session").code) // 27
        }
    }
}
