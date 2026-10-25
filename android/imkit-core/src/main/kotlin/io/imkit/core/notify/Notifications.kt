package io.imkit.core.notify

import io.imkit.core.client.ConnectionState
import io.imkit.core.client.ImClient
import io.imkit.core.client.registerPushToken
import io.imkit.core.client.unregisterPushToken
import io.imkit.core.model.ConversationId
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.launch

/**
 * The decision half of notifications (C6), separate from rendering so it can
 * be tested without Android.
 *
 * Rules:
 * - nothing while the user is looking at that very conversation with the app
 *   in front;
 * - nothing for a muted conversation, or while the host suppresses all
 *   notices (for example a privacy screen is up);
 * - an in-app banner when the app is in front, a system notification otherwise;
 * - the sound is always played by the app, never by the notification channel
 *   (some vendor ROMs silence app channels whatever their settings), and it is
 *   throttled so a burst rings once: 2 s for direct chats, 10 s for groups.
 */
public object NotificationDecider {
    public const val THROTTLE_DIRECT_MS: Long = 2_000L
    public const val THROTTLE_GROUP_MS: Long = 10_000L

    public data class Input(
        val conversation: ConversationId,
        val muted: Boolean,
        val appInForeground: Boolean,
        /** The conversation on screen, if any. */
        val openConversation: ConversationId?,
        /** Host policy: suppress every notice (privacy screen, do-not-disturb). */
        val suppressAll: Boolean,
        val now: Long,
        /** When the app last played the alert sound; 0 for never. */
        val lastAlertAt: Long,
    )

    public sealed interface Decision {
        public data class Suppress(val reason: String) : Decision

        public data class Banner(val playSound: Boolean) : Decision

        public data class SystemNotification(val playSound: Boolean) : Decision
    }

    public fun decide(input: Input): Decision {
        if (input.suppressAll) return Decision.Suppress("suppressed by host")
        if (input.muted) return Decision.Suppress("muted")
        if (input.appInForeground && input.openConversation == input.conversation) return Decision.Suppress("conversation on screen")
        val window = if (input.conversation.isGroup) THROTTLE_GROUP_MS else THROTTLE_DIRECT_MS
        val sound = input.lastAlertAt == 0L || input.now - input.lastAlertAt > window
        return if (input.appInForeground) Decision.Banner(sound) else Decision.SystemNotification(sound)
    }
}

/**
 * Tracks incoming-call notices so one is cancelled on every way a call can
 * end: answered here or elsewhere, rejected, cancelled by the caller, timed
 * out, ended, or the connection lost (C6). A notice left behind would keep
 * ringing for a call that no longer exists.
 */
public class CallNoticeTracker {
    public enum class End { ANSWERED, ANSWERED_ELSEWHERE, REJECTED, CANCELLED, TIMED_OUT, ENDED, CONNECTION_LOST }

    private val shown = LinkedHashSet<String>()

    /** A call started ringing; returns false when its notice is already shown. */
    @Synchronized
    public fun ring(callSessionId: String): Boolean = shown.add(callSessionId)

    /** The call ended in any way; returns true when its notice must be cancelled now. */
    @Synchronized
    public fun end(callSessionId: String, @Suppress("UNUSED_PARAMETER") how: End): Boolean = shown.remove(callSessionId)

    /** The connection is gone: every notice must go. */
    @Synchronized
    public fun endAll(): List<String> = shown.toList().also { shown.clear() }

    @Synchronized
    public fun active(): Set<String> = shown.toSet()
}

/** What an offline push says (the FCM data map; server `push.Notification.Data`). */
public data class PushNotice(
    val kind: Kind,
    /** The conversation as this recipient keys it: a group topic or the peer's uid. */
    val topic: String,
    val isGroup: Boolean,
    val senderName: String?,
    val senderAvatar: String?,
    val groupName: String?,
    /** The message preview, or the notice text for group-join notices; absent for encrypted messages. */
    val preview: String?,
) {
    public enum class Kind { MESSAGE, E2E_MESSAGE, GROUP_JOIN, UNKNOWN }

    public companion object {
        /** Null when the map is not an ImKit push (no topic). */
        public fun parse(data: Map<String, String>): PushNotice? {
            val topic = data["topic"]?.takeIf { it.isNotEmpty() } ?: return null
            val kind = when (data["type"]) {
                null, "" -> PushNotice.Kind.MESSAGE
                "e2e_message" -> PushNotice.Kind.E2E_MESSAGE
                "group_join" -> PushNotice.Kind.GROUP_JOIN
                else -> PushNotice.Kind.UNKNOWN
            }
            return PushNotice(
                kind = kind,
                topic = topic,
                isGroup = data["isGroup"] == "true",
                senderName = data["senderName"]?.takeIf { it.isNotEmpty() },
                senderAvatar = data["senderAvatar"]?.takeIf { it.isNotEmpty() },
                groupName = data["groupName"]?.takeIf { it.isNotEmpty() },
                preview = data["preview"]?.takeIf { it.isNotEmpty() },
            )
        }
    }
}

/** A push service: FCM, APNs, Web Push. */
public interface PushTokenSource {
    /** `fcm`, `apns` or `webpush` (spec 17). */
    public val provider: String

    /** The current token; null when unavailable (no Play services, not configured). */
    public suspend fun token(): String?
}

/**
 * Registers the push token after every login (spec 17) — not only when the
 * token changes, since at that moment the socket is usually down and the
 * registration would be lost — and unregisters it on logout.
 */
public class PushRegistration(
    private val client: ImClient,
    private val source: PushTokenSource,
    private val scope: CoroutineScope,
) {
    @Volatile private var lastToken: String? = null
    private var job: Job? = null

    public fun start() {
        if (job != null) return
        job = scope.launch {
            client.state.collect { s ->
                if (s is ConnectionState.Connected) registerNow()
            }
        }
    }

    /** Call when the provider reports a new token. */
    public suspend fun registerNow() {
        val token = try {
            source.token()
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            null
        } ?: return
        if (client.registerPushToken(token, source.provider)) lastToken = token
    }

    /** Before logging out, while still connected. */
    public fun unregister() {
        job?.cancel()
        job = null
        lastToken?.let { client.unregisterPushToken(it, source.provider) }
        lastToken = null
    }
}
