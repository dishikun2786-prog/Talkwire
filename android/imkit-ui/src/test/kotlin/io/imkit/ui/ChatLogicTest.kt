package io.imkit.ui

import android.content.Context
import android.net.Uri
import androidx.test.core.app.ApplicationProvider
import io.imkit.core.engine.ConversationSummary
import io.imkit.core.model.ChatMessage
import io.imkit.core.model.ConversationId
import io.imkit.core.model.GroupMember
import io.imkit.core.model.GroupRole
import io.imkit.core.model.MessageStatus
import io.imkit.ui.chat.readersOf
import io.imkit.ui.components.ImTime
import io.imkit.ui.components.avatarColor
import io.imkit.ui.components.avatarInitial
import io.imkit.ui.host.ImDeepLink
import io.imkit.ui.host.ImProfile
import io.imkit.ui.host.ImUiHost
import io.imkit.ui.host.ProfileCache
import io.imkit.ui.screens.ImChatState
import io.imkit.ui.screens.ImDestination
import io.imkit.ui.screens.MemberPermissions
import io.imkit.ui.screens.buildRows
import io.imkit.ui.screens.dividerAnchor
import io.imkit.ui.screens.defaultGroupName
import io.imkit.ui.screens.lastLine
import io.imkit.ui.screens.listKey
import io.imkit.ui.screens.listedConversations
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import java.util.TimeZone

@RunWith(RobolectricTestRunner::class)
class ChatLogicTest {
    private val g = ConversationId("grp_x")
    private val day = 86_400_000L
    private val utc = TimeZone.getTimeZone("UTC")

    private fun m(from: String, seq: Long?, ts: Long, localId: String? = null) =
        ChatMessage(g, seq, localId, from, "x", "text", ts = ts, status = if (seq == null) MessageStatus.SENDING else MessageStatus.RECEIVED)

    @Test
    fun rowsPutAvatarsNamesDaysAndTheUnreadDivider() {
        val t = listOf(
            m("a", 1, 10 * day + 1),
            m("a", 2, 10 * day + 2),
            m("b", 3, 10 * day + 3),
            m("b", 4, 11 * day + 1),
            m("me", 5, 11 * day + 2),
        )
        val rows = buildRows(t, "me", isGroup = true, unreadAfter = 2) { x, y -> ImTime.dayDiff(x, y, utc) == 0 }
        assertEquals(listOf(false, true, true, true, true), rows.map { it.showAvatar })
        assertEquals(listOf(true, false, true, true, true), rows.map { it.showName })
        assertEquals(listOf(true, false, false, true, false), rows.map { it.dayHeader })
        assertEquals(listOf(false, false, true, false, false), rows.map { it.unreadHeader })
        // Nothing above the first message to divide from: no divider there.
        assertTrue(buildRows(t, "me", true, unreadAfter = 0) { _, _ -> true }.none { it.unreadHeader })
        // Direct chats show no sender names.
        assertTrue(buildRows(t, "me", false, null) { _, _ -> true }.none { it.showName })
    }

    @Test
    fun theListShowsDirectChatsOnlyWithSomethingInThem() {
        val last = ChatMessage(ConversationId("uid_a"), 1, null, "uid_a", "hi", "text", ts = 1, status = MessageStatus.RECEIVED)
        fun s(id: String, last: ChatMessage? = null, draft: String? = null, pinned: Boolean = false, left: Boolean = false) =
            ConversationSummary(ConversationId(id), null, last, 0, draft, pinned, false, left, 0)
        val shown = listedConversations(
            listOf(
                s("uid_empty"),
                s("uid_msg", last = last),
                s("uid_draft", draft = "typing"),
                s("uid_pinned", pinned = true),
                s("grp_new"),
                s("grp_left", left = true),
                s("grp_left_msgs", last = last, left = true),
            ),
        ).map { it.id.id }.toSet()
        assertEquals(setOf("uid_msg", "uid_draft", "uid_pinned", "grp_new", "grp_left_msgs"), shown)
    }

    @Test
    fun theDividerMarksWhatWasUnreadWhenTheChatOpened() {
        fun summary(unread: Int, lastRead: Long) =
            ConversationSummary(ConversationId("uid_x"), null, null, unread, null, false, false, false, 0, lastReadSeq = lastRead)
        val t = listOf(m("me", 1, 1), m("a", 2, 2), m("a", 3, 3))
        // Two unread at open: the divider goes above the first of them.
        val opened = buildRows(t, "me", false, dividerAnchor(summary(unread = 2, lastRead = 1))) { _, _ -> true }
        assertEquals(listOf(false, true, false), opened.map { it.unreadHeader })
        // Everything read at open (found on a phone): messages arriving while the chat is open get no divider.
        val live = buildRows(t, "me", false, dividerAnchor(summary(unread = 0, lastRead = 1))) { _, _ -> true }
        assertTrue(live.none { it.unreadHeader })
    }

    @Test
    fun aMessageKeepsItsListKeyWhenTheServerStoresIt() {
        val pending = m("me", null, 1, localId = "L1")
        assertEquals(listKey(pending), listKey(pending.copy(seq = 9, status = MessageStatus.SENT)))
        assertEquals("s:4", listKey(m("a", 4, 1)))
    }

    @Test
    fun readersAreMembersPastTheMessageExceptTheSender() {
        val msg = m("me", 5, 1)
        val cursors = mapOf("a" to 5L, "b" to 4L, "c" to 9L, "me" to 9L)
        assertEquals(listOf("a", "c"), readersOf(msg, cursors, listOf("me", "a", "b", "c", "d")))
        assertTrue(readersOf(m("me", null, 1), cursors, listOf("a")).isEmpty())
    }

    @Test
    fun memberPermissionsMirrorTheServer() {
        val owner = GroupMember("o", GroupRole.OWNER, 0)
        val admin = GroupMember("ad", GroupRole.ADMIN, 0)
        val admin2 = GroupMember("ad2", GroupRole.ADMIN, 0)
        val member = GroupMember("m", GroupRole.MEMBER, 0)
        assertEquals(MemberPermissions(true, true, true), MemberPermissions.of(GroupRole.OWNER, "o", admin))
        assertFalse(MemberPermissions.of(GroupRole.OWNER, "o", owner).any)
        assertEquals(MemberPermissions(false, true, false), MemberPermissions.of(GroupRole.ADMIN, "ad", member))
        assertFalse("admins do not remove admins", MemberPermissions.of(GroupRole.ADMIN, "ad", admin2).any)
        assertFalse(MemberPermissions.of(GroupRole.ADMIN, "ad", owner).any)
        assertFalse(MemberPermissions.of(GroupRole.MEMBER, "m", admin).any)
    }

    @Test
    fun deepLinksAndDestinationsRoundTrip() {
        val links = listOf(ImDeepLink.Conversations, ImDeepLink.Conversation(ConversationId("uid_abc")), ImDeepLink.GroupInfo("grp_z"))
        for (l in links) assertEquals(l, ImDeepLink.parse(l.uri))
        assertNull(ImDeepLink.parse(Uri.parse("https://example.com/chat/x")))
        assertNull("group links need a group topic", ImDeepLink.parse(Uri.parse("imkit://group/uid_x")))

        val ds = listOf(
            ImDestination.Conversations,
            ImDestination.Chat(ConversationId("uid_abc")),
            ImDestination.Info(ConversationId("grp_q")),
            ImDestination.Contacts,
            ImDestination.PickMembers(null),
            ImDestination.PickMembers("grp_q"),
        )
        for (d in ds) assertEquals(d, ImChatState.decode(ImChatState.encode(d)))
        assertNull(ImChatState.decode("chat:"))
    }

    @Test
    fun conversationLinesShowDraftsAndGroupSenders() {
        val msg = ChatMessage(g, 3, null, "bob", "hi", "text", ts = 1, status = MessageStatus.RECEIVED)
        fun sum(draft: String? = null, id: ConversationId = g, last: ChatMessage? = msg) =
            ConversationSummary(id, null, last, 0, draft, false, false, false, 1)
        val line = { s: ConversationSummary ->
            lastLine(s, { it.content }, { if (it == "bob") "Bob" else it }, "me", { "[Draft] $it" }, { n, t -> "$n: $t" })
        }
        assertEquals("Bob: hi", line(sum()))
        assertEquals("[Draft] wip", line(sum(draft = "wip")))
        assertEquals("hi", line(sum(id = ConversationId("uid_bob"), last = msg.copy(conversation = ConversationId("uid_bob")))))
        assertEquals("hi", line(sum(last = msg.copy(from = "me"))))
        assertEquals("", line(sum(last = null)))
    }

    @Test
    fun smallHelpers() {
        assertEquals("Ann, Bo", defaultGroupName(listOf("Ann", "Bo")))
        val long = defaultGroupName(List(10) { "Person$it" })
        assertTrue(long.length <= 30 && long.endsWith("…"))
        assertEquals("😀", avatarInitial("😀 smile"))
        assertEquals("Z", avatarInitial(" zed"))
        assertEquals("?", avatarInitial(""))
        val palette = listOf(androidx.compose.ui.graphics.Color.Red, androidx.compose.ui.graphics.Color.Blue)
        assertEquals(avatarColor("Ann", palette), avatarColor("Ann", palette))
        assertEquals("abc123", ProfileCache.shortUid("uid_abc123xyz"))
    }

    @Test
    fun timesFollowTheCalendarDay() {
        val ctx = ApplicationProvider.getApplicationContext<Context>()
        val now = 20 * day + 12 * 3_600_000L
        assertEquals("Now", ImTime.conversationTime(ctx, now - 10_000, now, utc))
        assertEquals("5 min", ImTime.conversationTime(ctx, now - 5 * 60_000, now, utc))
        assertEquals("Yesterday", ImTime.conversationTime(ctx, now - day, now, utc))
        assertEquals("", ImTime.conversationTime(ctx, 0, now, utc))
        assertEquals("Today", ImTime.chatDate(ctx, now - 3_600_000, now, utc))
        // 23:59 yesterday is yesterday even though it is under 24 h ago.
        assertEquals(1, ImTime.dayDiff(20 * day - 60_000, now, utc))
        assertEquals("Last seen 3 min ago", ImTime.lastSeen(ctx, now - 3 * 60_000, now))
        assertEquals("", ImTime.lastSeen(ctx, now + 1, now))
    }

    private class Host(private val answers: MutableMap<String, ImProfile>) : ImUiHost {
        val calls = mutableListOf<Set<String>>()
        var gate: CompletableDeferred<Unit>? = null
        override val contacts: StateFlow<List<ImProfile>> = MutableStateFlow(listOf(ImProfile("c1", "Contact")))

        override suspend fun profiles(uids: Collection<String>): Map<String, ImProfile> {
            calls += uids.toSet()
            gate?.await()
            return uids.mapNotNull { answers[it] }.associateBy { it.uid }
        }
    }

    @OptIn(ExperimentalCoroutinesApi::class)
    @Test
    fun profilesAreFetchedOnceAndMissesWait() = runTest {
        var now = 1_000L
        val host = Host(mutableMapOf("a" to ImProfile("a", "Ann")))
        val scope = TestScope(StandardTestDispatcher(testScheduler))
        val cache = ProfileCache(host, scope, clock = { now }, retryAfterMs = 60_000)
        scope.advanceUntilIdle()
        assertEquals("contacts seed the cache", "Contact", cache.name("c1"))

        host.gate = CompletableDeferred()
        cache.request(listOf("a", "b", "c1"))
        cache.request(listOf("a", "b"))
        scope.advanceUntilIdle()
        assertEquals("in-flight uids are not asked twice", listOf(setOf("a", "b")), host.calls)
        host.gate!!.complete(Unit)
        scope.advanceUntilIdle()
        assertEquals("Ann", cache.name("a"))
        assertEquals("b", cache.name("b"))

        cache.request(listOf("a", "b"))
        scope.advanceUntilIdle()
        assertEquals("a miss is not retried at once", 1, host.calls.size)
        now += 60_000
        cache.request(listOf("b"))
        scope.advanceUntilIdle()
        assertEquals(listOf(setOf("a", "b"), setOf("b")), host.calls)
        scope.cancel()
    }
}

private fun TestScope.cancel() = coroutineContext[kotlinx.coroutines.Job]?.cancel()
