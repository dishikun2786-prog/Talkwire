/**
 * Persistence for the chat engine. The logical tables are the same as the
 * Android Room store (`messages`, `pending_messages`, `conversations`,
 * `recalled_seqs`, `clear_states`, `read_cursors`, `owner`); implementations
 * are `MemoryChatStore` here and `IdbChatStore` in `@imkit/storage-idb`.
 *
 * The engine is the only writer and keeps its own in-memory view; a store
 * only has to remember. A message row is keyed by `(conversation, seq)` once
 * the server stored it and by `(conversation, localId)` while pending;
 * `promote` turns one into the other atomically.
 */

import { isPending, timelineOrder, type ChatMessage, type ClearState, type ConversationId, type StoredConversation } from "./model.js";

export interface ChatStore {
  // ownership (C4)
  /** The uid whose data this store holds; null when never claimed. */
  owner(): Promise<string | null>;
  /** Deletes everything in every table, the owner included. */
  wipe(): Promise<void>;
  setOwner(uid: string): Promise<void>;

  // messages
  /** Inserts or replaces by key (seq when set, local id otherwise). */
  putMessage(message: ChatMessage): Promise<void>;
  /** Atomically removes the pending row `localId` and stores `stored` (which has a seq). */
  promote(conversation: ConversationId, localId: string, stored: ChatMessage): Promise<void>;
  message(conversation: ConversationId, seq: number): Promise<ChatMessage | null>;
  pendingMessage(conversation: ConversationId, localId: string): Promise<ChatMessage | null>;
  /** The newest `limit` stored messages plus every pending one, in timeline order. */
  latestMessages(conversation: ConversationId, limit: number): Promise<ChatMessage[]>;
  /** Up to `limit` stored messages with seq below `beforeSeq`, in timeline order. */
  messagesBefore(conversation: ConversationId, beforeSeq: number, limit: number): Promise<ChatMessage[]>;
  /** Stored seqs, ascending. */
  seqs(conversation: ConversationId): Promise<number[]>;
  /** Pending own messages in every conversation. */
  pendingMessages(): Promise<ChatMessage[]>;
  deleteMessage(conversation: ConversationId, seq: number): Promise<void>;
  deletePending(conversation: ConversationId, localId: string): Promise<void>;
  /** Deletes stored messages with seq ≤ `seq`; pending ones stay. */
  deleteThrough(conversation: ConversationId, seq: number): Promise<void>;
  /** Deletes every message of the conversation, pending ones included. */
  deleteAllMessages(conversation: ConversationId): Promise<void>;
  /** Stored messages after `afterSeq` not sent by `me` and not system messages. */
  countUnread(conversation: ConversationId, afterSeq: number, me: string): Promise<number>;

  // recall tombstones (C3)
  addRecalled(conversation: ConversationId, seq: number): Promise<void>;
  recalledSeqs(conversation: ConversationId): Promise<number[]>;

  // clear watermark
  clearState(conversation: ConversationId): Promise<ClearState | null>;
  setClearState(conversation: ConversationId, state: ClearState): Promise<void>;

  // group read cursors
  readCursors(conversation: ConversationId): Promise<Map<string, number>>;
  setReadCursor(conversation: ConversationId, uid: string, seq: number): Promise<void>;

  // conversations
  conversations(): Promise<StoredConversation[]>;
  conversation(id: ConversationId): Promise<StoredConversation | null>;
  putConversation(conversation: StoredConversation): Promise<void>;
  /** Deletes the conversation and everything stored for it. */
  deleteConversation(id: ConversationId): Promise<void>;
}

/** Rows are copied in and out so callers cannot change what the store holds. */
const copy = <T>(v: T): T => structuredClone(v);

/** A store that keeps everything in memory; for tests, and for apps that want no persistence. */
export class MemoryChatStore implements ChatStore {
  private ownerUid: string | null = null;
  /** conversation → seq → message */
  private readonly stored = new Map<ConversationId, Map<number, ChatMessage>>();
  /** conversation → localId → message */
  private readonly pending = new Map<ConversationId, Map<string, ChatMessage>>();
  private readonly recalled = new Map<ConversationId, Set<number>>();
  private readonly clears = new Map<ConversationId, ClearState>();
  private readonly cursors = new Map<ConversationId, Map<string, number>>();
  private readonly convs = new Map<ConversationId, StoredConversation>();

  async owner() {
    return this.ownerUid;
  }

  async wipe() {
    this.ownerUid = null;
    this.stored.clear();
    this.pending.clear();
    this.recalled.clear();
    this.clears.clear();
    this.cursors.clear();
    this.convs.clear();
  }

  async setOwner(uid: string) {
    this.ownerUid = uid;
  }

  private bucket<K, V>(m: Map<ConversationId, Map<K, V>>, c: ConversationId): Map<K, V> {
    let b = m.get(c);
    if (!b) m.set(c, (b = new Map()));
    return b;
  }

  async putMessage(message: ChatMessage) {
    if (message.seq !== null) this.bucket(this.stored, message.conversation).set(message.seq, copy(message));
    else if (message.localId) this.bucket(this.pending, message.conversation).set(message.localId, copy(message));
    else throw new Error("a message needs a seq or a local id");
  }

  async promote(conversation: ConversationId, localId: string, stored: ChatMessage) {
    if (stored.seq === null) throw new Error("promote needs a stored message");
    this.pending.get(conversation)?.delete(localId);
    this.bucket(this.stored, conversation).set(stored.seq, copy(stored));
  }

  async message(conversation: ConversationId, seq: number) {
    const m = this.stored.get(conversation)?.get(seq);
    return m ? copy(m) : null;
  }

  async pendingMessage(conversation: ConversationId, localId: string) {
    const m = this.pending.get(conversation)?.get(localId);
    return m ? copy(m) : null;
  }

  async latestMessages(conversation: ConversationId, limit: number) {
    const stored = [...(this.stored.get(conversation)?.values() ?? [])].sort(timelineOrder).slice(-limit);
    const pend = [...(this.pending.get(conversation)?.values() ?? [])];
    return [...stored, ...pend].sort(timelineOrder).map(copy);
  }

  async messagesBefore(conversation: ConversationId, beforeSeq: number, limit: number) {
    return [...(this.stored.get(conversation)?.values() ?? [])]
      .filter((m) => m.seq! < beforeSeq)
      .sort(timelineOrder)
      .slice(-limit)
      .map(copy);
  }

  async seqs(conversation: ConversationId) {
    return [...(this.stored.get(conversation)?.keys() ?? [])].sort((a, b) => a - b);
  }

  async pendingMessages() {
    const out: ChatMessage[] = [];
    for (const b of this.pending.values()) for (const m of b.values()) if (isPending(m)) out.push(copy(m));
    return out.sort(timelineOrder);
  }

  async deleteMessage(conversation: ConversationId, seq: number) {
    this.stored.get(conversation)?.delete(seq);
  }

  async deletePending(conversation: ConversationId, localId: string) {
    this.pending.get(conversation)?.delete(localId);
  }

  async deleteThrough(conversation: ConversationId, seq: number) {
    const b = this.stored.get(conversation);
    if (!b) return;
    for (const s of [...b.keys()]) if (s <= seq) b.delete(s);
  }

  async deleteAllMessages(conversation: ConversationId) {
    this.stored.delete(conversation);
    this.pending.delete(conversation);
  }

  async countUnread(conversation: ConversationId, afterSeq: number, me: string) {
    let n = 0;
    for (const m of this.stored.get(conversation)?.values() ?? []) {
      if (m.seq! > afterSeq && m.from !== me && m.contentType !== "system") n++;
    }
    return n;
  }

  async addRecalled(conversation: ConversationId, seq: number) {
    this.bucketSet(conversation).add(seq);
  }

  private bucketSet(c: ConversationId): Set<number> {
    let s = this.recalled.get(c);
    if (!s) this.recalled.set(c, (s = new Set()));
    return s;
  }

  async recalledSeqs(conversation: ConversationId) {
    return [...(this.recalled.get(conversation) ?? [])].sort((a, b) => a - b);
  }

  async clearState(conversation: ConversationId) {
    const s = this.clears.get(conversation);
    return s ? { ...s } : null;
  }

  async setClearState(conversation: ConversationId, state: ClearState) {
    this.clears.set(conversation, { ...state });
  }

  async readCursors(conversation: ConversationId) {
    return new Map(this.cursors.get(conversation) ?? []);
  }

  async setReadCursor(conversation: ConversationId, uid: string, seq: number) {
    this.bucket(this.cursors, conversation).set(uid, seq);
  }

  async conversations() {
    return [...this.convs.values()].map(copy);
  }

  async conversation(id: ConversationId) {
    const c = this.convs.get(id);
    return c ? copy(c) : null;
  }

  async putConversation(conversation: StoredConversation) {
    this.convs.set(conversation.id, copy(conversation));
  }

  async deleteConversation(id: ConversationId) {
    this.convs.delete(id);
    this.stored.delete(id);
    this.pending.delete(id);
    this.recalled.delete(id);
    this.clears.delete(id);
    this.cursors.delete(id);
  }
}
