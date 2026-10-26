package io.talkwire.call

import android.content.Context
import android.view.View
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.emptyFlow
import java.util.concurrent.CopyOnWriteArrayList

public data class MediaJoin(
    /** The local user's IM uid; adapters use it as the RTC user id unless told otherwise. */
    val uid: String,
    val callType: CallType,
    val credentials: RtcCredentials,
)

/**
 * What an RTC adapter implements. Media never goes through the IM server:
 * the host's RTC service carries it, with the credentials the server hands
 * each side.
 *
 * Methods may be called while a previous one is still running; [leave] must
 * work at any time, also when [join] failed or never finished, and must
 * turn the camera and the microphone off.
 */
public interface CallMediaEngine {
    /** Joins the channel; throws when it cannot (the manager then ends the call). */
    public suspend fun join(params: MediaJoin)

    /** Turns the camera on or off; on when the call became a video call. */
    public suspend fun setVideo(on: Boolean, videoQuality: String? = null)

    public fun setMuted(muted: Boolean)

    /** Loudspeaker or earpiece. */
    public fun setSpeakerOn(on: Boolean) {}

    public fun switchCamera() {}

    public suspend fun leave()

    /**
     * The RTC user id of the other side once their video can be shown; null
     * while there is none. The call screen then asks for [createRemoteVideoView].
     */
    public val remoteVideo: StateFlow<String?> get() = NoRemoteVideo

    /** A view showing this side's camera, already bound when returned; null without video support. */
    public fun createLocalVideoView(context: Context): View? = null

    /**
     * A view showing [rtcUserId]'s video, already bound when returned. The
     * screen puts it into the layout only after this returns: some SDKs crash
     * when an unbound render view is measured.
     */
    public fun createRemoteVideoView(context: Context, rtcUserId: String): View? = null

    /** Emits when the media broke for good mid-call; the manager ends the call. */
    public val failures: Flow<Unit> get() = emptyFlow()
}

private val NoRemoteVideo: StateFlow<String?> = MutableStateFlow<String?>(null).asStateFlow()

/** Joins nothing: signaling without media, for tests and demos. [log] records every call. */
public class NoMediaEngine : CallMediaEngine {
    public val log: MutableList<String> = CopyOnWriteArrayList()

    override suspend fun join(params: MediaJoin) {
        log += "join ${params.callType.wire} ${params.credentials.channelId}"
    }

    override suspend fun setVideo(on: Boolean, videoQuality: String?) {
        log += "video $on"
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
}
