package io.talkwire.ui.screens

import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.sizeIn
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.ArrowBack
import androidx.compose.material.icons.outlined.AddCircleOutline
import androidx.compose.material.icons.outlined.Delete
import androidx.compose.material.icons.outlined.Groups
import androidx.compose.material.icons.outlined.NotificationsOff
import androidx.compose.material.icons.outlined.Person
import androidx.compose.material.icons.outlined.PushPin
import androidx.compose.material3.Divider
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.Surface
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
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.hapticfeedback.HapticFeedbackType
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalHapticFeedback
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.withStyle
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.DpOffset
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import io.talkwire.core.engine.ConversationSummary
import io.talkwire.core.model.ChatMessage
import io.talkwire.core.model.PresenceState
import io.talkwire.ui.R
import io.talkwire.ui.components.ImAvatar
import io.talkwire.ui.components.ImConfirmDialog
import io.talkwire.ui.components.ImTime
import io.talkwire.ui.host.ProfileCache
import io.talkwire.ui.theme.ImTheme
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

/**
 * The conversations the list shows: a group I left only while it still has
 * messages, and a direct chat only once it has a message, a draft or a pin
 * (contacts get empty direct chats so that history can be fetched). Groups
 * show even when empty: a new group has no messages yet.
 */
internal fun listedConversations(all: List<ConversationSummary>): List<ConversationSummary> = all
    .filter { !it.left || it.lastMessage != null }
    .filter { it.id.isGroup || it.lastMessage != null || !it.draft.isNullOrEmpty() || it.pinned }
    .sortedWith(ConversationOrder)

/**
 * The conversation list order: pinned first, then by last activity. The
 * engine already keeps this order; this is the rule if a host sorts itself.
 */
public val ConversationOrder: Comparator<ConversationSummary> =
    compareByDescending<ConversationSummary> { it.pinned }.thenByDescending { it.updatedAt }

/** One line of the last message: drafts first, group senders named. */
internal fun lastLine(summary: ConversationSummary, preview: (ChatMessage) -> String, senderName: (String) -> String, me: String, draftLabel: (String) -> String, senderLabel: (String, String) -> String): String {
    summary.draft?.takeIf { it.isNotBlank() }?.let { return draftLabel(it) }
    val m = summary.lastMessage ?: return ""
    val text = preview(m)
    return if (summary.id.isGroup && m.from != me && m.senderKind == ChatMessage.SENDER_USER) senderLabel(senderName(m.from), text) else text
}

/** The text around a search match: what comes before (a long lead cut to "…"), the match, the rest. */
internal data class Snippet(val before: String, val match: String, val after: String)

internal fun searchSnippet(text: String, query: String, lead: Int = 16): Snippet {
    val flat = text.replace(Regex("\\s+"), " ")
    val at = flat.indexOf(query, ignoreCase = true)
    if (at < 0) return Snippet(flat, "", "")
    val start = maxOf(0, at - lead)
    return Snippet((if (start > 0) "…" else "") + flat.substring(start, at), flat.substring(at, at + query.length), flat.substring(at + query.length))
}

@Composable
internal fun ConversationListScreen(state: ImChatState, onExit: (() -> Unit)?) {
    val engine = state.engine
    val context = LocalContext.current
    val colors = ImTheme.colors
    val scope = rememberCoroutineScope()
    val summaries by engine.conversations.collectAsState()
    val groups by engine.groups.collectAsState()
    val people by state.profiles.profiles.collectAsState()
    val presence by engine.presence.records.collectAsState()
    var menu by remember { mutableStateOf(false) }
    var deleting by remember { mutableStateOf<ConversationSummary?>(null) }
    val visible = remember(summaries) { listedConversations(summaries) }
    // Search: chats by name, and the text messages stored on this device, a moment after typing stops.
    var query by remember { mutableStateOf("") }
    val q = query.trim()
    var hits by remember { mutableStateOf<Pair<String, List<ChatMessage>>?>(null) }
    LaunchedEffect(q) {
        if (q.isEmpty()) return@LaunchedEffect
        delay(250)
        hits = q to try {
            engine.searchMessages(q)
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            emptyList()
        }
    }
    val messageHits = hits?.takeIf { it.first == q }?.second

    LaunchedEffect(visible) {
        state.profiles.request(visible.flatMap { s -> listOfNotNull(s.id.id.takeIf { !s.id.isGroup }, s.lastMessage?.from) })
    }
    LaunchedEffect(messageHits) {
        messageHits?.let { ms -> state.profiles.request(ms.flatMap { m -> listOfNotNull(m.from, m.conversation.id.takeIf { !m.conversation.isGroup }) }) }
    }

    val row: @Composable (ConversationSummary) -> Unit = { s ->
        val title = state.title(s.id, s, groups, people)
        val online = !s.id.isGroup && presence.containsKey(s.id.id) && engine.presence.state(s.id.id) == PresenceState.ONLINE
        ConversationItem(
            summary = s,
            title = title,
            avatarUrl = if (s.id.isGroup) null else state.host.resolveMediaUrl(people[s.id.id]?.avatarUrl),
            online = online,
            line = lastLine(
                s,
                preview = { m -> state.types.preview(m, context) },
                senderName = { uid -> people[uid]?.name ?: ProfileCache.fallbackName(context, uid) },
                me = engine.me,
                draftLabel = { d -> context.getString(R.string.talkwire_draft, d) },
                senderLabel = { n, t -> context.getString(R.string.talkwire_sender_preview, n, t) },
            ),
            time = ImTime.conversationTime(context, s.lastMessage?.ts ?: s.updatedAt),
            onClick = { state.navigate(ImDestination.Chat(s.id)) },
            onPin = { scope.launch { runCatching { engine.setPinned(s.id, !s.pinned) } } },
            onMute = { scope.launch { runCatching { engine.setMuted(s.id, !s.muted) } } },
            onDelete = { deleting = s },
        )
        Divider(Modifier.padding(start = 76.dp), color = colors.divider, thickness = 0.5.dp)
    }

    Column(Modifier.fillMaxSize().background(colors.background)) {
        Surface(color = colors.background) {
            Row(Modifier.fillMaxWidth().padding(horizontal = 4.dp, vertical = 4.dp), verticalAlignment = Alignment.CenterVertically) {
                if (onExit != null) {
                    IconButton(onClick = onExit) { Icon(Icons.Filled.ArrowBack, stringResource(R.string.talkwire_back), tint = colors.textPrimary) }
                } else {
                    Spacer(Modifier.width(12.dp))
                }
                Text(stringResource(R.string.talkwire_chats), fontSize = 18.sp, fontWeight = FontWeight.Medium, color = colors.textPrimary, modifier = Modifier.weight(1f))
                Box {
                    IconButton(onClick = { menu = true }, modifier = Modifier.testTag("talkwire_new")) {
                        Icon(Icons.Outlined.AddCircleOutline, stringResource(R.string.talkwire_new_chat), tint = colors.textPrimary)
                    }
                    DropdownMenu(expanded = menu, onDismissRequest = { menu = false }) {
                        DropdownMenuItem(text = { Text(stringResource(R.string.talkwire_new_chat)) }, leadingIcon = { Icon(Icons.Outlined.Person, null) }, onClick = { menu = false; state.navigate(ImDestination.Contacts) })
                        DropdownMenuItem(text = { Text(stringResource(R.string.talkwire_new_group)) }, leadingIcon = { Icon(Icons.Outlined.Groups, null) }, onClick = { menu = false; state.navigate(ImDestination.PickMembers(null)) })
                    }
                }
            }
        }
        if (visible.isNotEmpty() || query.isNotEmpty()) SearchField(query) { query = it }
        if (q.isNotEmpty()) {
            val chatHits = visible.filter { state.title(it.id, it, groups, people).contains(q, ignoreCase = true) }
            val shownHits = messageHits.orEmpty()
            if (messageHits != null && chatHits.isEmpty() && shownHits.isEmpty()) {
                Box(Modifier.fillMaxSize().padding(32.dp), contentAlignment = Alignment.Center) {
                    Text(stringResource(R.string.talkwire_no_results), color = colors.textSecondary, fontSize = 14.sp, textAlign = TextAlign.Center)
                }
            } else {
                LazyColumn(Modifier.fillMaxSize().testTag("talkwire_search_results")) {
                    if (chatHits.isNotEmpty()) {
                        item(key = "h_chats") { SectionHeader(stringResource(R.string.talkwire_chats)) }
                        items(chatHits, key = { "c_" + it.id.id }) { s -> row(s) }
                    }
                    if (shownHits.isNotEmpty()) {
                        item(key = "h_messages") { SectionHeader(stringResource(R.string.talkwire_search_messages)) }
                        items(shownHits, key = { m -> "m_${m.conversation.id}:${m.seq ?: m.localId}" }) { m ->
                            val c = m.conversation
                            val sender = if (c.isGroup && m.from != engine.me) people[m.from]?.name ?: ProfileCache.fallbackName(context, m.from) else null
                            SearchHitItem(
                                title = state.title(c, summaries.firstOrNull { it.id == c }, groups, people),
                                isGroup = c.isGroup,
                                avatarUrl = if (c.isGroup) null else state.host.resolveMediaUrl(people[c.id]?.avatarUrl),
                                senderPrefix = sender?.let { context.getString(R.string.talkwire_sender_preview, it, "") },
                                snippet = searchSnippet(m.content, q),
                                time = ImTime.conversationTime(context, m.ts),
                                onClick = { state.navigate(ImDestination.Chat(c)) },
                            )
                            Divider(Modifier.padding(start = 76.dp), color = colors.divider, thickness = 0.5.dp)
                        }
                    }
                }
            }
        } else if (visible.isEmpty()) {
            Box(Modifier.fillMaxSize().padding(32.dp), contentAlignment = Alignment.Center) {
                Text(stringResource(R.string.talkwire_no_conversations), color = colors.textSecondary, fontSize = 14.sp, textAlign = TextAlign.Center)
            }
        } else {
            LazyColumn(Modifier.fillMaxSize().testTag("talkwire_conversations")) {
                items(visible, key = { it.id.id }) { s -> row(s) }
            }
        }
    }

    deleting?.let { s ->
        ImConfirmDialog(
            message = stringResource(R.string.talkwire_delete_chat_confirm),
            confirmLabel = stringResource(R.string.talkwire_delete_chat),
            dismissLabel = stringResource(R.string.talkwire_cancel),
            destructive = true,
            onConfirm = { deleting = null; scope.launch { runCatching { engine.deleteConversation(s.id) } } },
            onDismiss = { deleting = null },
        )
    }
}

@Composable
private fun SectionHeader(label: String) {
    Text(label, fontSize = 12.sp, color = ImTheme.colors.textSecondary, modifier = Modifier.padding(start = 16.dp, top = 10.dp, bottom = 4.dp))
}

/** A message found by search: the chat it is in, the text around the match. */
@Composable
private fun SearchHitItem(
    title: String,
    isGroup: Boolean,
    avatarUrl: String?,
    senderPrefix: String?,
    snippet: Snippet,
    time: String,
    onClick: () -> Unit,
) {
    val colors = ImTheme.colors
    Row(
        Modifier.fillMaxWidth().clickable(onClick = onClick).padding(horizontal = 16.dp, vertical = 10.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        if (isGroup) {
            Box(Modifier.size(ImTheme.dimens.avatarInList).clip(CircleShape).background(colors.accent.copy(alpha = 0.15f)), contentAlignment = Alignment.Center) {
                Icon(Icons.Outlined.Groups, null, tint = colors.accent)
            }
        } else {
            ImAvatar(avatarUrl, title, size = ImTheme.dimens.avatarInList)
        }
        Spacer(Modifier.width(12.dp))
        Column(Modifier.weight(1f)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(title, fontSize = 16.sp, color = colors.textPrimary, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.weight(1f))
                if (time.isNotEmpty()) Text(time, fontSize = 12.sp, color = colors.textHint)
            }
            Spacer(Modifier.height(2.dp))
            val line = buildAnnotatedString {
                senderPrefix?.let { append(it) }
                append(snippet.before)
                withStyle(SpanStyle(background = colors.accent.copy(alpha = 0.28f))) { append(snippet.match) }
                append(snippet.after)
            }
            Text(line, fontSize = 13.sp, color = colors.textSecondary, maxLines = 1, overflow = TextOverflow.Ellipsis)
        }
    }
}

/** A row of the conversation list; long press for pin, mute and delete. */
@OptIn(ExperimentalFoundationApi::class)
@Composable
public fun ConversationItem(
    summary: ConversationSummary,
    title: String,
    avatarUrl: String?,
    online: Boolean,
    line: String,
    time: String,
    onClick: () -> Unit,
    onPin: () -> Unit,
    onMute: () -> Unit,
    onDelete: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val colors = ImTheme.colors
    val haptic = LocalHapticFeedback.current
    var menu by remember { mutableStateOf(false) }
    Box(modifier) {
        Row(
            Modifier
                .fillMaxWidth()
                .background(if (summary.pinned) colors.divider.copy(alpha = 0.25f) else Color.Transparent)
                .combinedClickable(onClick = onClick, onLongClick = { haptic.performHapticFeedback(HapticFeedbackType.LongPress); menu = true })
                .padding(horizontal = 16.dp, vertical = 10.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Box {
                if (summary.id.isGroup) {
                    Box(Modifier.size(ImTheme.dimens.avatarInList).clip(CircleShape).background(colors.accent.copy(alpha = 0.15f)), contentAlignment = Alignment.Center) {
                        Icon(Icons.Outlined.Groups, null, tint = colors.accent)
                    }
                } else {
                    ImAvatar(avatarUrl, title, size = ImTheme.dimens.avatarInList)
                }
                if (online) Box(Modifier.size(12.dp).align(Alignment.BottomEnd).clip(CircleShape).background(colors.online))
            }
            Spacer(Modifier.width(12.dp))
            Column(Modifier.weight(1f)) {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Text(
                        title,
                        fontSize = 16.sp,
                        fontWeight = if (summary.unread > 0) FontWeight.SemiBold else FontWeight.Normal,
                        color = colors.textPrimary,
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                        modifier = Modifier.weight(1f, fill = false),
                    )
                    if (summary.muted) {
                        Spacer(Modifier.width(4.dp))
                        Icon(Icons.Outlined.NotificationsOff, stringResource(R.string.talkwire_mute), tint = colors.textHint, modifier = Modifier.size(14.dp))
                    }
                }
                Spacer(Modifier.height(2.dp))
                Text(line, fontSize = 13.sp, color = colors.textSecondary, maxLines = 1, overflow = TextOverflow.Ellipsis)
            }
            Spacer(Modifier.width(8.dp))
            Column(horizontalAlignment = Alignment.End) {
                if (time.isNotEmpty()) Text(time, fontSize = 12.sp, color = colors.textHint)
                if (summary.unread > 0) {
                    Spacer(Modifier.height(4.dp))
                    if (summary.muted) {
                        Box(Modifier.size(8.dp).clip(CircleShape).background(colors.error))
                    } else {
                        Box(Modifier.sizeIn(minWidth = 18.dp, minHeight = 18.dp).clip(CircleShape).background(colors.error).padding(horizontal = 5.dp), contentAlignment = Alignment.Center) {
                            Text(if (summary.unread > 99) "99+" else summary.unread.toString(), color = Color.White, fontSize = 11.sp)
                        }
                    }
                }
            }
        }
        DropdownMenu(expanded = menu, onDismissRequest = { menu = false }, offset = DpOffset(16.dp, 0.dp)) {
            DropdownMenuItem(
                text = { Text(stringResource(if (summary.pinned) R.string.talkwire_unpin else R.string.talkwire_pin)) },
                leadingIcon = { Icon(Icons.Outlined.PushPin, null) },
                onClick = { menu = false; onPin() },
            )
            DropdownMenuItem(
                text = { Text(stringResource(if (summary.muted) R.string.talkwire_unmute else R.string.talkwire_mute)) },
                leadingIcon = { Icon(Icons.Outlined.NotificationsOff, null) },
                onClick = { menu = false; onMute() },
            )
            DropdownMenuItem(
                text = { Text(stringResource(R.string.talkwire_delete_chat), color = colors.error) },
                leadingIcon = { Icon(Icons.Outlined.Delete, null, tint = colors.error) },
                onClick = { menu = false; onDelete() },
            )
        }
    }
}
