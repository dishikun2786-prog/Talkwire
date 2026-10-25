package io.imkit.examples.redpacket

import android.content.Context
import android.widget.Toast
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.background
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Redeem
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import io.imkit.core.engine.ChatEngine
import io.imkit.core.engine.OutgoingMessage
import io.imkit.core.model.ChatMessage
import io.imkit.core.model.ConversationId
import io.imkit.ui.components.ImAlertDialog
import io.imkit.ui.host.AttachmentAction
import io.imkit.ui.message.MessageAction
import io.imkit.ui.message.MessageScope
import io.imkit.ui.message.MessageType
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.launch

/**
 * The red packet bubble. Registered with
 * `MessageTypeRegistry.Builder().register(RedPacketType(store))`; that is
 * the whole integration on the message side.
 */
class RedPacketType(private val store: RedPacketStore) : MessageType {
    override fun matches(message: ChatMessage): Boolean = message.contentType == RedPacketStore.WHAT

    override val framed: Boolean get() = false

    @Composable
    override fun Content(message: ChatMessage, scope: MessageScope) {
        val id = redPacketIdOf(message.extra)
        val cards by store.cards.collectAsState()
        val card = id?.let { cards[it] }
        LaunchedEffect(id) { if (id != null) store.refresh(id) }
        val context = LocalContext.current
        val tapScope = rememberCoroutineScope()
        RedPacketBubble(card, message.content, onClaim = { if (id != null) onTap(id, context, tapScope) }, onLongPress = scope.onLongPress)
    }

    /** Claims while there is something to claim; otherwise shows how it stands. */
    private fun onTap(id: String, context: Context, scope: CoroutineScope) {
        val card = store.cards.value[id]
        if (card != null && (card.myAmount != null || card.remaining == 0)) {
            showState(context, card)
            return
        }
        scope.launch {
            store.claim(id).onSuccess { showState(context, it) }.onFailure { e ->
                val c = (e as? RedPacketException)?.card
                if (c != null) showState(context, c) else Toast.makeText(context, context.getString(R.string.redpacket_failed), Toast.LENGTH_SHORT).show()
            }
        }
    }

    @OptIn(ExperimentalFoundationApi::class)
    @Composable
    private fun RedPacketBubble(card: RedPacketCard?, fallback: String, onClaim: () -> Unit, onLongPress: () -> Unit) {
        val opened = card != null && (card.myAmount != null || card.remaining == 0)
        val status = when {
            card == null -> fallback
            card.myAmount != null -> stringResource(R.string.redpacket_you_got, card.myAmount)
            card.remaining == 0 -> stringResource(R.string.redpacket_all_claimed, card.claimed, card.count)
            else -> stringResource(R.string.redpacket_tap_to_open)
        }
        val top = if (opened) Color(0xFFF3B9A2) else Color(0xFFF35543)
        val end = if (opened) Color(0xFFE59A82) else Color(0xFFE0362B)
        Column(
            Modifier
                .width(220.dp)
                .clip(RoundedCornerShape(8.dp))
                .background(Brush.verticalGradient(listOf(top, end)))
                .combinedClickable(onLongClick = onLongPress, onClick = onClaim)
                .testTag("redpacket_card"),
        ) {
            Row(Modifier.padding(12.dp), verticalAlignment = Alignment.CenterVertically) {
                Text("🧧", fontSize = 28.sp)
                Spacer(Modifier.width(10.dp))
                Column {
                    Text(stringResource(R.string.redpacket_title), color = Color.White, fontSize = 15.sp, fontWeight = FontWeight.Medium)
                    Text(status, color = Color.White.copy(alpha = 0.85f), fontSize = 12.sp)
                }
            }
            Text(
                stringResource(R.string.redpacket_label),
                color = Color.White.copy(alpha = 0.7f),
                fontSize = 11.sp,
                modifier = Modifier.fillMaxWidth().background(Color.White.copy(alpha = 0.12f)).padding(horizontal = 12.dp, vertical = 4.dp),
            )
        }
    }

    override fun preview(message: ChatMessage, context: Context): String = context.getString(R.string.redpacket_preview)

    /** A card has no text to copy and nothing to save; it can only be forwarded as is. */
    override fun actions(message: ChatMessage): Set<MessageAction> = emptySet()

    private fun showState(context: Context, card: RedPacketCard) {
        val text = when {
            card.myAmount != null -> context.getString(R.string.redpacket_you_got, card.myAmount)
            else -> context.getString(R.string.redpacket_all_claimed, card.claimed, card.count)
        }
        Toast.makeText(context, text, Toast.LENGTH_SHORT).show()
    }
}

/**
 * The attachment panel entry that creates and sends a packet; groups only,
 * like the host's route. Shown through `ImUiHost.attachmentActions`.
 */
class RedPacketSender(private val api: RedPacketApi, private val engine: ChatEngine, private val store: RedPacketStore, private val scope: CoroutineScope) {
    /** Which conversation the create dialog is open for; drawn by [Dialog]. */
    private val target = mutableStateOf<ConversationId?>(null)

    fun action(conversation: ConversationId, label: String): AttachmentAction? =
        if (!conversation.isGroup) null else AttachmentAction("red_packet", label, Icons.Outlined.Redeem) { target.value = it }

    /** Draw once, above the chat UI. */
    @Composable
    fun Dialog() {
        val id = target.value ?: return
        val context = LocalContext.current
        var total by remember(id) { mutableStateOf("10") }
        var count by remember(id) { mutableStateOf("3") }
        val t = total.toIntOrNull()
        val c = count.toIntOrNull()
        val valid = t != null && c != null && c in 1..100 && t >= c
        ImAlertDialog(
            onDismissRequest = { target.value = null },
            title = stringResource(R.string.redpacket_send_title),
            confirmLabel = stringResource(R.string.redpacket_send),
            confirmEnabled = valid,
            dismissLabel = stringResource(R.string.redpacket_cancel),
            onConfirm = {
                target.value = null
                scope.launch {
                    runCatching {
                        val (card, message) = api.create(id.id, t!!, c!!)
                        store.merge(card)
                        engine.send(id, OutgoingMessage(content = message.string("content") ?: "[Red packet]", contentType = message.string("contentType"), extra = message.get("extra")))
                    }.onFailure { Toast.makeText(context, context.getString(R.string.redpacket_failed), Toast.LENGTH_SHORT).show() }
                }
            },
        ) {
            Column {
                OutlinedTextField(total, { total = it.filter(Char::isDigit).take(7) }, label = { Text(stringResource(R.string.redpacket_total)) }, keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Number), singleLine = true)
                Spacer(Modifier.height(8.dp))
                OutlinedTextField(count, { count = it.filter(Char::isDigit).take(3) }, label = { Text(stringResource(R.string.redpacket_count)) }, keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Number), singleLine = true)
            }
        }
    }
}
