package io.talkwire.call.dingrtc

import android.content.Context
import android.util.Log
import android.view.SurfaceView
import android.view.View
import com.ding.rtc.DingRtcAuthInfo
import com.ding.rtc.DingRtcEngine
import com.ding.rtc.DingRtcEngineEventListener

/**
 * [DingRtc] over the SDK (`com.ding.rtc:dingrtc-full:3.0.0`). Ported from the
 * reference deployment, where every call below was checked against the
 * SDK's bytecode and on devices:
 *
 * - `joinChannel(DingRtcAuthInfo, nick)` needs the app id, channel, user id
 *   and token all set; an empty app id fails with "invalid appId".
 * - Its Int result only says whether the call itself was accepted; the join's
 *   outcome comes later through `onJoinChannelResult`.
 * - The connection callback is `onConnectionStatusChanged` (past tense); a
 *   method named like the 2.0 SDK's would silently override nothing.
 * - 3.0 has no audio denoise call, and its video encoder settings have no
 *   bitrate.
 * - `setLocalViewConfig`, `setRemoteViewConfig` and `startPreview` return error
 *   codes instead of throwing; they are passed on.
 * - Render views are [SafeRtcSurfaceView], not the SDK's factory: its views
 *   crash the process when measured before they are bound.
 */
public class RealDingRtc(context: Context) : DingRtc {
    private val app = context.applicationContext
    private var engine: DingRtcEngine? = null

    override var listener: DingRtcListener? = null

    override fun open() {
        if (engine != null) return
        // The second argument is the "extras" string; the official sample passes "".
        val e = runCatching { DingRtcEngine.create(app, "") }
            .onFailure { Log.e(TAG, "DingRtcEngine.create failed: ${it.message}", it) }
            .getOrNull() ?: return
        engine = e
        e.setRtcEngineEventListener(object : DingRtcEngineEventListener() {
            override fun onJoinChannelResult(result: Int, channel: String?, userId: String?, elapsed: Int) {
                listener?.onJoinResult(result)
            }

            override fun onOccurError(error: Int, message: String?) {
                listener?.onError(error, message)
            }

            override fun onConnectionStatusChanged(
                status: DingRtcEngine.DingRtcConnectionStatus?,
                reason: DingRtcEngine.DingRtcConnectionStatusChangeReason?,
            ) {
                if (status == DingRtcEngine.DingRtcConnectionStatus.DingRtcConnectionStatusFailed) listener?.onFatal()
            }

            override fun onRemoteTrackAvailableNotify(
                uid: String?,
                audioTrack: DingRtcEngine.DingRtcAudioTrack?,
                videoTrack: DingRtcEngine.DingRtcVideoTrack?,
            ) {
                if (uid.isNullOrBlank()) return
                listener?.onRemoteVideo(uid, videoTrack != null && videoTrack != DingRtcEngine.DingRtcVideoTrack.DingRtcVideoTrackNo)
            }

            override fun onRemoteUserOffLineNotify(uid: String?, reason: DingRtcEngine.DingRtcUserOfflineReason?) {
                if (!uid.isNullOrBlank()) listener?.onRemoteLeft(uid)
            }

            // Dropped by the server or the channel closed (an expired token, for one).
            override fun onBye(code: Int) {
                listener?.onFatal()
            }
        })
    }

    override fun close() {
        val e = engine ?: return
        engine = null
        runCatching { e.leaveChannel() }.onFailure { Log.w(TAG, "leaveChannel failed: ${it.message}") }
        runCatching { e.destroy() }.onFailure { Log.w(TAG, "destroy failed: ${it.message}") }
    }

    override fun join(appId: String, token: String, channel: String, userId: String, userName: String): Int {
        val e = engine ?: return NOT_OPEN
        val auth = DingRtcAuthInfo().apply {
            this.appId = appId
            this.channelId = channel
            this.userId = userId
            this.token = token
        }
        return runCatching { e.joinChannel(auth, userName) }
            .onFailure { Log.e(TAG, "joinChannel threw: ${it.message}", it) }
            .getOrDefault(NOT_OPEN)
    }

    override fun publishAudio(on: Boolean) {
        engine?.let { e -> runCatching { e.publishLocalAudioStream(on) } }
    }

    override fun publishVideo(on: Boolean) {
        engine?.let { e -> runCatching { e.publishLocalVideoStream(on) } }
    }

    override fun setVideoSize(width: Int, height: Int, frameRate: Int) {
        val e = engine ?: return
        runCatching {
            val config = DingRtcEngine.DingRtcVideoEncoderConfiguration()
            config.dimensions = DingRtcEngine.DingRtcVideoDimensions(width, height)
            config.frameRate = frameRate
            e.setVideoEncoderConfiguration(config)
        }.onFailure { Log.w(TAG, "setVideoEncoderConfiguration failed: ${it.message}") }
    }

    override fun setSpeakerOn(on: Boolean) {
        engine?.let { e -> runCatching { e.enableSpeakerphone(on) } }
    }

    override fun switchCamera() {
        engine?.let { e -> runCatching { e.switchCamera() } }
    }

    override fun createRenderView(context: Context): View = SafeRtcSurfaceView(context)

    override fun bindLocal(view: View): Int {
        val e = engine ?: return NOT_OPEN
        val surface = view as? SurfaceView ?: return NOT_OPEN
        return runCatching {
            val canvas = DingRtcEngine.DingRtcVideoCanvas()
            canvas.view = surface
            val viewRc = e.setLocalViewConfig(canvas, DingRtcEngine.DingRtcVideoTrack.DingRtcVideoTrackCamera)
            if (viewRc != 0) viewRc else e.startPreview()
        }.getOrDefault(NOT_OPEN)
    }

    override fun stopPreview() {
        engine?.let { e -> runCatching { e.stopPreview() } }
    }

    override fun bindRemote(view: View, rtcUserId: String): Int {
        val e = engine ?: return NOT_OPEN
        val surface = view as? SurfaceView ?: return NOT_OPEN
        return runCatching {
            val canvas = DingRtcEngine.DingRtcVideoCanvas()
            canvas.view = surface
            e.setRemoteViewConfig(canvas, rtcUserId, DingRtcEngine.DingRtcVideoTrack.DingRtcVideoTrackCamera)
        }.getOrDefault(NOT_OPEN)
    }

    private companion object {
        const val TAG = "RealDingRtc"

        /** Returned when there is no SDK engine or the SDK threw. */
        const val NOT_OPEN = -1
    }
}
