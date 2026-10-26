package io.talkwire.core.engine

import io.talkwire.core.ImIds
import io.talkwire.core.client.ConnectionState
import io.talkwire.core.client.ImClient
import io.talkwire.core.client.ImEvent
import io.talkwire.core.client.CAP_SYNC_SUMMARY
import io.talkwire.core.client.ImException
import io.talkwire.core.client.PublishOutcome
import io.talkwire.core.client.SyncTopic
import io.talkwire.core.client.clearConversation
import io.talkwire.core.client.groups
import io.talkwire.core.client.history
import io.talkwire.core.client.members
import io.talkwire.core.client.publish
import io.talkwire.core.client.queryPresence
import io.talkwire.core.client.recall
import io.talkwire.core.client.sendRead
import io.talkwire.core.client.sendTyping
import io.talkwire.core.client.subscribe
import io.talkwire.core.client.syncSummary
import io.talkwire.core.client.ackGroupEvent
import io.talkwire.core.client.ackTopicCleared
import io.talkwire.core.model.ChatMessage
import io.talkwire.core.model.ConversationId
import io.talkwire.core.model.GroupInfo
import io.talkwire.core.model.GroupMember
import io.talkwire.core.model.GroupRole
import io.talkwire.core.model.MessageKind
import io.talkwire.core.model.MessageStatus
import io.talkwire.core.model.SendFailure
import io.talkwire.core.protocol.Reply
import io.talkwire.core.protocol.WireMessage
import io.talkwire.core.store.ChatStore
import io.talkwire.core.store.ClearState
import io.talkwire.core.store.StoredConversation
import io.talkwire.core.sync.PresenceTracker
import io.talkwire.core.sync.SyncMath
import io.talkwire.core.sync.TopicResolver
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.CoroutineStart
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import java.util.UUID
import java.util.concurrent.ConcurrentHashMap
import kotlin.math.min

/**
 * The chat state of one signed-in user on top of an [ImClient]: timelines,
 * the conversation list, the send queue, sync, presence, read positions and
 * group rosters. The engine reacts to the client's [ImClient.events] and is
 * the only writer of its [ChatStore].
 *
 * Call [open] before [ImClient.start] (or use [start]) so the first login is
 * not missed.
 *
 * Red lines kept here:
 * - **C2**: a `pub` whose reply timed out while the connection stayed up
 *   stays "sending" and is not resent on that connection; an own message
 *   that comes back from the server is matched by `clientMsgId`, then by
 *   file URL for media, then by text — only when exactly one candidate fits.
 * - **C3**: incremental pulls start at the end of the local contiguous run;
 *   recalled seqs and the clear watermark are not holes; a page proves which
 *   local messages were recalled while offline.
 * - **C4**: [open] wipes the store when it belongs to another user.
 * - **C5**: presence lives only in [presence].
 * - **C9**: my group role comes from the roster; a `grp_sync` marks the
 *   roster stale; a failed fetch means "member".
 */
public class ChatEngine(
    public val client: ImClient,
    private val store: ChatStore,
    /** The host's id of the signed-in user. */
    appUserId: String,
    parentScope: CoroutineScope,
    private val config: EngineConfig = EngineConfig(),
) {
    /** My uid. */
    public val me: String = ImIds.uid(appUserId)

    private val scope = CoroutineScope(parentScope.coroutineContext + SupervisorJob(parentScope.coroutineContext[Job]))

    public val presence: PresenceTracker = PresenceTracker(config.presenceTtlMs, config.clock)

    private val _conversations = MutableStateFlow<List<ConversationSummary>>(emptyList())
    public val conversations: StateFlow<List<ConversationSummary>> = _conversations.asStateFlow()
    private val summaries = ConcurrentHashMap<ConversationId, ConversationSummary>()

    private val _groups = MutableStateFlow<List<GroupInfo>>(emptyList())
    /** The last group list from the server. */
    public val groups: StateFlow<List<GroupInfo>> = _groups.asStateFlow()

    private val _typing = MutableStateFlow<Map<ConversationId, Map<String, Long>>>(emptyMap())
    /** Who is typing where, with the time each note runs out; see [typingIn]. */
    public val typing: StateFlow<Map<ConversationId, Map<String, Long>>> = _typing.asStateFlow()

    private val _events = MutableSharedFlow<ChatEvent>(extraBufferCapacity = Int.MAX_VALUE)
    public val events: SharedFlow<ChatEvent> = _events.asSharedFlow()

    private class Window(val flow: MutableStateFlow<List<ChatMessage>>, @Volatile var size: Int)

    private class Roster(val members: List<GroupMember>, @Volatile var stale: Boolean)

    private val windows = ConcurrentHashMap<ConversationId, Window>()
    private val cursorFlows = ConcurrentHashMap<ConversationId, MutableStateFlow<Map<String, Long>>>()
    private val locks = ConcurrentHashMap<ConversationId, Mutex>()
    private val rosters = ConcurrentHashMap<String, Roster>()

    /** Bumped by every `grp_sync`, so a roster fetched while one arrived is not taken as fresh. */
    private val rosterEpochs = ConcurrentHashMap<String, Long>()
    private val inFlight = ConcurrentHashMap.newKeySet<String>()
    private val retries = ConcurrentHashMap<String, Int>()
    private val retryScheduled = ConcurrentHashMap.newKeySet<String>()
    private val unsentReads = ConcurrentHashMap.newKeySet<ConversationId>()

    /**
     * The `readsAt` of each group as last pulled, for this process only (spec
     * 8.1): after a restart a group with read receipts is pulled once more.
     */
    private val readsSeen = ConcurrentHashMap<ConversationId, Long>()
    private val syncLock = Mutex()
    private var eventsJob: Job? = null
    private var sweeperJob: Job? = null

    // ---------------------------------------------------------------- lifecycle

    /**
     * Checks that the store belongs to this user (C4) and starts reacting to
     * the client. Safe to call more than once.
     */
    public suspend fun open() {
        if (eventsJob != null) return
        val owner = store.owner()
        if (owner != me) {
            // Another user's data, or a store that was never claimed: start
            // clean. If the wipe fails the owner is not set, so the next open
            // tries again instead of adopting someone else's cache.
            store.wipe()
            store.setOwner(me)
        }
        for (c in store.conversations()) refreshSummary(c.id)
        // Subscribed before open() returns: the events flow has no replay, so
        // a Connected emitted by a start() right after open() must not be lost.
        eventsJob = scope.launch(start = CoroutineStart.UNDISPATCHED) { client.events.collect { handle(it) } }
        sweeperJob = scope.launch {
            while (isActive) {
                sweepStaleSending()
                delay(min(config.staleSendingMs, 30_000L))
            }
        }
    }

    /** [open], then start the client. */
    public suspend fun start() {
        open()
        client.start()
    }

    /** Stops reacting and cancels pending work; the client is left to the caller. */
    public fun close() {
        scope.cancel()
    }

    // ---------------------------------------------------------------- reading

    /**
     * The messages of a conversation, newest [EngineConfig.windowSize] first
     * loaded; see [loadOlder]. The first call opens the window and fills it
     * from the store in the background, so collect the flow rather than
     * reading `value` once right after opening it.
     */
    public fun messages(conversation: ConversationId): StateFlow<List<ChatMessage>> {
        val w = windows.computeIfAbsent(conversation) { Window(MutableStateFlow(emptyList()), config.windowSize) }
        scope.launch { reloadWindow(conversation) }
        return w.flow.asStateFlow()
    }

    /** Pages older messages in, from the store first and then from the server. Returns how many were added. */
    public suspend fun loadOlder(conversation: ConversationId, count: Int = config.pageSize): Int {
        val w = windows[conversation] ?: return 0
        val oldest = w.flow.value.mapNotNull { it.seq }.minOrNull() ?: return 0
        if (oldest <= 1) return 0
        val local = store.messagesBefore(conversation, oldest, count)
        if (local.size >= count) {
            w.size += local.size
            reloadWindow(conversation)
            return local.size
        }
        val page = client.history(conversation.wireTopic, limit = count, beforeSeq = oldest)
        withConversation(conversation) {
            for (m in page.messages) applyIncomingLocked(conversation, m, live = false)
        }
        val added = store.messagesBefore(conversation, oldest, count).size
        w.size += added
        reloadWindow(conversation)
        return added
    }

    /**
     * How far each member has read in a group (uid → seq, without me), kept
     * current by read notes and history pages. Empty for direct chats: there
     * the peer's position is [ConversationSummary.peerReadSeq].
     */
    public fun readCursors(conversation: ConversationId): StateFlow<Map<String, Long>> {
        var created = false
        val flow = cursorFlows.computeIfAbsent(conversation) { created = true; MutableStateFlow(emptyMap()) }
        if (created) scope.launch { flow.value = store.readCursors(conversation) }
        return flow.asStateFlow()
    }

    /**
     * Who is typing in [conversation]: people who typed within
     * [EngineConfig.typingTtlMs], and the group assistant
     * ([ChatMessage.AI_ASSISTANT_UID]) for [EngineConfig.aiTypingTtlMs] after
     * `ai_typing`. A message from someone ends their note at once.
     */
    public fun typingIn(conversation: ConversationId, now: Long = config.clock()): Set<String> =
        _typing.value[conversation]?.filterValues { now <= it }?.keys ?: emptySet()

    private fun noteTyping(c: ConversationId, uid: String, ttlMs: Long) {
        val until = config.clock() + ttlMs
        _typing.update { it + (c to ((it[c] ?: emptyMap()) + (uid to until))) }
    }

    private fun endTyping(c: ConversationId, uid: String) {
        _typing.update { all ->
            val notes = all[c] ?: return@update all
            if (uid !in notes) return@update all
            val rest = notes - uid
            if (rest.isEmpty()) all - c else all + (c to rest)
        }
    }

    // ---------------------------------------------------------------- sending (C2)

    /** Stores the message as "sending" and publishes it. Returns the pending message. */
    public suspend fun send(conversation: ConversationId, outgoing: OutgoingMessage): ChatMessage {
        val msg = ChatMessage(
            conversation = conversation,
            seq = null,
            localId = UUID.randomUUID().toString(),
            from = me,
            content = outgoing.content,
            contentType = outgoing.contentType ?: config.textContentType,
            fileUrl = outgoing.fileUrl,
            fileName = outgoing.fileName,
            duration = outgoing.duration,
            extra = outgoing.extra,
            ts = config.clock(),
            status = MessageStatus.SENDING,
        )
        withConversation(conversation) {
            ensureConversationLocked(conversation, title = null, touchedAt = msg.ts)
            store.putMessage(msg)
        }
        scope.launch { publishNow(msg) }
        return msg
    }

    /**
     * Sends a pending message again with the same `clientMsgId`; the server
     * stores it at most once. For failed messages, and for "sending" ones the
     * user wants pushed.
     */
    public suspend fun retry(message: ChatMessage) {
        val localId = message.localId ?: return
        if (message.seq != null || localId in inFlight) return
        retries.remove(localId)
        val again = message.copy(status = MessageStatus.SENDING, failure = null)
        withConversation(message.conversation) { store.putMessage(again) }
        publishNow(again)
    }

    /** Drops a message that never reached the server. Returns its file URLs so the app can delete the upload. */
    public suspend fun discard(message: ChatMessage): List<String> {
        val localId = message.localId ?: return emptyList()
        if (message.seq != null) return emptyList()
        withConversation(message.conversation) { store.deletePending(message.conversation, localId) }
        retries.remove(localId)
        return listOfNotNull(message.fileUrl)
    }

    private suspend fun publishNow(msg: ChatMessage) {
        val localId = msg.localId ?: return
        if (!inFlight.add(localId)) return
        try {
            val outcome = client.publish(
                topic = msg.conversation.wireTopic,
                content = msg.content,
                contentType = msg.contentType,
                clientMsgId = localId,
                fileUrl = msg.fileUrl,
                fileName = msg.fileName,
                duration = msg.duration,
                extra = msg.extra,
            )
            when (outcome) {
                is PublishOutcome.Stored -> {
                    retries.remove(localId)
                    withConversation(msg.conversation) {
                        val cur = store.pendingMessage(msg.conversation, localId) ?: msg
                        val ts = if (outcome.ts > 0) outcome.ts else cur.ts
                        store.promote(msg.conversation, localId, cur.copy(seq = outcome.seq, ts = ts, status = MessageStatus.SENT, failure = null))
                    }
                }
                is PublishOutcome.Refused -> {
                    val failure = SendFailure(outcome.code, outcome.errCode, outcome.reason)
                    if (failure.isDeterministic) fail(msg, failure) else scheduleRetry(msg, failure)
                }
                // The connection is alive, the server most likely has it: keep
                // "sending" and do not resend on this connection.
                PublishOutcome.Pending -> Unit
                PublishOutcome.Lost -> scheduleRetry(msg, SendFailure(0, null, "connection lost"))
            }
        } finally {
            inFlight.remove(localId)
        }
    }

    private suspend fun fail(msg: ChatMessage, failure: SendFailure) {
        val localId = msg.localId ?: return
        retries.remove(localId)
        var failed: ChatMessage? = null
        withConversation(msg.conversation) {
            val cur = store.pendingMessage(msg.conversation, localId) ?: return@withConversation
            failed = cur.copy(status = MessageStatus.FAILED, failure = failure)
            store.putMessage(failed!!)
        }
        failed?.let { _events.tryEmit(ChatEvent.SendFailed(it)) }
    }

    private fun scheduleRetry(msg: ChatMessage, failure: SendFailure) {
        val localId = msg.localId ?: return
        val attempt = (retries[localId] ?: 0) + 1
        if (attempt > config.maxAutoRetries) {
            scope.launch { fail(msg, failure) }
            return
        }
        retries[localId] = attempt
        if (!retryScheduled.add(localId)) return
        scope.launch {
            try {
                delay(min(config.retryMaxDelayMs, config.retryBaseDelayMs shl min(attempt - 1, 5)))
                client.state.first { it is ConnectionState.Connected }
            } finally {
                retryScheduled.remove(localId)
            }
            val cur = store.pendingMessage(msg.conversation, localId) ?: return@launch
            if (cur.status == MessageStatus.FAILED) return@launch // the user gave up or it failed for good
            publishNow(cur)
        }
    }

    /** "Sending" for too long with nothing in flight or scheduled: show it as failed so the user can retry. */
    private suspend fun sweepStaleSending() {
        val now = config.clock()
        for (m in store.pendingMessages()) {
            val id = m.localId ?: continue
            if (m.status != MessageStatus.SENDING || id in inFlight || id in retryScheduled) continue
            if (now - m.ts > config.staleSendingMs) fail(m, SendFailure(0, null, "no confirmation"))
        }
    }

    /** After a (re)login: publish what is still pending from a previous connection. Safe: same clientMsgId. */
    private suspend fun resendPending() {
        for (m in store.pendingMessages()) {
            val id = m.localId ?: continue
            if (m.status != MessageStatus.SENDING || id in inFlight || id in retryScheduled) continue
            scope.launch { publishNow(m) }
        }
    }

    // ---------------------------------------------------------------- actions

    /** Recalls an own stored message for everyone (spec 9.5). */
    public suspend fun recall(message: ChatMessage) {
        val seq = requireNotNull(message.seq) { "only stored messages can be recalled; use discard" }
        val reply = client.recall(message.conversation.wireTopic, seq)
        if (!reply.ok) throw ImException.Refused(reply)
        withConversation(message.conversation) { tombstoneLocked(message.conversation, seq) }
    }

    /**
     * A recalled message becomes a tombstone (who sent it and when, no
     * content), so the chat can show "… unsent a message"; one this device
     * never had leaves only the recalled seq. Either way the seq counts as
     * recalled for the sync cursor.
     */
    private suspend fun tombstoneLocked(c: ConversationId, seq: Long) {
        val held = store.message(c, seq)
        if (held != null && !held.isRecalled) store.putMessage(held.recalled())
        store.addRecalled(c, seq)
    }

    /** Clears the conversation for every member, permanently (spec 11.2). */
    public suspend fun clear(conversation: ConversationId) {
        val result = client.clearConversation(conversation.wireTopic)
        withConversation(conversation) { applyClearLocked(conversation, result.clearVersion, result.clearBeforeSeq) }
    }

    /** Marks everything stored in the conversation as read and tells the others (spec 12.3). */
    public suspend fun markRead(conversation: ConversationId) {
        var seq = 0L
        withConversation(conversation) {
            val conv = store.conversation(conversation) ?: return@withConversation
            val max = store.seqs(conversation).lastOrNull() ?: return@withConversation
            if (max <= conv.lastReadSeq) return@withConversation
            store.putConversation(conv.copy(lastReadSeq = max))
            seq = max
        }
        if (seq > 0 && !client.sendRead(conversation.wireTopic, seq)) unsentReads.add(conversation)
    }

    public fun sendTyping(conversation: ConversationId): Boolean = client.sendTyping(conversation.wireTopic)

    /** Subscribes this connection to the conversation's pushes (spec 7). */
    public suspend fun subscribe(conversation: ConversationId): Reply = client.subscribe(conversation.wireTopic)

    /** Adds a direct conversation (for example with a new contact) so that sync covers it. */
    public suspend fun ensureConversation(conversation: ConversationId, title: String? = null) {
        withConversation(conversation) { ensureConversationLocked(conversation, title, touchedAt = 0) }
    }

    /** Forgets the conversation on this device only. */
    public suspend fun deleteConversation(conversation: ConversationId) {
        withConversation(conversation) { store.deleteConversation(conversation) }
        summaries.remove(conversation)
        publishSummaries()
    }

    public suspend fun setDraft(conversation: ConversationId, draft: String?): Unit = updateConversation(conversation) { it.copy(draft = draft?.takeIf { d -> d.isNotEmpty() }) }

    public suspend fun setPinned(conversation: ConversationId, pinned: Boolean): Unit = updateConversation(conversation) { it.copy(pinned = pinned) }

    public suspend fun setMuted(conversation: ConversationId, muted: Boolean): Unit = updateConversation(conversation) { it.copy(muted = muted) }

    private suspend fun updateConversation(conversation: ConversationId, change: (StoredConversation) -> StoredConversation) {
        withConversation(conversation) {
            val c = store.conversation(conversation) ?: StoredConversation(conversation)
            store.putConversation(change(c))
        }
    }

    // ---------------------------------------------------------------- local search

    /**
     * Text messages stored on this device that contain [query] (case does
     * not matter), newest first. Reads the [scanPerConversation] most recent
     * messages of each conversation through the store, so it also works when
     * message text is encrypted at rest; older messages are not searched.
     */
    public suspend fun searchMessages(query: String, limit: Int = 50, scanPerConversation: Int = 500): List<ChatMessage> {
        val q = query.trim()
        if (q.isEmpty()) return emptyList()
        val hits = ArrayList<ChatMessage>()
        for (c in store.conversations()) {
            for (m in store.latestMessages(c.id, scanPerConversation)) {
                if (m.kind == MessageKind.TEXT && m.content.contains(q, ignoreCase = true)) hits += m
            }
        }
        return hits.sortedByDescending { it.ts }.take(limit)
    }

    // ---------------------------------------------------------------- groups (C9)

    /** The group list from the server; also reconciles which groups I left. Null when the request failed. */
    public suspend fun refreshGroups(): List<GroupInfo>? {
        val list = try {
            client.groups()
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            return null
        }
        _groups.value = list
        val listed = list.associateBy { it.topic }
        for (g in list) {
            val id = ConversationId(g.topic)
            withConversation(id) {
                val c = store.conversation(id)
                if (c == null || c.title != g.name || c.left) {
                    store.putConversation((c ?: StoredConversation(id)).copy(title = g.name, left = false))
                }
            }
        }
        // A local group missing from the list: kicked or disbanded while away,
        // and the replayed event has not arrived yet (spec 8 step 2).
        for (c in store.conversations()) {
            if (c.id.isGroup && !c.left && c.id.id !in listed) markLeft(c.id)
        }
        return list
    }

    /** The roster, fetched again when stale or [refresh]; null when the request failed. */
    public suspend fun members(topic: String, refresh: Boolean = false): List<GroupMember>? {
        val cached = rosters[topic]
        if (cached != null && !cached.stale && !refresh) return cached.members
        val epoch = rosterEpochs[topic] ?: 0L
        val list = try {
            client.members(topic)
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            return null
        }
        rosters[topic] = Roster(list, stale = (rosterEpochs[topic] ?: 0L) != epoch)
        return list
    }

    /**
     * Marks a group's roster stale and emits [ChatEvent.GroupChanged], as a
     * `grp_sync` would: for changes this device made through another module
     * that the server does not echo back to it (approving a join request
     * tells only the requester, F45). Screens showing the members reload on
     * the event.
     */
    public fun noteGroupChanged(topic: String) {
        rosterEpochs.merge(topic, 1L, Long::plus)
        rosters[topic]?.stale = true
        _events.tryEmit(ChatEvent.GroupChanged(ConversationId(topic)))
    }

    /** Whether the cached roster of [topic] is marked stale; null when none is cached. */
    internal fun rosterStale(topic: String): Boolean? = rosters[topic]?.stale

    /** How many `grp_sync` hints for [topic] the engine has handled. */
    internal fun rosterEpoch(topic: String): Long = rosterEpochs[topic] ?: 0L

    /** My role, from the roster; "member" when the roster cannot be fetched (fail closed). */
    public suspend fun myRole(topic: String): GroupRole =
        members(topic)?.firstOrNull { it.uid == me }?.role ?: GroupRole.MEMBER

    private suspend fun markLeft(id: ConversationId) {
        rosters.remove(id.id)
        var changed = false
        withConversation(id) {
            val c = store.conversation(id) ?: return@withConversation
            if (!c.left) {
                store.putConversation(c.copy(left = true))
                changed = true
            }
        }
        if (changed) _events.tryEmit(ChatEvent.RemovedFromGroup(id))
    }

    // ---------------------------------------------------------------- sync (spec 8, C3)

    /**
     * The full sync of protocol spec 8. Runs after every login when
     * [EngineConfig.autoSync]. With a server that offers `sync_summary`, only
     * the conversations that changed are pulled (spec 8.1).
     */
    public suspend fun syncAll() {
        syncLock.withLock {
            refreshGroups()
            val summary = fetchSummary()
            var done = 0
            var failed = 0
            var pulled = 0
            for (c in store.conversations()) {
                if (c.left) continue
                val changes = summary?.get(c.id)
                try {
                    if (summary != null && !changed(c.id, changes)) {
                        if (changes != null) applySummary(c.id, changes)
                        done++
                        continue
                    }
                    syncConversation(c.id, changes?.lastSeq)
                    changes?.readsAt?.let { readsSeen[c.id] = it }
                    done++
                    pulled++
                } catch (e: CancellationException) {
                    throw e
                } catch (e: Exception) {
                    failed++
                }
            }
            try {
                for (s in client.queryPresence(emptyList())) presence.apply(s.uid, s.online, s.rev, s.lastSeenAt)
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                // presence is best effort
            }
            for (c in unsentReads.toList()) {
                val conv = store.conversation(c) ?: continue
                if (conv.lastReadSeq > 0 && client.sendRead(c.wireTopic, conv.lastReadSeq)) unsentReads.remove(c)
            }
            resendPending()
            _events.tryEmit(ChatEvent.Synced(done, failed, pulled))
        }
    }

    /** The `sync_summary` by conversation; null when the server does not offer it or it failed (then every conversation is pulled). */
    private suspend fun fetchSummary(): Map<ConversationId, SyncTopic>? {
        if (client.session?.has(CAP_SYNC_SUMMARY) != true) return null
        val topics = try {
            client.syncSummary()
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            return null
        }
        return topics.mapNotNull { t -> (if (t.isGroup) t.topic else t.peer)?.let { ConversationId(it) to t } }.toMap()
    }

    /** Whether a conversation has to be pulled, by its summary entry (spec 8.1). */
    private suspend fun changed(id: ConversationId, t: SyncTopic?): Boolean {
        // A group listed by get_groups but not summarized: pull it. A direct
        // chat missing from the summary has no conversation on the server yet.
        if (t == null) return id.isGroup
        val clear = store.clearState(id)
        if (t.clearVersion > (clear?.clearVersion ?: 0)) return true
        if (t.readsAt != null && readsSeen[id] != t.readsAt) return true
        val end = SyncMath.resumeSeq(store.seqs(id), store.recalledSeqs(id), clear?.clearBeforeSeq ?: 0)
        return t.lastSeq > end
    }

    /** What an unchanged conversation's summary entry still tells: my read position on another device. */
    private suspend fun applySummary(id: ConversationId, t: SyncTopic) {
        val read = t.readSeq ?: return
        withConversation(id) { advanceOwnReadLocked(id, read) }
    }

    /**
     * Pulls what this device missed in one conversation, starting at the end
     * of the local contiguous run (C3). The first page also carries the clear
     * watermark, applied before any message is stored. Seqs the server skips
     * are recorded as recalled (spec 8.2), up to [lastSeq] from the summary
     * when it is known, so the next sync does not start over from them.
     */
    public suspend fun syncConversation(conversation: ConversationId, lastSeq: Long? = null) {
        val st = store.clearState(conversation)
        val heldBefore = store.seqs(conversation)
        val recalled = store.recalledSeqs(conversation).toHashSet()
        var resume = SyncMath.resumeSeq(heldBefore, recalled, st?.clearBeforeSeq ?: 0)
        var fetched = 0
        var first = true
        while (fetched < config.offlinePullMax) {
            val afterSeq = resume.takeIf { it > 0 }
            val page = client.history(conversation.wireTopic, limit = config.pageSize, afterSeq = afterSeq)
            val full = page.messages.size >= config.pageSize
            withConversation(conversation) {
                if (first) applyClearLocked(conversation, page.clearVersion, page.clearBeforeSeq)
                page.readCursors?.forEach { (uid, seq) -> if (uid != me) store.setReadCursor(conversation, uid, seq) }
                val watermark = store.clearState(conversation)?.clearBeforeSeq ?: 0
                val gone = SyncMath.seqsGone(heldBefore, afterSeq ?: 0, page.messages.map { it.seq }, full, lastSeq, watermark)
                for (s in gone) if (recalled.add(s)) tombstoneLocked(conversation, s)
                for (m in page.messages) applyIncomingLocked(conversation, m, live = false)
                // The user's own read position as the server keeps it: readSeq
                // (strict), or a group's own read cursor. A new device would
                // otherwise count all the history it pulled as unread.
                val ownRead = listOfNotNull(page.readSeq, page.readCursors?.get(me)).maxOrNull()
                if (ownRead != null) advanceOwnReadLocked(conversation, ownRead)
            }
            first = false
            fetched += page.messages.size
            if (afterSeq == null || !full) break
            val max = page.messages.maxOf { it.seq }
            if (max <= resume) break
            resume = max
        }
    }

    // ---------------------------------------------------------------- events

    private suspend fun handle(e: ImEvent) {
        try {
            when (e) {
                is ImEvent.Connected -> onConnected(e)
                is ImEvent.Disconnected -> Unit
                is ImEvent.Message -> onMessage(e.message)
                is ImEvent.Recalled -> resolve(e.topic, e.from)?.let { c ->
                    withConversation(c) { tombstoneLocked(c, e.seq) }
                }
                is ImEvent.TopicCleared -> {
                    resolve(e.topic, e.from)?.let { c ->
                        var applied = false
                        withConversation(c) { applied = applyClearLocked(c, e.clearVersion, e.clearBeforeSeq) }
                        if (applied) _events.tryEmit(ChatEvent.ConversationCleared(c, e.from))
                    }
                    e.offlineId?.let { client.ackTopicCleared(it) }
                }
                is ImEvent.Presence -> presence.apply(e.uid, e.online, e.rev)
                is ImEvent.Typing -> resolve(e.topic, e.from)?.let { c ->
                    if (e.from != me) noteTyping(c, e.from, config.typingTtlMs)
                }
                is ImEvent.Read -> resolve(e.topic, e.from)?.let { c -> onRead(c, e.from, e.seq) }
                is ImEvent.GroupSync -> {
                    // Only a hint (D1): refresh, do not trust its contents. A
                    // p2p topic here is the D2 defect of old servers: ignore it.
                    if (ImIds.isGroupTopic(e.topic)) {
                        rosterEpochs.merge(e.topic, 1L, Long::plus)
                        rosters[e.topic]?.stale = true
                        val id = ConversationId(e.topic)
                        withConversation(id) {
                            val c = store.conversation(id)
                            val title = e.name ?: c?.title
                            if (c == null || c.left || c.title != title) {
                                store.putConversation((c ?: StoredConversation(id, updatedAt = config.clock())).copy(title = title, left = false))
                            }
                        }
                        _events.tryEmit(ChatEvent.GroupChanged(id))
                    }
                    e.eventId?.let { client.ackGroupEvent(it) }
                }
                is ImEvent.GroupRemoved -> {
                    if (ImIds.isGroupTopic(e.topic)) markLeft(ConversationId(e.topic))
                    e.eventId?.let { client.ackGroupEvent(it) }
                }
                is ImEvent.ContactSync -> _events.tryEmit(ChatEvent.ContactChanged(e.what, e.peerUid))
                is ImEvent.Custom -> Unit
                is ImEvent.Module -> {
                    // The group assistant is answering (spec 15.2): only in a group this device knows.
                    val aiTopic = e.frame.string("topic")
                    if (e.frame.string("type") == "ai_typing" && aiTopic != null && ImIds.isGroupTopic(aiTopic) && summaries.containsKey(ConversationId(aiTopic))) {
                        noteTyping(ConversationId(aiTopic), ChatMessage.AI_ASSISTANT_UID, config.aiTypingTtlMs)
                    }
                    val action = e.frame.string("action")
                    if (e.eventId != null && (action == "group_join_request" || action == "group_join_reviewed")) {
                        client.ackGroupEvent(e.eventId)
                    }
                }
            }
        } catch (ce: CancellationException) {
            throw ce
        } catch (ex: Exception) {
            // One bad frame must not stop the engine.
        }
    }

    private fun onConnected(e: ImEvent.Connected) {
        if (e.session.uid != me) {
            client.stop()
            _events.tryEmit(ChatEvent.WrongAccount(me, e.session.uid))
            return
        }
        if (config.autoSync) scope.launch { syncAll() }
    }

    private suspend fun onMessage(w: WireMessage) {
        val c = resolve(w.topic, w.from) ?: return
        endTyping(c, w.from)
        var arrived: ChatMessage? = null
        withConversation(c) {
            if (c.isGroup && w.groupName != null) ensureConversationLocked(c, w.groupName, touchedAt = w.ts)
            if (applyIncomingLocked(c, w, live = true)) arrived = store.message(c, w.seq)
        }
        arrived?.let { _events.tryEmit(ChatEvent.MessageArrived(it, if (c.isGroup) w.groupName else null)) }
    }

    private suspend fun onRead(c: ConversationId, from: String, seq: Long) {
        withConversation(c) {
            val conv = store.conversation(c) ?: return@withConversation
            when {
                from == me -> advanceOwnReadLocked(c, seq)
                c.isGroup -> store.setReadCursor(c, from, seq)
                from == c.id -> if (seq > conv.peerReadSeq) store.putConversation(conv.copy(peerReadSeq = seq))
            }
        }
    }

    private suspend fun resolve(topic: String, from: String): ConversationId? =
        TopicResolver.conversationOf(topic, from, me) {
            summaries.keys.filter { !it.isGroup }.map { it.id }
        }

    // ---------------------------------------------------------------- applying (caller holds the conversation lock)

    /**
     * Stores a message from the server. Own messages replace their pending
     * row (C2). Returns true when someone else's message was new.
     */
    private suspend fun applyIncomingLocked(c: ConversationId, w: WireMessage, live: Boolean): Boolean {
        val existing = store.message(c, w.seq)
        val clear = store.clearState(c)
        if (clear != null && w.seq <= clear.clearBeforeSeq) return false
        ensureConversationLocked(c, title = null, touchedAt = if (existing == null) w.ts else 0)
        if (w.from == me) {
            if (existing != null && existing.status == MessageStatus.SENT) return false
            val pending = store.pendingMessages().filter { it.conversation == c }
            val match = w.clientMsgId?.let { id -> pending.firstOrNull { it.localId == id } }
                ?: pending.filter {
                    if (w.fileUrl != null) it.fileUrl == w.fileUrl else it.fileUrl == null && it.content == w.content && it.contentType == w.contentType
                }.singleOrNull()
            val msg = w.toChatMessage(c, MessageStatus.SENT, match?.localId ?: w.clientMsgId)
            if (match?.localId != null) {
                store.promote(c, match.localId, msg)
                retries.remove(match.localId)
            } else {
                store.putMessage(msg)
            }
            return false
        }
        if (existing != null) return false
        store.putMessage(w.toChatMessage(c, MessageStatus.RECEIVED, null))
        return live
    }

    /** Moves the user's own read position forward to [seq]; never back. */
    private suspend fun advanceOwnReadLocked(c: ConversationId, seq: Long) {
        val conv = store.conversation(c) ?: return
        if (seq > conv.lastReadSeq) store.putConversation(conv.copy(lastReadSeq = seq))
    }

    /** Applies a clear watermark if it is newer than what is known. Returns whether it was applied. */
    private suspend fun applyClearLocked(c: ConversationId, version: Long, beforeSeq: Long): Boolean {
        if (version <= 0) return false
        val prev = store.clearState(c)
        if (prev != null && prev.clearVersion >= version) return false
        store.deleteThrough(c, beforeSeq)
        store.setClearState(c, ClearState(version, beforeSeq))
        return true
    }

    private suspend fun ensureConversationLocked(c: ConversationId, title: String?, touchedAt: Long) {
        val cur = store.conversation(c)
        if (cur == null) {
            store.putConversation(StoredConversation(c, title = title, updatedAt = touchedAt))
            return
        }
        val newTitle = title ?: cur.title
        val newTime = maxOf(cur.updatedAt, touchedAt)
        if (newTitle != cur.title || newTime != cur.updatedAt) store.putConversation(cur.copy(title = newTitle, updatedAt = newTime))
    }

    private fun WireMessage.toChatMessage(c: ConversationId, status: MessageStatus, localId: String?): ChatMessage = ChatMessage(
        conversation = c,
        seq = seq,
        localId = localId,
        from = from,
        content = content,
        contentType = contentType,
        fileUrl = fileUrl,
        fileName = fileName,
        duration = duration,
        extra = extra,
        senderKind = senderKind,
        forwardFromMsgId = forwardFromMsgId,
        forwardFromTopic = forwardFromTopic,
        ts = ts,
        status = status,
    )

    // ---------------------------------------------------------------- views

    private suspend fun <T> withConversation(c: ConversationId, block: suspend () -> T): T {
        val r = locks.computeIfAbsent(c) { Mutex() }.withLock { block() }
        reloadWindow(c)
        cursorFlows[c]?.let { it.value = store.readCursors(c) }
        refreshSummary(c)
        return r
    }

    private suspend fun reloadWindow(c: ConversationId) {
        val w = windows[c] ?: return
        w.flow.value = store.latestMessages(c, w.size)
    }

    private suspend fun refreshSummary(c: ConversationId) {
        val conv = store.conversation(c)
        if (conv == null) {
            summaries.remove(c)
        } else {
            summaries[c] = ConversationSummary(
                id = c,
                title = conv.title,
                lastMessage = store.latestMessages(c, 1).lastOrNull(),
                unread = store.countUnread(c, conv.lastReadSeq, me),
                draft = conv.draft,
                pinned = conv.pinned,
                muted = conv.muted,
                left = conv.left,
                updatedAt = conv.updatedAt,
                lastReadSeq = conv.lastReadSeq,
                peerReadSeq = conv.peerReadSeq,
            )
        }
        publishSummaries()
    }

    private fun publishSummaries() {
        _conversations.value = summaries.values.sortedWith(compareByDescending<ConversationSummary> { it.pinned }.thenByDescending { it.updatedAt })
    }
}
