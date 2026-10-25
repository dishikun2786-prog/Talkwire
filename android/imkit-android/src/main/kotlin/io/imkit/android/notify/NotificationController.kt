package io.imkit.android.notify

import io.imkit.core.engine.ChatEngine
import io.imkit.core.engine.ChatEvent
import io.imkit.core.model.ChatMessage
import io.imkit.core.model.ConversationId
import io.imkit.core.notify.CallNoticeTracker
import io.imkit.core.notify.NotificationDecider
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.Job
import kotlinx.coroutines.launch

/** Builds the system notice for a message; null to post none. */
public fun interface MessageNotificationFactory {
    public fun create(message: ChatMessage, groupName: String?): MessageNotification?
}

/** Shows the in-app banner (the UI is the app's). */
public fun interface BannerSink {
    public fun show(message: ChatMessage, groupName: String?)
}

/**
 * Turns new messages into notices by [NotificationDecider]'s rules (C6) and
 * keeps call notices in step with the call's life. The app keeps
 * [appInForeground], [openConversation] and [suppressAll] current.
 */
public class NotificationController(
    private val engine: ChatEngine,
    private val notifier: ImNotifier,
    private val alert: AlertPlayer,
    private val notices: MessageNotificationFactory,
    private val banners: BannerSink,
    private val scope: CoroutineScope,
    private val clock: () -> Long = System::currentTimeMillis,
) {
    @Volatile public var appInForeground: Boolean = false

    /** The conversation on screen; opening one also removes its notice. */
    @Volatile public var openConversation: ConversationId? = null
        set(value) {
            field = value
            value?.let { notifier.cancelConversation(it) }
        }

    /** Host policy: no notices at all (privacy screen, do-not-disturb). */
    @Volatile public var suppressAll: Boolean = false

    private val lastAlert = LongArray(2) // [direct, group]
    private val calls = CallNoticeTracker()
    private var job: Job? = null

    public fun start() {
        if (job != null) return
        job = scope.launch(start = CoroutineStart.UNDISPATCHED) {
            engine.events.collect { e -> if (e is ChatEvent.MessageArrived) onMessage(e.message, e.groupName) }
        }
    }

    public fun stop() {
        job?.cancel()
        job = null
    }

    /** One arriving message (also callable directly, e.g. for an offline push). */
    @Synchronized
    public fun onMessage(message: ChatMessage, groupName: String?) {
        val c = message.conversation
        val kind = if (c.isGroup) 1 else 0
        val now = clock()
        val muted = engine.conversations.value.firstOrNull { it.id == c }?.muted ?: false
        val decision = NotificationDecider.decide(
            NotificationDecider.Input(c, muted, appInForeground, openConversation, suppressAll, now, lastAlert[kind]),
        )
        val playSound = when (decision) {
            is NotificationDecider.Decision.Suppress -> return
            is NotificationDecider.Decision.Banner -> {
                banners.show(message, groupName)
                decision.playSound
            }
            is NotificationDecider.Decision.SystemNotification -> {
                notices.create(message, groupName)?.let { notifier.showMessage(it) }
                decision.playSound
            }
        }
        if (playSound) {
            lastAlert[kind] = now
            alert.play()
        }
    }

    /** An incoming call rings: posts its notice once. */
    public fun onCallRing(callSessionId: String, notice: CallNotification) {
        if (calls.ring(callSessionId)) notifier.showIncomingCall(callSessionId, notice)
    }

    /** The call ended in any way: its notice goes (C6). */
    public fun onCallEnded(callSessionId: String, how: CallNoticeTracker.End) {
        if (calls.end(callSessionId, how)) notifier.cancelCall(callSessionId)
    }

    /** For example on logout. */
    public fun cancelAllCalls() {
        calls.endAll().forEach { notifier.cancelCall(it) }
    }
}
