package io.talkwire.call

import android.Manifest
import android.app.Notification
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.content.pm.ServiceInfo
import android.os.Build
import android.os.IBinder
import android.os.PowerManager
import android.util.Log
import androidx.annotation.DrawableRes
import androidx.core.app.NotificationChannelCompat
import androidx.core.app.NotificationCompat
import androidx.core.app.NotificationManagerCompat
import androidx.core.content.ContextCompat
import androidx.core.content.IntentCompat
import io.talkwire.android.notify.CallNotification
import io.talkwire.android.notify.NotificationController
import io.talkwire.core.notify.CallNoticeTracker
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.Job
import kotlinx.coroutines.launch

/** What the ongoing-call notification says; the app owns the names and the screen it opens. */
public class OngoingCallNotice(
    public val title: CharSequence,
    public val text: CharSequence,
    @DrawableRes public val smallIcon: Int,
    /** Opens the call screen. */
    public val contentIntent: PendingIntent?,
)

/**
 * Keeps the process and the microphone (and camera) while this device is in
 * a call, so that a call goes on with the app in the background. Started and
 * stopped by [CallSystem]; all call state lives in the [CallManager].
 *
 * Declared in this module's manifest with the microphone and camera types.
 * Which of them it claims is decided when it starts, from the permissions
 * granted at that moment: Android 14 refuses a type whose permission is
 * missing, and a ringing video call has no camera permission yet.
 */
public class CallForegroundService : Service() {
    private var wakeLock: PowerManager.WakeLock? = null

    override fun onCreate() {
        super.onCreate()
        wakeLock = getSystemService(PowerManager::class.java)
            ?.newWakeLock(PowerManager.PARTIAL_WAKE_LOCK, "$packageName:talkwire-call")
            ?.also { it.acquire(MAX_WAKE_MS) }
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        // A null intent means the system recreated the service after the process
        // died. The call died with it (its state was in memory), so this service
        // stands for nothing: staying would leave an ongoing notice nobody can
        // clear. START_NOT_STICKY below keeps that from happening again.
        if (intent == null) {
            stopSelf()
            return START_NOT_STICKY
        }
        val video = intent.getBooleanExtra(EXTRA_VIDEO, false)
        return try {
            val notification = notification(intent)
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
                var type = 0
                if (granted(this, Manifest.permission.RECORD_AUDIO)) type = type or ServiceInfo.FOREGROUND_SERVICE_TYPE_MICROPHONE
                if (video && granted(this, Manifest.permission.CAMERA)) type = type or ServiceInfo.FOREGROUND_SERVICE_TYPE_CAMERA
                if (type == 0) {
                    // Nothing it may claim yet: no foreground service. The call does not
                    // need it, only survives the background less well.
                    stopSelf()
                    return START_NOT_STICKY
                }
                startForeground(NOTIFICATION_ID, notification, type)
            } else {
                startForeground(NOTIFICATION_ID, notification)
            }
            START_NOT_STICKY
        } catch (e: Exception) {
            // Android 14 also refuses while the app is not in an eligible state;
            // crashing here would take the call (and the app) with it.
            Log.w(TAG, "startForeground refused: ${e.message}")
            stopSelf()
            START_NOT_STICKY
        }
    }

    override fun onDestroy() {
        wakeLock?.let { if (it.isHeld) it.release() }
        wakeLock = null
        super.onDestroy()
    }

    override fun onBind(intent: Intent?): IBinder? = null

    private fun notification(intent: Intent): Notification {
        val channel = intent.getStringExtra(EXTRA_CHANNEL) ?: CallSystem.ONGOING_CHANNEL
        NotificationManagerCompat.from(this).createNotificationChannel(
            NotificationChannelCompat.Builder(channel, NotificationManagerCompat.IMPORTANCE_LOW)
                .setName(getString(R.string.talkwire_call_ongoing_channel))
                .setShowBadge(false)
                .build(),
        )
        return NotificationCompat.Builder(this, channel)
            .setSmallIcon(intent.getIntExtra(EXTRA_ICON, android.R.drawable.sym_call_outgoing))
            .setContentTitle(intent.getCharSequenceExtra(EXTRA_TITLE))
            .setContentText(intent.getCharSequenceExtra(EXTRA_TEXT))
            .setContentIntent(IntentCompat.getParcelableExtra(intent, EXTRA_CONTENT, PendingIntent::class.java))
            .setCategory(NotificationCompat.CATEGORY_CALL)
            .setOngoing(true)
            .setSilent(true)
            .setPriority(NotificationCompat.PRIORITY_LOW)
            .build()
    }

    public companion object {
        private const val TAG = "CallForegroundService"
        private const val NOTIFICATION_ID = 0x1a03
        private const val EXTRA_VIDEO = "talkwire.call.video"
        private const val EXTRA_TITLE = "talkwire.call.title"
        private const val EXTRA_TEXT = "talkwire.call.text"
        private const val EXTRA_ICON = "talkwire.call.icon"
        private const val EXTRA_CONTENT = "talkwire.call.content"
        private const val EXTRA_CHANNEL = "talkwire.call.channel"

        /** A call longer than this keeps going, without the wake lock. */
        private const val MAX_WAKE_MS = 4 * 60 * 60 * 1_000L

        /** Starts the service, or updates its notice and types when it runs. */
        public fun start(context: Context, notice: OngoingCallNotice, video: Boolean, channel: String = CallSystem.ONGOING_CHANNEL) {
            val app = context.applicationContext
            val intent = Intent(app, CallForegroundService::class.java)
                .putExtra(EXTRA_VIDEO, video)
                .putExtra(EXTRA_TITLE, notice.title)
                .putExtra(EXTRA_TEXT, notice.text)
                .putExtra(EXTRA_ICON, notice.smallIcon)
                .putExtra(EXTRA_CONTENT, notice.contentIntent)
                .putExtra(EXTRA_CHANNEL, channel)
            try {
                ContextCompat.startForegroundService(app, intent)
            } catch (e: Exception) {
                // Background start restrictions: the call goes on without it.
                Log.w(TAG, "start refused: ${e.message}")
            }
        }

        public fun stop(context: Context) {
            context.applicationContext.stopService(Intent(context.applicationContext, CallForegroundService::class.java))
        }

        internal fun granted(context: Context, permission: String): Boolean =
            ContextCompat.checkSelfPermission(context, permission) == PackageManager.PERMISSION_GRANTED
    }
}

/**
 * Keeps the system in step with the [CallManager]'s state: the incoming-call
 * notice (through talkwire-android's [NotificationController], which also cancels
 * it however the call ends) and the ring while it rings, the ringback while
 * this device calls, and the [CallForegroundService] while this device is
 * calling or in a call. Start it once with the manager.
 */
public class CallSystem(
    context: Context,
    private val manager: CallManager,
    private val notifications: NotificationController,
    private val scope: CoroutineScope,
    /** The incoming-call notice, with the full-screen intent that opens the call screen. */
    private val incomingNotice: (CallState.Incoming) -> CallNotification,
    /** The ongoing-call notice. */
    private val ongoingNotice: (CallState) -> OngoingCallNotice,
    /** Plays the ring and the ringback; default the system's sounds, null for silence. */
    ringer: CallRinger? = SystemCallRinger(context),
) {
    private val app = context.applicationContext
    private val rings = ringer?.let { RingFollower(it) }
    private var job: Job? = null

    public fun start() {
        if (job != null) return
        job = scope.launch(start = CoroutineStart.UNDISPATCHED) {
            var previous: CallState = CallState.Idle
            var serviceVideo: Boolean? = null
            manager.state.collect { s ->
                rings?.onState(s)
                val wasRinging = previous as? CallState.Incoming
                if (s is CallState.Incoming && wasRinging?.callSessionId != s.callSessionId) {
                    wasRinging?.let { notifications.onCallEnded(it.callSessionId, CallNoticeTracker.End.ENDED) }
                    notifications.onCallRing(s.callSessionId, incomingNotice(s))
                } else if (wasRinging != null && s !is CallState.Incoming) {
                    notifications.onCallEnded(wasRinging.callSessionId, endOf(wasRinging.callSessionId, s))
                }
                when (s) {
                    is CallState.Outgoing, is CallState.Active -> {
                        val video = (s as? CallState.Active)?.callType == CallType.VIDEO || (s as? CallState.Outgoing)?.callType == CallType.VIDEO
                        // Again when the call became video: the camera type is claimed then.
                        if (serviceVideo != video) {
                            CallForegroundService.start(app, ongoingNotice(s), video)
                            serviceVideo = video
                        }
                    }
                    else -> if (serviceVideo != null) {
                        CallForegroundService.stop(app)
                        serviceVideo = null
                    }
                }
                previous = s
            }
        }
    }

    public fun stop() {
        job?.cancel()
        job = null
        rings?.stop()
        CallForegroundService.stop(app)
    }

    public companion object {
        /** The channel of the ongoing-call notice (low importance, silent). */
        public const val ONGOING_CHANNEL: String = "talkwire_call_ongoing"

        /** How a ringing call stopped ringing, for its notice. */
        internal fun endOf(callSessionId: String, next: CallState): CallNoticeTracker.End = when {
            next is CallState.Active && next.callSessionId == callSessionId -> CallNoticeTracker.End.ANSWERED
            next is CallState.Ended -> when (next.reason) {
                CallEndReason.ANSWERED_ELSEWHERE -> CallNoticeTracker.End.ANSWERED_ELSEWHERE
                CallEndReason.REJECTED -> CallNoticeTracker.End.REJECTED
                CallEndReason.CANCELLED -> CallNoticeTracker.End.CANCELLED
                CallEndReason.NO_ANSWER -> CallNoticeTracker.End.TIMED_OUT
                CallEndReason.DISCONNECTED -> CallNoticeTracker.End.CONNECTION_LOST
                else -> CallNoticeTracker.End.ENDED
            }
            else -> CallNoticeTracker.End.ENDED
        }
    }
}
