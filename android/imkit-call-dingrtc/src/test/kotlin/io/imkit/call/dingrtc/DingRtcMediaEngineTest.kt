package io.imkit.call.dingrtc

import android.content.Context
import android.view.View
import androidx.test.core.app.ApplicationProvider
import io.imkit.call.CallMediaEngine
import io.imkit.call.CallType
import io.imkit.call.MediaJoin
import io.imkit.call.RtcCredentials
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.async
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Assert.fail
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import java.util.concurrent.CopyOnWriteArrayList

/** The engine's logic against a stand-in for the SDK; the SDK itself needs a device. */
@RunWith(RobolectricTestRunner::class)
class DingRtcMediaEngineTest {
    private val context = ApplicationProvider.getApplicationContext<Context>()

    /** Records every call; joins succeed at once unless told otherwise. */
    private class FakeRtc : DingRtc {
        val log = CopyOnWriteArrayList<String>()
        override var listener: DingRtcListener? = null
        var joinCode = 0
        var joinResult: Int? = 0
        val joinAsked = CompletableDeferred<Unit>()

        override fun open() {
            log += "open"
        }

        override fun close() {
            log += "close"
        }

        override fun join(appId: String, token: String, channel: String, userId: String, userName: String): Int {
            log += "join $appId $channel $userId $userName $token"
            joinAsked.complete(Unit)
            if (joinCode == 0) joinResult?.let { listener?.onJoinResult(it) }
            return joinCode
        }

        override fun publishAudio(on: Boolean) {
            log += "audio $on"
        }

        override fun publishVideo(on: Boolean) {
            log += "video $on"
        }

        override fun setVideoSize(width: Int, height: Int, frameRate: Int) {
            log += "size ${width}x$height@$frameRate"
        }

        override fun setSpeakerOn(on: Boolean) {
            log += "speaker $on"
        }

        override fun switchCamera() {
            log += "switch"
        }

        override fun createRenderView(context: Context): View = View(context)

        override fun bindLocal(view: View): Int {
            log += "bind local"
            return 0
        }

        override fun stopPreview() {
            log += "stop preview"
        }

        override fun bindRemote(view: View, rtcUserId: String): Int {
            log += "bind remote $rtcUserId"
            return 0
        }
    }

    private fun engine(rtc: FakeRtc, options: (DingRtcEngineOptions) -> DingRtcEngineOptions = { it }) =
        DingRtcMediaEngine(rtc, options(DingRtcEngineOptions(dispatcher = Dispatchers.Unconfined, joinTimeoutMs = 2_000)))

    private fun join(type: CallType, appId: String? = "app", quality: String? = "sd") =
        MediaJoin("uid_me", type, RtcCredentials("ch-1", "tok", appId, quality, denoiseEnabled = true))

    @Test
    fun isACallMediaEngine() {
        val e: CallMediaEngine = engine(FakeRtc())
        assertEquals(null, e.remoteVideo.value)
    }

    @Test
    fun joinsAVoiceCallAsTheImUidAndPublishesTheMicrophone() = runBlocking {
        val rtc = FakeRtc()
        engine(rtc).join(join(CallType.VOICE))
        assertEquals(listOf("open", "join app ch-1 uid_me uid_me tok", "audio true"), rtc.log.toList())
    }

    @Test
    fun videoAtTheHostsQualityWithBothViewsBound() = runBlocking {
        val rtc = FakeRtc()
        val e = engine(rtc)
        e.join(join(CallType.VIDEO, quality = "hd"))
        assertEquals(listOf("open", "join app ch-1 uid_me uid_me tok", "audio true", "size 1280x720@15", "video true"), rtc.log.toList())
        // The preview starts with the local view, after binding.
        e.createLocalVideoView(context)
        assertEquals("bind local", rtc.log.last())
        rtc.listener!!.onRemoteVideo("uid_peer", true)
        assertEquals("uid_peer", e.remoteVideo.value)
        e.createRemoteVideoView(context, "uid_peer")
        assertEquals("bind remote uid_peer", rtc.log.last())
        rtc.listener!!.onRemoteVideo("uid_peer", false)
        assertEquals(null, e.remoteVideo.value)
    }

    @Test
    fun upgradesToVideoAndBackAndMuteHoldsAcrossTheJoin() = runBlocking {
        val rtc = FakeRtc()
        val e = engine(rtc)
        e.setMuted(true) // before the call: no SDK call yet
        e.join(join(CallType.VOICE))
        assertEquals("audio false", rtc.log.last())
        e.setVideo(true, "hd")
        assertEquals(listOf("size 1280x720@15", "video true"), rtc.log.takeLast(2))
        e.switchCamera()
        assertEquals("switch", rtc.log.last())
        e.setVideo(false)
        assertEquals(listOf("video false", "stop preview"), rtc.log.takeLast(2))
        e.switchCamera() // camera off: nothing to switch
        assertEquals("stop preview", rtc.log.last())
        e.setMuted(false)
        e.setSpeakerOn(true)
        assertEquals(listOf("audio true", "speaker true"), rtc.log.takeLast(2))
    }

    @Test
    fun hungUpWhileConnectingNothingStaysOn() = runBlocking {
        val rtc = FakeRtc().apply { joinResult = null } // the SDK has not answered yet
        val e = engine(rtc)
        val joining = async(Dispatchers.Default) { e.join(join(CallType.VIDEO)) }
        withTimeout(2_000) { rtc.joinAsked.await() }
        e.leave()
        joining.await() // returns quietly: the call was left
        assertEquals(listOf("open", "join app ch-1 uid_me uid_me tok", "stop preview", "close"), rtc.log.toList())
        rtc.listener?.onJoinResult(0) // too late: nothing happens
        assertFalse(rtc.log.contains("audio true"))
    }

    @Test
    fun aJoinThatFailsThrowsAndFreesTheSdk() = runBlocking {
        val rejected = FakeRtc().apply { joinCode = 16974340 }
        expectFailure { engine(rejected).join(join(CallType.VOICE)) }
        assertEquals(listOf("stop preview", "close"), rejected.log.takeLast(2))
        val failed = FakeRtc().apply { joinResult = 16974081 }
        expectFailure { engine(failed).join(join(CallType.VOICE)) }
        assertEquals("close", failed.log.last())
        expectFailure { engine(FakeRtc()).join(join(CallType.VOICE, appId = null)) }
    }

    @Test
    fun theHostsOwnUserIdsAndAName() = runBlocking {
        val rtc = FakeRtc()
        engine(rtc) { DingRtcEngineOptions(userId = { "host-42" }, userName = { "Alice" }, dispatcher = Dispatchers.Unconfined) }.join(join(CallType.VOICE))
        assertEquals("join app ch-1 host-42 Alice tok", rtc.log[1])
    }

    @Test
    fun aDroppedConnectionIsAFailureOnlyInACall() = runBlocking {
        val rtc = FakeRtc()
        val e = engine(rtc)
        val failure = async(Dispatchers.Default) { withTimeout(2_000) { e.failures.first() } }
        e.join(join(CallType.VOICE))
        Thread.sleep(50) // the collector is listening
        rtc.listener!!.onFatal()
        failure.await()
        e.leave()
        assertTrue(rtc.log.last() == "close")
    }

    private suspend fun expectFailure(block: suspend () -> Unit) {
        try {
            block()
        } catch (e: Exception) {
            return
        }
        fail("expected the join to fail")
    }
}
