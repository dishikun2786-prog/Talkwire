/**
 * IndexedDB storage for the ImKit web client (5.4). The object stores are the
 * logical tables of the Android Room store: `messages` keyed by
 * (conversation, seq), `pending_messages` by (conversation, localId),
 * `conversations`, `recalled_seqs`, `clear_states`, `read_cursors`, `owner`
 * and `content_keys`.
 *
 * With `encrypt: true` each conversation gets its own AES-GCM key, created
 * non-extractable by WebCrypto and kept in `content_keys`: script on the page
 * can use it but never read it. Message text, `extra` and drafts are sealed
 * with it; seqs, senders, types and file URLs stay readable so the store can
 * be queried. Deleting a conversation deletes its key, which makes whatever
 * copies of its rows remain on disk unreadable (crypto-shredding).
 *
 * Writes never hold a transaction across a WebCrypto call: crypto happens
 * before the transaction opens or after it completes.
 */

import {
  MemoryChatStore,
  isPending,
  timelineOrder,
  type ChatMessage,
  type ChatStore,
  type ClearState,
  type ConversationId,
  type StoredConversation,
} from "@imkit/client";

export { MemoryChatStore };

const DB_VERSION = 1;
const STORES = ["messages", "pending_messages", "conversations", "recalled_seqs", "clear_states", "read_cursors", "owner", "content_keys"] as const;
type StoreName = (typeof STORES)[number];

export interface IdbStoreOptions {
  /** Database name; one per app. Default "imkit". */
  name?: string;
  /** Seal message text, `extra` and drafts with a non-extractable per-conversation key. */
  encrypt?: boolean;
  indexedDB?: IDBFactory;
  crypto?: Crypto;
}

interface Sealed {
  iv: Uint8Array;
  data: ArrayBuffer;
}

/** A message row: the body is either plain (`content`, `extra`) or `sealed`. */
type MessageRow = Omit<ChatMessage, "content" | "extra"> & { content?: string; extra?: unknown; sealed?: Sealed };
type ConversationRow = Omit<StoredConversation, "draft"> & { draft?: string; sealedDraft?: Sealed };

function req<T>(r: IDBRequest<T>): Promise<T> {
  return new Promise((resolve, reject) => {
    r.onsuccess = () => resolve(r.result);
    r.onerror = () => reject(r.error);
  });
}

function done(tx: IDBTransaction): Promise<void> {
  return new Promise((resolve, reject) => {
    tx.oncomplete = () => resolve();
    tx.onerror = () => reject(tx.error);
    tx.onabort = () => reject(tx.error ?? new Error("transaction aborted"));
  });
}

const convRange = (c: ConversationId, lo: number | string, hi: number | string) => IDBKeyRange.bound([c, lo], [c, hi]);
const seqRange = (c: ConversationId) => convRange(c, -Infinity, Infinity);
const localRange = (c: ConversationId) => convRange(c, "", "￿");

export class IdbChatStore implements ChatStore {
  private constructor(
    private readonly db: IDBDatabase,
    private readonly encrypt: boolean,
    private readonly crypto: Crypto,
  ) {}

  static async open(options: IdbStoreOptions = {}): Promise<IdbChatStore> {
    const factory = options.indexedDB ?? globalThis.indexedDB;
    if (!factory) throw new Error("no IndexedDB in this environment");
    const open = factory.open(options.name ?? "imkit", DB_VERSION);
    open.onupgradeneeded = () => {
      const db = open.result;
      const create = (name: StoreName, keyPath: string | string[]) => {
        if (!db.objectStoreNames.contains(name)) db.createObjectStore(name, { keyPath });
      };
      create("messages", ["conversation", "seq"]);
      create("pending_messages", ["conversation", "localId"]);
      create("conversations", "id");
      create("recalled_seqs", ["conversation", "seq"]);
      create("clear_states", "conversation");
      create("read_cursors", ["conversation", "uid"]);
      create("owner", "id");
      create("content_keys", "conversation");
    };
    const db = await req(open);
    return new IdbChatStore(db, options.encrypt ?? false, options.crypto ?? globalThis.crypto);
  }

  close(): void {
    this.db.close();
  }

  private tx(names: StoreName | StoreName[], mode: IDBTransactionMode = "readonly"): IDBTransaction {
    return this.db.transaction(names, mode);
  }

  private async all<T>(name: StoreName, range?: IDBKeyRange): Promise<T[]> {
    return req(this.tx(name).objectStore(name).getAll(range) as IDBRequest<T[]>);
  }

  private async put(name: StoreName, value: unknown): Promise<void> {
    const t = this.tx(name, "readwrite");
    t.objectStore(name).put(value);
    await done(t);
  }

  // ------------------------------------------------------------- crypto

  private async keyFor(conversation: ConversationId, create: boolean): Promise<CryptoKey | null> {
    const row = await req(this.tx("content_keys").objectStore("content_keys").get(conversation) as IDBRequest<{ key: CryptoKey } | undefined>);
    if (row) return row.key;
    if (!create) return null;
    const key = await this.crypto.subtle.generateKey({ name: "AES-GCM", length: 256 }, false, ["encrypt", "decrypt"]);
    const t = this.tx("content_keys", "readwrite");
    const store = t.objectStore("content_keys");
    // Another writer may have created one meanwhile: keep the first.
    const existing = await req(store.get(conversation) as IDBRequest<{ key: CryptoKey } | undefined>);
    if (!existing) store.put({ conversation, key, createdAt: Date.now() });
    await done(t);
    return existing?.key ?? key;
  }

  private async seal(conversation: ConversationId, value: unknown): Promise<Sealed> {
    const key = (await this.keyFor(conversation, true))!;
    const iv = this.crypto.getRandomValues(new Uint8Array(12));
    const data = await this.crypto.subtle.encrypt({ name: "AES-GCM", iv }, key, new TextEncoder().encode(JSON.stringify(value)));
    return { iv, data };
  }

  private async unseal<T>(conversation: ConversationId, sealed: Sealed): Promise<T | null> {
    const key = await this.keyFor(conversation, false);
    if (!key) return null; // shredded
    try {
      const plain = await this.crypto.subtle.decrypt({ name: "AES-GCM", iv: sealed.iv as Uint8Array<ArrayBuffer> }, key, sealed.data);
      return JSON.parse(new TextDecoder().decode(plain)) as T;
    } catch {
      return null;
    }
  }

  private async toRow(m: ChatMessage): Promise<MessageRow> {
    if (!this.encrypt) return { ...m };
    const { content, extra, ...rest } = m;
    return { ...rest, sealed: await this.seal(m.conversation, { content, extra }) };
  }

  private async fromRow(r: MessageRow): Promise<ChatMessage> {
    const { sealed, ...rest } = r;
    if (!sealed) return { ...rest, content: rest.content ?? "" } as ChatMessage;
    const body = await this.unseal<{ content: string; extra?: unknown }>(r.conversation, sealed);
    const m = { ...rest, content: body?.content ?? "" } as ChatMessage;
    if (body?.extra !== undefined) m.extra = body.extra;
    return m;
  }

  private async fromRows(rows: MessageRow[]): Promise<ChatMessage[]> {
    return Promise.all(rows.map((r) => this.fromRow(r)));
  }

  // ------------------------------------------------------------- ownership

  async owner(): Promise<string | null> {
    const row = await req(this.tx("owner").objectStore("owner").get(0) as IDBRequest<{ uid: string } | undefined>);
    return row?.uid ?? null;
  }

  async wipe(): Promise<void> {
    const t = this.tx([...STORES], "readwrite");
    for (const s of STORES) t.objectStore(s).clear();
    await done(t);
  }

  async setOwner(uid: string): Promise<void> {
    await this.put("owner", { id: 0, uid });
  }

  // ------------------------------------------------------------- messages

  async putMessage(message: ChatMessage): Promise<void> {
    const row = await this.toRow(message);
    if (message.seq !== null) await this.put("messages", row);
    else if (message.localId) await this.put("pending_messages", row);
    else throw new Error("a message needs a seq or a local id");
  }

  async promote(conversation: ConversationId, localId: string, stored: ChatMessage): Promise<void> {
    if (stored.seq === null) throw new Error("promote needs a stored message");
    const row = await this.toRow(stored);
    const t = this.tx(["messages", "pending_messages"], "readwrite");
    t.objectStore("pending_messages").delete([conversation, localId]);
    t.objectStore("messages").put(row);
    await done(t);
  }

  async message(conversation: ConversationId, seq: number): Promise<ChatMessage | null> {
    const row = await req(this.tx("messages").objectStore("messages").get([conversation, seq]) as IDBRequest<MessageRow | undefined>);
    return row ? this.fromRow(row) : null;
  }

  async pendingMessage(conversation: ConversationId, localId: string): Promise<ChatMessage | null> {
    const row = await req(this.tx("pending_messages").objectStore("pending_messages").get([conversation, localId]) as IDBRequest<MessageRow | undefined>);
    return row ? this.fromRow(row) : null;
  }

  private async newest(conversation: ConversationId, limit: number, beforeSeq = Infinity): Promise<MessageRow[]> {
    if (limit <= 0) return [];
    const out: MessageRow[] = [];
    const t = this.tx("messages");
    const range = IDBKeyRange.bound([conversation, -Infinity], [conversation, beforeSeq], false, true);
    await new Promise<void>((resolve, reject) => {
      const cursor = t.objectStore("messages").openCursor(range, "prev");
      cursor.onsuccess = () => {
        const c = cursor.result;
        if (!c || out.length >= limit) return resolve();
        out.push(c.value as MessageRow);
        c.continue();
      };
      cursor.onerror = () => reject(cursor.error);
    });
    return out.reverse();
  }

  async latestMessages(conversation: ConversationId, limit: number): Promise<ChatMessage[]> {
    const stored = await this.newest(conversation, limit);
    const pending = await this.all<MessageRow>("pending_messages", localRange(conversation));
    return (await this.fromRows([...stored, ...pending])).sort(timelineOrder);
  }

  async messagesBefore(conversation: ConversationId, beforeSeq: number, limit: number): Promise<ChatMessage[]> {
    return (await this.fromRows(await this.newest(conversation, limit, beforeSeq))).sort(timelineOrder);
  }

  async seqs(conversation: ConversationId): Promise<number[]> {
    const keys = await req(this.tx("messages").objectStore("messages").getAllKeys(seqRange(conversation)));
    return keys.map((k) => (k as [string, number])[1]).sort((a, b) => a - b);
  }

  async pendingMessages(): Promise<ChatMessage[]> {
    return (await this.fromRows(await this.all<MessageRow>("pending_messages"))).filter(isPending).sort(timelineOrder);
  }

  async deleteMessage(conversation: ConversationId, seq: number): Promise<void> {
    const t = this.tx("messages", "readwrite");
    t.objectStore("messages").delete([conversation, seq]);
    await done(t);
  }

  async deletePending(conversation: ConversationId, localId: string): Promise<void> {
    const t = this.tx("pending_messages", "readwrite");
    t.objectStore("pending_messages").delete([conversation, localId]);
    await done(t);
  }

  async deleteThrough(conversation: ConversationId, seq: number): Promise<void> {
    const t = this.tx("messages", "readwrite");
    t.objectStore("messages").delete(convRange(conversation, -Infinity, seq));
    await done(t);
  }

  async deleteAllMessages(conversation: ConversationId): Promise<void> {
    const t = this.tx(["messages", "pending_messages"], "readwrite");
    t.objectStore("messages").delete(seqRange(conversation));
    t.objectStore("pending_messages").delete(localRange(conversation));
    await done(t);
  }

  async countUnread(conversation: ConversationId, afterSeq: number, me: string): Promise<number> {
    const rows = await this.all<MessageRow>("messages", IDBKeyRange.bound([conversation, afterSeq], [conversation, Infinity], true, false));
    return rows.filter((r) => r.from !== me && r.contentType !== "system").length;
  }

  // ------------------------------------------------------------- tombstones, watermark, cursors

  async addRecalled(conversation: ConversationId, seq: number): Promise<void> {
    await this.put("recalled_seqs", { conversation, seq });
  }

  async recalledSeqs(conversation: ConversationId): Promise<number[]> {
    const keys = await req(this.tx("recalled_seqs").objectStore("recalled_seqs").getAllKeys(seqRange(conversation)));
    return keys.map((k) => (k as [string, number])[1]).sort((a, b) => a - b);
  }

  async clearState(conversation: ConversationId): Promise<ClearState | null> {
    const row = await req(this.tx("clear_states").objectStore("clear_states").get(conversation) as IDBRequest<(ClearState & { conversation: string }) | undefined>);
    return row ? { clearVersion: row.clearVersion, clearBeforeSeq: row.clearBeforeSeq } : null;
  }

  async setClearState(conversation: ConversationId, state: ClearState): Promise<void> {
    await this.put("clear_states", { conversation, ...state });
  }

  async readCursors(conversation: ConversationId): Promise<Map<string, number>> {
    const rows = await this.all<{ uid: string; seq: number }>("read_cursors", localRange(conversation));
    return new Map(rows.map((r) => [r.uid, r.seq]));
  }

  async setReadCursor(conversation: ConversationId, uid: string, seq: number): Promise<void> {
    await this.put("read_cursors", { conversation, uid, seq });
  }

  // ------------------------------------------------------------- conversations

  private async toConvRow(c: StoredConversation): Promise<ConversationRow> {
    if (!this.encrypt || c.draft === undefined) return { ...c };
    const { draft, ...rest } = c;
    return { ...rest, sealedDraft: await this.seal(c.id, draft) };
  }

  private async fromConvRow(r: ConversationRow): Promise<StoredConversation> {
    const { sealedDraft, ...rest } = r;
    if (!sealedDraft) return rest;
    const draft = await this.unseal<string>(r.id, sealedDraft);
    return draft ? { ...rest, draft } : rest;
  }

  async conversations(): Promise<StoredConversation[]> {
    return Promise.all((await this.all<ConversationRow>("conversations")).map((r) => this.fromConvRow(r)));
  }

  async conversation(id: ConversationId): Promise<StoredConversation | null> {
    const row = await req(this.tx("conversations").objectStore("conversations").get(id) as IDBRequest<ConversationRow | undefined>);
    return row ? this.fromConvRow(row) : null;
  }

  async putConversation(conversation: StoredConversation): Promise<void> {
    await this.put("conversations", await this.toConvRow(conversation));
  }

  async deleteConversation(id: ConversationId): Promise<void> {
    const t = this.tx(["conversations", "messages", "pending_messages", "recalled_seqs", "clear_states", "read_cursors", "content_keys"], "readwrite");
    t.objectStore("conversations").delete(id);
    t.objectStore("messages").delete(seqRange(id));
    t.objectStore("pending_messages").delete(localRange(id));
    t.objectStore("recalled_seqs").delete(seqRange(id));
    t.objectStore("clear_states").delete(id);
    t.objectStore("read_cursors").delete(localRange(id));
    // The key goes last: whatever of the rows survives on disk can no longer be read.
    t.objectStore("content_keys").delete(id);
    await done(t);
  }
}
