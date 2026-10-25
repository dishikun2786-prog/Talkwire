package io.imkit.core.notify

import io.imkit.core.model.ConversationId
import io.imkit.core.notify.NotificationDecider.Decision
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class NotificationsTest {
    private val direct = ConversationId("uid_gbY32PzSxto")
    private val group = ConversationId("grp_abcdefabcdef")

    private fun input(
        c: ConversationId = direct,
        foreground: Boolean = false,
        open: ConversationId? = null,
        muted: Boolean = false,
        suppress: Boolean = false,
        now: Long = 100_000,
        lastAlert: Long = 0,
    ) = NotificationDecider.Input(c, muted, foreground, open, suppress, now, lastAlert)

    @Test
    fun c6_nothingWhileLookingAtThatConversation() {
        assertTrue(NotificationDecider.decide(input(foreground = true, open = direct)) is Decision.Suppress)
        // The same conversation open but the app in the background: notify.
        assertTrue(NotificationDecider.decide(input(foreground = false, open = direct)) is Decision.SystemNotification)
        // Another conversation on screen: banner.
        assertEquals(Decision.Banner(true), NotificationDecider.decide(input(foreground = true, open = group)))
    }

    @Test
    fun c6_mutedAndHostSuppression() {
        assertTrue(NotificationDecider.decide(input(muted = true)) is Decision.Suppress)
        assertTrue(NotificationDecider.decide(input(suppress = true)) is Decision.Suppress)
    }

    @Test
    fun c6_soundIsThrottledPerKind() {
        assertEquals(Decision.SystemNotification(false), NotificationDecider.decide(input(now = 101_000, lastAlert = 100_000)))
        assertEquals(Decision.SystemNotification(true), NotificationDecider.decide(input(now = 102_001, lastAlert = 100_000)))
        assertEquals(Decision.SystemNotification(false), NotificationDecider.decide(input(c = group, now = 109_000, lastAlert = 100_000)))
        assertEquals(Decision.SystemNotification(true), NotificationDecider.decide(input(c = group, now = 110_001, lastAlert = 100_000)))
    }

    @Test
    fun c6_callNoticeIsCancelledOnEveryEnd() {
        for (how in CallNoticeTracker.End.values()) {
            val t = CallNoticeTracker()
            assertTrue(t.ring("c1"))
            assertFalse("shown once", t.ring("c1"))
            assertTrue("$how cancels", t.end("c1", how))
            assertFalse("nothing left after $how", t.end("c1", how))
        }
        val t = CallNoticeTracker()
        t.ring("a")
        t.ring("b")
        assertEquals(listOf("a", "b"), t.endAll())
        assertTrue(t.active().isEmpty())
    }

    @Test
    fun parsesTheServerPushPayload() {
        val m = PushNotice.parse(mapOf("topic" to "uid_x", "isGroup" to "false", "senderName" to "Alice", "preview" to "hi"))!!
        assertEquals(PushNotice.Kind.MESSAGE, m.kind)
        assertEquals("Alice", m.senderName)
        val e = PushNotice.parse(mapOf("topic" to "uid_x", "isGroup" to "false", "type" to "e2e_message", "e2e" to "true"))!!
        assertEquals(PushNotice.Kind.E2E_MESSAGE, e.kind)
        assertNull(e.preview)
        val j = PushNotice.parse(mapOf("topic" to "grp_x", "isGroup" to "true", "type" to "group_join", "groupName" to "G", "preview" to "x wants in"))!!
        assertEquals(PushNotice.Kind.GROUP_JOIN, j.kind)
        assertTrue(j.isGroup)
        assertNull(PushNotice.parse(mapOf("other" to "x")))
    }
}
