package io.talkwire.sample

import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.provider.Settings
import io.talkwire.android.crypto.AndroidKeystoreKeyWrapper
import io.talkwire.android.media.MediaSender
import io.talkwire.android.notify.CallNotification
import io.talkwire.android.notify.ImNotifier
import io.talkwire.android.notify.MessageNotification
import io.talkwire.android.notify.NotificationController
import io.talkwire.android.notify.SoundPoolAlertPlayer
import io.talkwire.call.CallManager
import io.talkwire.call.CallSystem
import io.talkwire.call.CallType
import io.talkwire.call.NoMediaEngine
import io.talkwire.call.OngoingCallNotice
import io.talkwire.core.client.ImClient
import io.talkwire.core.client.ImClientConfig
import io.talkwire.core.client.TokenProvider
import io.talkwire.core.engine.ChatEngine
import io.talkwire.core.engine.EngineConfig
import io.talkwire.core.media.MediaUploader
import io.talkwire.examples.redpacket.RedPacketApi
import io.talkwire.examples.redpacket.RedPacketSender
import io.talkwire.examples.redpacket.RedPacketStore
import io.talkwire.examples.redpacket.RedPacketType
import io.talkwire.groupjoin.GroupJoinClient
import io.talkwire.storage.room.RoomChatStore
import io.talkwire.ui.host.ImDeepLink
import io.talkwire.ui.message.MessageTypeRegistry
import io.talkwire.ui.screens.ImBannerState
import io.talkwire.videocompress.Media3VideoCompressor
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.launch
import okhttp3.OkHttpClient

/**
 * Everything Talkwire needs for one signed-in account. This is the whole
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

    /**
     * Calls (optional module talkwire-call). The sample has no RTC service, so
     * the media engine only logs: ringing, answering, the call screen and the
     * notices work, sound and video do not. An app passes a real adapter,
     * such as talkwire-call-dingrtc's DingRtcMediaEngine.
     */
    val calls = CallManager(client, engine.me, NoMediaEngine(), scope)

    /** Joining groups by QR code (optional module talkwire-group-join). */
    val groupJoin = GroupJoinClient(client)

    val host = SampleHost(
        http,
        tokens,
        MediaSender(app, engine, MediaUploader(SampleConfig.HOST_URL, tokens, http), videoCompressor = Media3VideoCompressor(app)),
        redPacketSender,
        app.getString(R.string.sample_red_packet),
        calls,
        groupJoin,
        engine,
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

    private val callSystem = CallSystem(
        app,
        calls,
        notifications,
        scope,
        incomingNotice = { ring ->
            CallNotification(
                title = ring.callerName ?: app.getString(R.string.sample_someone),
                text = app.getString(if (ring.callType == CallType.VIDEO) R.string.sample_incoming_video else R.string.sample_incoming_voice),
                fullScreenIntent = appIntent(),
                contentIntent = appIntent(),
            )
        },
        ongoingNotice = {
            OngoingCallNotice(
                app.getString(R.string.sample_ongoing_call),
                app.getString(R.string.sample_ongoing_call_text),
                R.drawable.ic_sample_notification,
                appIntent(),
            )
        },
    )

    fun start() {
        notifier.createChannels()
        notifications.start()
        calls.start()
        callSystem.start()
        scope.launch {
            engine.start()
            runCatching { host.refreshContacts() }
        }
    }

    fun close() {
        callSystem.stop()
        calls.stop()
        notifications.stop()
        client.stop()
        engine.close()
        scope.cancel()
    }

    /** Brings the app to the front as it was; the call screen shows over whatever is open. */
    private fun appIntent(): PendingIntent {
        val i = Intent(app, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP)
        return PendingIntent.getActivity(app, 0, i, PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT)
    }

    private fun openIntent(link: ImDeepLink): PendingIntent {
        val i = Intent(Intent.ACTION_VIEW, link.uri, app, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP)
        return PendingIntent.getActivity(app, link.uri.hashCode(), i, PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT)
    }
}
