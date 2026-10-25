/**
 * One contract for every store: the in-memory one, IndexedDB, and IndexedDB
 * with encryption at rest (fake-indexeddb stands in for the browser's).
 */

import "fake-indexeddb/auto";
import type { ChatMessage, ChatStore } from "@imkit/client";
import { describe, expect, it } from "vitest";
import { IdbChatStore, MemoryChatStore } from "../src/index.js";

let n = 0;
const stores: [string, () => Promise<ChatStore>][] = [
  ["memory", async () => new MemoryChatStore()],
  ["IndexedDB", async () => IdbChatStore.open({ name: "t" + ++n })],
  ["IndexedDB, encrypted", async () => IdbChatStore.open({ name: "t" + ++n, encrypt: true })],
];

const msg = (conversation: string, seq: number | null, over: Partial<ChatMessage> = {}): ChatMessage => ({
  conversation,
  seq,
  localId: seq === null ? "l" + Math.random() : null,
  from: "uid_peer0000001",
  content: "m" + seq,
  contentType: "text",
  duration: 0,
  senderKind: "user",
  ts: seq ?? 999,
  status: seq === null ? "sending" : "received",
  ...over,
});

describe.each(stores)("%s store", (_name, make) => {
  /** C4: another account's cache is wiped entirely; the same account keeps its data. */
  it("C4_SwitchingAccountClearsAllCaches", async () => {
    const s = await make();
    expect(await s.owner()).toBeNull();
    await s.setOwner("uid_alice000001");
    const c = "uid_peer0000001";
    await s.putMessage(msg(c, 1));
    await s.putMessage(msg(c, null));
    await s.putConversation({ id: c, lastReadSeq: 1, peerReadSeq: 0, pinned: true, muted: false, left: false, updatedAt: 5, draft: "half" });
    await s.addRecalled(c, 2);
    await s.setClearState(c, { clearVersion: 1, clearBeforeSeq: 0 });
    await s.setReadCursor("grp_g0000000001", "uid_x", 3);

    // Same account again: everything is still there (the engine wipes only on a different owner).
    expect(await s.owner()).toBe("uid_alice000001");
    expect(await s.seqs(c)).toEqual([1]);
    expect((await s.conversation(c))?.draft).toBe("half");

    // A different account: the engine wipes every table.
    await s.wipe();
    await s.setOwner("uid_bob00000001");
    expect(await s.owner()).toBe("uid_bob00000001");
    expect(await s.seqs(c)).toEqual([]);
    expect(await s.pendingMessages()).toEqual([]);
    expect(await s.conversations()).toEqual([]);
    expect(await s.recalledSeqs(c)).toEqual([]);
    expect(await s.clearState(c)).toBeNull();
    expect((await s.readCursors("grp_g0000000001")).size).toBe(0);
  });

  it("keeps stored and pending rows apart and promotes atomically", async () => {
    const s = await make();
    const c = "grp_g0000000001";
    for (const seq of [3, 1, 2, 5]) await s.putMessage(msg(c, seq, { extra: { k: seq } }));
    const pending = msg(c, null, { from: "uid_me000000001", content: "out" });
    await s.putMessage(pending);
    expect(await s.seqs(c)).toEqual([1, 2, 3, 5]);
    expect((await s.latestMessages(c, 2)).map((m) => m.seq)).toEqual([3, 5, null]);
    expect((await s.messagesBefore(c, 3, 10)).map((m) => m.seq)).toEqual([1, 2]);
    expect((await s.message(c, 2))?.extra).toEqual({ k: 2 });
    expect((await s.pendingMessages()).map((m) => m.content)).toEqual(["out"]);

    await s.promote(c, pending.localId!, { ...pending, seq: 6, status: "sent" });
    expect(await s.pendingMessages()).toEqual([]);
    expect((await s.message(c, 6))?.localId).toBe(pending.localId);
    expect(await s.countUnread(c, 2, "uid_me000000001")).toBe(2); // 3 and 5; 6 is mine

    await s.deleteThrough(c, 2);
    expect(await s.seqs(c)).toEqual([3, 5, 6]);
    await s.deleteAllMessages(c);
    expect(await s.seqs(c)).toEqual([]);
  });

  it("deletes a conversation with everything stored for it", async () => {
    const s = await make();
    const c = "uid_peer0000001";
    const other = "uid_other000001";
    await s.putMessage(msg(c, 1));
    await s.putMessage(msg(other, 1));
    await s.putConversation({ id: c, lastReadSeq: 0, peerReadSeq: 0, pinned: false, muted: false, left: false, updatedAt: 0 });
    await s.addRecalled(c, 2);
    await s.deleteConversation(c);
    expect(await s.conversation(c)).toBeNull();
    expect(await s.seqs(c)).toEqual([]);
    expect(await s.recalledSeqs(c)).toEqual([]);
    expect(await s.seqs(other)).toEqual([1]);
  });
});

describe("encryption at rest", () => {
  it("seals text and extra, and deleting the conversation shreds its key", async () => {
    const name = "enc" + ++n;
    const s = await IdbChatStore.open({ name, encrypt: true });
    const c = "uid_peer0000001";
    await s.putMessage(msg(c, 1, { content: "secret words", extra: { card: "x" } }));
    await s.putConversation({ id: c, lastReadSeq: 0, peerReadSeq: 0, pinned: false, muted: false, left: false, updatedAt: 0, draft: "unsent" });
    expect((await s.message(c, 1))?.content).toBe("secret words");
    expect((await s.conversation(c))?.draft).toBe("unsent");

    // What IndexedDB holds: no plaintext anywhere in the row.
    const raw = await new Promise<Record<string, unknown>>((resolve, reject) => {
      const open = indexedDB.open(name);
      open.onsuccess = () => {
        const r = open.result.transaction("messages").objectStore("messages").get([c, 1]);
        r.onsuccess = () => resolve(r.result as Record<string, unknown>);
        r.onerror = () => reject(r.error);
      };
    });
    expect(raw.content).toBeUndefined();
    expect(raw.extra).toBeUndefined();
    expect(raw.sealed).toBeDefined();
    expect(JSON.stringify(raw)).not.toContain("secret");

    // The key is not extractable.
    const keyRow = await new Promise<{ key: CryptoKey }>((resolve) => {
      const open = indexedDB.open(name);
      open.onsuccess = () => {
        const r = open.result.transaction("content_keys").objectStore("content_keys").get(c);
        r.onsuccess = () => resolve(r.result as { key: CryptoKey });
      };
    });
    expect(keyRow.key.extractable).toBe(false);
    await expect(crypto.subtle.exportKey("raw", keyRow.key)).rejects.toThrow();

    await s.deleteConversation(c);
    expect(await s.conversation(c)).toBeNull();
  });
});
