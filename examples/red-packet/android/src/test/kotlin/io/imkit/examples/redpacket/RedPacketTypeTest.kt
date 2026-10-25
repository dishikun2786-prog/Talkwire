package io.imkit.examples.redpacket

import android.content.Context
import androidx.compose.foundation.layout.Column
import androidx.compose.ui.test.junit4.createComposeRule
import androidx.compose.ui.test.onNodeWithText
import androidx.test.core.app.ApplicationProvider
import io.imkit.core.client.ImClient
import io.imkit.core.client.ImClientConfig
import io.imkit.core.client.TokenProvider
import io.imkit.core.json.jsonObject
import io.imkit.core.model.ChatMessage
import io.imkit.core.model.ConversationId
import io.imkit.core.model.MessageStatus
import io.imkit.core.testing.MiniServer
import io.imkit.ui.message.MessageScope
import io.imkit.ui.message.MessageTypeRegistry
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
import kotlin.random.Random

/** The card joins by registration alone and follows the host's pushes. */
@RunWith(RobolectricTestRunner::class)
class RedPacketTypeTest {
    @get:Rule
    val rule = createComposeRule()

    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private val server = MiniServer()
    private lateinit var client: ImClient
    private lateinit var store: RedPacketStore
    private val group = ConversationId("grp_party")

    private val message = ChatMessage(
        group, 1, null, "uid_bob", "[Red packet]", "red_packet",
        extra = jsonObject { put("redPacketId", "p1") }, ts = 1, status = MessageStatus.RECEIVED,
    )

    @Before
    fun setUp() = runBlocking {
        client = ImClient(ImClientConfig(endpoints = listOf("ws://mini"), tokenProvider = TokenProvider { "alice" }, transport = server.transport, random = Random(1)), scope)
        client.start()
        client.awaitConnected(5_000)
        // Nothing listens there: fetching the card fails quietly and pushes are the only source.
        store = RedPacketStore(RedPacketApi("http://127.0.0.1:9", TokenProvider { "alice" }), client, scope)
    }

    @After
    fun tearDown() {
        client.stop()
        scope.cancel()
    }

    private fun push(claimed: Int, count: Int) = server.transport.connections.last().push(
        jsonObject {
            put("action", "custom_update")
            put("topic", group.id)
            put("what", "red_packet")
            put("extra", jsonObject {
                put("id", "p1"); put("topic", group.id); put("senderId", "bob")
                put("total", 30); put("count", count); put("claimed", claimed); put("remaining", count - claimed)
            })
        },
    )

    @Test
    fun theCardIsDrawnByRegistrationAndFollowsPushes() {
        val withCard = MessageTypeRegistry.Builder().register(RedPacketType(store)).build()
        val scopeOf = MessageScope(isMine = false, onLongPress = {}, openImage = {}, resolveUrl = { it })
        rule.setContent {
            Column { withCard.typeOf(message).Content(message, scopeOf) }
        }
        rule.onNodeWithText("Red packet").assertExists()
        // No state yet: the sender's fallback text.
        rule.onNodeWithText("[Red packet]").assertExists()

        push(claimed = 1, count = 3)
        rule.waitUntil(5_000) { store.cards.value["p1"]?.claimed == 1 }
        rule.onNodeWithText("Tap to open").assertExists()

        store.merge(RedPacketCard("p1", group.id, "bob", 30, 3, 2, 1, myAmount = 10))
        push(claimed = 3, count = 3)
        rule.waitUntil(5_000) { store.cards.value["p1"]?.claimed == 3 }
        // A push carries nobody's amount; my own stays.
        rule.onNodeWithText("You got 10").assertExists()

        val ctx = ApplicationProvider.getApplicationContext<Context>()
        assertEquals("[Red packet]", withCard.preview(message.copy(content = "something else"), ctx))
    }

    @Test
    fun withoutRegistrationTheKitShowsTheFallbackText() {
        val ctx = ApplicationProvider.getApplicationContext<Context>()
        val plain = MessageTypeRegistry.Default
        assertEquals("[Red packet]", plain.preview(message, ctx))
        rule.setContent { plain.typeOf(message).Content(message, MessageScope(false, {}, {}, { it })) }
        rule.onNodeWithText("[Red packet]").assertExists()
    }
}
