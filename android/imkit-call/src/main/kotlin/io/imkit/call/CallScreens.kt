package io.imkit.call

import android.Manifest
import android.content.Context
import android.view.View
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Call
import androidx.compose.material.icons.filled.CallEnd
import androidx.compose.material.icons.filled.Cameraswitch
import androidx.compose.material.icons.filled.Mic
import androidx.compose.material.icons.filled.MicOff
import androidx.compose.material.icons.filled.Videocam
import androidx.compose.material.icons.filled.VideocamOff
import androidx.compose.material.icons.filled.VolumeUp
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.key
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.produceState
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.core.content.ContextCompat
import io.imkit.ui.components.ImAvatar
import io.imkit.ui.theme.ImTheme
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

// ------------------------------------------------------------------ texts

/** The text for an end reason; unknown reasons get the plain "call ended" (spec 16.4). */
public fun callEndedText(context: Context, reason: String): String = context.getString(
    when (reason) {
        CallEndReason.NO_ANSWER -> R.string.imkit_call_ended_no_answer
        CallEndReason.REJECTED -> R.string.imkit_call_ended_rejected
        CallEndReason.CANCELLED -> R.string.imkit_call_ended_cancelled
        CallEndReason.DISCONNECTED -> R.string.imkit_call_ended_disconnected
        CallEndReason.FAILED -> R.string.imkit_call_ended_failed
        CallEndReason.ANSWERED_ELSEWHERE -> R.string.imkit_call_ended_answered_elsewhere
        CallEndReason.CREDIT_EXHAUSTED -> R.string.imkit_call_ended_credit_exhausted
        CallEndReason.MAX_DURATION_REACHED -> R.string.imkit_call_ended_max_duration_reached
        else -> R.string.imkit_call_ended_user_hangup
    },
)

/** The text for a refused invite. */
public fun callRefusedText(context: Context, r: InviteResult.Refused): String = context.getString(
    when {
        r.reason == "offline" -> R.string.imkit_call_refused_offline
        r.reason == "busy" || r.reason == "busy_here" -> R.string.imkit_call_refused_busy
        r.reason == "not_friends" || r.errCode == "not_friends" -> R.string.imkit_call_refused_not_friends
        else -> R.string.imkit_call_refused
    },
)

/** "m:ss" (or "h:mm:ss") for [seconds]. */
internal fun elapsedText(seconds: Long): String {
    val s = seconds.coerceAtLeast(0)
    val h = s / 3600
    val m = (s % 3600) / 60
    val sec = s % 60
    return if (h > 0) "%d:%02d:%02d".format(h, m, sec) else "%d:%02d".format(m, sec)
}

// ------------------------------------------------------------------ permissions

/**
 * Asks for [permissions] that are not granted yet, then calls back with the
 * ones granted. Call in composition; use the returned function on clicks.
 */
@Composable
internal fun rememberPermissionRequest(): (permissions: List<String>, then: (granted: Set<String>) -> Unit) -> Unit {
    val context = LocalContext.current
    var pending by remember { mutableStateOf<((Set<String>) -> Unit)?>(null) }
    val launcher = rememberLauncherForActivityResult(ActivityResultContracts.RequestMultiplePermissions()) { result ->
        val granted = result.filterValues { it }.keys + grantedOf(context, result.keys)
        pending?.invoke(granted)
        pending = null
    }
    return remember(launcher) {
        { permissions, then ->
            val missing = permissions.filter { ContextCompat.checkSelfPermission(context, it) != android.content.pm.PackageManager.PERMISSION_GRANTED }
            if (missing.isEmpty()) {
                then(permissions.toSet())
            } else {
                pending = { granted -> then(granted + (permissions - missing.toSet())) }
                launcher.launch(missing.toTypedArray())
            }
        }
    }
}

private fun grantedOf(context: Context, permissions: Collection<String>): Set<String> =
    permissions.filter { ContextCompat.checkSelfPermission(context, it) == android.content.pm.PackageManager.PERMISSION_GRANTED }.toSet()

private const val MIC = Manifest.permission.RECORD_AUDIO
private const val CAMERA = Manifest.permission.CAMERA

private fun needs(video: Boolean) = if (video) listOf(MIC, CAMERA) else listOf(MIC)

// ------------------------------------------------------------------ buttons

/**
 * Voice and video call buttons for a single chat's top bar. They ask for the
 * microphone (and the camera) first; [onRefused] gets a text to show when the
 * call cannot be made.
 */
@Composable
public fun CallButtons(
    manager: CallManager,
    peer: String,
    onRefused: (String) -> Unit,
    modifier: Modifier = Modifier,
) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    val state by manager.state.collectAsState()
    val request = rememberPermissionRequest()
    val busy = state !is CallState.Idle && state !is CallState.Ended
    val tint = ImTheme.colors.textPrimary

    fun call(type: CallType) {
        request(needs(type == CallType.VIDEO)) { granted ->
            when {
                MIC !in granted -> onRefused(context.getString(R.string.imkit_call_mic_needed))
                type == CallType.VIDEO && CAMERA !in granted -> onRefused(context.getString(R.string.imkit_call_camera_needed))
                else -> scope.launch {
                    val r = manager.invite(peer, type)
                    if (r is InviteResult.Refused) onRefused(callRefusedText(context, r))
                }
            }
        }
    }

    Row(modifier) {
        IconButton(onClick = { call(CallType.VOICE) }, enabled = !busy) {
            Icon(Icons.Filled.Call, stringResource(R.string.imkit_call_voice_call), tint = tint)
        }
        IconButton(onClick = { call(CallType.VIDEO) }, enabled = !busy) {
            Icon(Icons.Filled.Videocam, stringResource(R.string.imkit_call_video_call), tint = tint)
        }
    }
}

// ------------------------------------------------------------------ the call screen

private val Backdrop = Color(0xFF111827)
private val ActionColor = Color(0x29FFFFFF)
private val AcceptColor = Color(0xFF16A34A)
private val EndColor = Color(0xFFDC2626)

/**
 * The call screen while a call rings, runs or has just ended; nothing when
 * idle. Put it over the app's content (it fills its box), or in the activity
 * the incoming-call notice opens. [nameOf] and [avatarOf] name the person on
 * the caller's side, where the callee is known by uid; the callee sees the
 * name and picture the ring carried.
 */
@Composable
public fun CallScreen(
    manager: CallManager,
    modifier: Modifier = Modifier,
    nameOf: (uid: String) -> String? = { null },
    avatarOf: (uid: String) -> String? = { null },
) {
    val state by manager.state.collectAsState()
    val s = state
    if (s is CallState.Idle) return
    val scope = rememberCoroutineScope()
    val request = rememberPermissionRequest()
    var notice by remember(s.javaClass) { mutableStateOf<Int?>(null) }

    val peerUid = (s as? CallState.Outgoing)?.peer ?: (s as? CallState.Active)?.peer
    val name = peerUid?.let(nameOf)
        ?: (s as? CallState.Incoming)?.callerName
        ?: (s as? CallState.Active)?.callerName
        ?: stringResource(R.string.imkit_call_someone)
    val avatar = peerUid?.let(avatarOf) ?: (s as? CallState.Incoming)?.callerAvatar
    val videoActive = s is CallState.Active && s.callType == CallType.VIDEO

    Box(modifier.fillMaxSize().background(Backdrop).semantics { contentDescription = name }) {
        if (videoActive) Videos(manager.media, cameraOn = (s as CallState.Active).cameraOn)
        Column(
            Modifier.fillMaxSize().statusBarsPadding().navigationBarsPadding().padding(24.dp),
            horizontalAlignment = Alignment.CenterHorizontally,
        ) {
            Spacer(Modifier.height(if (videoActive) 8.dp else 72.dp))
            if (!videoActive) {
                ImAvatar(url = avatar, name = name, size = 96.dp)
                Spacer(Modifier.height(16.dp))
            }
            Text(name, color = Color.White, fontSize = 24.sp, fontWeight = FontWeight.SemiBold, textAlign = TextAlign.Center)
            Spacer(Modifier.height(8.dp))
            Text(statusText(s), color = Color.White.copy(alpha = 0.8f), fontSize = 15.sp, textAlign = TextAlign.Center)
            notice?.let {
                Spacer(Modifier.height(12.dp))
                Notice { Text(stringResource(it), color = Color.White, fontSize = 14.sp) }
            }
            if (s is CallState.Active) {
                when (s.upgrade) {
                    Upgrade.THEIRS -> {
                        Spacer(Modifier.height(16.dp))
                        Notice {
                            Column(horizontalAlignment = Alignment.CenterHorizontally) {
                                Text(stringResource(R.string.imkit_call_video_requested), color = Color.White, fontSize = 14.sp)
                                Spacer(Modifier.height(8.dp))
                                Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                                    Pill(stringResource(R.string.imkit_call_accept_video), AcceptColor) {
                                        request(listOf(CAMERA)) { granted ->
                                            if (CAMERA in granted) scope.launch { manager.acceptVideo() } else notice = R.string.imkit_call_camera_needed
                                        }
                                    }
                                    Pill(stringResource(R.string.imkit_call_decline_video), ActionColor) { scope.launch { manager.declineVideo() } }
                                }
                            }
                        }
                    }
                    Upgrade.MINE -> {
                        Spacer(Modifier.height(16.dp))
                        Notice { Text(stringResource(R.string.imkit_call_video_asked), color = Color.White, fontSize = 14.sp) }
                    }
                    Upgrade.DECLINED -> {
                        Spacer(Modifier.height(16.dp))
                        Notice { Text(stringResource(R.string.imkit_call_video_declined), color = Color.White, fontSize = 14.sp) }
                    }
                    null -> Unit
                }
            }
            Spacer(Modifier.weight(1f))
            Row(horizontalArrangement = Arrangement.spacedBy(20.dp), verticalAlignment = Alignment.Top) {
                when (s) {
                    is CallState.Incoming -> {
                        RoundAction(Icons.Filled.CallEnd, stringResource(R.string.imkit_call_decline), EndColor) { scope.launch { manager.reject() } }
                        if (s.callType == CallType.VIDEO) {
                            RoundAction(Icons.Filled.Mic, stringResource(R.string.imkit_call_accept_as_voice), ActionColor) {
                                request(needs(false)) { granted ->
                                    if (MIC in granted) scope.launch { manager.accept(asVoice = true) } else notice = R.string.imkit_call_mic_needed
                                }
                            }
                        }
                        RoundAction(Icons.Filled.Call, stringResource(R.string.imkit_call_accept), AcceptColor) {
                            val video = s.callType == CallType.VIDEO
                            request(needs(video)) { granted ->
                                when {
                                    MIC !in granted -> notice = R.string.imkit_call_mic_needed
                                    // No camera: the call goes on as a voice call.
                                    video && CAMERA !in granted -> scope.launch { manager.accept(asVoice = true) }
                                    else -> scope.launch { manager.accept() }
                                }
                            }
                        }
                    }
                    is CallState.Outgoing -> RoundAction(Icons.Filled.CallEnd, stringResource(R.string.imkit_call_cancel), EndColor) { scope.launch { manager.cancel() } }
                    is CallState.Active -> {
                        RoundAction(
                            if (s.muted) Icons.Filled.MicOff else Icons.Filled.Mic,
                            stringResource(if (s.muted) R.string.imkit_call_unmute else R.string.imkit_call_mute),
                            ActionColor,
                            selected = s.muted,
                        ) { manager.setMuted(!s.muted) }
                        RoundAction(Icons.Filled.VolumeUp, stringResource(R.string.imkit_call_speaker), ActionColor, selected = s.speakerOn) { manager.setSpeakerOn(!s.speakerOn) }
                        if (s.callType == CallType.VIDEO) {
                            RoundAction(
                                if (s.cameraOn) Icons.Filled.VideocamOff else Icons.Filled.Videocam,
                                stringResource(if (s.cameraOn) R.string.imkit_call_camera_off else R.string.imkit_call_camera_on),
                                ActionColor,
                            ) { scope.launch { manager.setCameraOn(!s.cameraOn) } }
                            if (s.cameraOn) RoundAction(Icons.Filled.Cameraswitch, stringResource(R.string.imkit_call_switch_camera), ActionColor) { manager.switchCamera() }
                        } else if (s.upgrade == null) {
                            RoundAction(Icons.Filled.Videocam, stringResource(R.string.imkit_call_switch_to_video), ActionColor) {
                                request(listOf(CAMERA)) { granted ->
                                    if (CAMERA in granted) scope.launch { manager.requestVideo() } else notice = R.string.imkit_call_camera_needed
                                }
                            }
                        }
                        RoundAction(Icons.Filled.CallEnd, stringResource(R.string.imkit_call_hang_up), EndColor) { scope.launch { manager.hangup() } }
                    }
                    else -> Unit
                }
            }
            Spacer(Modifier.height(24.dp))
        }
    }
}

@Composable
private fun statusText(s: CallState): String = when (s) {
    is CallState.Incoming -> stringResource(if (s.callType == CallType.VIDEO) R.string.imkit_call_incoming_video else R.string.imkit_call_incoming_voice)
    is CallState.Outgoing -> stringResource(R.string.imkit_call_calling)
    is CallState.Active -> if (s.connecting) {
        stringResource(R.string.imkit_call_connecting)
    } else {
        val seconds by produceState(0L, s.callSessionId) {
            while (true) {
                value = (System.currentTimeMillis() - s.since) / 1000
                delay(1_000)
            }
        }
        elapsedText(seconds)
    }
    is CallState.Ended -> callEndedText(LocalContext.current, s.reason)
    CallState.Idle -> ""
}

/** The other side's video, with this side's camera in the corner, from the adapter. */
@Composable
private fun Videos(media: CallMediaEngine, cameraOn: Boolean) {
    val remote by media.remoteVideo.collectAsState()
    Box(Modifier.fillMaxSize()) {
        val uid = remote
        if (uid != null) {
            // A new view per remote user: the adapter binds it before it enters the layout.
            key(uid) {
                AndroidView(factory = { ctx -> media.createRemoteVideoView(ctx, uid) ?: View(ctx) }, modifier = Modifier.fillMaxSize())
            }
        } else {
            Text(
                stringResource(R.string.imkit_call_waiting_for_video),
                color = Color.White.copy(alpha = 0.7f),
                fontSize = 14.sp,
                modifier = Modifier.align(Alignment.Center),
            )
        }
        if (cameraOn) {
            key("local") {
                AndroidView(
                    factory = { ctx -> media.createLocalVideoView(ctx) ?: View(ctx) },
                    modifier = Modifier.align(Alignment.TopEnd).statusBarsPadding().padding(16.dp).width(108.dp).height(144.dp).clip(RoundedCornerShape(8.dp)),
                )
            }
        }
    }
}

@Composable
private fun Notice(content: @Composable () -> Unit) {
    Box(Modifier.clip(RoundedCornerShape(12.dp)).background(Color(0x1FFFFFFF)).padding(horizontal = 14.dp, vertical = 10.dp)) { content() }
}

@Composable
private fun Pill(label: String, color: Color, onClick: () -> Unit) {
    Box(
        Modifier.clip(RoundedCornerShape(50)).background(color).clickable(role = Role.Button, onClick = onClick).padding(horizontal = 16.dp, vertical = 8.dp),
    ) { Text(label, color = Color.White, fontSize = 14.sp) }
}

@Composable
private fun RoundAction(icon: ImageVector, label: String, color: Color, selected: Boolean = false, onClick: () -> Unit) {
    Column(horizontalAlignment = Alignment.CenterHorizontally, modifier = Modifier.width(72.dp)) {
        Box(
            Modifier.size(60.dp).clip(CircleShape).background(if (selected) Color.White else color).clickable(role = Role.Button, onClickLabel = label, onClick = onClick)
                .semantics { contentDescription = label },
            contentAlignment = Alignment.Center,
        ) {
            Icon(icon, contentDescription = null, tint = if (selected) Backdrop else Color.White)
        }
        Spacer(Modifier.height(6.dp))
        Text(label, color = Color.White, fontSize = 12.sp, textAlign = TextAlign.Center, modifier = Modifier.fillMaxWidth())
    }
}
