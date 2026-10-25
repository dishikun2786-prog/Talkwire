package io.imkit.ui.host

import android.net.Uri
import androidx.compose.foundation.layout.RowScope
import androidx.compose.runtime.Composable
import androidx.compose.runtime.Immutable
import androidx.compose.ui.graphics.vector.ImageVector
import io.imkit.core.model.ConversationId
import io.imkit.core.model.GroupRole
import kotlinx.coroutines.flow.StateFlow

/** A person as the host knows them. The framework itself only knows uids. */
@Immutable
public data class ImProfile(
    val uid: String,
    val name: String,
    val avatarUrl: String? = null,
)

/**
 * An action the app adds to the attachment panel (a red packet, a transfer,
 * a location). [onClick] gets the conversation it was opened in.
 */
@Immutable
public data class AttachmentAction(
    val id: String,
    val label: String,
    val icon: ImageVector,
    val onClick: (ConversationId) -> Unit,
)

/** An action in the conversation's top bar (a call button, say). */
@Immutable
public data class TopBarAction(
    val id: String,
    val label: String,
    val icon: ImageVector,
    val onClick: (ConversationId) -> Unit,
)

/**
 * Everything the chat UI needs from the app (4.4): who people are, who the
 * user's contacts are, where media goes, and the app's own extras. One
 * interface with defaults instead of the source app's thirty-odd screen
 * parameters; navigation goes through [ImDeepLink] and
 * [io.imkit.ui.screens.ImChatState].
 */
public interface ImUiHost {
    /** Names and avatars for [uids]; missing ones fall back to a short uid. */
    public suspend fun profiles(uids: Collection<String>): Map<String, ImProfile>

    /** The user's contacts, for new chats and new groups. Empty when the app has none. */
    public val contacts: StateFlow<List<ImProfile>>

    /** Reloads [contacts], for example when the contacts screen opens. */
    public suspend fun refreshContacts() {}

    /**
     * Uploads media and sends it; null when the app does not support media.
     * The default implementation is [io.imkit.android.media.MediaSender].
     */
    public val media: io.imkit.android.media.MediaSender? get() = null

    /**
     * Makes a stored media URL loadable: relative paths from older servers
     * need the media host's base URL. The default keeps the URL as it is.
     */
    public fun resolveMediaUrl(url: String?): String? = url?.takeIf { it.isNotBlank() }

    /** Extra attachment panel entries, after photo, camera and file. */
    public fun attachmentActions(conversation: ConversationId): List<AttachmentAction> = emptyList()

    /** Extra top bar actions of a conversation. */
    public fun topBarActions(conversation: ConversationId): List<TopBarAction> = emptyList()

    /**
     * Top bar content that needs composition, after [topBarActions]: an
     * optional module's buttons that ask for a permission first (the call
     * module's call buttons, say). Null adds nothing.
     */
    public val topBarExtras: (@Composable RowScope.(conversation: ConversationId) -> Unit)? get() = null

    /**
     * Sections an optional module adds to a group's info screen, after the
     * members and before the chat settings (the group-join module's QR code
     * and join requests, say). [role] is the user's own role in the group.
     * Null adds nothing.
     */
    public val groupInfoExtras: (@Composable (topic: String, role: GroupRole) -> Unit)? get() = null

    /**
     * A tap on someone's avatar. The default does nothing: group members
     * cannot open each other's profiles unless the app decides so.
     */
    public fun onOpenProfile(uid: String) {}
}

/**
 * Links into the chat UI, for notifications, banners and the app's own
 * screens: `imkit://chat/{conversation}`, `imkit://chats`,
 * `imkit://group/{topic}`.
 */
public sealed interface ImDeepLink {
    public data object Conversations : ImDeepLink

    public data class Conversation(val id: ConversationId) : ImDeepLink

    public data class GroupInfo(val topic: String) : ImDeepLink

    public val uri: Uri
        get() = when (this) {
            Conversations -> Uri.parse("$SCHEME://chats")
            is Conversation -> Uri.Builder().scheme(SCHEME).authority("chat").appendPath(id.id).build()
            is GroupInfo -> Uri.Builder().scheme(SCHEME).authority("group").appendPath(topic).build()
        }

    public companion object {
        public const val SCHEME: String = "imkit"

        /** Null for anything that is not an ImKit link. */
        public fun parse(uri: Uri): ImDeepLink? {
            if (uri.scheme != SCHEME) return null
            val id = uri.pathSegments.firstOrNull()?.takeIf { it.isNotBlank() }
            return when (uri.host) {
                "chats" -> Conversations
                "chat" -> id?.let { Conversation(ConversationId(it)) }
                "group" -> id?.takeIf { it.startsWith("grp_") }?.let { GroupInfo(it) }
                else -> null
            }
        }
    }
}
