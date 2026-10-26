package io.talkwire.call

import android.Manifest
import android.app.Application
import androidx.compose.foundation.layout.Column
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.printToString
import androidx.test.core.app.ApplicationProvider
import io.talkwire.core.ImIds
import io.talkwire.core.client.ImClient
import io.talkwire.core.client.ImClientConfig
import io.talkwire.core.client.TokenProvider
import io.talkwire.core.json.JsonObject
import io.talkwire.core.json.JsonObjectBuilder
import io.talkwire.core.json.jsonObject
import io.talkwire.core.testing.FakeServer
import io.talkwire.core.testing.eventually
import io.talkwire.core.testing.loginOk
import io.talkwire.ui.theme.ImTheme
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.runBlocking
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Before
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.Shadows.shadowOf
import java.util.Locale
import java.util.concurrent.CopyOnWriteArrayList
import kotlin.random.Random

/** The call screen and buttons against a scripted server, with the permissions granted. */
@RunWith(RobolectricTestRunner::class)
class CallScreensTest {
    @get:Rule
    val rule = createComposeRule()

    private val scope = CoroutineScope(Dispatchers.Default + SupervisorJob())
    private val bob = ImIds.uid("bob")

    @Before
    fun grant() {
        shadowOf(ApplicationProvider.getApplicationContext<Application>()).grantPermissions(Manifest.permission.RECORD_AUDIO, Manifest.permission.CAMERA)
    }

    @After
    fun tearDown() = scope.cancel()

    private fun JsonObjectBuilder.creds() {
        put("channelId", "ch-1")
        put("rtcToken", "rtc")
        put("appId", "app")
        put("denoiseEnabled", true)
    }

    private fun reply(f: JsonObject, extra: JsonObjectBuilder.() -> Unit = {}) = jsonObject {
        put("id", f.string("id"))
        put("action", f.string("action"))
        put("code", 200)
        extra()
    }

    private fun manager(server: FakeServer, user: String, media: CallMediaEngine = NoMediaEngine()): CallManager = runBlocking {
        val uid = ImIds.uid(user)
        val handle = server.onFrame
        server.onFrame = { c, f -> if (f.string("action") == "login") c.push(loginOk(uid)) else handle(c, f) }
        val client = ImClient(
            ImClientConfig(endpoints = listOf("ws://fake"), tokenProvider = TokenProvider { user }, transport = server, requestTimeoutMs = 2_000, random = Random(1)),
            scope,
        )
        CallManager(client, uid, media, scope, endedHoldMs = 60_000).also {
            it.start()
            client.start()
            client.awaitConnected(5_000)
        }
    }

    private fun sent(server: FakeServer, action: String) = server.sent.filter { it.string("action") == action }

    private fun waitForText(text: String) {
        try {
            rule.waitUntil(5_000) { rule.onAllNodesWithText(text).fetchSemanticsNodes().isNotEmpty() }
        } catch (e: androidx.compose.ui.test.ComposeTimeoutException) {
            throw AssertionError(listOf("'$text' not shown", rule.onRoot().printToString()).joinToString(System.lineSeparator()), e)
        }
    }

    @Test
    fun aRingingVideoCallIsAnswered() {
        val server = FakeServer { c, f -> if (f.string("action") == "call_accept") c.push(reply(f) { put("callSessionId", f.string("callSessionId")); creds() }) }
        val media = NoMediaEngine()
        val m = manager(server, "bob", media)
        rule.setContent { ImTheme(darkTheme = false) { CallScreen(m) } }
        server.connections.last().push(jsonObject { put("type", "call_ring"); put("callSessionId", "cs-1"); put("callType", "video"); put("callerName", "Alice"); put("ringTimeoutSeconds", 45) })
        waitForText("Incoming video call")
        rule.onNodeWithText("Alice").assertExists()
        rule.onNodeWithText("Answer with voice").assertExists()
        rule.onNodeWithContentDescription("Answer").performClick()
        eventually { m.state.value is CallState.Active }
        assertEquals(null, sent(server, "call_accept").single().bool("acceptAsVoice"))
        eventually { "join video ch-1" in media.log }
        waitForText("Hang up")
        rule.onNodeWithText("Turn camera off").assertExists()
        rule.onNodeWithText("Waiting for their video…").assertExists()
    }

    @Test
    fun theButtonsCallAndTheScreenCancels() {
        val server = FakeServer { c, f ->
            when (f.string("action")) {
                "call_invite" -> c.push(reply(f) { put("topic", f.string("topic")); put("callSessionId", "cs-2") })
                "call_cancel" -> c.push(reply(f) { put("callSessionId", f.string("callSessionId")) })
            }
        }
        val m = manager(server, "alice")
        val refused = CopyOnWriteArrayList<String>()
        rule.setContent {
            ImTheme(darkTheme = false) {
                Column {
                    CallButtons(m, bob, onRefused = { refused += it })
                    CallScreen(m, nameOf = { if (it == bob) "Bob" else null })
                }
            }
        }
        rule.onNodeWithContentDescription("Voice call").performClick()
        waitForText("Calling…")
        rule.onNodeWithText("Bob").assertExists()
        assertEquals("voice", sent(server, "call_invite").single().string("callType"))
        rule.onNodeWithContentDescription("Cancel").performClick()
        eventually { sent(server, "call_cancel").size == 1 }
        waitForText("Cancelled")
        assertEquals(emptyList<String>(), refused.toList())
    }

    @Test
    fun aRefusedCallSaysWhy() {
        val server = FakeServer { c, f ->
            if (f.string("action") == "call_invite") c.push(jsonObject { put("id", f.string("id")); put("action", "call_invite"); put("code", 409); put("reason", "offline") })
        }
        val m = manager(server, "alice")
        val refused = CopyOnWriteArrayList<String>()
        rule.setContent { ImTheme(darkTheme = false) { CallButtons(m, bob, onRefused = { refused += it }) } }
        rule.onNodeWithContentDescription("Video call").performClick()
        eventually { refused.isNotEmpty() }
        assertEquals(listOf("They are offline"), refused.toList())
    }

    @Test
    fun anUnknownEndReasonReadsAsAPlainEnd() {
        val server = FakeServer { c, f -> if (f.string("action") == "call_accept") c.push(reply(f) { put("callSessionId", f.string("callSessionId")); creds() }) }
        val m = manager(server, "bob")
        rule.setContent { ImTheme(darkTheme = false) { CallScreen(m) } }
        val conn = server.connections.last()
        conn.push(jsonObject { put("type", "call_ring"); put("callSessionId", "cs-3"); put("callType", "voice"); put("ringTimeoutSeconds", 45) })
        waitForText("Incoming voice call")
        rule.onNodeWithContentDescription("Answer").performClick()
        eventually { m.state.value is CallState.Active }
        waitForText("Switch to video")
        conn.push(jsonObject { put("type", "call_ended"); put("callSessionId", "cs-3"); put("reason", "host_said_so") })
        waitForText("Call ended")
        rule.onNodeWithText("Someone").assertExists()
    }

    @Test
    fun theScreenFollowsTheKitsLanguage() {
        val server = FakeServer()
        val m = manager(server, "bob")
        rule.setContent { ImTheme(darkTheme = false, locale = Locale.SIMPLIFIED_CHINESE) { CallScreen(m) } }
        server.connections.last().push(jsonObject { put("type", "call_ring"); put("callSessionId", "cs-4"); put("callType", "voice"); put("ringTimeoutSeconds", 45) })
        waitForText("邀请你语音通话")
        rule.onNodeWithText("接听").assertExists()
        rule.onNodeWithText("对方").assertExists()
    }
}
