package io.talkwire.android.notify

import android.annotation.SuppressLint
import android.app.PendingIntent
import android.content.Context
import android.graphics.Bitmap
import androidx.annotation.DrawableRes
import androidx.core.app.NotificationChannelCompat
import androidx.core.app.NotificationCompat
import androidx.core.app.NotificationManagerCompat
import io.talkwire.core.model.ConversationId

/** A message notice; the host supplies the names, since it owns profiles. */
public class MessageNotification(
    public val conversation: ConversationId,
    public val title: CharSequence,
    public val text: CharSequence,
    public val contentIntent: PendingIntent?,
    public val largeIcon: Bitmap? = null,
)

/** An incoming-call notice. [fullScreenIntent] opens the call screen over the lock screen. */
public class CallNotification(
    public val title: CharSequence,
    public val text: CharSequence,
    public val fullScreenIntent: PendingIntent?,
    public val contentIntent: PendingIntent?,
    public val actions: List<NotificationCompat.Action> = emptyList(),
)

/**
 * Posts and cancels notices. Both channels are silent on purpose: the sound
 * is played by the app ([AlertPlayer]), because some vendor ROMs never play an
 * app channel's sound whatever its settings (C6).
 *
 * Message notices are keyed by conversation, one per conversation, replaced
 * by the next; call notices by call session. Posting needs the app's
 * POST_NOTIFICATIONS permission on Android 13+; without it the post methods
 * return false.
 */
public class ImNotifier(
    context: Context,
    private val channels: Channels,
) {
    public data class Channels(
        val messagesName: CharSequence,
        val callsName: CharSequence,
        @DrawableRes val smallIcon: Int,
        val messages: String = "talkwire_messages",
        val calls: String = "talkwire_calls",
    )

    private val app = context.applicationContext
    private val manager = NotificationManagerCompat.from(app)

    /** Creates (or updates the names of) both channels. Call once at start-up. */
    public fun createChannels() {
        manager.createNotificationChannel(
            NotificationChannelCompat.Builder(channels.messages, NotificationManagerCompat.IMPORTANCE_HIGH)
                .setName(channels.messagesName)
                .setSound(null, null)
                .setVibrationEnabled(false)
                .build(),
        )
        manager.createNotificationChannel(
            NotificationChannelCompat.Builder(channels.calls, NotificationManagerCompat.IMPORTANCE_HIGH)
                .setName(channels.callsName)
                .setSound(null, null)
                .setVibrationEnabled(false)
                .build(),
        )
    }

    @SuppressLint("MissingPermission") // checked through areNotificationsEnabled
    public fun showMessage(n: MessageNotification): Boolean {
        if (!manager.areNotificationsEnabled()) return false
        val notification = NotificationCompat.Builder(app, channels.messages)
            .setSmallIcon(channels.smallIcon)
            .setContentTitle(n.title)
            .setContentText(n.text)
            .setLargeIcon(n.largeIcon)
            .setContentIntent(n.contentIntent)
            .setAutoCancel(true)
            .setSilent(true)
            .setCategory(NotificationCompat.CATEGORY_MESSAGE)
            .setPriority(NotificationCompat.PRIORITY_HIGH)
            .build()
        manager.notify(n.conversation.id, MESSAGE_ID, notification)
        return true
    }

    public fun cancelConversation(conversation: ConversationId) {
        manager.cancel(conversation.id, MESSAGE_ID)
    }

    @SuppressLint("MissingPermission") // checked through areNotificationsEnabled
    public fun showIncomingCall(callSessionId: String, n: CallNotification): Boolean {
        if (!manager.areNotificationsEnabled()) return false
        val builder = NotificationCompat.Builder(app, channels.calls)
            .setSmallIcon(channels.smallIcon)
            .setContentTitle(n.title)
            .setContentText(n.text)
            .setContentIntent(n.contentIntent)
            .setOngoing(true)
            .setSilent(true)
            .setCategory(NotificationCompat.CATEGORY_CALL)
            .setPriority(NotificationCompat.PRIORITY_MAX)
        n.fullScreenIntent?.let { builder.setFullScreenIntent(it, true) }
        n.actions.forEach { builder.addAction(it) }
        manager.notify(callSessionId, CALL_ID, builder.build())
        return true
    }

    public fun cancelCall(callSessionId: String) {
        manager.cancel(callSessionId, CALL_ID)
    }

    public companion object {
        public const val MESSAGE_ID: Int = 0x1a01
        public const val CALL_ID: Int = 0x1a02
    }
}
