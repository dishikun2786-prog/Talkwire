// SPDX-License-Identifier: AGPL-3.0-only
package io.talkwire.e2ee

import android.content.Context
import android.content.res.Configuration
import androidx.compose.ui.semantics.SemanticsProperties
import androidx.compose.ui.test.SemanticsMatcher
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.onRoot
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performTextInput
import androidx.compose.ui.test.printToString
import androidx.test.core.app.ApplicationProvider
import io.talkwire.core.client.ImClient
import io.talkwire.core.client.ImClientConfig
import io.talkwire.core.client.TokenProvider
import io.talkwire.core.testing.MiniServer
import io.talkwire.core.testing.eventually
import io.talkwire.ui.theme.ImTheme
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.runBlocking
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import java.util.Locale
import kotlin.random.Random

@RunWith(RobolectricTestRunner::class)
class E2eeScreensTest {
    @get:Rule
    val rule = createComposeRule()

    private val context = ApplicationProvider.getApplicationContext<Context>()
    private val scope = CoroutineScope(Dispatchers.Default + SupervisorJob())
    private val server = MiniServer()
    private val relay = TestRelay(server)
    private val all = mutableListOf<Pair<E2eeChats, E2eeDatabase>>()

    @After
    fun tearDown() {
        all.forEach { (c, _) -> c.client.stop(); c.stop() }
        scope.cancel()
        all.forEach { (_, db) -> db.close() }
    }

    private fun chats(user: String): E2eeChats = runBlocking {
        val client = ImClient(
            ImClientConfig(endpoints = listOf("ws://mini"), tokenProvider = TokenProvider { user }, transport = server.transport, random = Random(user.hashCode())),
            scope,
        )
        val db = E2eeDatabase.inMemory(context)
        val chats = E2eeChats(client, server.uidOf(user), db, scope, TestWrapper())
        chats.start()
        client.start()
        client.awaitConnected(5_000)
        eventually { relay.bundles.containsKey(server.uidOf(user)) }
        chats.also { all += it to db }
    }

    private fun localized(lang: String): Context =
        context.createConfigurationContext(Configuration(context.resources.configuration).apply { setLocale(Locale(lang)) })

    /** Red line C10: each class has its own text, and only being offline is a reason to try again. */
    @Test
    fun C10_E2EEFailureReasons() {
        val retry = mapOf("en" to "try again", "zh" to "重试", "vi" to "thử lại")
        for ((lang, word) in retry) {
            val c = localized(lang)
            val texts = E2eeFailure.values().associateWith { e2eeFailureText(c, it, "Bob") }
            assertEquals("every class its own text ($lang)", E2eeFailure.values().size, texts.values.toSet().size)
            for ((failure, text) in texts) {
                assertEquals("$lang, $failure: only a retryable class says to try again", failure.retryable, text.contains(word, ignoreCase = true))
            }
            assertTrue("the peer by name ($lang)", texts.getValue(E2eeFailure.PEER_KEYS_UNAVAILABLE).contains("Bob"))
        }
        // From the wire to the class.
        assertEquals(E2eeFailure.NOT_ALLOWED, E2eeSignaling.classify(401, "not friends"))
        assertEquals(E2eeFailure.NOT_ALLOWED, E2eeSignaling.classify(0, "not_owner"))
        assertEquals(E2eeFailure.GROUP_NOT_ENCRYPTED, E2eeSignaling.classify(409, "group_not_encrypted"))
        assertEquals(E2eeFailure.DISABLED, E2eeSignaling.classify(409, "e2ee_disabled"))
        assertEquals(E2eeFailure.DISABLED, E2eeSignaling.classify(0, "group_e2ee_disabled"))
        assertEquals(E2eeFailure.DISABLED, E2eeSignaling.classify(501, "module not enabled: e2ee"))
        assertEquals(E2eeFailure.SERVER, E2eeSignaling.classify(400, "bad request"))
    }

    @Test
    fun theSafetyNumberInGroupsOfFive() {
        val n = (0 until 60).joinToString("") { (it % 10).toString() }
        assertEquals(listOf("01234 56789 01234 56789", "01234 56789 01234 56789", "01234 56789 01234 56789"), safetyNumberLines(n))
    }

    private fun waitForText(text: String) {
        try {
            rule.waitUntil(5_000) { rule.onAllNodesWithText(text, substring = true).fetchSemanticsNodes().isNotEmpty() }
        } catch (e: Throwable) {
            throw AssertionError("no \"$text\" in:\n" + rule.onRoot().printToString(), e)
        }
    }

    @Test
    fun aConversationOnScreen() {
        val alice = chats("alice")
        val bob = chats("bob")
        val topic = alice.topicFor(bob.me)
        rule.setContent { ImTheme { EncryptedChatScreen(alice, topic, "Bob", peer = bob.me) } }
        waitForText(context.getString(R.string.talkwire_e2ee_empty))

        rule.onNode(SemanticsMatcher.keyIsDefined(SemanticsProperties.EditableText)).performTextInput("hello bob")
        rule.onNodeWithContentDescription(context.getString(R.string.talkwire_e2ee_send)).performClick()
        waitForText("hello bob")
        eventually { bob.messages(topic).value.map { it.text } == listOf("hello bob") }

        runBlocking { bob.sendDirect(alice.me, "hi alice") }
        waitForText("hi alice")

        rule.onNodeWithContentDescription(context.getString(R.string.talkwire_e2ee_more)).performClick()
        rule.onNodeWithText(context.getString(R.string.talkwire_e2ee_safety_number)).performClick()
        val number = runBlocking { alice.safetyNumber(bob.me) }!!
        waitForText(safetyNumberLines(number).first())
    }

    @Test
    fun aPeerWithoutKeysIsExplainedAndTheDraftKept() {
        val alice = chats("alice")
        val carol = server.uidOf("carol")
        rule.setContent { ImTheme { EncryptedChatScreen(alice, alice.topicFor(carol), "Carol", peer = carol) } }
        rule.onNode(SemanticsMatcher.keyIsDefined(SemanticsProperties.EditableText)).performTextInput("are you there")
        rule.onNodeWithContentDescription(context.getString(R.string.talkwire_e2ee_send)).performClick()
        waitForText(context.getString(R.string.talkwire_e2ee_failed_peer_keys, "Carol"))
        // The text is given back to send later, not lost.
        rule.onNodeWithText("are you there").assertExists()
    }
}
