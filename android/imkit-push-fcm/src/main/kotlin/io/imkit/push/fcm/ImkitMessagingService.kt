package io.imkit.push.fcm

import com.google.firebase.messaging.FirebaseMessagingService
import com.google.firebase.messaging.RemoteMessage
import io.imkit.core.notify.PushNotice

/**
 * Subclass this and declare it in the app's manifest with the
 * `com.google.firebase.MESSAGING_EVENT` intent filter. The IM server sends
 * data-only pushes (spec 17), which always reach [onMessageReceived], even in
 * the background; [onImPush] gets them parsed, everything else goes to
 * [onOtherMessage].
 */
public abstract class ImkitMessagingService : FirebaseMessagingService() {
    final override fun onMessageReceived(message: RemoteMessage) {
        val notice = PushNotice.parse(message.data)
        if (notice != null) onImPush(notice) else onOtherMessage(message)
    }

    final override fun onNewToken(token: String) {
        onTokenChanged(token)
    }

    /**
     * An offline message, an encrypted message (no preview) or a group-join
     * request. The app is usually not connected now: post a notice, and let
     * the next connection's sync fetch the message itself.
     */
    public abstract fun onImPush(notice: PushNotice)

    /** A push that is not ImKit's. */
    public open fun onOtherMessage(message: RemoteMessage) {}

    /**
     * The token changed. If a session is connected, call
     * `PushRegistration.registerNow()`; otherwise nothing is needed, since the
     * token is registered again after every login.
     */
    public open fun onTokenChanged(token: String) {}
}
