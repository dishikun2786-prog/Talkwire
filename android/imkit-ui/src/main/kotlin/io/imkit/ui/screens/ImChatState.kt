package io.imkit.ui.screens

import androidx.compose.runtime.Composable
import androidx.compose.runtime.Immutable
import androidx.compose.runtime.Stable
import androidx.compose.runtime.mutableStateListOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.Saver
import androidx.compose.runtime.saveable.rememberSaveable
import io.imkit.core.ImIds
import io.imkit.core.engine.ChatEngine
import io.imkit.core.engine.ConversationSummary
import io.imkit.core.model.ConversationId
import io.imkit.core.model.GroupInfo
import io.imkit.ui.host.ImDeepLink
import io.imkit.ui.host.ImProfile
import io.imkit.ui.host.ImUiHost
import io.imkit.ui.host.ProfileCache
import io.imkit.ui.message.MessageTypeRegistry
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

/**
 * Limits and switches of the chat UI. Servers can change some of these at
 * runtime; pass what the host's bootstrap reports.
 */
@Immutable
public data class ImUiConfig(
    val messageMaxLength: Int = 20_000,
    val typingThrottleMs: Long = 3_000,
    val maxAdmins: Int = 10,
    /** Off by default: in groups, members cannot open each other's profiles. */
    val groupMembersCanOpenProfiles: Boolean = false,
    /**
     * The protocol has no call that lists my direct chats, so on a new device
     * or after an account switch they would stay hidden until someone writes.
     * On by default: every contact of the host gets a direct conversation that
     * is synced once; the list shows it only when it has a message.
     */
    val chatsFromContacts: Boolean = true,
)

/** Where the chat UI is. */
public sealed interface ImDestination {
    public data object Conversations : ImDestination

    public data class Chat(val id: ConversationId) : ImDestination

    /** Settings of a conversation: members and roles for a group, mute/pin/clear for both. */
    public data class Info(val id: ConversationId) : ImDestination

    /** Pick a contact to chat with. */
    public data object Contacts : ImDestination

    /** Pick people for a new group ([topic] null) or to add to [topic]. */
    public data class PickMembers(val topic: String?) : ImDestination
}

/**
 * The state of the chat UI: what is open, who people are, and the engine
 * and host it talks to. Created by [rememberImChatState]; one per screen
 * that hosts [ImChatApp].
 */
@Stable
public class ImChatState internal constructor(
    public val engine: ChatEngine,
    public val host: ImUiHost,
    public val types: MessageTypeRegistry,
    public val config: ImUiConfig,
    /** Outlives single screens: an upload started in a chat finishes after the chat is closed. */
    public val scope: CoroutineScope,
    initial: List<ImDestination>,
) {
    private val stack = mutableStateListOf<ImDestination>().apply { addAll(initial.ifEmpty { listOf(ImDestination.Conversations) }) }

    public val profiles: ProfileCache = ProfileCache(host, scope)

    init {
        if (config.chatsFromContacts) scope.launch { discoverChats() }
    }

    /** Direct chats with the host's contacts that this device does not know yet, fetched once each. */
    private suspend fun discoverChats() {
        val tried = HashSet<String>()
        host.contacts.collect { contacts ->
            val known = engine.conversations.value.mapTo(HashSet()) { it.id.id }
            for (c in contacts) {
                val id = ConversationId(c.uid)
                if (id.isGroup || !tried.add(c.uid) || c.uid in known) continue
                try {
                    engine.ensureConversation(id)
                    engine.syncConversation(id)
                } catch (e: CancellationException) {
                    throw e
                } catch (e: Exception) {
                    // Offline now: the conversation is stored, so the engine's next full sync covers it.
                }
            }
        }
    }

    private val _openConversation = MutableStateFlow(openOf(stack.last()))

    /** The chat on screen, for [io.imkit.android.notify.NotificationController.openConversation]. */
    public val openConversation: StateFlow<ConversationId?> = _openConversation.asStateFlow()

    public val current: ImDestination get() = stack.last()
    public val backStack: List<ImDestination> get() = stack

    public fun navigate(destination: ImDestination) {
        if (stack.last() == destination) return
        stack.add(destination)
        changed()
    }

    /** Replaces the top of the stack (a picker that turns into the chat it created). */
    public fun replace(destination: ImDestination) {
        stack[stack.lastIndex] = destination
        changed()
    }

    /** False when already at the root, so the host can close the chat UI. */
    public fun back(): Boolean {
        if (stack.size <= 1) return false
        stack.removeAt(stack.lastIndex)
        changed()
        return true
    }

    /** Opens a link from a notification, a banner or the app; the list stays underneath. */
    public fun open(link: ImDeepLink) {
        val target = when (link) {
            ImDeepLink.Conversations -> null
            is ImDeepLink.Conversation -> ImDestination.Chat(link.id)
            is ImDeepLink.GroupInfo -> ImDestination.Info(ConversationId.group(link.topic))
        }
        stack.clear()
        stack.add(ImDestination.Conversations)
        if (target is ImDestination.Info) stack.add(ImDestination.Chat(target.id))
        if (target != null) stack.add(target)
        changed()
    }

    private fun changed() {
        _openConversation.value = openOf(stack.last())
    }

    /** A conversation's name: the group's name, else the peer's, else a short id. */
    public fun title(id: ConversationId, summary: ConversationSummary?, groups: List<GroupInfo>, people: Map<String, ImProfile>): String =
        if (id.isGroup) {
            groups.firstOrNull { it.topic == id.id }?.name?.takeIf { it.isNotBlank() } ?: summary?.title?.takeIf { it.isNotBlank() } ?: ProfileCache.shortUid(id.id)
        } else {
            people[id.id]?.name?.takeIf { it.isNotBlank() } ?: summary?.title?.takeIf { it.isNotBlank() } ?: ProfileCache.shortUid(id.id)
        }

    /** The topic the server knows the conversation by: the group topic or the canonical `p2p_` topic. */
    public fun canonicalTopic(id: ConversationId): String = if (id.isGroup) id.id else ImIds.p2pTopic(engine.me, id.id)

    public companion object {
        private fun openOf(d: ImDestination): ConversationId? = (d as? ImDestination.Chat)?.id

        internal fun encode(d: ImDestination): String = when (d) {
            ImDestination.Conversations -> "list"
            is ImDestination.Chat -> "chat:" + d.id.id
            is ImDestination.Info -> "info:" + d.id.id
            ImDestination.Contacts -> "contacts"
            is ImDestination.PickMembers -> "pick:" + (d.topic ?: "")
        }

        internal fun decode(s: String): ImDestination? = when {
            s == "list" -> ImDestination.Conversations
            s == "contacts" -> ImDestination.Contacts
            s.startsWith("chat:") -> s.removePrefix("chat:").takeIf { it.isNotEmpty() }?.let { ImDestination.Chat(ConversationId(it)) }
            s.startsWith("info:") -> s.removePrefix("info:").takeIf { it.isNotEmpty() }?.let { ImDestination.Info(ConversationId(it)) }
            s.startsWith("pick:") -> ImDestination.PickMembers(s.removePrefix("pick:").takeIf { it.isNotEmpty() })
            else -> null
        }
    }
}

/**
 * Creates the chat UI's state. [start] is used once; afterwards the back
 * stack survives rotation and process death.
 */
@Composable
public fun rememberImChatState(
    engine: ChatEngine,
    host: ImUiHost,
    types: MessageTypeRegistry = MessageTypeRegistry.Default,
    config: ImUiConfig = ImUiConfig(),
    start: ImDeepLink = ImDeepLink.Conversations,
): ImChatState {
    val scope = rememberCoroutineScope()
    val saver = remember(engine, host, types, config) {
        Saver<ImChatState, ArrayList<String>>(
            save = { s -> ArrayList(s.backStack.map { ImChatState.encode(it) }) },
            restore = { list -> ImChatState(engine, host, types, config, scope, list.mapNotNull { ImChatState.decode(it) }) },
        )
    }
    return rememberSaveable(engine, host, types, config, saver = saver) {
        ImChatState(engine, host, types, config, scope, emptyList()).also { it.open(start) }
    }
}
