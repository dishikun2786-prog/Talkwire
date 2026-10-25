package io.imkit.sample

import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.provider.Settings
import io.imkit.android.crypto.AndroidKeystoreKeyWrapper
import io.imkit.android.media.MediaSender
import io.imkit.android.notify.ImNotifier
import io.imkit.android.notify.MessageNotification
import io.imkit.android.notify.NotificationController
import io.imkit.android.notify.SoundPoolAlertPlayer
import io.imkit.core.client.ImClient
import io.imkit.core.client.ImClientConfig
import io.imkit.core.client.TokenProvider
import io.imkit.core.engine.ChatEngine
import io.imkit.core.engine.EngineConfig
import io.imkit.core.media.MediaUploader
import io.imkit.examples.redpacket.RedPacketApi
import io.imkit.examples.redpacket.RedPacketSender
import io.imkit.examples.redpacket.RedPacketStore
import io.imkit.examples.redpacket.RedPacketType
import io.imkit.storage.room.RoomChatStore
import io.imkit.ui.host.ImDeepLink
import io.imkit.ui.message.MessageTypeRegistry
import io.imkit.ui.screens.ImBannerState
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.launch
import okhttp3.OkHttpClient

/**
 * Everything ImKit needs for one signed-in account. This is the whole
 * integration an app writes: a token provider, the endpoints, a store,
 * the host adapter ([SampleHost]) and the notification setup.
 */
class ChatSession(context: Context, val account: Account, http: OkHttpClient) {
    private val app = context.applicationContext
    val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)

    private val tokens = TokenProvider { account.token }

    val client = ImClient(
        ImClientConfig(
            endpoints = listOf(SampleConfig.IM_URL),
            tokenProvider = tokens,
            // Stable per install, so a second phone signed in to the same account stays connected too.
            deviceName = "sample-" + (Settings.Secure.getString(app.contentResolver, Settings.Secure.ANDROID_ID) ?: "device").take(16),
        ),
        scope,
    )

    val engine = ChatEngine(
        client,
        RoomChatStore.open(app, keys = AndroidKeystoreKeyWrapper()),
        account.userId,
        scope,
        EngineConfig(),
    )

    private val redPacketApi = RedPacketApi(SampleConfig.HOST_URL, tokens, http)
    val redPackets = RedPacketStore(redPacketApi, client, scope)
    val redPacketSender = RedPacketSender(redPacketApi, engine, redPackets, scope)

    /** The built-in types plus the host's red packet: registering is all it takes. */
    val types: MessageTypeRegistry = MessageTypeRegistry.Builder().register(RedPacketType(redPackets)).build()

    val host = SampleHost(
        http,
        tokens,
        MediaSender(app, engine, MediaUploader(SampleConfig.HOST_URL, tokens, http)),
        redPacketSender,
        app.getString(R.string.sample_red_packet),
    )

    val banners = ImBannerState()

    private val notifier = ImNotifier(app, ImNotifier.Channels(app.getString(R.string.sample_channel_messages), app.getString(R.string.sample_channel_calls), R.drawable.ic_sample_notification))

    val notifications = NotificationController(
        engine,
        notifier,
        SoundPoolAlertPlayer(app, R.raw.sample_ding),
        { message, groupName ->
            val contact = host.contacts.value.firstOrNull { it.uid == message.from }?.name
            MessageNotification(
                conversation = message.conversation,
                title = groupName ?: contact ?: app.getString(R.string.sample_new_message),
                text = types.preview(message, app),
                contentIntent = openIntent(ImDeepLink.Conversation(message.conversation)),
            )
        },
        banners.sink,
        scope,
    )

    fun start() {
        notifier.createChannels()
        notifications.start()
        scope.launch {
            engine.start()
            runCatching { host.refreshContacts() }
        }
    }

    fun close() {
        notifications.stop()
        client.stop()
        engine.close()
        scope.cancel()
    }

    private fun openIntent(link: ImDeepLink): PendingIntent {
        val i = Intent(Intent.ACTION_VIEW, link.uri, app, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP)
        return PendingIntent.getActivity(app, link.uri.hashCode(), i, PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT)
    }
}
