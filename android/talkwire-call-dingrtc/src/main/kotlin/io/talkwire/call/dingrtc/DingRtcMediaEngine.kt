package io.talkwire.call.dingrtc

import android.content.Context
import android.util.Log
import android.view.View
import io.talkwire.call.CallMediaEngine
import io.talkwire.call.CallType
import io.talkwire.call.MediaJoin
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.TimeoutCancellationException
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeout

/** What the SDK tells the engine; called on the SDK's threads. */
public interface DingRtcListener {
    /** The outcome of a join: 0 joined, anything else an error code. */
    public fun onJoinResult(result: Int)

    public fun onError(code: Int, message: String?)

    /** [rtcUserId]'s camera can be shown, or stopped ([available] false). */
    public fun onRemoteVideo(rtcUserId: String, available: Boolean)

    public fun onRemoteLeft(rtcUserId: String)

    /** The connection failed for good, or the server dropped this user (an expired token). */
    public fun onFatal()
}

/**
 * The DingRTC calls the engine makes, so that the engine's logic runs against
 * a stand-in in tests. [RealDingRtc] is the SDK. Every method that returns an
 * Int returns the SDK's code: 0 is success.
 */
public interface DingRtc {
    public var listener: DingRtcListener?

    /** Creates the SDK engine; does nothing while it exists. */
    public fun open()

    /** Leaves any channel and frees the SDK engine (camera, microphone, codecs). Safe at any time. */
    public fun close()

    /** Asks to join; the outcome comes through [DingRtcListener.onJoinResult]. */
    public fun join(appId: String, token: String, channel: String, userId: String, userName: String): Int

    public fun publishAudio(on: Boolean)

    public fun publishVideo(on: Boolean)

    public fun setVideoSize(width: Int, height: Int, frameRate: Int)

    public fun setSpeakerOn(on: Boolean)

    public fun switchCamera()

    public fun createRenderView(context: Context): View

    /** Binds [view] to the camera, then starts the preview: in this order (a later bind stays black). */
    public fun bindLocal(view: View): Int

    public fun stopPreview()

    public fun bindRemote(view: View, rtcUserId: String): Int
}

public class DingRtcEngineOptions(
    /** The DingRTC user id the host signed the token for; default the IM uid. */
    public val userId: (MediaJoin) -> String = { it.uid },
    /** The name others see in the channel; default the user id. */
    public val userName: (MediaJoin) -> String? = { null },
    /** Camera size for the quality the host names ("sd", "hd"): sd 640×480, hd 1280×720. */
    public val dimension: (videoQuality: String?) -> Pair<Int, Int> = { if (it == "hd") 1280 to 720 else 640 to 480 },
    public val frameRate: Int = 15,
    /** How long to wait for the SDK to report the join. */
    public val joinTimeoutMs: Long = 15_000,
    /** Where SDK calls run; the SDK wants the main thread. */
    public val dispatcher: CoroutineDispatcher = Dispatchers.Main,
)

/**
 * A [CallMediaEngine] over Alibaba Cloud's DingRTC Android SDK (ARTC 3.0,
 * `com.ding.rtc:dingrtc-full`):
 *
 *     val media = DingRtcMediaEngine(context)
 *     val calls = CallManager(client, me, media, scope)
 *
 * The host answers calls with a DingRTC channel, the app id and each side's
 * token; the token names the user it is for, by default the user's IM uid.
 * A host that signs tokens for its own user ids passes [DingRtcEngineOptions.userId].
 *
 * One call at a time. [leave] may come at any moment, also while [join] is on
 * its way: the SDK engine is freed, so a call hung up while connecting never
 * leaves the camera or the microphone on.
 */
public class DingRtcMediaEngine(
    private val rtc: DingRtc,
    private val options: DingRtcEngineOptions = DingRtcEngineOptions(),
) : CallMediaEngine {
    public constructor(context: Context, options: DingRtcEngineOptions = DingRtcEngineOptions()) : this(RealDingRtc(context), options)

    private val lock = Any()

    /** Bumped by leave: work started before it must not touch the next call. */
    @Volatile private var generation = 0
    @Volatile private var joinResult: CompletableDeferred<Int>? = null
    @Volatile private var joined = false

    /** The SDK engine exists (a join, or a local view that came first). */
    @Volatile private var opened = false
    @Volatile private var muted = false
    @Volatile private var videoOn = false
    @Volatile private var quality: String? = null

    private val _remoteVideo = MutableStateFlow<String?>(null)
    override val remoteVideo: StateFlow<String?> = _remoteVideo.asStateFlow()

    private val _failures = MutableSharedFlow<Unit>(extraBufferCapacity = 1)
    override val failures: Flow<Unit> = _failures.asSharedFlow()

    private val listener = object : DingRtcListener {
        override fun onJoinResult(result: Int) {
            joinResult?.complete(result)
        }

        override fun onError(code: Int, message: String?) {
            Log.w(TAG, "DingRTC error $code: $message")
            // Only an error before the join's outcome makes the join fail.
            joinResult?.complete(code.takeIf { it != 0 } ?: -1)
        }

        override fun onRemoteVideo(rtcUserId: String, available: Boolean) {
            if (available) {
                _remoteVideo.value = rtcUserId
            } else {
                _remoteVideo.compareAndSet(rtcUserId, null)
            }
        }

        override fun onRemoteLeft(rtcUserId: String) {
            _remoteVideo.compareAndSet(rtcUserId, null)
        }

        override fun onFatal() {
            if (joined) _failures.tryEmit(Unit)
        }
    }

    override suspend fun join(params: MediaJoin) {
        val appId = params.credentials.appId ?: throw IllegalArgumentException("DingRTC needs the app id: the host's calls/answer must return appId")
        leave() // never two calls in one engine
        val gen = generation
        val userId = options.userId(params)
        val result = CompletableDeferred<Int>()
        withContext(options.dispatcher) {
            synchronized(lock) {
                if (gen != generation) return@withContext
                joinResult = result
                rtc.listener = listener
                rtc.open()
                opened = true
                quality = params.credentials.videoQuality
            }
            val code = rtc.join(appId, params.credentials.rtcToken, params.credentials.channelId, userId, options.userName(params) ?: userId)
            if (code != 0) result.complete(code)
        }
        if (gen != generation) return
        val outcome = try {
            withTimeout(options.joinTimeoutMs) { result.await() }
        } catch (e: TimeoutCancellationException) {
            null
        }
        if (gen != generation) return // left meanwhile; leave freed everything
        if (outcome != 0) {
            leave()
            throw IllegalStateException(if (outcome == null) "DingRTC join timed out" else "DingRTC join failed: $outcome")
        }
        withContext(options.dispatcher) {
            synchronized(lock) {
                if (gen != generation) return@withContext
                joined = true
                // Joining does not publish anything: the microphone goes on here.
                rtc.publishAudio(!muted)
                if (params.callType == CallType.VIDEO) startVideo()
            }
        }
    }

    override suspend fun setVideo(on: Boolean, videoQuality: String?) {
        withContext(options.dispatcher) {
            synchronized(lock) {
                if (videoQuality != null) quality = videoQuality
                if (!joined) return@withContext
                if (on) startVideo() else stopVideo()
            }
        }
    }

    /** Under [lock]. The preview starts when the call screen asks for the local view. */
    private fun startVideo() {
        val (w, h) = options.dimension(quality)
        rtc.setVideoSize(w, h, options.frameRate)
        rtc.publishVideo(true)
        videoOn = true
    }

    /** Under [lock]. */
    private fun stopVideo() {
        rtc.publishVideo(false)
        rtc.stopPreview()
        videoOn = false
    }

    override fun setMuted(muted: Boolean) {
        synchronized(lock) {
            this.muted = muted
            if (joined) rtc.publishAudio(!muted)
        }
    }

    override fun setSpeakerOn(on: Boolean) {
        synchronized(lock) { if (joined) rtc.setSpeakerOn(on) }
    }

    override fun switchCamera() {
        synchronized(lock) { if (joined && videoOn) rtc.switchCamera() }
    }

    override suspend fun leave() {
        val wasOpen: Boolean
        synchronized(lock) {
            generation++
            // Wakes a join that waits for the SDK; it sees the new generation and returns.
            joinResult?.complete(LEFT)
            joinResult = null
            wasOpen = opened
            opened = false
            joined = false
            videoOn = false
            _remoteVideo.value = null
        }
        if (!wasOpen) return
        withContext(options.dispatcher) {
            synchronized(lock) {
                // The preview may run without a joined channel (the view came first): stop it in any case.
                rtc.stopPreview()
                rtc.close()
                rtc.listener = null
            }
        }
    }

    override fun createLocalVideoView(context: Context): View {
        val view = rtc.createRenderView(context)
        synchronized(lock) {
            rtc.open()
            opened = true
            val code = rtc.bindLocal(view)
            if (code != 0) Log.w(TAG, "local preview refused: $code")
        }
        return view
    }

    override fun createRemoteVideoView(context: Context, rtcUserId: String): View {
        val view = rtc.createRenderView(context)
        synchronized(lock) {
            val code = rtc.bindRemote(view, rtcUserId)
            if (code != 0) Log.w(TAG, "remote view of $rtcUserId refused: $code")
        }
        return view
    }

    private companion object {
        const val TAG = "DingRtcMediaEngine"

        /** What a join waiting for the SDK hears when the call is left. */
        const val LEFT = Int.MIN_VALUE
    }
}
