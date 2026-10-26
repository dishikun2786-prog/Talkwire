package io.talkwire.ui

import android.content.Context
import androidx.compose.foundation.layout.Column
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.test.assertCountEquals
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onAllNodesWithText
import androidx.compose.ui.test.onNodeWithContentDescription
import androidx.compose.ui.test.onNodeWithTag
import androidx.compose.ui.test.onNodeWithText
import androidx.compose.ui.test.performClick
import androidx.compose.ui.test.performTouchInput
import androidx.compose.ui.text.input.TextFieldValue
import androidx.test.core.app.ApplicationProvider
import io.talkwire.core.json.jsonObject
import io.talkwire.core.model.ChatMessage
import io.talkwire.core.model.ConversationId
import io.talkwire.core.model.MessageStatus
import io.talkwire.ui.chat.MessageActionsSheet
import io.talkwire.ui.chat.MessageComposer
import io.talkwire.ui.chat.ReplyRef
import io.talkwire.ui.chat.replyExtra
import io.talkwire.ui.chat.replyRef
import io.talkwire.ui.chat.VoiceInput
import io.talkwire.ui.message.MessageAction
import io.talkwire.ui.message.MessageScope
import io.talkwire.ui.message.MessageType
import io.talkwire.ui.message.MessageTypeRegistry
import io.talkwire.ui.theme.ImTheme
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import java.util.Locale

/** UI red lines of the Compose kit (C7, C8), on Robolectric. */
@RunWith(RobolectricTestRunner::class)
class RedLineUiTest {
    @get:Rule
    val rule = createComposeRule()

    private val peer = ConversationId("uid_peer")

    private fun msg(contentType: String, content: String = "", fileUrl: String? = null, duration: Int = 0, seq: Long = 1) =
        ChatMessage(peer, seq, null, "uid_peer", content, contentType, fileUrl = fileUrl, duration = duration, ts = 1, status = MessageStatus.RECEIVED)

    @Composable
    private fun Draw(m: ChatMessage, registry: MessageTypeRegistry) {
        registry.typeOf(m).Content(m, MessageScope(isMine = false, onLongPress = {}, openImage = {}, resolveUrl = { it }))
    }

    /** A host card that joins only by registering. */
    private object TestCardType : MessageType {
        override fun matches(message: ChatMessage) = message.contentType == "test_card"
        override val framed get() = false

        @Composable
        override fun Content(message: ChatMessage, scope: MessageScope) {
            Text("CARD:" + message.content)
        }

        override fun preview(message: ChatMessage, context: Context) = "[Card]"
    }

    @Test
    fun C7_BubblesFromContentType() {
        val registry = MessageTypeRegistry.Builder().register(TestCardType).build()
        rule.setContent {
            Column {
                // Text that looks like a placeholder is still text.
                Draw(msg("text", "[Image]", seq = 1), registry)
                // An image whose content is a caption-like string is still an image, and the string is not drawn.
                Draw(msg("image/jpeg", "a sunset", fileUrl = "https://h/i.jpg", seq = 2), registry)
                Draw(msg("audio/mp4", "[Voice]", fileUrl = "https://h/v.m4a", duration = 3, seq = 3), registry)
                Draw(msg("audio/mp4", "[Voice]", fileUrl = "https://h/w.m4a", duration = 50, seq = 4), registry)
                Draw(msg("test_card", "hello", seq = 5), registry)
                // Not registered here: falls back to the sender's text.
                Draw(msg("red_packet", "[Red packet]", seq = 6), registry)
            }
        }
        rule.onNodeWithText("[Image]").assertExists()
        rule.onNodeWithContentDescription("Image").assertExists()
        rule.onAllNodesWithText("a sunset").assertCountEquals(0)
        rule.onAllNodesWithText("[Voice]").assertCountEquals(0)
        rule.onNodeWithText("CARD:hello").assertExists()
        rule.onNodeWithText("[Red packet]").assertExists()

        val short = rule.onNodeWithContentDescription("3″").fetchSemanticsNode().size.width
        val long = rule.onNodeWithContentDescription("50″").fetchSemanticsNode().size.width
        assertTrue("a 50 s clip ($long px) must be wider than a 3 s one ($short px)", long > short)

        // Previews come from the type too, in the UI's language, not from the content.
        val ctx = ApplicationProvider.getApplicationContext<Context>()
        assertEquals("[Image]", registry.preview(msg("image/jpeg", "a sunset", fileUrl = "u"), ctx))
        assertEquals("[Card]", registry.preview(msg("test_card", "hello"), ctx))
        assertEquals("[Image]", registry.preview(msg("text", "[Image]"), ctx))
    }

    private class Counts {
        var press = 0
        var release = 0
        val drags = mutableListOf<Float>()
    }

    @Composable
    private fun VoiceComposer(c: Counts) {
        var value by remember { mutableStateOf(TextFieldValue("")) }
        MessageComposer(
            value = value,
            onValueChange = { value = it },
            onSend = {},
            onToggleAttachments = {},
            voice = VoiceInput(onPress = { c.press++ }, onDrag = { c.drags += it }, onRelease = { c.release++ }),
        )
    }

    @Test
    fun C8_HoldToTalkSingleGesture() {
        val c = Counts()
        rule.setContent { VoiceComposer(c) }
        rule.onNodeWithContentDescription("Voice message").performClick()
        val button = rule.onNodeWithTag("talkwire_hold_to_talk")

        // Press, then slide well past touch slop: still one gesture, nothing sent yet.
        button.performTouchInput {
            down(center)
            moveBy(Offset(0f, -40f))
            moveBy(Offset(25f, -200f))
        }
        rule.waitForIdle()
        assertEquals(1, c.press)
        assertEquals("moving the finger must not end the gesture", 0, c.release)
        assertEquals(2, c.drags.size)
        assertTrue("drag is the offset from the press point", c.drags.last() < -200f)

        button.performTouchInput { up() }
        rule.waitForIdle()
        assertEquals(1, c.press)
        assertEquals(1, c.release)

        // A system cancel also ends it exactly once.
        button.performTouchInput {
            down(center)
            moveBy(Offset(0f, -30f))
            cancel()
        }
        rule.waitForIdle()
        assertEquals(2, c.press)
        assertEquals(2, c.release)
    }

    @Test
    fun C8_SheetsFollowAppLocale() {
        rule.setContent {
            ImTheme(darkTheme = false, locale = Locale.SIMPLIFIED_CHINESE) {
                // The screen behind the sheet, in the app's language.
                Text(stringResource(R.string.talkwire_chats))
                MessageActionsSheet(
                    actions = listOf(MessageAction.COPY, MessageAction.FORWARD),
                    failed = false,
                    onAction = {},
                    onFailedAction = {},
                    onDismiss = {},
                )
            }
        }
        rule.waitForIdle()
        rule.onNodeWithText("聊天").assertExists()
        rule.onNodeWithText("复制").assertExists()
        rule.onNodeWithText("转发").assertExists()
        rule.onAllNodesWithText("Copy").assertCountEquals(0)
    }

    /**
     * Why the kit needs its own sheet: a plain Material sheet is its own
     * window and re-provides the context, so the language set through
     * [ImTheme] is lost inside it.
     */
    @OptIn(ExperimentalMaterial3Api::class)
    @Test
    fun plainSheetLosesTheAppLocale() {
        rule.setContent {
            ImTheme(darkTheme = false, locale = Locale.SIMPLIFIED_CHINESE) {
                Text(stringResource(R.string.talkwire_chats))
                ModalBottomSheet(onDismissRequest = {}) { Text(stringResource(R.string.talkwire_action_copy)) }
            }
        }
        rule.waitForIdle()
        rule.onNodeWithText("聊天").assertExists()
        rule.onNodeWithText("Copy").assertExists()
    }

    @Test
    fun aReplyQuotesTheMessageInItsExtra() {
        val m = msg("text", "ok").copy(extra = jsonObject { put("replyTo", jsonObject { put("seq", 7); put("from", "uid_a"); put("text", "lunch?") }) })
        assertEquals(ReplyRef(7, "uid_a", "lunch?"), m.replyRef())
        val back = replyExtra(ReplyRef(9, "uid_b", "x".repeat(300)))
        val inner = back.get("replyTo") as io.talkwire.core.json.JsonObject
        assertEquals(120, inner.string("text")!!.length)
    }
}
