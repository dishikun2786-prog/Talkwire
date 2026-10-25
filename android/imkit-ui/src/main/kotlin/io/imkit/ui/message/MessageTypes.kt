package io.imkit.ui.message

import android.content.Context
import androidx.compose.runtime.Composable
import androidx.compose.runtime.Stable
import androidx.compose.runtime.staticCompositionLocalOf
import io.imkit.core.json.JsonObject
import io.imkit.core.model.ChatMessage
import io.imkit.core.model.MessageKind
import io.imkit.ui.R

/**
 * What a message type gets from the screen it is drawn on. Custom types use
 * it to look like the built-in ones; what they need beyond it (an HTTP
 * client, the host's state) they bring themselves.
 */
@Stable
public class MessageScope(
    public val isMine: Boolean,
    /** Opens the message's actions sheet (copy, recall, forward …). */
    public val onLongPress: () -> Unit,
    /** Opens an image full screen. */
    public val openImage: (String) -> Unit,
    /** Turns a stored media URL into one that can be loaded (see [io.imkit.ui.host.ImUiHost.resolveMediaUrl]). */
    public val resolveUrl: (String?) -> String?,
)

/** What a long press may offer for a message. */
public enum class MessageAction { COPY, SAVE, SHARE, REPLY, FORWARD, SELECT, RECALL }

/**
 * One kind of message bubble (4.3). The type is chosen by [matches], which
 * looks at `contentType` (and `extra`), never at the text in `content`:
 * that text is a fallback for clients that do not know the type, so a
 * message whose content reads "[Image]" is still text unless its type says
 * otherwise (C7).
 */
public interface MessageType {
    /** True when this type draws [message]. Types are tried in registration order. */
    public fun matches(message: ChatMessage): Boolean

    /**
     * True: drawn inside the standard bubble (colour, tail, failed border).
     * False: a card that draws its own container, like an image or a host card.
     */
    public val framed: Boolean get() = true

    @Composable
    public fun Content(message: ChatMessage, scope: MessageScope)

    /** One line for the conversation list and notifications, in [context]'s language. */
    public fun preview(message: ChatMessage, context: Context): String

    /** What a long press offers; recall only ever applies to own messages. */
    public fun actions(message: ChatMessage): Set<MessageAction> =
        setOf(MessageAction.REPLY, MessageAction.FORWARD, MessageAction.SELECT, MessageAction.RECALL)
}

/**
 * The message types the UI knows: the app's own first, then the built-in
 * text, image, video, voice, file and system types, then a fallback that
 * shows the content text of anything unknown. A custom type joins only by
 * being registered; nothing else in the kit names it (4.6).
 */
public class MessageTypeRegistry private constructor(private val types: List<MessageType>) {
    public fun typeOf(message: ChatMessage): MessageType = types.first { it.matches(message) }

    public fun preview(message: ChatMessage, context: Context): String = typeOf(message).preview(message, context)

    public class Builder {
        private val custom = mutableListOf<MessageType>()

        /** Adds a type before the built-in ones, so it can also take over a built-in content type. */
        public fun register(type: MessageType): Builder = apply { custom += type }

        public fun build(): MessageTypeRegistry = MessageTypeRegistry(custom + BuiltInTypes.all + UnknownType)
    }

    public companion object {
        public val Default: MessageTypeRegistry = Builder().build()
    }
}

public val LocalMessageTypes: androidx.compose.runtime.ProvidableCompositionLocal<MessageTypeRegistry> =
    staticCompositionLocalOf { MessageTypeRegistry.Default }

/** Reads `extra` fields without caring whether the server sent an object. */
public fun ChatMessage.extraObject(): JsonObject? = extra as? JsonObject

public fun ChatMessage.extraInt(name: String): Int = extraObject()?.int(name) ?: 0

public fun ChatMessage.extraString(name: String): String? = extraObject()?.nonEmptyString(name)

/** Seconds, from the message's own field, else `extra.duration` (older senders). */
public val ChatMessage.durationSeconds: Int
    get() = if (duration > 0) duration else extraInt("duration")

internal object BuiltInTypes {
    val all: List<MessageType> = listOf(TextType, ImageType, VideoType, VoiceType, FileType, SystemType)
}

internal object TextType : MessageType {
    override fun matches(message: ChatMessage): Boolean = message.kind == MessageKind.TEXT

    @Composable
    override fun Content(message: ChatMessage, scope: MessageScope) {
        TextContent(message.content, scope.isMine)
    }

    override fun preview(message: ChatMessage, context: Context): String = message.content

    override fun actions(message: ChatMessage): Set<MessageAction> = super.actions(message) + MessageAction.COPY
}

internal object ImageType : MessageType {
    override fun matches(message: ChatMessage): Boolean = message.kind == MessageKind.IMAGE
    override val framed: Boolean get() = false

    @Composable
    override fun Content(message: ChatMessage, scope: MessageScope) {
        ImageContent(message, scope)
    }

    override fun preview(message: ChatMessage, context: Context): String = context.getString(R.string.imkit_preview_image)

    override fun actions(message: ChatMessage): Set<MessageAction> = super.actions(message) + MessageAction.SAVE + MessageAction.SHARE
}

internal object VideoType : MessageType {
    override fun matches(message: ChatMessage): Boolean = message.kind == MessageKind.VIDEO
    override val framed: Boolean get() = false

    @Composable
    override fun Content(message: ChatMessage, scope: MessageScope) {
        VideoContent(message, scope)
    }

    override fun preview(message: ChatMessage, context: Context): String = context.getString(R.string.imkit_preview_video)

    override fun actions(message: ChatMessage): Set<MessageAction> = super.actions(message) + MessageAction.SAVE + MessageAction.SHARE
}

internal object VoiceType : MessageType {
    override fun matches(message: ChatMessage): Boolean = message.kind == MessageKind.VOICE
    override val framed: Boolean get() = false

    @Composable
    override fun Content(message: ChatMessage, scope: MessageScope) {
        VoiceContent(message, scope)
    }

    override fun preview(message: ChatMessage, context: Context): String = context.getString(R.string.imkit_preview_voice)
}

internal object FileType : MessageType {
    override fun matches(message: ChatMessage): Boolean = message.kind == MessageKind.FILE

    @Composable
    override fun Content(message: ChatMessage, scope: MessageScope) {
        FileContent(message, scope)
    }

    override fun preview(message: ChatMessage, context: Context): String =
        context.getString(R.string.imkit_preview_file, message.fileName ?: "")

    override fun actions(message: ChatMessage): Set<MessageAction> = super.actions(message) + MessageAction.SAVE + MessageAction.SHARE
}

internal object SystemType : MessageType {
    override fun matches(message: ChatMessage): Boolean = message.kind == MessageKind.SYSTEM
    override val framed: Boolean get() = false

    @Composable
    override fun Content(message: ChatMessage, scope: MessageScope) {
        SystemContent(message.content)
    }

    override fun preview(message: ChatMessage, context: Context): String = message.content

    override fun actions(message: ChatMessage): Set<MessageAction> = emptySet()
}

/** A type nobody registered: its content is the sender's fallback text, so show that. */
internal object UnknownType : MessageType {
    override fun matches(message: ChatMessage): Boolean = true

    @Composable
    override fun Content(message: ChatMessage, scope: MessageScope) {
        TextContent(message.content.ifBlank { "" }, scope.isMine, unsupported = message.content.isBlank())
    }

    override fun preview(message: ChatMessage, context: Context): String =
        message.content.ifBlank { context.getString(R.string.imkit_preview_unsupported) }

    override fun actions(message: ChatMessage): Set<MessageAction> = super.actions(message) + MessageAction.COPY
}
