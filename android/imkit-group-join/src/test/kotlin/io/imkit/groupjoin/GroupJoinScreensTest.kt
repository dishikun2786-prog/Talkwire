package io.imkit.groupjoin

import android.content.Context
import androidx.compose.ui.test.assertIsNotEnabled
import androidx.compose.ui.test.assertIsOff
import androidx.compose.ui.test.isToggleable
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.printToString
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performTextInput
import androidx.compose.ui.test.assertCountEquals
import androidx.test.core.app.ApplicationProvider
import io.imkit.core.client.ImClient
import io.imkit.core.client.ImClientConfig
import io.imkit.core.client.TokenProvider
import io.imkit.core.engine.ChatEngine
import io.imkit.core.engine.ChatEvent
import io.imkit.core.json.JsonArray
import io.imkit.core.json.JsonObject
import io.imkit.core.json.jsonObject
import io.imkit.core.model.ConversationId
import io.imkit.core.store.InMemoryChatStore
import io.imkit.core.testing.MiniServer
import io.imkit.core.testing.eventually
import io.imkit.ui.theme.ImTheme
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import java.util.Locale
import java.util.concurrent.CopyOnWriteArrayList
import kotlin.random.Random

/** The scan-to-join screens against an engine on the in-memory server, whose module replies each test scripts. */
@RunWith(RobolectricTestRunner::class)
class GroupJoinScreensTest {
    @get:Rule
    val rule = createComposeRule()

    private val server = MiniServer()
    private val scope = CoroutineScope(Dispatchers.Default + SupervisorJob())
    private val topic = "grp_scan"

    /** Replies to the module's actions; null leaves a frame to the in-memory server. */
    private val replies = HashMap<String, (JsonObject) -> JsonObject?>()

    init {
        server.override = { f -> replies[f.string("action")]?.invoke(f) }
    }

    @After
    fun tearDown() = scope.cancel()

    private fun engine(user: String): ChatEngine = runBlocking {
        val client = ImClient(
            ImClientConfig(endpoints = listOf("ws://mini"), tokenProvider = TokenProvider { user }, transport = server.transport, random = Random(1)),
            scope,
        )
        ChatEngine(client, InMemoryChatStore(), user, scope).also {
            it.open()
            it.start()
            client.awaitConnected(5_000)
        }
    }

    private fun sent(action: String): List<JsonObject> = server.requests.filter { it.string("action") == action }

    /** Waits for [text] on screen; a timeout shows the screen and the requests the server got. */
    private fun waitForText(text: String) {
        try {
            rule.waitUntil(5_000) { rule.onAllNodesWithText(text).fetchSemanticsNodes().isNotEmpty() }
        } catch (e: androidx.compose.ui.test.ComposeTimeoutException) {
            val screen = rule.onRoot().printToString()
            val requests = server.requests.map { it.string("action") }
            throw AssertionError(listOf("'$text' not shown", screen, "requests: $requests").joinToString(System.lineSeparator()), e)
        }
    }

    private fun ok(action: String, fields: io.imkit.core.json.JsonObjectBuilder.() -> Unit = {}): JsonObject = jsonObject {
        put("action", action)
        put("code", 200)
        fields()
    }

    private fun refused(action: String, code: Int, errCode: String? = null) = jsonObject {
        put("action", action)
        put("code", code)
        put("reason", "refused")
        put("errCode", errCode)
    }

    private fun preview(requireApproval: Boolean = false, alreadyMember: Boolean = false): (JsonObject) -> JsonObject = {
        ok("group_qr_resolve") {
            put("topic", topic)
            put("name", "Scan club")
            put("memberCount", 3)
            if (alreadyMember) put("alreadyMember", true)
            if (requireApproval) put("requireApproval", true)
        }
    }

    // ---------------------------------------------------------------- QR card

    @Test
    fun theQrCardShowsTheLinkAndResetsOnlyAfterConfirming() {
        val api = GroupJoinClient(engine("alice").client)
        var n = 0
        replies["group_qr_token"] = { ok("group_qr_token") { put("topic", topic); put("qrToken", "tok${++n}"); put("expiresAt", 1_900_000_000_000L) } }
        val drawn = CopyOnWriteArrayList<String>()
        rule.setContent {
            ImTheme(darkTheme = false) {
                GroupQrCard(api, topic, canRefresh = true, linkFor = { "https://app.example/join/$it" }, qrCode = { drawn += it })
            }
        }
        waitForText("https://app.example/join/tok1")
        assertEquals("https://app.example/join/tok1", drawn.last())
        assertTrue(sent("group_qr_token").single().bool("refreshQr") == null)

        rule.onNodeWithText("Reset QR code").performClick()
        rule.onNodeWithText("Reset the QR code? The old code and its link stop working at once.").assertExists()
        assertEquals(1, sent("group_qr_token").size) // nothing sent before confirming
        rule.onAllNodesWithText("Reset QR code")[1].performClick()
        waitForText("https://app.example/join/tok2")
        assertEquals(true, sent("group_qr_token").last().bool("refreshQr"))
    }

    @Test
    fun aNonMemberSeesWhyThereIsNoCode() {
        val api = GroupJoinClient(engine("alice").client)
        replies["group_qr_token"] = { refused("group_qr_token", 401) }
        rule.setContent { ImTheme(darkTheme = false) { GroupQrCard(api, topic) } }
        waitForText("Only the group owner or admins can do this.")
    }

    // ---------------------------------------------------------------- joining

    @Test
    fun joiningSubscribesAndListsTheGroupThenOpensIt() {
        val bob = engine("bob")
        val api = GroupJoinClient(bob.client)
        replies["group_qr_resolve"] = preview()
        replies["group_join"] = { f ->
            // As a server does: bob is now a member.
            server.groups[topic] = MiniServer.Group("Scan club", mutableMapOf(server.uidOf("alice") to "owner", server.uidOf("bob") to "member"))
            assertEquals(null, f.string("joinMessage")) // nothing to say when no approval is needed
            ok("group_join") { put("topic", topic); put("joinStatus", "joined") }
        }
        val opened = CopyOnWriteArrayList<String>()
        rule.setContent { ImTheme(darkTheme = false) { JoinGroupScreen(api, bob, "tok", onOpen = { opened += it }) } }
        waitForText("Join group")
        rule.onNodeWithText("3 members").assertExists()
        rule.onNodeWithText("Join group").performClick()
        waitForText("Open chat")
        rule.onNodeWithText("You are already in this group.").assertExists()
        rule.onNodeWithText("4 members").assertExists() // the preview's count was taken before I joined

        // The legacy server neither subscribes the joiner nor tells anyone (D7).
        assertTrue(sent("sub").any { it.string("topic") == topic })
        eventually { bob.groups.value.any { it.topic == topic } }
        assertTrue(bob.conversations.value.any { it.id == ConversationId(topic) })
        rule.onNodeWithText("Open chat").performClick()
        assertEquals(listOf(topic), opened)
    }

    @Test
    fun askingToJoinSendsTheMessageCutTo60CodePoints() {
        val bob = engine("bob")
        val api = GroupJoinClient(bob.client)
        replies["group_qr_resolve"] = preview(requireApproval = true)
        replies["group_join"] = { ok("group_join") { put("topic", topic); put("joinStatus", "pending_approval") } }
        rule.setContent { ImTheme(darkTheme = false) { JoinGroupScreen(api, bob, "tok") } }
        waitForText("Ask to join")
        rule.onNodeWithText("A group admin approves new members.").assertExists()
        rule.onNodeWithContentDescription("Message to the admins (optional)").performTextInput("😀".repeat(70))
        rule.onNodeWithText("Ask to join").performClick()
        waitForText("Request sent. You will be told when an admin decides.")
        assertEquals("😀".repeat(60), sent("group_join").single().string("joinMessage"))
        assertTrue(sent("sub").none { it.string("topic") == topic })
    }

    /** Found on a phone: after the admin decided, the screen still said the request was sent. */
    @Test
    fun aPendingRequestFollowsTheAdminsDecision() {
        val bob = engine("bob")
        val api = GroupJoinClient(bob.client)
        replies["group_qr_resolve"] = preview(requireApproval = true)
        replies["group_join"] = { ok("group_join") { put("topic", topic); put("joinStatus", "pending_approval") } }
        val opened = CopyOnWriteArrayList<String>()
        rule.setContent { ImTheme(darkTheme = false) { JoinGroupScreen(api, bob, "tok", onOpen = { opened += it }) } }
        waitForText("Ask to join")
        rule.onNodeWithText("Ask to join").performClick()
        waitForText("Request sent. You will be told when an admin decides.")

        // A decision on another group changes nothing.
        val push = { t: String, status: String ->
            server.transport.connections.last().push(jsonObject { put("action", "group_join_reviewed"); put("topic", t); put("name", "Scan club"); put("joinStatus", status) })
        }
        push("grp_other", "approved")
        rule.waitForIdle()
        rule.onNodeWithText("Request sent. You will be told when an admin decides.").assertExists()

        server.groups[topic] = MiniServer.Group("Scan club", mutableMapOf(server.uidOf("alice") to "owner", server.uidOf("bob") to "member"))
        push(topic, "approved")
        waitForText("You joined Scan club")
        rule.onNodeWithText("4 members").assertExists()
        eventually { sent("sub").any { it.string("topic") == topic } }
        rule.onNodeWithText("Open chat").performClick()
        assertEquals(listOf(topic), opened)
    }

    @Test
    fun aDeclinedRequestSaysSo() {
        val bob = engine("bob")
        val api = GroupJoinClient(bob.client)
        replies["group_qr_resolve"] = preview(requireApproval = true)
        replies["group_join"] = { ok("group_join") { put("topic", topic); put("joinStatus", "pending_approval") } }
        rule.setContent { ImTheme(darkTheme = false) { JoinGroupScreen(api, bob, "tok") } }
        waitForText("Ask to join")
        rule.onNodeWithText("Ask to join").performClick()
        waitForText("Request sent. You will be told when an admin decides.")
        server.transport.connections.last().push(jsonObject { put("action", "group_join_reviewed"); put("topic", topic); put("name", "Scan club"); put("joinStatus", "rejected") })
        waitForText("Your request to join Scan club was declined")
        rule.onAllNodesWithText("Open chat").assertCountEquals(0)
        assertTrue(sent("sub").none { it.string("topic") == topic })
    }

    @Test
    fun aTokenThatNoLongerWorksSaysSo() {
        val bob = engine("bob")
        val api = GroupJoinClient(bob.client)
        replies["group_qr_resolve"] = { refused("group_qr_resolve", 404) }
        rule.setContent { ImTheme(darkTheme = false) { JoinGroupScreen(api, bob, "old") } }
        waitForText("This QR code is invalid or has expired.")
    }

    @Test
    fun aGroupThatFillsUpBeforeJoiningSaysItIsFull() {
        val bob = engine("bob")
        val api = GroupJoinClient(bob.client)
        replies["group_qr_resolve"] = preview()
        replies["group_join"] = { refused("group_join", 409) }
        rule.setContent { ImTheme(darkTheme = false) { JoinGroupScreen(api, bob, "tok") } }
        waitForText("Join group")
        rule.onNodeWithText("Join group").performClick()
        waitForText("This group is full.")
        rule.onAllNodesWithText("Join group").assertCountEquals(0)
    }

    @Test
    fun aMemberIsToldAndCanOpenTheChatAtOnce() {
        val bob = engine("bob")
        val api = GroupJoinClient(bob.client)
        replies["group_qr_resolve"] = preview(alreadyMember = true)
        rule.setContent { ImTheme(darkTheme = false) { JoinGroupScreen(api, bob, "tok", onOpen = {}) } }
        waitForText("Open chat")
        assertTrue(sent("group_join").isEmpty())
    }

    // ---------------------------------------------------------------- managing

    private fun requestList(vararg ids: Long) = ok("group_join_requests_list") {
        if (ids.isNotEmpty()) {
            put(
                "joinRequests",
                JsonArray(
                    ids.map { id ->
                        jsonObject {
                            put("id", id)
                            put("requesterUid", "uid_applicant$id")
                            put("requesterName", "Applicant $id")
                            put("message", "hi from $id")
                            put("createdAt", 1_000L * id)
                        }
                    },
                ),
            )
        }
    }

    @Test
    fun aManagerApprovesAndTheListFollowsPushes() {
        val alice = engine("alice")
        val api = GroupJoinClient(alice.client)
        val changed = CopyOnWriteArrayList<ConversationId>()
        scope.launch { alice.events.collect { if (it is ChatEvent.GroupChanged) changed += it.conversation } }
        var pending = listOf(1L)
        replies["group_join_requests_list"] = { requestList(*pending.toLongArray()) }
        replies["group_join_review"] = { f ->
            pending = pending - f.long("joinRequestId")!!
            ok("group_join_review") { put("joinStatus", if (f.bool("approve") == true) "approved" else "rejected") }
        }
        rule.setContent { ImTheme(darkTheme = false) { JoinRequestsPanel(api, alice, topic) } }
        waitForText("Applicant 1")
        rule.onNodeWithText("hi from 1").assertExists()
        rule.onNodeWithText("Approve").performClick()
        waitForText("No pending requests")
        // The server tells only the applicant (F45): the panel tells this device's engine, so the member list reloads.
        eventually { changed.contains(ConversationId(topic)) }
        val review = sent("group_join_review").single()
        assertEquals(topic, review.string("topic"))
        assertEquals(1L, review.long("joinRequestId"))
        assertEquals(true, review.bool("approve"))

        // A new request is pushed: the panel lists again.
        pending = listOf(2L)
        server.transport.connections.last().push(jsonObject { put("action", "group_join_request"); put("topic", topic); put("name", "Scan club"); put("from", "uid_applicant2") })
        waitForText("Applicant 2")
    }

    @Test
    fun aRequestHandledByAnotherAdminDisappearsWithAReason() {
        val alice = engine("alice")
        val api = GroupJoinClient(alice.client)
        var pending = listOf(1L)
        replies["group_join_requests_list"] = { requestList(*pending.toLongArray()) }
        replies["group_join_review"] = {
            pending = emptyList()
            refused("group_join_review", 409)
        }
        rule.setContent { ImTheme(darkTheme = false) { JoinRequestsPanel(api, alice, topic) } }
        waitForText("Decline")
        rule.onNodeWithText("Decline").performClick()
        waitForText("Another admin already handled this request.")
        rule.onNodeWithText("No pending requests").assertExists()
    }

    @Test
    fun aFullGroupOnAStrictServerKeepsTheRequest() {
        val alice = engine("alice")
        val api = GroupJoinClient(alice.client)
        replies["group_join_requests_list"] = { requestList(1L) }
        replies["group_join_review"] = { refused("group_join_review", 409, errCode = "group_full") }
        rule.setContent { ImTheme(darkTheme = false) { JoinRequestsPanel(api, alice, topic) } }
        waitForText("Approve")
        rule.onNodeWithText("Approve").performClick()
        waitForText("This group is full.")
        rule.onNodeWithText("Applicant 1").assertExists()
        assertEquals(1, sent("group_join_requests_list").size) // not listed again
    }

    @Test
    fun theApprovalSwitchGoesBackWhenRefused() {
        val api = GroupJoinClient(engine("alice").client)
        replies["get_group_join_settings"] = { ok("get_group_join_settings") }
        replies["update_group_join_settings"] = { refused("update_group_join_settings", 401) }
        rule.setContent { ImTheme(darkTheme = false) { JoinApprovalSetting(api, topic, editable = true) } }
        rule.waitUntil(5_000) { sent("get_group_join_settings").isNotEmpty() }
        rule.waitForIdle()
        rule.onNode(isToggleable()).performClick()
        waitForText("Only the group owner or admins can do this.")
        rule.onNode(isToggleable()).assertIsOff()
        assertEquals(true, sent("update_group_join_settings").single().bool("requireApproval"))
    }

    @Test
    fun onlyTheOwnerCanTurnTheSwitch() {
        val api = GroupJoinClient(engine("alice").client)
        replies["get_group_join_settings"] = { ok("get_group_join_settings") { put("requireApproval", true) } }
        rule.setContent { ImTheme(darkTheme = false) { JoinApprovalSetting(api, topic, editable = false) } }
        rule.waitUntil(5_000) { sent("get_group_join_settings").isNotEmpty() }
        rule.waitForIdle()
        rule.onNode(isToggleable()).assertIsNotEnabled()
    }

    // ---------------------------------------------------------------- language

    @Test
    fun theScreensFollowTheKitsLanguage() {
        val bob = engine("bob")
        val api = GroupJoinClient(bob.client)
        replies["group_qr_resolve"] = preview(requireApproval = true)
        rule.setContent { ImTheme(darkTheme = false, locale = Locale.SIMPLIFIED_CHINESE) { JoinGroupScreen(api, bob, "tok") } }
        waitForText("申请加入")
        rule.onNodeWithText("3 人").assertExists()
    }

    @Test
    fun noticesForBannersAndNotifications() {
        val ctx = ApplicationProvider.getApplicationContext<Context>()
        assertEquals("Ann asked to join Scan club", groupJoinNotice(ctx, GroupJoinEvent.Request(topic, "Scan club", "uid_ann")) { "Ann" })
        assertEquals("You joined Scan club", groupJoinNotice(ctx, GroupJoinEvent.Reviewed(topic, "Scan club", approved = true)))
        assertEquals("Your request to join Scan club was declined", groupJoinNotice(ctx, GroupJoinEvent.Reviewed(topic, "Scan club", approved = false)))
    }

    @Test
    fun theMessageLimitNeverSplitsASurrogatePair() {
        assertEquals("ab", limitCodePoints("ab", 60))
        assertEquals("😀😀", limitCodePoints("😀😀😀", 2))
        assertEquals(4, limitCodePoints("😀😀😀", 2).length)
    }
}
