package io.talkwire.call

import io.talkwire.core.client.ImClient
import io.talkwire.core.protocol.Reply
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.Job
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

/** Where this device's call is. */
public sealed interface CallState {
    public data object Idle : CallState

    /** Calling [peer]; [callSessionId] once the server acknowledged. */
    public data class Outgoing(val peer: String, val callType: CallType, val callSessionId: String?, val since: Long) : CallState

    public data class Incoming(
        val callSessionId: String,
        val callType: CallType,
        val callerName: String?,
        val callerAvatar: String?,
        val ringTimeoutSeconds: Int,
        val since: Long,
    ) : CallState

    public data class Active(
        val callSessionId: String,
        val callType: CallType,
        /** The callee's uid on the caller's side; unknown to the callee (the ring has no uid). */
        val peer: String?,
        val callerName: String?,
        /** Joining the RTC channel. */
        val connecting: Boolean,
        val muted: Boolean,
        val speakerOn: Boolean,
        /** In a video call: whether this side's camera is on. */
        val cameraOn: Boolean,
        /** A switch to video asked by me, asked by them, or just declined by them. */
        val upgrade: Upgrade?,
        val since: Long,
    ) : CallState

    /** [reason] is one of [CallEndReason] or a reason of the host's. */
    public data class Ended(val callSessionId: String?, val reason: String, val since: Long) : CallState
}

public enum class Upgrade { MINE, THEIRS, DECLINED }

public sealed interface InviteResult {
    public data class Ok(val callSessionId: String) : InviteResult

    /**
     * [reason] is the server's: "offline", "busy", "not_friends", a host
     * reason, or a text for people; "busy_here" when this device is in a call,
     * "connection" when the request did not get through.
     */
    public data class Refused(val code: Int, val reason: String, val errCode: String?) : InviteResult
}

/**
 * The state of this device's call — ringing, active, ended — driving a
 * [CallMediaEngine]. One per signed-in client; [start] it once and keep it for
 * the session. Which of an account's devices takes a call is the server's
 * business (spec 16.2, multi-device): the others hear `answered_elsewhere`.
 */
public class CallManager(
    public val client: ImClient,
    /** My IM uid. */
    public val me: String,
    /** The RTC adapter; the call screen also takes its video views from here. */
    public val media: CallMediaEngine,
    private val scope: CoroutineScope,
    /** How long an ended call stays on screen before the state goes back to idle. */
    private val endedHoldMs: Long = 3_000,
    private val clock: () -> Long = System::currentTimeMillis,
) {
    public val signaling: CallSignaling = CallSignaling(client)

    private val _state = MutableStateFlow<CallState>(CallState.Idle)
    public val state: StateFlow<CallState> = _state.asStateFlow()

    private val lock = Any()
    private val early = ArrayList<Pair<Long, CallSignal>>()
    private var timer: Job? = null
    private var jobs: List<Job> = emptyList()

    /** cancel() came before the server told the call's id. */
    private var cancelBeforeAck = false

    /** Listens to the server's call pushes and the media's failures; does nothing while listening. */
    public fun start() {
        synchronized(lock) {
            if (jobs.isNotEmpty()) return
            jobs = listOf(
                scope.launch(start = CoroutineStart.UNDISPATCHED) { signaling.signals.collect { onSignal(it) } },
                scope.launch(start = CoroutineStart.UNDISPATCHED) { media.failures.collect { onMediaFailure() } },
            )
        }
    }

    /**
     * Stops listening, leaving any call in progress (the server ends it after
     * the disconnect grace period). [start] undoes it.
     */
    public fun stop() {
        val old = synchronized(lock) { jobs.also { jobs = emptyList() } }
        old.forEach { it.cancel() }
        clearTimer()
    }

    private val current: CallState get() = _state.value

    private val free: Boolean get() = current.let { it is CallState.Idle || it is CallState.Ended }

    // ---------------------------------------------------------------- actions

    /** Calls [peer] (a uid). Fails at once when this device is in a call. */
    public suspend fun invite(peer: String, callType: CallType, clientCallId: String? = null): InviteResult {
        synchronized(lock) {
            if (!free) return InviteResult.Refused(409, "busy_here", null)
            clearTimer()
            early.clear()
            cancelBeforeAck = false
            _state.value = CallState.Outgoing(peer, callType, null, clock())
        }
        val reply: Reply = try {
            signaling.invite(peer, callType.wire, clientCallId)
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            endedIfOutgoing(CallEndReason.FAILED)
            return InviteResult.Refused(0, "connection", null)
        }
        if (!reply.ok) {
            synchronized(lock) {
                val s = current
                if (s is CallState.Outgoing && s.callSessionId == null) _state.value = CallState.Idle
            }
            return InviteResult.Refused(reply.code, reply.reason ?: "", reply.errCode)
        }
        val callSessionId = reply.frame.string("callSessionId") ?: ""
        val replay: List<CallSignal>
        val cancelled: Boolean
        synchronized(lock) {
            cancelled = cancelBeforeAck
            if (cancelBeforeAck) {
                cancelBeforeAck = false
                early.clear()
                replay = emptyList()
            } else {
                val s = current
                if (s is CallState.Outgoing && s.callSessionId == null) {
                    _state.value = s.copy(callSessionId = callSessionId)
                    // Pushes that arrived before the acknowledgement (F49).
                    val now = clock()
                    replay = early.filter { (at, sig) -> sig.callSessionId == callSessionId && now - at < EARLY_SIGNAL_MS }.map { it.second }
                    early.clear()
                } else {
                    replay = emptyList()
                }
            }
        }
        if (cancelled) {
            // Cancelled while the invite was on its way: the callee is ringing now.
            quietly { signaling.cancel(callSessionId) }
            return InviteResult.Ok(callSessionId)
        }
        replay.forEach { onSignal(it) }
        return InviteResult.Ok(callSessionId)
    }

    /** Answers the ringing call; a video call answered as voice stays voice. False when it did not work. */
    public suspend fun accept(asVoice: Boolean = false): Boolean {
        val s = current as? CallState.Incoming ?: return false
        val reply: Reply = try {
            signaling.accept(s.callSessionId, asVoice)
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            endedIf(s.callSessionId, CallEndReason.FAILED) { it is CallState.Incoming }
            return false
        }
        if (!reply.ok) {
            // Cancelled or answered elsewhere meanwhile; a call_ended may follow.
            endedIf(s.callSessionId, CallEndReason.FAILED) { it is CallState.Incoming }
            return false
        }
        val callType = if (asVoice) CallType.VOICE else s.callType
        synchronized(lock) {
            val now = current
            if (now !is CallState.Incoming || now.callSessionId != s.callSessionId) return false
            clearTimer()
            _state.value = active(s.callSessionId, callType, peer = null, callerName = s.callerName)
        }
        joinMedia(s.callSessionId, callType, RtcCredentials.of(reply.frame))
        return true
    }

    /** Declines the ringing call. */
    public suspend fun reject() {
        val s = current as? CallState.Incoming ?: return
        ended(s.callSessionId, CallEndReason.REJECTED)
        quietly { signaling.reject(s.callSessionId) }
    }

    /** Stops calling before the other side answers. */
    public suspend fun cancel() {
        val id: String
        synchronized(lock) {
            val s = current as? CallState.Outgoing ?: return
            if (s.callSessionId == null) {
                // Not acknowledged yet: invite() cancels once it is.
                cancelBeforeAck = true
                _state.value = CallState.Ended(null, CallEndReason.CANCELLED, clock())
                scheduleIdle()
                return
            }
            id = s.callSessionId
        }
        ended(id, CallEndReason.CANCELLED)
        quietly { signaling.cancel(id) }
    }

    /** Ends the active call. */
    public suspend fun hangup() {
        val s = current as? CallState.Active ?: return
        ended(s.callSessionId, CallEndReason.USER_HANGUP)
        leaveMedia()
        quietly { signaling.hangup(s.callSessionId) }
    }

    /** Whatever ends the call from here: decline, cancel or hang up. */
    public suspend fun end() {
        when (current) {
            is CallState.Incoming -> reject()
            is CallState.Outgoing -> cancel()
            is CallState.Active -> hangup()
            else -> Unit
        }
    }

    /** Asks the other side to switch to video. */
    public suspend fun requestVideo(): Boolean {
        val s = current as? CallState.Active ?: return false
        if (s.callType == CallType.VIDEO || s.upgrade == Upgrade.MINE) return false
        patchActive(s.callSessionId) { it.copy(upgrade = Upgrade.MINE) }
        val reply = try {
            signaling.upgrade(s.callSessionId)
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            null
        }
        if (reply?.ok == true) return true
        patchActive(s.callSessionId) { if (it.upgrade == Upgrade.MINE) it.copy(upgrade = null) else it }
        return false
    }

    /** Agrees to the other side's switch to video; `call_upgraded` turns the camera on. */
    public suspend fun acceptVideo() {
        val s = current as? CallState.Active ?: return
        if (s.upgrade != Upgrade.THEIRS) return
        patchActive(s.callSessionId) { it.copy(upgrade = null) }
        quietly { signaling.upgradeAccept(s.callSessionId) }
    }

    public suspend fun declineVideo() {
        val s = current as? CallState.Active ?: return
        if (s.upgrade != Upgrade.THEIRS) return
        patchActive(s.callSessionId) { it.copy(upgrade = null) }
        quietly { signaling.upgradeReject(s.callSessionId) }
    }

    public fun setMuted(muted: Boolean) {
        val s = current as? CallState.Active ?: return
        media.setMuted(muted)
        patchActive(s.callSessionId) { it.copy(muted = muted) }
    }

    public fun setSpeakerOn(on: Boolean) {
        val s = current as? CallState.Active ?: return
        media.setSpeakerOn(on)
        patchActive(s.callSessionId) { it.copy(speakerOn = on) }
    }

    /** Turns this side's camera off or on again in a video call; the call stays a video call. */
    public suspend fun setCameraOn(on: Boolean) {
        val s = current as? CallState.Active ?: return
        if (s.callType != CallType.VIDEO || s.cameraOn == on) return
        patchActive(s.callSessionId) { it.copy(cameraOn = on) }
        quietly { media.setVideo(on) }
    }

    public fun switchCamera() {
        val s = current as? CallState.Active ?: return
        if (s.callType == CallType.VIDEO && s.cameraOn) media.switchCamera()
    }

    // ---------------------------------------------------------------- pushes

    private suspend fun onSignal(sig: CallSignal) {
        if (sig is CallSignal.Ring) {
            synchronized(lock) {
                if (!free) return
                clearTimer()
                _state.value = CallState.Incoming(sig.callSessionId, sig.callType, sig.callerName, sig.callerAvatar, sig.ringTimeoutSeconds, clock())
                // The server ends an unanswered call; this only covers a lost connection.
                timer = scope.launch {
                    delay((sig.ringTimeoutSeconds + 5) * 1_000L)
                    endedIf(sig.callSessionId, CallEndReason.NO_ANSWER) { it is CallState.Incoming }
                }
            }
            return
        }
        val s = current
        val mine = s.callSessionIdOrNull() == sig.callSessionId
        if (!mine) {
            synchronized(lock) {
                // A push for the call this device asked for, ahead of its id (F49).
                val now = current
                if (now is CallState.Outgoing && now.callSessionId == null) {
                    early += clock() to sig
                    if (early.size > 20) early.removeAt(0)
                }
            }
            return
        }
        when (sig) {
            is CallSignal.Answered -> {
                val callType = synchronized(lock) {
                    val now = current as? CallState.Outgoing ?: return
                    if (now.callSessionId != sig.callSessionId) return
                    _state.value = active(sig.callSessionId, now.callType, peer = now.peer, callerName = null)
                    now.callType
                }
                joinMedia(sig.callSessionId, callType, sig.credentials)
            }
            is CallSignal.Ended -> {
                val wasActive = s is CallState.Active
                ended(sig.callSessionId, sig.reason)
                if (wasActive) leaveMedia()
            }
            is CallSignal.UpgradeRequest -> patchActive(sig.callSessionId) { it.copy(upgrade = Upgrade.THEIRS) }
            is CallSignal.UpgradeDeclined -> patchActive(sig.callSessionId) { it.copy(upgrade = Upgrade.DECLINED) }
            is CallSignal.Upgraded -> {
                if (current !is CallState.Active) return
                patchActive(sig.callSessionId) { it.copy(callType = CallType.VIDEO, upgrade = null, cameraOn = true, speakerOn = true) }
                media.setSpeakerOn(true)
                quietly { media.setVideo(true, sig.videoQuality) }
            }
            is CallSignal.Ring -> Unit
        }
    }

    private suspend fun onMediaFailure() {
        val s = current as? CallState.Active ?: return
        // The RTC service dropped us for good: end the call for both sides.
        ended(s.callSessionId, CallEndReason.FAILED)
        leaveMedia()
        quietly { signaling.hangup(s.callSessionId) }
    }

    // ---------------------------------------------------------------- helpers

    private fun active(callSessionId: String, callType: CallType, peer: String?, callerName: String?) = CallState.Active(
        callSessionId = callSessionId,
        callType = callType,
        peer = peer,
        callerName = callerName,
        connecting = true,
        muted = false,
        // A video call starts on the loudspeaker, a voice call at the ear.
        speakerOn = callType == CallType.VIDEO,
        cameraOn = callType == CallType.VIDEO,
        upgrade = null,
        since = clock(),
    )

    private suspend fun joinMedia(callSessionId: String, callType: CallType, credentials: RtcCredentials) {
        try {
            media.join(MediaJoin(me, callType, credentials))
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            // No media, no call: end it for both sides.
            ended(callSessionId, CallEndReason.FAILED)
            leaveMedia()
            quietly { signaling.hangup(callSessionId) }
            return
        }
        val still = current as? CallState.Active
        if (still == null || still.callSessionId != callSessionId) {
            // Ended while joining: leave what join set up.
            leaveMedia()
            return
        }
        media.setSpeakerOn(still.speakerOn)
        if (still.muted) media.setMuted(true)
        patchActive(callSessionId) { it.copy(connecting = false) }
    }

    /** Leaving must finish even when the caller's coroutine is cancelled: the camera must not stay on. */
    private suspend fun leaveMedia() {
        withContext(NonCancellable) { quietly { media.leave() } }
    }

    private fun patchActive(callSessionId: String, fn: (CallState.Active) -> CallState.Active) {
        synchronized(lock) {
            val s = current
            if (s is CallState.Active && s.callSessionId == callSessionId) _state.value = fn(s)
        }
    }

    private fun ended(callSessionId: String, reason: String) {
        synchronized(lock) {
            clearTimer()
            _state.value = CallState.Ended(callSessionId, reason, clock())
            scheduleIdle()
        }
    }

    private fun endedIf(callSessionId: String, reason: String, phase: (CallState) -> Boolean) {
        synchronized(lock) {
            val s = current
            if (phase(s) && s.callSessionIdOrNull() == callSessionId) ended(callSessionId, reason)
        }
    }

    private fun endedIfOutgoing(reason: String) {
        synchronized(lock) {
            val s = current as? CallState.Outgoing ?: return
            clearTimer()
            _state.value = CallState.Ended(s.callSessionId, reason, clock())
            scheduleIdle()
        }
    }

    /** Called under [lock]. */
    private fun scheduleIdle() {
        val s = current
        timer = scope.launch {
            delay(endedHoldMs)
            _state.compareAndSet(s, CallState.Idle)
        }
    }

    private fun clearTimer() {
        timer?.cancel()
        timer = null
    }

    private suspend fun quietly(block: suspend () -> Unit) {
        try {
            block()
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            // Best effort: the server or the other side ends what this could not.
        }
    }

    private companion object {
        /** How long pushes about a call this device has just asked for are kept until the server tells its id (F49). */
        const val EARLY_SIGNAL_MS = 10_000L
    }
}

private fun CallState.callSessionIdOrNull(): String? = when (this) {
    is CallState.Outgoing -> callSessionId
    is CallState.Incoming -> callSessionId
    is CallState.Active -> callSessionId
    is CallState.Ended -> callSessionId
    CallState.Idle -> null
}
