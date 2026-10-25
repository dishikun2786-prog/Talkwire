package io.imkit.android.notify

import android.app.Notification
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import androidx.test.core.app.ApplicationProvider
import io.imkit.core.ImIds
import io.imkit.core.client.ImClient
import io.imkit.core.client.ImClientConfig
import io.imkit.core.client.TokenProvider
import io.imkit.core.engine.ChatEngine
import io.imkit.core.engine.ChatEvent
import io.imkit.core.engine.EngineConfig
import io.imkit.core.model.ChatMessage
import io.imkit.core.model.ConversationId
import io.imkit.core.model.MessageStatus
import io.imkit.core.notify.CallNoticeTracker
import io.imkit.core.store.InMemoryChatStore
import io.imkit.core.testing.MiniServer
import io.imkit.core.testing.eventually
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.Shadows.shadowOf
import java.util.concurrent.CopyOnWriteArrayList
import kotlin.random.Random

/** C6 on Android: what gets posted, what rings, and call notices cancelled on every end. */
@RunWith(RobolectricTestRunner::class)
class NotificationControllerTest {
    private val ctx = ApplicationProvider.getApplicationContext<Context>()
    private val manager = ctx.getSystemService(NotificationManager::class.java)
    private val scope = CoroutineScope(Dispatchers.Default + SupervisorJob())
    private val server = MiniServer()
    private val alice = ImIds.uid("alice")
    private val bob = ImIds.uid("bob")
    private val withBob = ConversationId(bob)
    private val group = ConversationId("grp_gggggggggggg")
    private val channels = ImNotifier.Channels("Messages", "Calls", android.R.drawable.ic_dialog_info)
    private val notifier = ImNotifier(ctx, channels)
    private var now = 100_000L
    private var rings = 0
    private val banners = CopyOnWriteArrayList<ChatMessage>()
    private lateinit var engine: ChatEngine
    private lateinit var controller: NotificationController

    @Before
    fun setUp() = runBlocking {
        notifier.createChannels()
        val client = ImClient(
            ImClientConfig(
                endpoints = listOf("ws://mini"),
                tokenProvider = TokenProvider { "alice" },
                transport = server.transport,
                reconnectBaseDelayMs = 20,
                reconnectMaxDelayMs = 40,
                random = Random(1),
            ),
            scope,
        )
        engine = ChatEngine(client, InMemoryChatStore(), "alice", scope, EngineConfig())
        engine.open()
        controller = NotificationController(
            engine,
            notifier,
            { rings++ },
            { m, groupName -> MessageNotification(m.conversation, groupName ?: "Bob", m.content, intent()) },
            { m, _ -> banners += m },
            scope,
            clock = { now },
        )
    }

    @After
    fun tearDown() {
        engine.client.stop()
        scope.cancel()
    }

    private fun intent() = PendingIntent.getActivity(ctx, 0, Intent(Intent.ACTION_VIEW), PendingIntent.FLAG_IMMUTABLE)

    private fun msg(c: ConversationId = withBob, text: String = "hi") =
        ChatMessage(c, 1, null, bob, text, "text", ts = 1, status = MessageStatus.RECEIVED)

    private fun posted(c: ConversationId): Notification? =
        shadowOf(manager).activeNotifications.firstOrNull { it.tag == c.id && it.id == ImNotifier.MESSAGE_ID }?.notification

    /**
     * C6 in one place. Messages: the decision table, with silent channels and
     * the sound played by the app whenever something is shown. Calls: one
     * notice per call, gone on every way the call can end and on logout.
     */
    @Test
    fun C6_NotificationDecision() = runBlocking {
        for (id in listOf(channels.messages, channels.calls)) {
            val ch = manager.getNotificationChannel(id)
            assertNotNull(ch)
            assertNull("sound is the app's", ch.sound)
            assertFalse(ch.shouldVibrate())
        }

        data class Row(
            val foreground: Boolean,
            val open: ConversationId?,
            val muted: Boolean,
            val suppressAll: Boolean,
            val notice: Boolean,
            val banner: Boolean,
        )
        engine.ensureConversation(withBob)
        val rows = listOf(
            Row(foreground = false, open = null, muted = false, suppressAll = false, notice = true, banner = false),
            Row(foreground = false, open = withBob, muted = false, suppressAll = false, notice = true, banner = false), // on screen, app behind
            Row(foreground = true, open = group, muted = false, suppressAll = false, notice = false, banner = true),
            Row(foreground = true, open = null, muted = false, suppressAll = false, notice = false, banner = true),
            Row(foreground = true, open = withBob, muted = false, suppressAll = false, notice = false, banner = false),
            Row(foreground = false, open = null, muted = true, suppressAll = false, notice = false, banner = false),
            Row(foreground = true, open = group, muted = true, suppressAll = false, notice = false, banner = false),
            Row(foreground = false, open = null, muted = false, suppressAll = true, notice = false, banner = false),
            Row(foreground = true, open = group, muted = false, suppressAll = true, notice = false, banner = false),
        )
        for (r in rows) {
            manager.cancelAll()
            banners.clear()
            rings = 0
            now += 60_000 // past every throttle window
            controller.appInForeground = r.foreground
            controller.openConversation = r.open
            controller.suppressAll = r.suppressAll
            engine.setMuted(withBob, r.muted)
            controller.onMessage(msg(), null)
            assertEquals("notice: $r", r.notice, posted(withBob) != null)
            assertEquals("banner: $r", r.banner, banners.isNotEmpty())
            assertEquals("the app rings exactly when something is shown: $r", if (r.notice || r.banner) 1 else 0, rings)
        }

        for (how in CallNoticeTracker.End.values()) {
            val id = "call-$how"
            val n = CallNotification("Bob", "voice call", intent(), intent())
            controller.onCallRing(id, n)
            controller.onCallRing(id, n)
            val calls = shadowOf(manager).activeNotifications.filter { it.tag == id }
            assertEquals("posted once", 1, calls.size)
            val call = calls.single().notification
            assertEquals(Notification.CATEGORY_CALL, call.category)
            assertNotNull("full screen", call.fullScreenIntent)
            controller.onCallEnded(id, how)
            assertTrue("$how cancels", shadowOf(manager).activeNotifications.none { it.tag == id })
        }
        controller.onCallRing("a", CallNotification("A", "", null, null))
        controller.onCallRing("b", CallNotification("B", "", null, null))
        controller.cancelAllCalls()
        assertTrue(shadowOf(manager).activeNotifications.none { it.id == ImNotifier.CALL_ID })
    }

    @Test
    fun inTheBackgroundAMessageIsPostedAndRingsOncePerBurst() {
        controller.onMessage(msg(text = "one"), null)
        assertEquals("one", posted(withBob)!!.extras.getCharSequence(Notification.EXTRA_TEXT).toString())
        assertEquals(1, rings)
        now += 1_000
        controller.onMessage(msg(text = "two"), null)
        assertEquals("replaced, not stacked", "two", posted(withBob)!!.extras.getCharSequence(Notification.EXTRA_TEXT).toString())
        assertEquals("within 2 s: silent", 1, rings)
        now += 2_001
        controller.onMessage(msg(text = "three"), null)
        assertEquals(2, rings)
        // Groups ring at most every 10 s, independently of direct chats.
        controller.onMessage(msg(group), "Team")
        now += 5_000
        controller.onMessage(msg(group), "Team")
        assertEquals(3, rings)
        assertEquals("Team", posted(group)!!.extras.getCharSequence(Notification.EXTRA_TITLE).toString())
    }

    @Test
    fun openingAConversationRemovesItsNotice() {
        controller.onMessage(msg(), null)
        assertNotNull(posted(withBob))
        controller.openConversation = withBob
        assertNull(posted(withBob))
    }

    @Test
    fun messagesArrivingThroughTheEngineAreNoticed() = runBlocking {
        val synced = CopyOnWriteArrayList<ChatEvent>()
        scope.launch(start = CoroutineStart.UNDISPATCHED) { engine.events.collect { if (it is ChatEvent.Synced) synced += it } }
        controller.start()
        engine.client.start()
        eventually { synced.isNotEmpty() }
        server.pushTo(
            alice,
            io.imkit.core.json.jsonObject {
                put("action", "data"); put("topic", bob); put("from", bob); put("seq", 1); put("ts", 5)
                put("content", "live"); put("contentType", "text")
            },
        )
        eventually { posted(withBob) != null }
        assertEquals("live", posted(withBob)!!.extras.getCharSequence(Notification.EXTRA_TEXT).toString())
        assertEquals(1, rings)
    }
}
