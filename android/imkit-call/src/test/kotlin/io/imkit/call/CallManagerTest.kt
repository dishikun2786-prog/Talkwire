package io.imkit.call

import io.imkit.core.ImIds
import io.imkit.core.client.ImClient
import io.imkit.core.client.ImClientConfig
import io.imkit.core.client.TokenProvider
import io.imkit.core.json.JsonObject
import io.imkit.core.json.JsonObjectBuilder
import io.imkit.core.json.jsonObject
import io.imkit.core.testing.FakeServer
import io.imkit.core.testing.eventually
import io.imkit.core.testing.loginOk
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.async
import kotlinx.coroutines.cancel
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.runBlocking
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import java.util.concurrent.CopyOnWriteArrayList
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import kotlin.random.Random

/** The call state against a scripted server, the cases of the web module's tests. */
class CallManagerTest {
    private val scope = CoroutineScope(Dispatchers.Default + SupervisorJob())
    private val later = Executors.newSingleThreadScheduledExecutor()

    @After
    fun tearDown() {
        scope.cancel()
        later.shutdownNow()
    }

    private val alice = ImIds.uid("alice")
    private val bob = ImIds.uid("bob")

    private class FakeMedia : CallMediaEngine {
        val log = CopyOnWriteArrayList<String>()
        val joins = CopyOnWriteArrayList<MediaJoin>()
        var failJoin = false
        val broke = MutableSharedFlow<Unit>(extraBufferCapacity = 1)

        override suspend fun join(params: MediaJoin) {
            joins += params
            log += "join"
            if (failJoin) error("no camera")
        }

        override suspend fun setVideo(on: Boolean, videoQuality: String?) {
            log += "video $on ${videoQuality ?: ""}".trim()
        }

        override fun setMuted(muted: Boolean) {
            log += "muted $muted"
        }

        override fun setSpeakerOn(on: Boolean) {
            log += "speaker $on"
        }

        override suspend fun leave() {
            log += "leave"
        }

        override val failures: Flow<Unit> get() = broke
    }

    private fun reply(f: JsonObject, extra: JsonObjectBuilder.() -> Unit = {}) = jsonObject {
        put("id", f.string("id"))
        put("action", f.string("action"))
        put("code", 200)
        extra()
    }

    private fun JsonObjectBuilder.creds(who: String) {
        put("channelId", "ch-1")
        put("rtcToken", "rtc-$who")
        put("appId", "app")
        put("videoQuality", "sd")
        put("denoiseEnabled", true)
    }

    private fun after(ms: Long, block: () -> Unit) {
        later.schedule(block, ms, TimeUnit.MILLISECONDS)
    }

    private fun setup(server: FakeServer, user: String = "alice", media: FakeMedia = FakeMedia()): Pair<CallManager, FakeMedia> = runBlocking {
        val uid = ImIds.uid(user)
        val handle = server.onFrame
        server.onFrame = { c, f -> if (f.string("action") == "login") c.push(loginOk(uid)) else handle(c, f) }
        val client = ImClient(
            ImClientConfig(endpoints = listOf("ws://fake"), tokenProvider = TokenProvider { user }, transport = server, requestTimeoutMs = 2_000, random = Random(1)),
            scope,
        )
        val manager = CallManager(client, uid, media, scope, endedHoldMs = 50)
        manager.start()
        client.start()
        client.awaitConnected(5_000)
        manager to media
    }

    private fun sent(server: FakeServer, action: String): List<JsonObject> =
        server.sent.filter { it.string("action") == action }.map { f -> JsonObject(f.fields.filterKeys { it != "id" }) }

    private fun frame(build: JsonObjectBuilder.() -> Unit) = jsonObject(build)

    @Test
    fun callerInvitesIsAnsweredWithItsOwnCredentialsAndHangsUp() {
        val server = FakeServer { c, f ->
            when (f.string("action")) {
                "call_invite" -> {
                    c.push(reply(f) { put("topic", f.string("topic")); put("callSessionId", "cs-1") })
                    after(20) { c.push(jsonObject { put("type", "call_answered"); put("callSessionId", "cs-1"); creds("caller") }) }
                }
                "call_hangup" -> c.push(reply(f) { put("callSessionId", f.string("callSessionId")) })
            }
        }
        val (manager, media) = setup(server)
        assertEquals(InviteResult.Ok("cs-1"), runBlocking { manager.invite(bob, CallType.VOICE) })
        eventually { (manager.state.value as? CallState.Active)?.connecting == false }
        assertEquals(listOf(MediaJoin(alice, CallType.VOICE, RtcCredentials("ch-1", "rtc-caller", "app", "sd", true))), media.joins.toList())
        val active = manager.state.value as CallState.Active
        assertEquals("cs-1", active.callSessionId)
        assertEquals(bob, active.peer)
        assertFalse(active.speakerOn) // a voice call starts at the ear
        manager.setMuted(true)
        assertTrue((manager.state.value as CallState.Active).muted)
        runBlocking { manager.hangup() }
        assertEquals(CallEndReason.USER_HANGUP, (manager.state.value as CallState.Ended).reason)
        eventually { manager.state.value is CallState.Idle }
        assertEquals(listOf("join", "speaker false", "muted true", "leave"), media.log.toList())
        assertEquals(listOf(frame { put("action", "call_invite"); put("topic", bob); put("callType", "voice") }), sent(server, "call_invite"))
        assertEquals(listOf(frame { put("action", "call_hangup"); put("callSessionId", "cs-1") }), sent(server, "call_hangup"))
    }

    @Test
    fun calleeAnswersAsVoiceAndTheOtherSideEndsWithAnUnknownReason() {
        val server = FakeServer { c, f ->
            if (f.string("action") == "call_accept") c.push(reply(f) { put("callSessionId", f.string("callSessionId")); creds("callee") })
        }
        val (manager, media) = setup(server, user = "bob")
        val conn = server.connections.last()
        conn.push(jsonObject { put("type", "call_ring"); put("callSessionId", "cs-2"); put("callType", "video"); put("callerName", "Alice"); put("ringTimeoutSeconds", 45) })
        eventually { manager.state.value is CallState.Incoming }
        val ring = manager.state.value as CallState.Incoming
        assertEquals(CallType.VIDEO, ring.callType)
        assertEquals("Alice", ring.callerName)
        assertTrue(runBlocking { manager.accept(asVoice = true) })
        assertEquals(listOf(frame { put("action", "call_accept"); put("callSessionId", "cs-2"); put("acceptAsVoice", true) }), sent(server, "call_accept"))
        assertEquals(MediaJoin(bob, CallType.VOICE, RtcCredentials("ch-1", "rtc-callee", "app", "sd", true)), media.joins[0])
        val active = manager.state.value as CallState.Active
        assertEquals(CallType.VOICE, active.callType)
        assertEquals("Alice", active.callerName)
        assertFalse(active.connecting)
        // A push for someone else's call changes nothing.
        conn.push(jsonObject { put("type", "call_ended"); put("callSessionId", "cs-other"); put("reason", "user_hangup") })
        conn.push(jsonObject { put("type", "call_ended"); put("callSessionId", "cs-2"); put("reason", "host_said_so") })
        eventually { manager.state.value is CallState.Ended }
        assertEquals("host_said_so", (manager.state.value as CallState.Ended).reason)
        eventually { media.log.lastOrNull() == "leave" }
    }

    @Test
    fun keepsPushesThatBeatTheInvitesReply() {
        val server = FakeServer { c, f ->
            if (f.string("action") == "call_invite") {
                // The callee answered before this client heard its own call's id (F49).
                c.push(jsonObject { put("type", "call_answered"); put("callSessionId", "cs-3"); creds("caller") })
                after(20) { c.push(reply(f) { put("topic", f.string("topic")); put("callSessionId", "cs-3") }) }
            }
        }
        val (manager, media) = setup(server)
        assertTrue(runBlocking { manager.invite(bob, CallType.VOICE) } is InviteResult.Ok)
        eventually { manager.state.value is CallState.Active }
        eventually { media.joins.size == 1 }
    }

    @Test
    fun cancelsACallCancelledBeforeItsIdCameBack() {
        val replies = Channel<() -> Unit>(1)
        val server = FakeServer { c, f ->
            when (f.string("action")) {
                "call_invite" -> replies.trySend { c.push(reply(f) { put("topic", f.string("topic")); put("callSessionId", "cs-4") }) }
                "call_cancel" -> c.push(reply(f) { put("callSessionId", f.string("callSessionId")) })
            }
        }
        val (manager, _) = setup(server)
        runBlocking {
            val invited = async { manager.invite(bob, CallType.VIDEO) }
            val answer = replies.receive()
            manager.cancel()
            assertEquals(CallEndReason.CANCELLED, (manager.state.value as CallState.Ended).reason)
            answer()
            assertEquals(InviteResult.Ok("cs-4"), invited.await())
        }
        eventually { sent(server, "call_cancel").size == 1 }
        assertEquals(listOf(frame { put("action", "call_cancel"); put("callSessionId", "cs-4") }), sent(server, "call_cancel"))
    }

    @Test
    fun reportsRefusalsAndStaysIdle() {
        val server = FakeServer { c, f ->
            if (f.string("action") == "call_invite") {
                val offline = f.string("topic") == bob
                c.push(jsonObject { put("id", f.string("id")); put("action", "call_invite"); put("code", if (offline) 409 else 401); put("reason", if (offline) "offline" else "not_friends") })
            }
        }
        val (manager, _) = setup(server)
        assertEquals(InviteResult.Refused(409, "offline", null), runBlocking { manager.invite(bob, CallType.VOICE) })
        assertTrue(manager.state.value is CallState.Idle)
        assertEquals(InviteResult.Refused(401, "not_friends", null), runBlocking { manager.invite(ImIds.uid("carol"), CallType.VOICE) })
    }

    @Test
    fun switchesToVideoBothWaysAndEndsACallWhoseMediaWillNotStart() {
        val server = FakeServer { c, f ->
            val ok = { c.push(reply(f) { put("callSessionId", f.string("callSessionId")) }) }
            when (f.string("action")) {
                "call_accept" -> c.push(reply(f) { put("callSessionId", f.string("callSessionId")); creds("callee") })
                "call_upgrade" -> {
                    ok()
                    after(10) { c.push(jsonObject { put("type", "call_upgrade_declined"); put("callSessionId", f.string("callSessionId")) }) }
                }
                "call_upgrade_accept" -> {
                    ok()
                    c.push(jsonObject { put("type", "call_upgraded"); put("callSessionId", f.string("callSessionId")); put("videoQuality", "hd") })
                }
                "call_hangup" -> ok()
            }
        }
        val (manager, media) = setup(server, user = "bob")
        val conn = server.connections.last()
        conn.push(jsonObject { put("type", "call_ring"); put("callSessionId", "cs-5"); put("callType", "voice"); put("ringTimeoutSeconds", 45) })
        eventually { manager.state.value is CallState.Incoming }
        runBlocking { manager.accept() }
        assertTrue(runBlocking { manager.requestVideo() })
        eventually { (manager.state.value as? CallState.Active)?.upgrade == Upgrade.DECLINED }
        conn.push(jsonObject { put("type", "call_upgrade_request"); put("callSessionId", "cs-5"); put("toType", "video") })
        eventually { (manager.state.value as? CallState.Active)?.upgrade == Upgrade.THEIRS }
        runBlocking { manager.acceptVideo() }
        eventually { (manager.state.value as? CallState.Active)?.callType == CallType.VIDEO }
        eventually { "video true hd" in media.log }
        val video = manager.state.value as CallState.Active
        assertTrue(video.cameraOn && video.speakerOn) // video goes to the loudspeaker
        runBlocking { manager.setCameraOn(false) }
        assertEquals("video false", media.log.last())
        assertEquals(CallType.VIDEO, (manager.state.value as CallState.Active).callType) // still a video call
        assertEquals(listOf(frame { put("action", "call_upgrade"); put("callSessionId", "cs-5"); put("toType", "video") }), sent(server, "call_upgrade"))
        runBlocking { manager.hangup() }

        // Media that will not start: the call ends for both sides.
        val broken = FakeMedia().apply { failJoin = true }
        val server2 = FakeServer { c, f ->
            when (f.string("action")) {
                "call_accept" -> c.push(reply(f) { put("callSessionId", f.string("callSessionId")); creds("callee") })
                "call_hangup" -> c.push(reply(f) { put("callSessionId", f.string("callSessionId")) })
            }
        }
        val (second, _) = setup(server2, user = "bob", media = broken)
        server2.connections.last().push(jsonObject { put("type", "call_ring"); put("callSessionId", "cs-6"); put("callType", "voice"); put("ringTimeoutSeconds", 45) })
        eventually { second.state.value is CallState.Incoming }
        runBlocking { second.accept() }
        assertEquals(CallEndReason.FAILED, (second.state.value as CallState.Ended).reason)
        eventually { sent(server2, "call_hangup").any { it.string("callSessionId") == "cs-6" } }
        assertEquals("leave", broken.log.last())
    }

    @Test
    fun mediaThatBreaksMidCallEndsItForBothSides() {
        val server = FakeServer { c, f ->
            when (f.string("action")) {
                "call_accept" -> c.push(reply(f) { put("callSessionId", f.string("callSessionId")); creds("callee") })
                "call_hangup" -> c.push(reply(f) { put("callSessionId", f.string("callSessionId")) })
            }
        }
        val (manager, media) = setup(server, user = "bob")
        server.connections.last().push(jsonObject { put("type", "call_ring"); put("callSessionId", "cs-7"); put("callType", "voice"); put("ringTimeoutSeconds", 45) })
        eventually { manager.state.value is CallState.Incoming }
        runBlocking { manager.accept() }
        media.broke.tryEmit(Unit)
        eventually { manager.state.value is CallState.Ended }
        assertEquals(CallEndReason.FAILED, (manager.state.value as CallState.Ended).reason)
        eventually { sent(server, "call_hangup").size == 1 }
        eventually { media.log.lastOrNull() == "leave" }
    }

    @Test
    fun aSecondCallWhileInOneIsRefusedHereAndARingIsIgnored() {
        val server = FakeServer { c, f ->
            if (f.string("action") == "call_accept") c.push(reply(f) { put("callSessionId", f.string("callSessionId")); creds("callee") })
        }
        val (manager, _) = setup(server, user = "bob")
        val conn = server.connections.last()
        conn.push(jsonObject { put("type", "call_ring"); put("callSessionId", "cs-8"); put("callType", "voice"); put("ringTimeoutSeconds", 45) })
        eventually { manager.state.value is CallState.Incoming }
        runBlocking { manager.accept() }
        assertEquals(InviteResult.Refused(409, "busy_here", null), runBlocking { manager.invite(alice, CallType.VOICE) })
        conn.push(jsonObject { put("type", "call_ring"); put("callSessionId", "cs-9"); put("callType", "voice"); put("ringTimeoutSeconds", 45) })
        Thread.sleep(100)
        assertEquals("cs-8", (manager.state.value as CallState.Active).callSessionId)
        assertTrue(sent(server, "call_invite").isEmpty())
    }

    @Test
    fun ringingNoticesFollowTheCall() {
        assertEquals(io.imkit.core.notify.CallNoticeTracker.End.ANSWERED, CallSystem.endOf("a", CallState.Active("a", CallType.VOICE, null, null, false, false, false, false, null, 0)))
        assertEquals(io.imkit.core.notify.CallNoticeTracker.End.ANSWERED_ELSEWHERE, CallSystem.endOf("a", CallState.Ended("a", CallEndReason.ANSWERED_ELSEWHERE, 0)))
        assertEquals(io.imkit.core.notify.CallNoticeTracker.End.CANCELLED, CallSystem.endOf("a", CallState.Ended("a", CallEndReason.CANCELLED, 0)))
        assertEquals(io.imkit.core.notify.CallNoticeTracker.End.TIMED_OUT, CallSystem.endOf("a", CallState.Ended("a", CallEndReason.NO_ANSWER, 0)))
        assertEquals(io.imkit.core.notify.CallNoticeTracker.End.ENDED, CallSystem.endOf("a", CallState.Ended("a", "host_said_so", 0)))
        assertEquals(io.imkit.core.notify.CallNoticeTracker.End.ENDED, CallSystem.endOf("a", CallState.Idle))
    }

    @Test
    fun elapsedTimeReads() {
        assertEquals("0:00", elapsedText(0))
        assertEquals("1:05", elapsedText(65))
        assertEquals("1:00:01", elapsedText(3601))
    }
}
