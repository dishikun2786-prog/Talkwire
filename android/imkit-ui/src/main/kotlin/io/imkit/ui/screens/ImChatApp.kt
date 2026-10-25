package io.imkit.ui.screens

import androidx.activity.compose.BackHandler
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.slideInVertically
import androidx.compose.animation.slideOutVertically
import androidx.compose.foundation.clickable
import androidx.compose.foundation.gestures.detectVerticalDragGestures
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.Immutable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.Stable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveableStateHolder
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import io.imkit.android.notify.BannerSink
import io.imkit.core.model.ChatMessage
import io.imkit.core.model.ConversationId
import io.imkit.ui.components.ImAvatar
import io.imkit.ui.host.ProfileCache
import io.imkit.ui.message.LocalMessageTypes
import io.imkit.ui.theme.ImTheme
import kotlinx.coroutines.delay

/**
 * The whole chat UI: conversation list, chats, chat info, contacts and the
 * member picker, with the back stack in [state]. Wrap it in
 * [io.imkit.ui.theme.ImTheme] to restyle it. [onExit] adds a back arrow to
 * the list (null when the chat UI is a root screen).
 */
@Composable
public fun ImChatApp(state: ImChatState, modifier: Modifier = Modifier, onExit: (() -> Unit)? = null) {
    val holder = rememberSaveableStateHolder()
    BackHandler(enabled = state.backStack.size > 1) { state.back() }
    CompositionLocalProvider(LocalMessageTypes provides state.types) {
        Box(modifier) {
            val d = state.current
            // One saved-state slot per destination, so the list keeps its scroll position.
            holder.SaveableStateProvider(ImChatState.encode(d)) {
                when (d) {
                    ImDestination.Conversations -> ConversationListScreen(state, onExit)
                    is ImDestination.Chat -> ConversationScreen(state, d.id, onBack = { state.back() }, onOpenInfo = { state.navigate(ImDestination.Info(d.id)) })
                    is ImDestination.Info -> InfoScreen(state, d.id, onBack = { state.back() })
                    ImDestination.Contacts -> ContactsScreen(state, onBack = { state.back() })
                    is ImDestination.PickMembers -> PickMembersScreen(state, d.topic, onBack = { state.back() })
                }
            }
        }
    }
}

@Immutable
internal data class Banner(val message: ChatMessage, val groupName: String?, val stamp: Long)

/**
 * Banners for messages that arrive while the app is open in another chat or
 * screen (C6). Give [sink] to [io.imkit.android.notify.NotificationController];
 * draw [ImBannerHost] above the app's content.
 */
@Stable
public class ImBannerState {
    internal var current by mutableStateOf<Banner?>(null)
    private var counter = 0L

    public val sink: BannerSink = BannerSink { message, groupName ->
        // Written from the controller's coroutine; snapshot state accepts that.
        current = Banner(message, groupName, ++counter)
    }

    public fun dismiss() {
        current = null
    }
}

@Composable
public fun rememberImBannerState(): ImBannerState = remember { ImBannerState() }

/** Shows the latest banner for [visibleMs]; a tap opens the chat, an upward swipe hides it. */
@Composable
public fun ImBannerHost(
    banners: ImBannerState,
    chat: ImChatState,
    onOpen: (ConversationId) -> Unit,
    modifier: Modifier = Modifier,
    visibleMs: Long = 4_000,
) {
    val b = banners.current
    LaunchedEffect(b?.stamp) {
        if (b != null) {
            delay(visibleMs)
            if (banners.current?.stamp == b.stamp) banners.dismiss()
        }
    }
    val people by chat.profiles.profiles.collectAsState()
    var shown by remember { mutableStateOf<Banner?>(null) }
    if (b != null) shown = b
    AnimatedVisibility(visible = b != null, enter = slideInVertically { -it }, exit = slideOutVertically { -it }, modifier = modifier) {
        val s = shown ?: return@AnimatedVisibility
        val context = LocalContext.current
        val colors = ImTheme.colors
        LaunchedEffect(s.message.from) { chat.profiles.request(listOf(s.message.from)) }
        val sender = people[s.message.from]?.name?.takeIf { it.isNotBlank() } ?: ProfileCache.fallbackName(context, s.message.from)
        val title = if (s.groupName != null) "${s.groupName} · $sender" else sender
        Surface(
            shape = RoundedCornerShape(14.dp),
            color = colors.surface,
            shadowElevation = 8.dp,
            modifier = Modifier
                .fillMaxWidth()
                .statusBarsPadding()
                .padding(horizontal = 8.dp, vertical = 6.dp)
                .testTag("imkit_banner")
                .pointerInput(Unit) { detectVerticalDragGestures { _, dy -> if (dy < -8) banners.dismiss() } }
                .clickable { banners.dismiss(); onOpen(s.message.conversation) },
        ) {
            Row(Modifier.padding(12.dp), verticalAlignment = Alignment.CenterVertically) {
                ImAvatar(chat.host.resolveMediaUrl(people[s.message.from]?.avatarUrl), sender, size = 40.dp)
                Spacer(Modifier.width(10.dp))
                Column(Modifier.weight(1f)) {
                    Text(title, fontSize = 14.sp, fontWeight = FontWeight.Medium, color = colors.textPrimary, maxLines = 1, overflow = TextOverflow.Ellipsis)
                    Text(chat.types.preview(s.message, context), fontSize = 13.sp, color = colors.textSecondary, maxLines = 2, overflow = TextOverflow.Ellipsis)
                }
            }
        }
    }
}
