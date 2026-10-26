package io.talkwire.ui.screens

import android.Manifest
import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.content.pm.PackageManager
import android.net.Uri
import android.widget.Toast
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.PickVisualMediaRequest
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.RowScope
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyListState
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.ArrowBack
import androidx.compose.material.icons.filled.KeyboardArrowDown
import androidx.compose.material.icons.outlined.Close
import androidx.compose.material.icons.outlined.Forward
import androidx.compose.material.icons.outlined.MoreHoriz
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.FloatingActionButton
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.derivedStateOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.setValue
import androidx.compose.runtime.snapshotFlow
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.LocalLifecycleOwner
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.TextRange
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.TextFieldValue
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.repeatOnLifecycle
import io.talkwire.android.media.MediaSender
import io.talkwire.core.client.ConnectionState
import io.talkwire.core.client.forwardBroadcast
import io.talkwire.core.client.forwardBundle
import io.talkwire.core.engine.ChatEvent
import io.talkwire.core.engine.ConversationSummary
import io.talkwire.core.engine.OutgoingMessage
import io.talkwire.core.model.ChatMessage
import io.talkwire.core.model.ConversationId
import io.talkwire.core.model.MessageStatus
import io.talkwire.core.model.PresenceState
import io.talkwire.ui.R
import io.talkwire.ui.chat.AttachmentPanel
import io.talkwire.ui.chat.FailedAction
import io.talkwire.ui.chat.ForwardDialog
import io.talkwire.ui.chat.ForwardTarget
import io.talkwire.ui.chat.ImageViewerDialog
import io.talkwire.ui.chat.MAX_VOICE_SECONDS
import io.talkwire.ui.chat.MediaActions
import io.talkwire.ui.chat.MessageActionsSheet
import io.talkwire.ui.chat.MessageComposer
import io.talkwire.ui.chat.MessageRow
import io.talkwire.ui.chat.ReplyRef
import io.talkwire.ui.chat.VOICE_CANCEL_DP
import io.talkwire.ui.chat.VoiceInput
import io.talkwire.ui.chat.VoiceRecordOverlay
import io.talkwire.ui.chat.VoiceRecorder
import io.talkwire.ui.chat.readersOf
import io.talkwire.ui.chat.replyExtra
import io.talkwire.ui.components.ImAlertDialog
import io.talkwire.ui.components.ImAvatar
import io.talkwire.ui.components.ImTime
import io.talkwire.ui.host.ImProfile
import io.talkwire.ui.host.ProfileCache
import io.talkwire.ui.message.MessageAction
import io.talkwire.ui.message.MessageScope
import io.talkwire.ui.theme.ImTheme
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.filter
import kotlinx.coroutines.launch
import java.io.File

/** A list entry: one message, with what decides how it is drawn. */
internal data class ListEntry(
    val message: ChatMessage,
    val key: String,
    val showAvatar: Boolean,
    val showName: Boolean,
    val dayHeader: Boolean,
    val unreadHeader: Boolean,
)

/** Stable across sending → sent: own messages keep their local id. */
internal fun listKey(m: ChatMessage): String = m.localId?.let { "l:$it" } ?: "s:${m.seq}"

/**
 * Where the "new messages" divider goes for a chat opened with [summary]:
 * after the read position when something was unread, otherwise nowhere, so
 * messages that arrive while the chat is open do not get a divider.
 */
internal fun dividerAnchor(summary: ConversationSummary): Long = if (summary.unread > 0) summary.lastReadSeq else Long.MAX_VALUE

/**
 * Builds the rows of [timeline] (oldest first): avatars and names on the
 * edges of a run of the same sender, a header at every new day, and the
 * "new messages" divider above the first unread message of others, unless
 * that is the very first message (then there is nothing above to divide).
 */
internal fun buildRows(timeline: List<ChatMessage>, me: String, isGroup: Boolean, unreadAfter: Long?, sameDay: (Long, Long) -> Boolean): List<ListEntry> {
    val firstUnread = if (unreadAfter == null) -1 else timeline.indexOfFirst { it.from != me && (it.seq ?: 0) > unreadAfter }
    return timeline.mapIndexed { i, m ->
        val prev = timeline.getOrNull(i - 1)
        val next = timeline.getOrNull(i + 1)
        val newDay = prev == null || !sameDay(prev.ts, m.ts)
        val samePrev = prev != null && prev.from == m.from && !newDay
        val sameNext = next != null && next.from == m.from && sameDay(m.ts, next.ts)
        ListEntry(
            message = m,
            key = listKey(m),
            // The avatar sits by the last message of a run, the name above the first.
            showAvatar = !sameNext,
            showName = isGroup && !samePrev,
            dayHeader = newDay,
            unreadHeader = i == firstUnread && i > 0,
        )
    }
}

@Composable
internal fun ConversationScreen(state: ImChatState, id: ConversationId, onBack: () -> Unit, onOpenInfo: () -> Unit) {
    val engine = state.engine
    val host = state.host
    val context = LocalContext.current
    val colors = ImTheme.colors
    val scope = rememberCoroutineScope()

    val raw by remember(id) { engine.messages(id) }.collectAsState()
    val timeline = remember(raw) { raw.sortedWith(ChatMessage.TIMELINE) }
    val summaries by engine.conversations.collectAsState()
    val summary = summaries.firstOrNull { it.id == id }
    val groups by engine.groups.collectAsState()
    val people by state.profiles.profiles.collectAsState()
    val typingAll by engine.typing.collectAsState()
    val presence by engine.presence.records.collectAsState()
    val connection by engine.client.state.collectAsState()
    val cursors by remember(id) { engine.readCursors(id) }.collectAsState()
    var members by remember(id) { mutableStateOf<List<io.talkwire.core.model.GroupMember>>(emptyList()) }

    // The read position when the chat was opened: the divider stays put while reading.
    var unreadAfter by remember(id) { mutableStateOf<Long?>(null) }
    LaunchedEffect(summary != null) { if (unreadAfter == null && summary != null) unreadAfter = dividerAnchor(summary) }

    LaunchedEffect(id) {
        runCatching { engine.ensureConversation(id) }
        runCatching { engine.subscribe(id) }
    }
    if (id.isGroup) {
        LaunchedEffect(id) {
            suspend fun load(refresh: Boolean) {
                engine.members(id.id, refresh)?.let { list ->
                    members = list
                    state.profiles.request(list.map { it.uid })
                }
            }
            load(false)
            engine.events.filter { it is ChatEvent.GroupChanged && it.conversation == id }.collect { load(true) }
        }
    } else {
        LaunchedEffect(id) { state.profiles.request(listOf(id.id)) }
    }
    LaunchedEffect(timeline.size) { state.profiles.request(timeline.map { it.from }.distinct()) }

    // Read while the screen is actually visible.
    val lifecycle = LocalLifecycleOwner.current.lifecycle
    // Read through a State: the effect outlives the list it started with, and new arrivals must be read too.
    val newestSeq by rememberUpdatedState(timeline.lastOrNull { it.seq != null }?.seq)
    LaunchedEffect(id, lifecycle) {
        lifecycle.repeatOnLifecycle(Lifecycle.State.RESUMED) {
            snapshotFlow { newestSeq }.distinctUntilChanged().collect {
                runCatching { engine.markRead(id) }
            }
        }
    }

    // Draft: restored once, saved shortly after typing stops and when leaving.
    var input by remember(id) { mutableStateOf(TextFieldValue("")) }
    var draftLoaded by remember(id) { mutableStateOf(false) }
    LaunchedEffect(summary != null) {
        if (!draftLoaded && summary != null) {
            summary.draft?.let { input = TextFieldValue(it, TextRange(it.length)) }
            draftLoaded = true
        }
    }
    LaunchedEffect(id) {
        snapshotFlow { input.text }.distinctUntilChanged().collectLatest { t ->
            if (!draftLoaded) return@collectLatest
            delay(800)
            runCatching { engine.setDraft(id, t) }
        }
    }
    DisposableEffect(id) {
        onDispose { val t = input.text; state.scope.launch { runCatching { engine.setDraft(id, t) } } }
    }

    var replyTo by remember(id) { mutableStateOf<ReplyRef?>(null) }
    var showPanel by remember(id) { mutableStateOf(false) }
    var actionsFor by remember { mutableStateOf<ChatMessage?>(null) }
    var viewing by remember { mutableStateOf<String?>(null) }
    var forwardOne by remember { mutableStateOf<ChatMessage?>(null) }
    var forwardMany by remember { mutableStateOf(false) }
    var selection by remember(id) { mutableStateOf<Set<String>?>(null) }
    var mentionPicker by remember { mutableStateOf(false) }

    val name: (String) -> String = { uid -> if (uid == engine.me) context.getString(R.string.talkwire_you) else people[uid]?.name?.takeIf { it.isNotBlank() } ?: ProfileCache.fallbackName(context, uid) }
    val title = state.title(id, summary, groups, people)
    val left = summary?.left == true

    fun toast(res: Int, vararg args: Any) = Toast.makeText(context, context.getString(res, *args), Toast.LENGTH_SHORT).show()

    fun sendMedia(block: suspend (MediaSender) -> MediaSender.Result) {
        val sender = host.media ?: run { toast(R.string.talkwire_no_media); return }
        state.scope.launch {
            when (val r = block(sender)) {
                is MediaSender.Result.Sent -> Unit
                is MediaSender.Result.TooLarge -> toast(R.string.talkwire_too_large, (r.limitBytes / (1024 * 1024)).toInt())
                is MediaSender.Result.Failed -> toast(R.string.talkwire_send_media_failed)
            }
        }
    }

    // ---- pickers
    val pickMedia = rememberLauncherForActivityResult(ActivityResultContracts.PickVisualMedia()) { uri: Uri? ->
        if (uri != null) sendMedia { it.sendPicked(id, uri) }
    }
    val pickFile = rememberLauncherForActivityResult(ActivityResultContracts.OpenDocument()) { uri: Uri? ->
        if (uri != null) sendMedia { it.sendPicked(id, uri) }
    }
    var cameraFile by remember { mutableStateOf<File?>(null) }
    val takePhoto = rememberLauncherForActivityResult(ActivityResultContracts.TakePicture()) { ok ->
        val f = cameraFile
        cameraFile = null
        if (ok && f != null && f.length() > 0) {
            sendMedia { s -> s.sendPicked(id, Uri.fromFile(f)).also { f.delete() } }
        } else {
            f?.delete()
        }
    }

    // ---- voice
    val recorder = remember { VoiceRecorder(context.applicationContext) }
    var recording by remember { mutableStateOf(false) }
    var recordStart by remember { mutableStateOf(0L) }
    var elapsed by remember { mutableIntStateOf(0) }
    var dragY by remember { mutableFloatStateOf(0f) }
    val cancelPx = with(LocalDensity.current) { VOICE_CANCEL_DP.dp.toPx() }
    val cancelling = recording && dragY < -cancelPx
    val askMic = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { granted ->
        if (!granted) toast(R.string.talkwire_mic_permission)
    }
    fun finishRecording(send: Boolean) {
        if (!recording) return
        recording = false
        val seconds = ((System.currentTimeMillis() - recordStart) / 1000).toInt()
        if (!send) { recorder.cancel(); return }
        val file = recorder.stop()
        when {
            file == null || seconds < 1 -> { file?.delete(); toast(R.string.talkwire_record_too_short) }
            else -> sendMedia { it.sendVoice(id, file, seconds.coerceAtMost(MAX_VOICE_SECONDS)) }
        }
    }
    LaunchedEffect(recording) {
        while (recording) {
            elapsed = ((System.currentTimeMillis() - recordStart) / 1000).toInt()
            if (elapsed >= MAX_VOICE_SECONDS) { finishRecording(send = true); break }
            delay(200)
        }
    }
    DisposableEffect(Unit) { onDispose { if (recorder.isRecording) recorder.cancel() } }
    val voice = if (host.media == null) null else VoiceInput(
        onPress = {
            if (context.checkSelfPermission(Manifest.permission.RECORD_AUDIO) != PackageManager.PERMISSION_GRANTED) {
                askMic.launch(Manifest.permission.RECORD_AUDIO)
            } else if (recorder.start() == null) {
                toast(R.string.talkwire_mic_unavailable)
            } else {
                recordStart = System.currentTimeMillis()
                elapsed = 0
                dragY = 0f
                recording = true
            }
        },
        onDrag = { dragY = it },
        onRelease = { finishRecording(send = dragY >= -cancelPx) },
    )

    fun messageScope(m: ChatMessage) = MessageScope(
        isMine = m.from == engine.me,
        onLongPress = { if (selection == null) actionsFor = m },
        openImage = { viewing = it },
        resolveUrl = host::resolveMediaUrl,
        nameOf = name,
    )

    val listState = rememberLazyListState()
    val rows = remember(timeline, unreadAfter) {
        buildRows(timeline, engine.me, id.isGroup, unreadAfter) { a, b -> ImTime.dayDiff(a, b, java.util.TimeZone.getDefault()) == 0 }
    }

    Column(Modifier.fillMaxSize().background(colors.chatBackground).imePadding()) {
        // ---- top bar
        val sel = selection
        if (sel != null) {
            Row(Modifier.fillMaxWidth().background(colors.background).padding(horizontal = 4.dp, vertical = 4.dp), verticalAlignment = Alignment.CenterVertically) {
                IconButton(onClick = { selection = null }) { Icon(Icons.Outlined.Close, stringResource(R.string.talkwire_cancel), tint = colors.textPrimary) }
                Text(stringResource(R.string.talkwire_selected_count, sel.size), fontSize = 17.sp, color = colors.textPrimary, modifier = Modifier.weight(1f))
                IconButton(onClick = { if (sel.isNotEmpty()) forwardMany = true }, enabled = sel.isNotEmpty()) {
                    Icon(Icons.Outlined.Forward, stringResource(R.string.talkwire_action_forward), tint = if (sel.isNotEmpty()) colors.accent else colors.textHint)
                }
            }
        } else {
            // A typing note has no "stopped": it runs out after
            // EngineConfig.typingTtlMs (aiTypingTtlMs for the group assistant).
            // typingIn() applies that limit; while someone shows as typing,
            // look again every second so it can expire.
            var typingCheck by remember(id) { mutableIntStateOf(0) }
            val typingUids = remember(typingAll, typingCheck) { engine.typingIn(id).filterTo(LinkedHashSet()) { it != engine.me } }
            if (typingUids.isNotEmpty()) {
                LaunchedEffect(typingCheck) {
                    delay(1_000)
                    typingCheck++
                }
            }
            val subtitle = when {
                typingUids.isNotEmpty() && id.isGroup -> stringResource(R.string.talkwire_typing_named, name(typingUids.first()))
                typingUids.isNotEmpty() -> stringResource(R.string.talkwire_typing)
                !id.isGroup && engine.presence.state(id.id) == PresenceState.ONLINE -> stringResource(R.string.talkwire_online)
                !id.isGroup -> ImTime.lastSeen(context, presence[id.id]?.lastSeenAt ?: 0)
                else -> ""
            }
            ChatTopBar(
                title = if (id.isGroup && members.isNotEmpty()) "$title (${members.size})" else title,
                subtitle = subtitle,
                onBack = onBack,
                onInfo = onOpenInfo,
                actions = host.topBarActions(id),
                extras = host.topBarExtras,
                conversation = id,
            )
        }
        if (connection !is ConnectionState.Connected) {
            Text(
                stringResource(R.string.talkwire_connecting),
                fontSize = 12.sp,
                color = colors.textSecondary,
                textAlign = TextAlign.Center,
                modifier = Modifier.fillMaxWidth().background(colors.divider.copy(alpha = 0.5f)).padding(vertical = 4.dp),
            )
        }

        // ---- messages
        Box(Modifier.weight(1f).fillMaxWidth()) {
            MessageListView(
                rows = rows,
                listState = listState,
                state = state,
                id = id,
                people = people,
                members = members.map { it.uid },
                cursors = cursors,
                peerReadSeq = summary?.peerReadSeq ?: 0,
                selection = selection,
                onToggle = { key -> selection = selection?.let { if (key in it) it - key else it + key } },
                scopeFor = ::messageScope,
                name = name,
                onRetry = { m -> scope.launch { runCatching { engine.retry(m) } } },
            )
        }

        // ---- input
        if (left) {
            Text(
                stringResource(R.string.talkwire_removed_from_group),
                fontSize = 14.sp,
                color = colors.textSecondary,
                textAlign = TextAlign.Center,
                modifier = Modifier.fillMaxWidth().background(colors.surface).padding(16.dp).navigationBarsPadding(),
            )
        } else if (selection == null) {
            Column(Modifier.navigationBarsPadding()) {
                MessageComposer(
                    value = input,
                    onValueChange = { input = it },
                    onSend = { text ->
                        val reply = replyTo
                        input = TextFieldValue("")
                        replyTo = null
                        state.scope.launch {
                            runCatching { engine.send(id, OutgoingMessage(content = text, extra = reply?.let { replyExtra(it) })) }
                            runCatching { engine.setDraft(id, null) }
                        }
                    },
                    onToggleAttachments = { showPanel = !showPanel },
                    maxLength = state.config.messageMaxLength,
                    typingThrottleMs = state.config.typingThrottleMs,
                    onTyping = { engine.sendTyping(id) },
                    voice = voice,
                    replyTo = replyTo,
                    replyName = replyTo?.let { name(it.from) } ?: "",
                    onCancelReply = { replyTo = null },
                    onMention = if (id.isGroup) ({ mentionPicker = true }) else null,
                )
                AnimatedVisibility(visible = showPanel) {
                    AttachmentPanel(
                        onPickMedia = { showPanel = false; pickMedia.launch(PickVisualMediaRequest(ActivityResultContracts.PickVisualMedia.ImageAndVideo)) },
                        onTakePhoto = {
                            showPanel = false
                            val (f, uri) = MediaActions.cameraTarget(context)
                            cameraFile = f
                            runCatching { takePhoto.launch(uri) }.onFailure { cameraFile = null; f.delete() }
                        },
                        onPickFile = { showPanel = false; pickFile.launch(arrayOf("*/*")) },
                        extra = host.attachmentActions(id).map { a -> a to { showPanel = false; a.onClick(id) } },
                    )
                }
            }
        }
    }

    if (recording) VoiceRecordOverlay(elapsed, dragY, cancelling, recorder::amplitude)

    // ---- overlays
    actionsFor?.let { m ->
        val mine = m.from == engine.me
        val type = state.types.typeOf(m)
        val actions = type.actions(m).filter { a ->
            when (a) {
                MessageAction.RECALL -> mine && m.seq != null
                MessageAction.REPLY, MessageAction.FORWARD, MessageAction.SELECT -> m.seq != null
                MessageAction.SAVE -> MediaActions.canSave && m.fileUrl != null
                MessageAction.SHARE -> m.fileUrl != null
                MessageAction.COPY -> m.content.isNotBlank()
            }
        }.sortedBy { it.ordinal }
        MessageActionsSheet(
            actions = actions,
            failed = mine && m.status == MessageStatus.FAILED,
            onAction = { a ->
                when (a) {
                    MessageAction.COPY -> {
                        (context.getSystemService(Context.CLIPBOARD_SERVICE) as ClipboardManager).setPrimaryClip(ClipData.newPlainText("message", m.content))
                        toast(R.string.talkwire_copied)
                    }
                    MessageAction.SAVE, MessageAction.SHARE -> {
                        val url = host.resolveMediaUrl(m.fileUrl)
                        if (url != null) {
                            val fileName = m.fileName ?: ("talkwire-" + (m.seq ?: 0) + "." + (android.webkit.MimeTypeMap.getSingleton().getExtensionFromMimeType(m.contentType) ?: "bin"))
                            state.scope.launch {
                                if (a == MessageAction.SAVE) {
                                    toast(if (MediaActions.save(context, url, m.contentType, fileName)) R.string.talkwire_saved else R.string.talkwire_save_failed)
                                } else if (!MediaActions.share(context, url, m.contentType, fileName)) {
                                    toast(R.string.talkwire_action_failed)
                                }
                            }
                        }
                    }
                    MessageAction.REPLY -> replyTo = ReplyRef(m.seq ?: 0, m.from, type.preview(m, context))
                    MessageAction.FORWARD -> forwardOne = m
                    MessageAction.SELECT -> selection = setOf(listKey(m))
                    MessageAction.RECALL -> scope.launch {
                        try {
                            engine.recall(m)
                        } catch (e: CancellationException) {
                            throw e
                        } catch (e: Exception) {
                            toast(R.string.talkwire_recall_failed)
                        }
                    }
                }
            },
            onFailedAction = { f ->
                scope.launch {
                    when (f) {
                        FailedAction.RETRY -> runCatching { engine.retry(m) }
                        FailedAction.DISCARD -> runCatching { engine.discard(m) }
                    }
                }
            },
            onDismiss = { actionsFor = null },
        )
    }

    viewing?.let { url ->
        ImageViewerDialog(
            url = url,
            onSave = if (MediaActions.canSave) ({ state.scope.launch { toast(if (MediaActions.save(context, url, "image/jpeg", "talkwire-${System.currentTimeMillis()}.jpg")) R.string.talkwire_saved else R.string.talkwire_save_failed) } }) else null,
            onDismiss = { viewing = null },
        )
    }

    val targets = remember(summaries, groups, people) {
        summaries.filter { !it.left }.map { s ->
            ForwardTarget(s.id, state.title(s.id, s, groups, people), if (s.id.isGroup) null else host.resolveMediaUrl(people[s.id.id]?.avatarUrl))
        }
    }
    forwardOne?.let { m ->
        ForwardDialog(targets, multiple = true, onConfirm = { ids ->
            forwardOne = null
            state.scope.launch {
                val results = runCatching { engine.client.forwardBroadcast(state.canonicalTopic(id), m.seq ?: return@launch, ids.map { state.canonicalTopic(it) }) }.getOrNull()
                val failed = results?.count { !it.ok } ?: ids.size
                if (failed == 0) toast(R.string.talkwire_forwarded_ok) else toast(R.string.talkwire_forward_failed, failed)
            }
        }, onDismiss = { forwardOne = null })
    }
    if (forwardMany) {
        ForwardDialog(targets, multiple = false, onConfirm = { ids ->
            forwardMany = false
            val keys = selection.orEmpty()
            val seqs = timeline.filter { listKey(it) in keys }.mapNotNull { it.seq }
            selection = null
            val target = ids.firstOrNull() ?: return@ForwardDialog
            state.scope.launch {
                val ok = runCatching { engine.client.forwardBundle(state.canonicalTopic(id), seqs, state.canonicalTopic(target)).ok }.getOrDefault(false)
                toast(if (ok) R.string.talkwire_forwarded_ok else R.string.talkwire_action_failed)
            }
        }, onDismiss = { forwardMany = false })
    }

    if (mentionPicker) {
        val candidates = members.map { it.uid }.filter { it != engine.me }
        ImAlertDialog(
            onDismissRequest = { mentionPicker = false },
            title = null,
            confirmLabel = stringResource(R.string.talkwire_cancel),
            onConfirm = { mentionPicker = false },
            text = {
                LazyColumn(Modifier.heightIn(max = 360.dp)) {
                    items(candidates) { uid ->
                        Row(
                            Modifier.fillMaxWidth().clickable {
                                mentionPicker = false
                                // The "@" is already typed: add the name after it.
                                val at = input.selection.start
                                val insert = name(uid) + " "
                                val t = input.text.substring(0, at) + insert + input.text.substring(at)
                                input = TextFieldValue(t, TextRange(at + insert.length))
                            }.padding(vertical = 8.dp),
                            verticalAlignment = Alignment.CenterVertically,
                        ) {
                            ImAvatar(host.resolveMediaUrl(people[uid]?.avatarUrl), name(uid), size = 36.dp)
                            Spacer(Modifier.width(12.dp))
                            Text(name(uid), fontSize = 15.sp, color = colors.textPrimary)
                        }
                    }
                }
            },
        )
    }
}

@Composable
private fun ChatTopBar(
    title: String,
    subtitle: String,
    onBack: () -> Unit,
    onInfo: () -> Unit,
    actions: List<io.talkwire.ui.host.TopBarAction>,
    extras: (@Composable RowScope.(ConversationId) -> Unit)?,
    conversation: ConversationId,
) {
    val colors = ImTheme.colors
    Surface(color = colors.background) {
        Row(Modifier.fillMaxWidth().padding(horizontal = 4.dp, vertical = 4.dp), verticalAlignment = Alignment.CenterVertically) {
            IconButton(onClick = onBack) { Icon(Icons.Filled.ArrowBack, stringResource(R.string.talkwire_back), tint = colors.textPrimary) }
            Column(Modifier.weight(1f)) {
                Text(title, fontSize = 17.sp, fontWeight = FontWeight.Medium, color = colors.textPrimary, maxLines = 1, overflow = TextOverflow.Ellipsis, modifier = Modifier.testTag("talkwire_chat_title"))
                if (subtitle.isNotEmpty()) Text(subtitle, fontSize = 12.sp, color = colors.textSecondary, maxLines = 1)
            }
            actions.forEach { a -> IconButton(onClick = { a.onClick(conversation) }) { Icon(a.icon, a.label, tint = colors.textPrimary) } }
            extras?.invoke(this, conversation)
            IconButton(onClick = onInfo) { Icon(Icons.Outlined.MoreHoriz, stringResource(R.string.talkwire_chat_info), tint = colors.textPrimary) }
        }
    }
}

@Composable
private fun MessageListView(
    rows: List<ListEntry>,
    listState: LazyListState,
    state: ImChatState,
    id: ConversationId,
    people: Map<String, ImProfile>,
    members: List<String>,
    cursors: Map<String, Long>,
    peerReadSeq: Long,
    selection: Set<String>?,
    onToggle: (String) -> Unit,
    scopeFor: (ChatMessage) -> MessageScope,
    name: (String) -> String,
    onRetry: (ChatMessage) -> Unit,
) {
    val engine = state.engine
    val colors = ImTheme.colors
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    val reversed = remember(rows) { rows.asReversed() }
    var loadingOlder by remember(id) { mutableStateOf(false) }
    var noMore by remember(id) { mutableStateOf(false) }

    // Newest at index 0 (reverse layout): follow new messages only when already at the bottom.
    val newest = rows.lastOrNull()?.key
    LaunchedEffect(newest) {
        val last = rows.lastOrNull()?.message
        if (listState.firstVisibleItemIndex <= 1 || last?.from == engine.me) listState.animateScrollToItem(0)
    }
    val nearTop by remember { derivedStateOf { val info = listState.layoutInfo; info.totalItemsCount > 0 && (info.visibleItemsInfo.lastOrNull()?.index ?: 0) >= info.totalItemsCount - 3 } }
    LaunchedEffect(nearTop, rows.size) {
        if (!nearTop || loadingOlder || noMore || rows.isEmpty()) return@LaunchedEffect
        loadingOlder = true
        val added = try {
            engine.loadOlder(id)
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            -1
        }
        if (added == 0) noMore = true
        loadingOlder = false
    }

    Box(Modifier.fillMaxSize()) {
        LazyColumn(Modifier.fillMaxSize().testTag("talkwire_messages"), state = listState, reverseLayout = true, contentPadding = PaddingValues(vertical = 8.dp)) {
            itemsIndexed(reversed, key = { _, r -> r.key }) { _, r ->
                val m = r.message
                val mine = m.from == engine.me
                // A Column: reverse layout flips an item's root children, which would put the day header below its message.
                Column(Modifier.fillMaxWidth()) {
                    if (r.dayHeader) {
                        Text(ImTime.chatDate(context, m.ts), fontSize = 12.sp, color = colors.textSecondary, textAlign = TextAlign.Center, modifier = Modifier.fillMaxWidth().padding(vertical = 10.dp))
                    }
                    if (r.unreadHeader) {
                        Text(
                            stringResource(R.string.talkwire_new_messages),
                            fontSize = 11.sp,
                            color = colors.accent,
                            textAlign = TextAlign.Center,
                            modifier = Modifier.fillMaxWidth().background(colors.accent.copy(alpha = 0.08f)).padding(vertical = 6.dp),
                        )
                    }
                    val readers = if (id.isGroup && mine) readersOf(m, cursors, members).map { uid -> people[uid] ?: ImProfile(uid, name(uid)) } else emptyList()
                    val canOpen = !mine && (!id.isGroup || state.config.groupMembersCanOpenProfiles)
                    MessageRow(
                        message = m,
                        type = state.types.typeOf(m),
                        isMine = mine,
                        sender = people[m.from],
                        senderName = name(m.from),
                        showAvatar = r.showAvatar,
                        showSenderName = r.showName,
                        read = mine && !id.isGroup && m.seq != null && m.seq!! <= peerReadSeq,
                        readers = readers,
                        groupSize = members.size,
                        scope = scopeFor(m),
                        replyName = name,
                        onRetry = { onRetry(m) },
                        onAvatarClick = if (canOpen) ({ state.host.onOpenProfile(m.from) }) else null,
                        selecting = selection != null,
                        selected = selection?.contains(r.key) == true,
                        onToggleSelect = { onToggle(r.key) },
                    )
                }
            }
            if (loadingOlder) {
                item(key = "talkwire_loading_older") {
                    Box(Modifier.fillMaxWidth().padding(8.dp), contentAlignment = Alignment.Center) { CircularProgressIndicator(Modifier.size(20.dp), strokeWidth = 2.dp) }
                }
            }
        }
        val showJump by remember { derivedStateOf { listState.firstVisibleItemIndex > 2 } }
        AnimatedVisibility(showJump, enter = fadeIn(), exit = fadeOut(), modifier = Modifier.align(Alignment.BottomEnd).padding(16.dp)) {
            FloatingActionButton(onClick = { scope.launch { listState.animateScrollToItem(0) } }, containerColor = colors.surface, modifier = Modifier.size(40.dp)) {
                Icon(Icons.Filled.KeyboardArrowDown, stringResource(R.string.talkwire_jump_to_bottom), tint = colors.textSecondary)
            }
        }
    }
}
