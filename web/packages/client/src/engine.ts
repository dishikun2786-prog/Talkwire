/**
 * `ChatEngine`: the client's local view of conversations and messages, kept
 * in step with the server by the sync algorithm of protocol section 8. It is
 * written from the spec, not ported from another client:
 *
 * - after every login: `get_groups` (groups I am no longer in are marked
 *   left), then `get` for every conversation from the end of the local
 *   contiguous run of seqs (C3), page by page until a page is short; the
 *   first page carries the clear watermark and, from strict servers, my read
 *   position, which only moves forward; then `pres` for the single-chat peers;
 * - pending clears and group events delivered after login are applied and
 *   acknowledged with the ids they came with (sections 10.10, 11.3);
 * - a single chat is filed under the peer's uid whatever spelling a frame
 *   uses (section 5.5); a frame from myself (another device or tab) is filed
 *   under its topic;
 * - a publish whose reply timed out on a live connection stays "sending" and
 *   is not sent again on its own (C2); one cut off by a dropped connection is
 *   sent again after the next sync with the same clientMsgId, which the
 *   server de-duplicates;
 * - an own message pulled back from history is matched to the pending one by
 *   `fileUrl` for media, and by content for text (C2);
 * - my role in a group comes from the roster (`get_members`), which a
 *   `grp_sync` marks stale; without a roster the role is "member" (C9).
 */

import { conversationOf, isGroupTopic, isP2pTopic, uidOf } from "@imkit/protocol";
import { RequestRefused, type HistoryPage, type ImClient, type PublishFields, type PublishOutcome, type SyncTopic } from "./client.js";
import type { ImEvent } from "./connection.js";
import {
  AI_ASSISTANT_UID,
  isPending,
  isRecalled,
  messageKey,
  messageKind,
  newConversation,
  recalledCopy,
  timelineOrder,
  type ChatMessage,
  type ConversationId,
  type ConversationSummary,
  type GroupInfo,
  type GroupMember,
  type GroupRole,
  type StoredConversation,
} from "./model.js";
import { PresenceTracker } from "./presence.js";
import type { ChatStore } from "./store.js";
import { resumeSeq, seqsGone } from "./sync-math.js";
import { Emitter, Signal, randomId, sleep, type ReadonlySignal, type Unsubscribe } from "./util.js";
import type { WireMessage } from "./wire.js";

export interface EngineConfig {
  /** The host user id this engine belongs to; its uid keys the store (C4). Give this or `uid`. */
  appUserId?: string;
  uid?: string;
  /** Page size of history requests. */
  pageSize?: number;
  /** At most this many messages per conversation in one incremental pull. */
  offlinePullMax?: number;
  /** Run the full sync after every login. Off in transcript tests, which script every request. */
  autoSync?: boolean;
  /** Automatic resends of a message whose connection dropped. */
  maxAutoRetries?: number;
  /** A message still "sending" after this long with nothing in flight becomes failed. */
  staleSendingMs?: number;
  typingTtlMs?: number;
  /**
   * How long "the assistant is typing" lasts in a group without its reply.
   * The server waits up to 10 seconds for the host's answer, then posts a
   * notice, so the reply normally ends it first.
   */
  aiTypingTtlMs?: number;
  presenceTtlMs?: number;
  /** `contentType` of plain text. The reference app uses `text`. */
  textContentType?: string;
  /** Messages held per open conversation before older ones are paged in. */
  windowSize?: number;
  clock?: () => number;
}

/** A message to send. Media is uploaded to the host first; only its URL goes over the IM connection. */
export interface OutgoingMessage {
  content?: string;
  /** Plain text when omitted. */
  contentType?: string;
  fileUrl?: string;
  fileName?: string;
  /** Seconds, for audio and video. */
  duration?: number;
  extra?: unknown;
}

/** What the engine tells the app; protocol-level pushes stay on `client.events`. */
export type ChatEvent =
  /** A new message from someone else arrived live (not during a sync). */
  | { kind: "messageArrived"; message: ChatMessage; groupName?: string }
  /** A send failed for good: refused by the server, or out of automatic retries. */
  | { kind: "sendFailed"; message: ChatMessage }
  /** I am no longer in this group (kicked, disbanded, or missing from the group list). */
  | { kind: "removedFromGroup"; conversation: ConversationId }
  /** Someone cleared the conversation for everyone. */
  | { kind: "conversationCleared"; conversation: ConversationId; by: string }
  | { kind: "recalled"; conversation: ConversationId; seq: number; by: string }
  /** The server hinted that a group's name or roster changed. */
  | { kind: "groupChanged"; conversation: ConversationId }
  /** The host changed a friendship (`contact_sync`). */
  | { kind: "contactChanged"; what: string; peerUid: string }
  /**
   * A sync after login finished. `conversations` are up to date; `pulled` of
   * them needed a `get` (with `sync_summary`, only those that changed).
   */
  | { kind: "synced"; conversations: number; failed: number; pulled: number }
  /** The server logged in a different user than this engine belongs to; the client was stopped. */
  | { kind: "wrongAccount"; expected: string; actual: string };

interface Roster {
  members: GroupMember[];
  stale: boolean;
}

interface Window {
  signal: Signal<ChatMessage[]>;
  loaded: boolean;
}

export class ChatEngine {
  readonly me: string;
  readonly config: Required<Omit<EngineConfig, "appUserId" | "uid">>;
  readonly presence: PresenceTracker;
  readonly events = new Emitter<ChatEvent>();

  private readonly _conversations = new Signal<ConversationSummary[]>([]);
  readonly conversations: ReadonlySignal<ConversationSummary[]> = this._conversations;
  private readonly _groups = new Signal<GroupInfo[]>([]);
  readonly groups: ReadonlySignal<GroupInfo[]> = this._groups;
  private readonly _typing = new Signal<ReadonlyMap<ConversationId, ReadonlyMap<string, number>>>(new Map());
  readonly typing: ReadonlySignal<ReadonlyMap<ConversationId, ReadonlyMap<string, number>>> = this._typing;

  private readonly convs = new Map<ConversationId, StoredConversation>();
  private readonly lastMessage = new Map<ConversationId, ChatMessage>();
  private readonly unread = new Map<ConversationId, number>();
  private readonly windows = new Map<ConversationId, Window>();
  private readonly cursorSignals = new Map<ConversationId, Signal<ReadonlyMap<string, number>>>();
  private readonly rosters = new Map<string, Roster>();
  private readonly rosterEpochs = new Map<string, number>();
  private readonly inflight = new Set<string>();
  /** Local ids whose publish timed out on a live connection: no automatic resend (C2). */
  private readonly timedOut = new Set<string>();
  private readonly autoRetries = new Map<string, number>();
  private readonly unsentReads = new Set<ConversationId>();
  /**
   * The `readsAt` of each group as last pulled, for this process only (section
   * 8.1): after a restart a group with read receipts is pulled once more.
   */
  private readonly readsSeen = new Map<ConversationId, number>();
  private lock: Promise<unknown> = Promise.resolve();
  private opened = false;
  private syncing = false;
  private unsubscribe: Unsubscribe | null = null;
  private typingTimer: ReturnType<typeof setTimeout> | null = null;
  private groupRefreshTimer: ReturnType<typeof setTimeout> | null = null;

  constructor(
    readonly client: ImClient,
    readonly store: ChatStore,
    config: EngineConfig,
  ) {
    const uid = config.uid ?? (config.appUserId !== undefined ? uidOf(config.appUserId) : undefined);
    if (!uid) throw new Error("give appUserId or uid");
    this.me = uid;
    this.config = {
      pageSize: 50,
      offlinePullMax: 500,
      autoSync: true,
      maxAutoRetries: 5,
      staleSendingMs: 5 * 60_000,
      typingTtlMs: 10_000,
      aiTypingTtlMs: 15_000,
      presenceTtlMs: 180_000,
      textContentType: "text",
      windowSize: 50,
      clock: Date.now,
      ...withoutIds(config),
    };
    this.presence = new PresenceTracker(this.config.presenceTtlMs, this.config.clock);
  }

  /**
   * Claims the store for this user (wiping another user's data, C4), loads
   * the conversation list and starts listening to the client. Call before
   * `client.start()`.
   */
  async open(): Promise<void> {
    if (this.opened) return;
    await this.exclusive(async () => {
      const owner = await this.store.owner();
      if (owner !== this.me) {
        // Another account's cache, or one never claimed: neither can be trusted.
        await this.store.wipe();
        await this.store.setOwner(this.me);
      }
      for (const c of await this.store.conversations()) this.convs.set(c.id, c);
      for (const id of this.convs.keys()) await this.loadSummary(id);
      this.publishSummaries();
    });
    this.opened = true;
    this.unsubscribe = this.client.events.on((ev) => void this.exclusive(() => this.onEvent(ev)).catch(() => {}));
    if (this.client.isConnected) void this.onConnected(this.client.session!.uid);
  }

  close(): void {
    this.unsubscribe?.();
    this.unsubscribe = null;
    this.opened = false;
    this.presence.dispose();
    if (this.typingTimer) clearTimeout(this.typingTimer);
    if (this.groupRefreshTimer) clearTimeout(this.groupRefreshTimer);
  }

  // ------------------------------------------------------------------ reading

  /** The messages of a conversation in timeline order; older ones come with `loadOlder`. */
  messages(conversation: ConversationId): ReadonlySignal<ChatMessage[]> {
    let w = this.windows.get(conversation);
    if (!w) {
      w = { signal: new Signal<ChatMessage[]>([]), loaded: false };
      this.windows.set(conversation, w);
      const win = w;
      void this.exclusive(async () => {
        const held = await this.store.latestMessages(conversation, this.config.windowSize);
        // Messages applied before the load finished are already in the signal.
        win.signal.set(mergeMessages(held, win.signal.value));
        win.loaded = true;
      });
    }
    return w.signal;
  }

  conversation(id: ConversationId): ConversationSummary | undefined {
    return this._conversations.value.find((c) => c.id === id);
  }

  /** Read positions of group members (groups with read receipts on). */
  readCursors(conversation: ConversationId): ReadonlySignal<ReadonlyMap<string, number>> {
    let s = this.cursorSignals.get(conversation);
    if (!s) {
      s = new Signal<ReadonlyMap<string, number>>(new Map());
      this.cursorSignals.set(conversation, s);
      const sig = s;
      void this.store.readCursors(conversation).then((m) => sig.set(new Map([...m, ...sig.value])));
    }
    return s;
  }

  /** Who is typing in a conversation right now. */
  typingIn(conversation: ConversationId, now = this.config.clock()): string[] {
    const m = this._typing.value.get(conversation);
    return m ? [...m].filter(([, until]) => until > now).map(([uid]) => uid) : [];
  }

  /**
   * Pages older messages into the window: from the store first, then from
   * the server. Returns how many were added; 0 at the start of history.
   */
  async loadOlder(conversation: ConversationId, count = this.config.pageSize): Promise<number> {
    const window = this.messages(conversation) as Signal<ChatMessage[]>;
    await this.exclusive(async () => {}); // the initial load
    const oldest = window.value.find((m) => m.seq !== null)?.seq ?? null;
    const local = oldest === null ? [] : await this.store.messagesBefore(conversation, oldest, count);
    if (local.length > 0) {
      await this.exclusive(async () => window.set(mergeMessages(local, window.value)));
      return local.length;
    }
    if (oldest === 1 || !this.client.isConnected) return 0;
    const clear = await this.store.clearState(conversation);
    if (oldest !== null && clear && oldest <= clear.clearBeforeSeq + 1) return 0;
    const page = await this.client.history(conversation, oldest === null ? { limit: count } : { beforeSeq: oldest, limit: count });
    if (page.messages.length === 0) return 0;
    let added = 0;
    await this.exclusive(async () => {
      const before = window.value.length;
      for (const w of page.messages) await this.storeWire(conversation, w, false);
      added = window.value.length - before;
      await this.refreshSummary(conversation);
    });
    return added;
  }

  // ------------------------------------------------------------------ sending

  /** Queues a message; it is sent now when connected, after the next sync otherwise. */
  async send(conversation: ConversationId, outgoing: OutgoingMessage): Promise<ChatMessage> {
    const msg: ChatMessage = {
      conversation,
      seq: null,
      localId: randomId(),
      from: this.me,
      content: outgoing.content ?? "",
      contentType: outgoing.contentType ?? this.config.textContentType,
      duration: outgoing.duration ?? 0,
      senderKind: "user",
      ts: this.config.clock(),
      status: "sending",
    };
    if (outgoing.fileUrl) msg.fileUrl = outgoing.fileUrl;
    if (outgoing.fileName) msg.fileName = outgoing.fileName;
    if (outgoing.extra !== undefined && outgoing.extra !== null) msg.extra = outgoing.extra;
    await this.exclusive(async () => {
      await this.ensure(conversation);
      await this.store.putMessage(msg);
      this.applyToWindow(msg);
      await this.touch(conversation, msg.ts);
    });
    if (this.client.isConnected) void this.dispatch(msg);
    return msg;
  }

  /** Sends a pending message again with the same clientMsgId. */
  async retry(message: ChatMessage): Promise<void> {
    if (!message.localId || message.seq !== null) return;
    const held = await this.store.pendingMessage(message.conversation, message.localId);
    if (!held) return;
    this.timedOut.delete(held.localId!);
    this.autoRetries.delete(held.localId!);
    if (held.status !== "sending") {
      const again: ChatMessage = { ...held, status: "sending" };
      delete again.failure;
      await this.exclusive(async () => {
        await this.store.putMessage(again);
        this.applyToWindow(again);
        await this.refreshSummary(again.conversation);
      });
      await this.dispatch(again);
    } else {
      await this.dispatch(held);
    }
  }

  /** Drops a pending message; returns the media URLs it referenced, for the host to delete. */
  async discard(message: ChatMessage): Promise<string[]> {
    if (!message.localId || message.seq !== null) return [];
    await this.exclusive(async () => {
      await this.store.deletePending(message.conversation, message.localId!);
      this.removeFromWindow(message.conversation, (m) => m.seq === null && m.localId === message.localId);
      await this.refreshSummary(message.conversation);
    });
    return message.fileUrl ? [message.fileUrl] : [];
  }

  private async dispatch(msg: ChatMessage): Promise<void> {
    const localId = msg.localId!;
    if (this.inflight.has(localId)) return;
    this.inflight.add(localId);
    const fields: PublishFields = { content: msg.content, contentType: msg.contentType };
    if (msg.fileUrl) fields.fileUrl = msg.fileUrl;
    if (msg.fileName) fields.fileName = msg.fileName;
    if (msg.duration) fields.duration = msg.duration;
    if (msg.extra !== undefined) fields.extra = msg.extra;
    const outcome = await this.client
      .publish(msg.conversation, fields, localId)
      .catch((): PublishOutcome => ({ kind: "unknown", why: "connection_lost" }))
      .finally(() => this.inflight.delete(localId));
    await this.exclusive(async () => {
      const held = await this.store.pendingMessage(msg.conversation, localId);
      if (!held) return; // already stored through an echo or a history pull, or discarded
      if (outcome.kind === "stored") {
        this.timedOut.delete(localId);
        this.autoRetries.delete(localId);
        await this.promote(held, outcome.seq, outcome.ts || held.ts);
      } else if (outcome.kind === "refused") {
        const failed: ChatMessage = { ...held, status: "failed", failure: { code: outcome.reply.code } };
        if (outcome.reply.errCode) failed.failure!.errCode = outcome.reply.errCode;
        if (outcome.reply.reason) failed.failure!.reason = outcome.reply.reason;
        await this.store.putMessage(failed);
        this.applyToWindow(failed);
        await this.refreshSummary(held.conversation);
        this.events.emit({ kind: "sendFailed", message: failed });
      } else if (outcome.why === "timeout") {
        // The server may have stored it: stay "sending", do not send again on this connection (C2).
        this.timedOut.add(localId);
      }
      // connection_lost / not_connected: sent again after the next sync.
    });
  }

  /** Sends every queued message that is not in flight; called after each sync. */
  private async flushPending(): Promise<void> {
    const now = this.config.clock();
    for (const m of await this.store.pendingMessages()) {
      if (m.status !== "sending" || this.inflight.has(m.localId!)) continue;
      const tries = this.autoRetries.get(m.localId!) ?? 0;
      if (now - m.ts > this.config.staleSendingMs || tries >= this.config.maxAutoRetries) {
        await this.exclusive(async () => {
          const failed: ChatMessage = { ...m, status: "failed", failure: { code: 0 } };
          await this.store.putMessage(failed);
          this.applyToWindow(failed);
          await this.refreshSummary(m.conversation);
          this.events.emit({ kind: "sendFailed", message: failed });
        });
        continue;
      }
      // A new connection: the server de-duplicates by clientMsgId, so a resend is safe now.
      this.timedOut.delete(m.localId!);
      this.autoRetries.set(m.localId!, tries + 1);
      void this.dispatch(m);
    }
  }

  // ------------------------------------------------------------------ actions

  /** Recalls an own stored message; throws RequestRefused when the server says no. */
  async recall(message: ChatMessage): Promise<void> {
    if (message.seq === null) throw new Error("only a stored message can be recalled");
    const reply = await this.client.recall(message.conversation, message.seq);
    if (reply.code !== 200) throw new RequestRefused(reply);
    await this.exclusive(() => this.removeRecalled(message.conversation, message.seq!));
  }

  /** Clears the conversation for everyone, permanently (section 11.2). */
  async clear(conversation: ConversationId): Promise<void> {
    const r = await this.client.clearTopic(conversation);
    await this.exclusive(() => this.applyClear(conversation, r.clearVersion, r.clearBeforeSeq));
  }

  /** Marks everything stored in the conversation as read, here and on the server. */
  async markRead(conversation: ConversationId): Promise<void> {
    let seq = 0;
    await this.exclusive(async () => {
      const c = this.convs.get(conversation);
      const seqs = await this.store.seqs(conversation);
      seq = seqs.length ? seqs[seqs.length - 1]! : 0;
      if (!c || seq <= c.lastReadSeq) {
        seq = 0;
        return;
      }
      await this.putConv({ ...c, lastReadSeq: seq });
      await this.refreshSummary(conversation);
    });
    if (seq > 0 && !this.client.sendRead(conversation, seq)) this.unsentReads.add(conversation);
  }

  /** "Typing…" to the others; false when not connected. */
  sendTyping(conversation: ConversationId): boolean {
    return this.client.sendTyping(conversation);
  }

  subscribe(conversation: ConversationId) {
    return this.client.subscribe(conversation);
  }

  /** Makes sure a conversation exists, e.g. a single chat with a contact from the host. */
  async ensureConversation(conversation: ConversationId, title?: string): Promise<void> {
    await this.exclusive(async () => {
      await this.ensure(conversation, title);
      this.publishSummaries();
    });
  }

  /** Removes a conversation and everything stored for it from this device. */
  async deleteConversation(conversation: ConversationId): Promise<void> {
    await this.exclusive(async () => {
      await this.store.deleteConversation(conversation);
      this.convs.delete(conversation);
      this.lastMessage.delete(conversation);
      this.unread.delete(conversation);
      this.windows.get(conversation)?.signal.set([]);
      this.publishSummaries();
    });
  }

  setDraft(conversation: ConversationId, draft: string | undefined) {
    return this.updateConv(conversation, (c) => {
      const next = { ...c };
      if (draft) next.draft = draft;
      else delete next.draft;
      return next;
    });
  }

  setPinned(conversation: ConversationId, pinned: boolean) {
    return this.updateConv(conversation, (c) => ({ ...c, pinned }));
  }

  setMuted(conversation: ConversationId, muted: boolean) {
    return this.updateConv(conversation, (c) => ({ ...c, muted }));
  }

  // ------------------------------------------------------------------ local search

  /**
   * Text messages stored on this device that contain `query` (case does not
   * matter), newest first. Reads the `scanPerConversation` most recent
   * messages of each conversation through the store, so it also works when
   * message text is encrypted at rest; older messages are not searched.
   */
  async searchMessages(query: string, limit = 50, scanPerConversation = 500): Promise<ChatMessage[]> {
    const q = query.trim().toLowerCase();
    if (!q) return [];
    const hits: ChatMessage[] = [];
    for (const c of await this.store.conversations()) {
      for (const m of await this.store.latestMessages(c.id, scanPerConversation)) {
        if (messageKind(m.contentType, m.fileUrl) === "text" && m.content.toLowerCase().includes(q)) hits.push(m);
      }
    }
    return hits.sort((a, b) => b.ts - a.ts).slice(0, limit);
  }

  // ------------------------------------------------------------------ groups

  /** The group list from the server; also finds groups I left while away. Null when the request failed. */
  /**
   * Creates a group and lists it under its name straight away, before any
   * group refresh; returns the new conversation. Refused requests throw
   * `RequestRefused`.
   */
  async createGroup(name: string, members: readonly string[]): Promise<ConversationId> {
    const topic = await this.client.createGroup(name, members);
    await this.exclusive(async () => {
      const c = this.convs.get(topic);
      if (!c) await this.putConv(newConversation(topic, name));
      else if (c.left || c.title !== name) await this.putConv({ ...c, left: false, title: name });
      this.publishSummaries();
    });
    if (this.client.isConnected) void this.refreshGroups().catch(() => {});
    return topic;
  }

  async refreshGroups(): Promise<GroupInfo[] | null> {
    let groups: GroupInfo[];
    try {
      groups = await this.client.getGroups();
    } catch {
      return null;
    }
    await this.exclusive(async () => {
      const listed = new Set(groups.map((g) => g.topic));
      for (const g of groups) {
        const c = this.convs.get(g.topic);
        if (!c) await this.putConv(newConversation(g.topic, g.name));
        else if (c.title !== g.name || c.left) await this.putConv({ ...c, title: g.name, left: false });
      }
      for (const c of [...this.convs.values()]) {
        if (isGroupTopic(c.id) && !c.left && !listed.has(c.id)) {
          // Kicked or disbanded while away; the pending event may not have arrived yet.
          await this.putConv({ ...c, left: true });
          this.rosters.delete(c.id);
          this.events.emit({ kind: "removedFromGroup", conversation: c.id });
        }
      }
      this._groups.set(groups);
      this.publishSummaries();
    });
    return groups;
  }

  /** The roster of a group, fetched when missing, stale, or `refresh`; null when it cannot be fetched. */
  async members(topic: string, refresh = false): Promise<GroupMember[] | null> {
    const held = this.rosters.get(topic);
    if (held && !held.stale && !refresh) return held.members;
    try {
      const members = await this.client.getMembers(topic);
      this.rosters.set(topic, { members, stale: false });
      return members;
    } catch {
      this.rosters.delete(topic);
      return null;
    }
  }

  /** My role in a group, from the roster; "member" when it cannot be fetched (fail closed, C9). */
  async myRole(topic: string): Promise<GroupRole> {
    const members = await this.members(topic);
    return members?.find((m) => m.uid === this.me)?.role ?? "member";
  }

  /**
   * Marks a group's roster stale and emits `groupChanged`, as a `grp_sync`
   * would: for changes this device made through another module that the
   * server does not echo back to it (approving a join request tells only the
   * requester, F45). Screens showing the members reload on the event.
   */
  noteGroupChanged(topic: string): void {
    const r = this.rosters.get(topic);
    if (r) r.stale = true;
    this.rosterEpochs.set(topic, this.rosterEpoch(topic) + 1);
    this.events.emit({ kind: "groupChanged", conversation: topic });
  }

  /** Whether the cached roster is stale; null when none is cached. */
  rosterStale(topic: string): boolean | null {
    return this.rosters.get(topic)?.stale ?? null;
  }

  /** How many group change hints arrived for this group. */
  rosterEpoch(topic: string): number {
    return this.rosterEpochs.get(topic) ?? 0;
  }

  // ------------------------------------------------------------------ sync

  /**
   * The whole sync of section 8; runs after every login when `autoSync` is on.
   * With a server that offers `sync_summary`, only the conversations that
   * changed are pulled (section 8.1).
   */
  async syncAll(): Promise<void> {
    if (this.syncing) return;
    this.syncing = true;
    let done = 0;
    let failed = 0;
    let pulled = 0;
    try {
      await this.refreshGroups();
      const summary = await this.fetchSummary();
      const targets = [...this.convs.values()].filter((c) => !(isGroupTopic(c.id) && c.left)).map((c) => c.id);
      for (const id of targets) {
        if (!this.client.isConnected) break;
        const changes = summary?.get(id);
        try {
          if (summary && !(await this.changed(id, changes))) {
            if (changes) await this.applySummary(id, changes);
            done++;
            continue;
          }
          await this.syncConversation(id, changes?.lastSeq);
          if (changes?.readsAt !== undefined) this.readsSeen.set(id, changes.readsAt);
          done++;
          pulled++;
        } catch {
          failed++;
        }
      }
      const peers = targets.filter((id) => !isGroupTopic(id));
      if (peers.length > 0 && this.client.isConnected) await this.refreshPresence(peers).catch(() => {});
      await this.flushReads();
      await this.flushPending();
    } finally {
      this.syncing = false;
    }
    this.events.emit({ kind: "synced", conversations: done, failed, pulled });
  }

  /** The `sync_summary` by conversation; null when the server does not offer it or it failed (then every conversation is pulled). */
  private async fetchSummary(): Promise<Map<ConversationId, SyncTopic> | null> {
    if (!this.client.session?.capabilities.includes("sync-summary")) return null;
    let topics: SyncTopic[];
    try {
      topics = await this.client.syncSummary();
    } catch {
      return null;
    }
    const byConversation = new Map<ConversationId, SyncTopic>();
    for (const t of topics) {
      const id = t.isGroup ? t.topic : t.peer;
      if (id) byConversation.set(id, t);
    }
    return byConversation;
  }

  /** Whether a conversation has to be pulled, by its summary entry (section 8.1). */
  private async changed(id: ConversationId, t: SyncTopic | undefined): Promise<boolean> {
    // A group listed by get_groups but not summarized: pull it. A single chat
    // missing from the summary has no conversation on the server yet.
    if (!t) return isGroupTopic(id);
    const clear = await this.store.clearState(id);
    if (t.clearVersion > (clear?.clearVersion ?? 0)) return true;
    if (t.readsAt !== undefined && this.readsSeen.get(id) !== t.readsAt) return true;
    const end = resumeSeq(await this.store.seqs(id), await this.store.recalledSeqs(id), clear?.clearBeforeSeq ?? 0);
    return t.lastSeq > end;
  }

  /** What an unchanged conversation's summary entry still tells: my read position on another device. */
  private async applySummary(id: ConversationId, t: SyncTopic): Promise<void> {
    if (t.readSeq === undefined) return;
    await this.exclusive(async () => {
      const c = this.convs.get(id);
      if (!c || t.readSeq! <= c.lastReadSeq) return;
      await this.putConv({ ...c, lastReadSeq: t.readSeq! });
      await this.refreshSummary(id);
    });
  }

  /**
   * Pulls what the server has after the end of the local contiguous run (C3),
   * page by page; with nothing held, only the newest page. Seqs the server
   * skips are recorded as recalled (section 8.2), up to `lastSeq` from the
   * summary when it is known, so the next sync does not start over from them.
   */
  async syncConversation(conversation: ConversationId, lastSeq?: number): Promise<void> {
    const pageSize = this.config.pageSize;
    const clear = await this.store.clearState(conversation);
    const recalled = new Set(await this.store.recalledSeqs(conversation));
    let after = resumeSeq(await this.store.seqs(conversation), recalled, clear?.clearBeforeSeq ?? 0);
    const heldBefore = await this.store.seqs(conversation);
    let first = true;
    let pulled = 0;
    for (;;) {
      const page = await this.client.history(conversation, after > 0 ? { afterSeq: after, limit: pageSize } : { limit: pageSize });
      const full = page.messages.length >= pageSize;
      const from = after;
      await this.exclusive(async () => {
        await this.ensure(conversation);
        if (first) await this.applyPageState(conversation, page);
        for (const w of page.messages) await this.storeWire(conversation, w, false);
        const watermark = (await this.store.clearState(conversation))?.clearBeforeSeq ?? 0;
        const gone = seqsGone({ local: heldBefore, afterSeq: from, page: page.messages.map((m) => m.seq), full, lastSeq, floor: watermark });
        for (const s of gone) {
          if (recalled.has(s)) continue;
          await this.removeRecalled(conversation, s, false);
          recalled.add(s);
        }
        await this.refreshSummary(conversation);
      });
      first = false;
      pulled += page.messages.length;
      if (from === 0 || !full || pulled >= this.config.offlinePullMax) break;
      after = page.messages[page.messages.length - 1]!.seq;
    }
  }

  /** Presence of `uids` from the server into the tracker. */
  async refreshPresence(uids: readonly string[]): Promise<void> {
    for (const s of await this.client.queryPresence(uids)) this.presence.update(s.uid, s.online, s.rev, s.lastSeenAt);
  }

  private async applyPageState(conversation: ConversationId, page: HistoryPage): Promise<void> {
    if (page.clearVersion > 0) await this.applyClear(conversation, page.clearVersion, page.clearBeforeSeq, "", false);
    const c = this.convs.get(conversation)!;
    if (page.readSeq !== undefined && page.readSeq > c.lastReadSeq) await this.putConv({ ...c, lastReadSeq: page.readSeq });
    if (page.readCursors) for (const [uid, seq] of page.readCursors) await this.setCursor(conversation, uid, seq);
  }

  private async flushReads(): Promise<void> {
    for (const id of [...this.unsentReads]) {
      const c = this.convs.get(id);
      this.unsentReads.delete(id);
      if (c && c.lastReadSeq > 0 && !this.client.sendRead(id, c.lastReadSeq)) this.unsentReads.add(id);
    }
  }

  // ------------------------------------------------------------------ pushes

  private async onEvent(ev: ImEvent): Promise<void> {
    switch (ev.kind) {
      case "connected":
        void this.onConnected(ev.session.uid);
        return;
      case "disconnected":
        // Presence is not wiped: each record expires on its own (C5).
        this._typing.set(new Map());
        return;
      case "message":
        return this.onMessage(ev.message);
      case "recalled": {
        const conv = this.place(ev.topic, ev.from);
        if (!conv) return;
        await this.removeRecalled(conv, ev.seq);
        this.events.emit({ kind: "recalled", conversation: conv, seq: ev.seq, by: ev.from });
        return;
      }
      case "topicCleared": {
        const conv = this.place(ev.topic, ev.from);
        if (conv) await this.applyClear(conv, ev.clearVersion, ev.clearBeforeSeq, ev.from);
        if (ev.pendingId) this.client.ackTopicCleared(ev.pendingId);
        return;
      }
      case "presence":
        this.presence.update(ev.uid, ev.online, ev.rev);
        return;
      case "typing": {
        const conv = this.place(ev.topic, ev.from);
        if (!conv || ev.from === this.me) return;
        this.noteTyping(conv, ev.from, this.config.typingTtlMs);
        return;
      }
      case "read":
        return this.onRead(ev.topic, ev.from, ev.seq);
      case "groupSync":
        if (!isP2pTopic(ev.topic) && isGroupTopic(ev.topic)) await this.onGroupSync(ev.topic, ev.name);
        // D2: a grp_sync for a single chat is ignored, but still acknowledged.
        if (ev.eventId !== undefined) this.client.ackGroupEvent(ev.eventId);
        return;
      case "groupRemoved":
        if (isGroupTopic(ev.topic)) {
          const c = this.convs.get(ev.topic);
          if (c && !c.left) await this.putConv({ ...c, left: true });
          this.rosters.delete(ev.topic);
          this.publishSummaries();
          this.events.emit({ kind: "removedFromGroup", conversation: ev.topic });
        }
        if (ev.eventId !== undefined) this.client.ackGroupEvent(ev.eventId);
        return;
      case "contactSync":
        this.events.emit({ kind: "contactChanged", what: ev.what, peerUid: ev.from });
        return;
      case "module": {
        // The group assistant was asked and is answering (section 15): it
        // types under the uid its reply comes from.
        const topic = ev.frame.topic;
        if (ev.frame.type === "ai_typing" && typeof topic === "string" && isGroupTopic(topic) && this.convs.has(topic)) {
          this.noteTyping(topic, AI_ASSISTANT_UID, this.config.aiTypingTtlMs);
        }
        return;
      }
      default:
        return;
    }
  }

  private noteTyping(conv: ConversationId, uid: string, ttlMs: number): void {
    const next = new Map(this._typing.value);
    next.set(conv, new Map(next.get(conv) ?? []).set(uid, this.config.clock() + ttlMs));
    this._typing.set(next);
    this.scheduleTypingExpiry();
  }

  /** A message from someone ends their typing note in that conversation. */
  private endTyping(conv: ConversationId, uid: string): void {
    const m = this._typing.value.get(conv);
    if (!m?.has(uid)) return;
    const next = new Map(this._typing.value);
    const rest = new Map(m);
    rest.delete(uid);
    if (rest.size) next.set(conv, rest);
    else next.delete(conv);
    this._typing.set(next);
    this.scheduleTypingExpiry();
  }

  private async onConnected(uid: string): Promise<void> {
    if (uid !== this.me) {
      this.client.stop();
      this.events.emit({ kind: "wrongAccount", expected: this.me, actual: uid });
      return;
    }
    if (this.config.autoSync) await this.syncAll().catch(() => {});
  }

  private async onMessage(w: WireMessage): Promise<void> {
    const conv = this.place(w.topic, w.from);
    if (!conv) return;
    if (isGroupTopic(conv)) {
      const c = this.convs.get(conv);
      if (!c) await this.putConv(newConversation(conv, w.name));
      else if (c.left || (w.name && c.title !== w.name)) await this.putConv({ ...c, left: false, ...(w.name ? { title: w.name } : {}) });
    } else {
      await this.ensure(conv);
    }
    const isNew = await this.storeWire(conv, w, true);
    await this.refreshSummary(conv);
    if (w.from !== this.me) this.endTyping(conv, w.from);
    if (isNew && w.from !== this.me) {
      const message = (await this.store.message(conv, w.seq))!;
      const ev: ChatEvent = { kind: "messageArrived", message };
      const name = this.convs.get(conv)?.title;
      if (isGroupTopic(conv) && name) ev.groupName = name;
      this.events.emit(ev);
    }
  }

  private async onRead(topic: string, from: string, seq: number): Promise<void> {
    const conv = this.place(topic, from);
    if (!conv) return;
    const c = this.convs.get(conv);
    if (!c) return;
    if (from === this.me) {
      // My read on another device (section 12.3).
      if (seq > c.lastReadSeq) {
        await this.putConv({ ...c, lastReadSeq: seq });
        await this.refreshSummary(conv);
      }
    } else if (!isGroupTopic(conv)) {
      if (seq > c.peerReadSeq) {
        await this.putConv({ ...c, peerReadSeq: seq });
        this.publishSummaries();
      }
    } else {
      await this.setCursor(conv, from, seq);
    }
  }

  private async onGroupSync(topic: string, name: string | undefined): Promise<void> {
    const c = this.convs.get(topic);
    if (!c) await this.putConv(newConversation(topic, name));
    else if (c.left || (name && c.title !== name)) await this.putConv({ ...c, left: false, ...(name ? { title: name } : {}) });
    const r = this.rosters.get(topic);
    if (r) r.stale = true;
    this.rosterEpochs.set(topic, this.rosterEpoch(topic) + 1);
    this.publishSummaries();
    this.events.emit({ kind: "groupChanged", conversation: topic });
    if (this.config.autoSync && this.client.isConnected) {
      // A hint only (D1): refresh names and membership from the server, once per burst.
      if (this.groupRefreshTimer) clearTimeout(this.groupRefreshTimer);
      this.groupRefreshTimer = setTimeout(() => {
        this.groupRefreshTimer = null;
        void this.refreshGroups();
      }, 300);
    }
  }

  // ------------------------------------------------------------------ helpers

  /** Runs `fn` after every earlier exclusive section: store and window changes never interleave. */
  private exclusive<T>(fn: () => Promise<T>): Promise<T> {
    const next = this.lock.then(fn, fn);
    this.lock = next.catch(() => {});
    return next;
  }

  /** Files a frame's topic under a conversation (section 5.5). */
  private place(topic: string, from: string): ConversationId | null {
    const peers = [...this.convs.keys()].filter((id) => !isGroupTopic(id));
    const ref = conversationOf(topic, from || undefined, this.me, peers);
    if (!ref) return null;
    return ref.kind === "group" ? ref.topic : ref.peer;
  }

  private async ensure(conversation: ConversationId, title?: string): Promise<StoredConversation> {
    let c = this.convs.get(conversation);
    if (!c) {
      c = newConversation(conversation, title);
      await this.putConv(c);
    } else if (title !== undefined && c.title === undefined && !isGroupTopic(conversation)) {
      c = { ...c, title };
      await this.putConv(c);
    }
    return c;
  }

  private async putConv(c: StoredConversation): Promise<void> {
    this.convs.set(c.id, c);
    await this.store.putConversation(c);
  }

  private async updateConv(id: ConversationId, fn: (c: StoredConversation) => StoredConversation): Promise<void> {
    await this.exclusive(async () => {
      const c = await this.ensure(id);
      await this.putConv(fn(c));
      this.publishSummaries();
    });
  }

  private async touch(conversation: ConversationId, ts: number): Promise<void> {
    const c = this.convs.get(conversation)!;
    if (ts > c.updatedAt) await this.putConv({ ...c, updatedAt: ts });
    await this.refreshSummary(conversation);
  }

  /**
   * Stores a message from the server. Own messages replace their pending row:
   * by clientMsgId when the frame has one, otherwise by `fileUrl` for media
   * and by content for text (history replies carry no clientMsgId).
   * Returns whether the seq was new.
   */
  private async storeWire(conversation: ConversationId, w: WireMessage, live: boolean): Promise<boolean> {
    const existing = await this.store.message(conversation, w.seq);
    const recalled = !existing && (await this.store.recalledSeqs(conversation)).includes(w.seq);
    if (recalled) return false;
    const fromMe = w.from === this.me;
    const msg: ChatMessage = {
      conversation,
      seq: w.seq,
      localId: existing?.localId ?? (fromMe ? (w.clientMsgId ?? null) : null),
      from: w.from,
      content: w.content,
      contentType: w.contentType,
      duration: w.duration,
      senderKind: w.senderKind,
      ts: w.ts,
      status: fromMe ? "sent" : "received",
    };
    if (w.fileUrl) msg.fileUrl = w.fileUrl;
    if (w.fileName) msg.fileName = w.fileName;
    if (w.extra !== undefined) msg.extra = w.extra;
    if (w.forwardFromMsgId) msg.forwardFromMsgId = w.forwardFromMsgId;
    if (w.forwardFromTopic) msg.forwardFromTopic = w.forwardFromTopic;
    // Live pushes carry the broadcast time; the history time is the stored one (F34).
    if (existing && live) msg.ts = existing.ts;

    if (fromMe && !existing) {
      const pending = await this.matchPending(conversation, w);
      if (pending) {
        msg.localId = pending.localId;
        await this.store.promote(conversation, pending.localId!, msg);
        this.removeFromWindow(conversation, (m) => m.seq === null && m.localId === pending.localId);
        this.applyToWindow(msg);
        this.timedOut.delete(pending.localId!);
        await this.touch(conversation, msg.ts);
        return true;
      }
    }
    await this.store.putMessage(msg);
    this.applyToWindow(msg);
    if (!existing) {
      const c = this.convs.get(conversation);
      if (c && msg.ts > c.updatedAt) await this.putConv({ ...c, updatedAt: msg.ts });
    }
    return !existing;
  }

  private async matchPending(conversation: ConversationId, w: WireMessage): Promise<ChatMessage | null> {
    if (w.clientMsgId) {
      const byId = await this.store.pendingMessage(conversation, w.clientMsgId);
      if (byId) return byId;
    }
    const pending = (await this.store.pendingMessages()).filter((m) => m.conversation === conversation && isPending(m) && m.status === "sending");
    return (
      pending.find((m) => (w.fileUrl ? m.fileUrl === w.fileUrl : !m.fileUrl && m.content === w.content && m.contentType === w.contentType)) ?? null
    );
  }

  private async promote(held: ChatMessage, seq: number, ts: number): Promise<void> {
    const conversation = held.conversation;
    const existing = await this.store.message(conversation, seq);
    const stored: ChatMessage = { ...(existing ?? held), seq, ts: existing?.ts ?? ts, status: "sent", localId: held.localId };
    delete stored.failure;
    await this.store.promote(conversation, held.localId!, stored);
    this.removeFromWindow(conversation, (m) => m.seq === null && m.localId === held.localId);
    this.applyToWindow(stored);
    await this.touch(conversation, stored.ts);
  }

  /**
   * A recalled message becomes a tombstone (who sent it and when, no
   * content), so the chat can show "… unsent a message"; one this device
   * never had leaves only the recalled seq. Either way the seq counts as
   * recalled for the sync cursor.
   */
  private async removeRecalled(conversation: ConversationId, seq: number, refresh = true): Promise<void> {
    const held = await this.store.message(conversation, seq);
    if (held && !isRecalled(held)) {
      const tomb = recalledCopy(held);
      await this.store.putMessage(tomb);
      this.applyToWindow(tomb);
    }
    await this.store.addRecalled(conversation, seq);
    if (refresh) await this.refreshSummary(conversation);
  }

  private async applyClear(conversation: ConversationId, clearVersion: number, clearBeforeSeq: number, by = "", emit = true): Promise<void> {
    const held = await this.store.clearState(conversation);
    if (held && clearVersion <= held.clearVersion) return;
    await this.ensure(conversation);
    await this.store.deleteThrough(conversation, clearBeforeSeq);
    await this.store.setClearState(conversation, { clearVersion, clearBeforeSeq });
    this.removeFromWindow(conversation, (m) => m.seq !== null && m.seq <= clearBeforeSeq);
    await this.refreshSummary(conversation);
    if (emit) this.events.emit({ kind: "conversationCleared", conversation, by });
  }

  private async setCursor(conversation: ConversationId, uid: string, seq: number): Promise<void> {
    const held = (await this.store.readCursors(conversation)).get(uid) ?? 0;
    if (seq <= held) return;
    await this.store.setReadCursor(conversation, uid, seq);
    const s = this.cursorSignals.get(conversation);
    if (s) s.set(new Map(s.value).set(uid, seq));
  }

  private applyToWindow(msg: ChatMessage): void {
    const w = this.windows.get(msg.conversation);
    if (!w) return;
    w.signal.set(mergeMessages(w.signal.value, [msg]));
  }

  private removeFromWindow(conversation: ConversationId, test: (m: ChatMessage) => boolean): void {
    const w = this.windows.get(conversation);
    if (!w) return;
    const next = w.signal.value.filter((m) => !test(m));
    if (next.length !== w.signal.value.length) w.signal.set(next);
  }

  private async loadSummary(id: ConversationId): Promise<void> {
    const latest = (await this.store.latestMessages(id, 1)).at(-1);
    if (latest) this.lastMessage.set(id, latest);
    else this.lastMessage.delete(id);
    const c = this.convs.get(id);
    this.unread.set(id, c ? await this.store.countUnread(id, c.lastReadSeq, this.me) : 0);
  }

  private async refreshSummary(id: ConversationId): Promise<void> {
    await this.loadSummary(id);
    this.publishSummaries();
  }

  private publishSummaries(): void {
    const list: ConversationSummary[] = [...this.convs.values()].map((c) => {
      const s: ConversationSummary = { ...c, isGroup: isGroupTopic(c.id), unread: this.unread.get(c.id) ?? 0 };
      const last = this.lastMessage.get(c.id);
      if (last) s.lastMessage = last;
      return s;
    });
    list.sort((a, b) => Number(b.pinned) - Number(a.pinned) || b.updatedAt - a.updatedAt || a.id.localeCompare(b.id));
    this._conversations.set(list);
  }

  private scheduleTypingExpiry(): void {
    if (this.typingTimer) clearTimeout(this.typingTimer);
    const now = this.config.clock();
    let next = Infinity;
    for (const m of this._typing.value.values()) for (const until of m.values()) if (until > now) next = Math.min(next, until);
    if (next === Infinity) return;
    this.typingTimer = setTimeout(() => {
      this.typingTimer = null;
      const t = this.config.clock();
      const pruned = new Map<ConversationId, ReadonlyMap<string, number>>();
      for (const [conv, m] of this._typing.value) {
        const live = new Map([...m].filter(([, until]) => until > t));
        if (live.size) pruned.set(conv, live);
      }
      this._typing.set(pruned);
      this.scheduleTypingExpiry();
    }, next - now + 5);
  }

  /** Waits until every queued store change has been applied (for tests and orderly shutdown). */
  async idle(): Promise<void> {
    await this.exclusive(async () => {});
    await sleep(0);
  }
}

function withoutIds(config: EngineConfig): Omit<EngineConfig, "appUserId" | "uid"> {
  const rest: EngineConfig = { ...config };
  delete rest.appUserId;
  delete rest.uid;
  for (const k of Object.keys(rest) as (keyof EngineConfig)[]) if (rest[k] === undefined) delete rest[k];
  return rest;
}

/** Merges `incoming` into `held` by message key; the incoming copy wins. Result in timeline order. */
export function mergeMessages(held: readonly ChatMessage[], incoming: readonly ChatMessage[]): ChatMessage[] {
  const byKey = new Map<string, ChatMessage>();
  for (const m of held) byKey.set(messageKey(m), m);
  for (const m of incoming) {
    // A stored message replaces the pending row it came from.
    if (m.seq !== null && m.localId) byKey.delete("l:" + m.localId);
    byKey.set(messageKey(m), m);
  }
  return [...byKey.values()].sort(timelineOrder);
}
