/**
 * The M1 flow of android/talkwire-core's M1RealServerTest, for the web client,
 * against a real server under both protocol profiles: login and first sync,
 * a single-chat message, publish idempotency, offline catch-up over several
 * pages, reconnecting after a dropped socket, a group round trip, history
 * paging on a fresh device, recall, read receipts, a new device's unread
 * count, clearing for everyone, and presence.
 */

import { uidOf } from "@talkwire/protocol";
import { randomBytes } from "node:crypto";
import { describe, expect, it } from "vitest";
import {
  ChatEngine,
  ImClient,
  MemoryChatStore,
  WebSocketTransport,
  isRecalled,
  type ChatEvent,
  type ChatMessage,
  type ImSocket,
  type ImSocketListener,
  type ImTransport,
  type ReadonlySignal,
} from "../src/index.js";
import { RealServer, realServerAvailable } from "./real-server.js";
import { until } from "./replay.js";

/** Lets a test cut the network under a client. */
class KillableTransport implements ImTransport {
  private readonly live: { socket: ImSocket; listener: ImSocketListener }[] = [];
  private readonly inner = new WebSocketTransport();

  open(url: string, listener: ImSocketListener): ImSocket {
    const socket = this.inner.open(url, listener);
    this.live.push({ socket, listener });
    return socket;
  }

  killAll(): void {
    for (const { socket, listener } of this.live.splice(0)) {
      socket.close(4000, "killed");
      listener.onClosed(1006, "killed");
    }
  }
}

interface Device {
  client: ImClient;
  engine: ChatEngine;
  store: MemoryChatStore;
  transport: KillableTransport;
  events: ChatEvent[];
  synced(): number;
}

function device(server: RealServer, appUserId: string, deviceName?: string): Device {
  const transport = new KillableTransport();
  const client = new ImClient({
    endpoints: [server.wsUrl],
    tokenProvider: () => server.token(appUserId),
    transport,
    requestTimeoutMs: 5_000,
    publishTimeoutMs: 5_000,
    reconnectBaseDelayMs: 200,
    reconnectMaxDelayMs: 400,
    watchNetwork: false,
    ...(deviceName ? { deviceName } : {}),
  });
  const store = new MemoryChatStore();
  const engine = new ChatEngine(client, store, { appUserId, pageSize: 3 });
  const events: ChatEvent[] = [];
  engine.events.on((e) => events.push(e));
  return { client, engine, store, transport, events, synced: () => events.filter((e) => e.kind === "synced").length };
}

const seqsOf = (w: ReadonlySignal<ChatMessage[]>) => w.value.flatMap((m) => (m.seq === null ? [] : [m.seq]));

async function sendAndConfirm(d: Device, conv: string, window: ReadonlySignal<ChatMessage[]>, text: string): Promise<ChatMessage> {
  const pending = await d.engine.send(conv, { content: text });
  await until(`"${text}" stored`, () => window.value.some((m) => m.localId === pending.localId && m.status === "sent"));
  return window.value.find((m) => m.localId === pending.localId)!;
}

async function flow(server: RealServer, profile: "strict" | "legacy"): Promise<void> {
  const run = randomBytes(4).toString("hex");
  const aliceId = `m1web-${run}-alice`;
  const bobId = `m1web-${run}-bob`;
  const aliceUid = uidOf(aliceId);
  const bobUid = uidOf(bobId);
  const devices: Device[] = [];
  const make = (id: string, name?: string) => {
    const d = device(server, id, name);
    devices.push(d);
    return d;
  };
  try {
    const alice = make(aliceId);
    const bob = make(bobId);
    for (const d of [alice, bob]) await d.engine.open();
    // Single chats come from the host's contact list.
    await alice.engine.ensureConversation(bobUid, "Bob");
    await bob.engine.ensureConversation(aliceUid, "Alice");
    const aliceWin = alice.engine.messages(bobUid);
    const bobWin = bob.engine.messages(aliceUid);

    // Login and the first sync.
    alice.client.start();
    bob.client.start();
    await until("first sync", () => alice.synced() >= 1 && bob.synced() >= 1);
    const session = alice.client.session!;
    expect(session.uid).toBe(aliceUid);
    if (profile === "strict") expect(session.protocol, "strict login reply carries protocol").toBeGreaterThanOrEqual(1);
    else expect(session.protocol, "legacy login reply has no protocol").toBe(0);

    // A single-chat message.
    const hello = await sendAndConfirm(alice, bobUid, aliceWin, "hello");
    expect(hello.seq).toBe(1);
    await until("Bob receives hello", () => bobWin.value.some((m) => m.content === "hello" && m.status === "received"));
    expect(bob.events.some((e) => e.kind === "messageArrived" && e.message.content === "hello")).toBe(true);
    // Both open the chat: presence pushes follow subscriptions.
    expect((await alice.engine.subscribe(bobUid)).code).toBe(200);
    expect((await bob.engine.subscribe(aliceUid)).code).toBe(200);

    // Idempotency: the same clientMsgId twice is stored once (C2's foundation).
    const cid = `m1web-${run}-dup`;
    const first = await alice.client.publish(bobUid, { content: "dup", contentType: "text" }, cid);
    const again = await alice.client.publish(bobUid, { content: "dup", contentType: "text" }, cid);
    expect(first.kind).toBe("stored");
    expect(again).toEqual(first);
    await until("Bob receives dup", () => bobWin.value.some((m) => m.content === "dup"));
    await new Promise((r) => setTimeout(r, 300));
    expect(bobWin.value.filter((m) => m.content === "dup")).toHaveLength(1);
    await alice.engine.syncConversation(bobUid); // Alice's engine did not see the direct publish
    expect(seqsOf(aliceWin)).toEqual([1, 2]);

    // Offline catch-up: more than one page (3) while Bob is away.
    bob.client.stop();
    await until("Bob offline for Alice", () => alice.engine.presence.state(bobUid) === "offline");
    for (let i = 0; i < 4; i++) await sendAndConfirm(alice, bobUid, aliceWin, `while away ${i}`);
    const syncedBefore = bob.synced();
    bob.client.start();
    await until("Bob's sync after login", () => bob.synced() > syncedBefore);
    expect(seqsOf(bobWin)).toEqual([1, 2, 3, 4, 5, 6]);
    await until("Bob online for Alice", () => alice.engine.presence.state(bobUid) === "online");

    // Reconnect after the socket dies under the client.
    const beforeDrop = bob.synced();
    bob.transport.killAll();
    await sendAndConfirm(alice, bobUid, aliceWin, "after the drop");
    await until("Bob resynced", () => bob.synced() > beforeDrop && bob.client.isConnected);
    await until("Bob has the message", () => bobWin.value.some((m) => m.content === "after the drop"));
    expect(seqsOf(bobWin)).toEqual([1, 2, 3, 4, 5, 6, 7]);

    // A group round trip; roles come from the roster (C9).
    const topic = await alice.client.createGroup(`m1web ${run}`, [bobUid]);
    await until("Bob learns of the group", () => bob.engine.conversations.value.some((c) => c.id === topic));
    const aliceGroup = alice.engine.messages(topic);
    const bobGroup = bob.engine.messages(topic);
    await sendAndConfirm(alice, topic, aliceGroup, "to the group");
    await until("Bob receives the group message", () => bobGroup.value.some((m) => m.content === "to the group"));
    expect(await alice.engine.myRole(topic)).toBe("owner");
    expect(await bob.engine.myRole(topic)).toBe("member");

    // History paging on a fresh device: newest page first, then older pages.
    bob.client.stop(); // the legacy profile allows one connection per user
    const tablet = make(bobId, "tablet");
    await tablet.engine.open();
    await tablet.engine.ensureConversation(aliceUid, "Alice");
    const tabletWin = tablet.engine.messages(aliceUid);
    tablet.client.start();
    await until("tablet sync", () => tablet.synced() >= 1);
    expect(seqsOf(tabletWin)).toEqual([5, 6, 7]);
    let pages = 0;
    while ((await tablet.engine.loadOlder(aliceUid)) > 0) pages++;
    expect(seqsOf(tabletWin)).toEqual([1, 2, 3, 4, 5, 6, 7]);
    expect(pages).toBe(2);
    expect(tabletWin.value.slice(0, 2).map((m) => m.content)).toEqual(["hello", "dup"]);
    tablet.client.stop();
    const bobBack = bob.synced();
    bob.client.start();
    await until("Bob back", () => bob.synced() > bobBack);
    // Nothing changed while Bob was away: under strict the summary says so and
    // nothing is pulled (section 8.1); legacy pulls each conversation.
    const back = bob.events.filter((e) => e.kind === "synced").at(-1);
    expect(back).toMatchObject(profile === "strict" ? { conversations: 2, failed: 0, pulled: 0 } : { conversations: 2, failed: 0, pulled: 2 });

    // Recall: the content is gone on both sides; a tombstone says who unsent it.
    const target = aliceWin.value.find((m) => m.content === "while away 0")!;
    await alice.engine.recall(target);
    expect(isRecalled(aliceWin.value.find((m) => m.seq === target.seq)!)).toBe(true);
    await until("recall reaches Bob", () => bobWin.value.some((m) => m.seq === target.seq && isRecalled(m)));
    expect(bobWin.value.find((m) => m.seq === target.seq)!.from).toBe(aliceUid);

    // Read receipts.
    await bob.engine.markRead(aliceUid);
    const bobMax = Math.max(...seqsOf(bobWin));
    await until("Alice sees the read position", async () => (await alice.store.conversation(bobUid))?.peerReadSeq === bobMax);
    expect(bob.engine.conversation(aliceUid)?.unread).toBe(0);

    // A new device pulls history already read: strict returns Bob's own read
    // position as readSeq; legacy keeps none, so everything counts as unread.
    bob.client.stop();
    const laptop = make(bobId, "laptop");
    await laptop.engine.open();
    await laptop.engine.ensureConversation(aliceUid, "Alice");
    const laptopWin = laptop.engine.messages(aliceUid);
    laptop.client.start();
    await until("laptop sync", () => laptop.synced() >= 1);
    expect(seqsOf(laptopWin)).toEqual([5, 6, 7]);
    const laptopChat = laptop.engine.conversation(aliceUid)!;
    if (profile === "strict") {
      expect(laptopChat.unread, "a new device's history is already read").toBe(0);
      expect(laptopChat.lastReadSeq).toBe(bobMax);
    } else {
      expect(laptopChat.unread, "legacy keeps no single-chat read position").toBe(3);
    }
    laptop.client.stop();
    const bobAgain = bob.synced();
    bob.client.start();
    await until("Bob back again", () => bob.synced() > bobAgain);

    // Clear for everyone, permanently; later messages arrive normally.
    await alice.engine.clear(bobUid);
    expect(aliceWin.value).toEqual([]);
    await until("clear reaches Bob", () => bobWin.value.length === 0);
    await until("Bob is told", () => bob.events.some((e) => e.kind === "conversationCleared" && e.conversation === aliceUid));
    await bob.engine.syncConversation(aliceUid);
    expect(bobWin.value, "nothing comes back after a clear").toEqual([]);
    const fresh = await sendAndConfirm(alice, bobUid, aliceWin, "fresh start");
    expect(fresh.seq!).toBeGreaterThan(bobMax);
    await until("Bob receives after the clear", () => bobWin.value.map((m) => m.content).join() === "fresh start");

    // Presence when Alice leaves.
    alice.client.stop();
    await until("Alice offline for Bob", () => bob.engine.presence.state(aliceUid) === "offline");
  } finally {
    for (const d of devices) {
      d.engine.close();
      d.client.stop();
    }
  }
}

describe.skipIf(!realServerAvailable)("M1 against a real server", () => {
  for (const profile of ["strict", "legacy"] as const) {
    it(`${profile} profile`, { timeout: 180_000 }, async () => {
      const server = await RealServer.start(profile);
      try {
        await flow(server, profile);
      } catch (e) {
        throw new Error(`${e instanceof Error ? (e.stack ?? e.message) : String(e)}\n--- server log (${profile}) ---\n${server.logTail()}`);
      } finally {
        await server.close();
      }
    });
  }
});
