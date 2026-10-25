/**
 * Several tabs share one connection (5.3), with Node's BroadcastChannel
 * standing in for the browser's: one real connection, every tab logged in,
 * pushes reach every tab, a tab's own sends reach the others as a second
 * device would see them, and when the leader goes another tab takes over.
 */

import { uidOf } from "@imkit/protocol";
import { afterEach, describe, expect, it } from "vitest";
import { ChatEngine, ImClient, MemoryChatStore, createTabTransport, serveSharedWorker, type TabTransport, type TabTransportOptions } from "../src/index.js";
import { FakeServer } from "./fake-server.js";
import { until } from "./replay.js";

const cleanups: (() => void)[] = [];
afterEach(() => {
  for (const c of cleanups.splice(0).reverse()) c();
});

function tab(server: FakeServer, name: string, locks: LockManager | null, sharedWorker?: TabTransportOptions["sharedWorker"]) {
  const tabs: TabTransport = createTabTransport({ name, transport: server, locks, sharedWorker });
  const client = new ImClient({
    endpoints: ["ws://fake/ws/im"],
    tokenProvider: () => "tok:alice",
    transport: tabs.transport,
    loginTimeoutMs: 8_000,
    reconnectBaseDelayMs: 50,
    reconnectMaxDelayMs: 100,
    watchNetwork: false,
  });
  const engine = new ChatEngine(client, new MemoryChatStore(), { appUserId: "alice", autoSync: false });
  cleanups.push(() => {
    engine.close();
    client.stop();
    tabs.close();
  });
  return { tabs, client, engine };
}

const electionModes: [string, LockManager | null | undefined][] = [["heartbeat election", null]];
if ((globalThis as { navigator?: { locks?: LockManager } }).navigator?.locks) electionModes.push(["Web Locks", undefined]);

describe.each(electionModes)("tabs with %s", (_mode, locks) => {
  it("share one real connection and see each other's messages", async () => {
    const server = new FakeServer();
    const bob = uidOf("bob");
    let seq = 0;
    server.onFrame = (conn, f) => {
      if (f.action !== "pub") return false;
      conn.push({ action: "pub", code: 200, id: f.id, topic: f.topic, seq: ++seq, ts: 1_000 + seq });
      return true;
    };
    const name = "share-" + Math.random();
    const a = tab(server, name, locks ?? null);
    const b = tab(server, name, locks ?? null);
    for (const t of [a, b]) {
      await t.engine.open();
      await t.engine.ensureConversation(bob, "Bob");
      t.client.start();
    }
    await until("both logged in", () => a.client.isConnected && b.client.isConnected, 15_000);
    expect(server.live).toHaveLength(1);
    expect(server.sent("login")).toHaveLength(1);
    expect(a.client.session?.uid).toBe(uidOf("alice"));
    expect(b.client.session?.uid).toBe(uidOf("alice"));
    expect([a.tabs.role.value, b.tabs.role.value].sort()).toEqual(["follower", "leader"]);

    // A push reaches both tabs.
    server.live[0]!.push({ action: "data", topic: bob, from: bob, seq: 1, ts: 1, content: "hi", contentType: "text" });
    seq = 1;
    await until("both have hi", () => [a, b].every((t) => t.engine.messages(bob).value.some((m) => m.content === "hi")));

    // Tab a sends; tab b hears of it as from another device.
    await a.engine.send(bob, { content: "from a" });
    await until("b has a's message", () => b.engine.messages(bob).value.some((m) => m.content === "from a" && m.status === "sent"));
    await until("a stored it", () => a.engine.messages(bob).value.some((m) => m.content === "from a" && m.seq === 2));
    expect(server.sent("pub")).toHaveLength(1);

    // Tab b reads; tab a learns its read position moved.
    await b.engine.markRead(bob);
    await until("a's read position", () => a.engine.conversation(bob)?.lastReadSeq === 2);

    // The leader goes away; the other tab takes over with a new real connection.
    const [leader, follower] = a.tabs.role.value === "leader" ? [a, b] : [b, a];
    leader.client.stop();
    leader.tabs.close();
    await until("follower leads", () => follower.tabs.role.value === "leader", 15_000);
    await until("follower reconnected", () => follower.client.isConnected && server.live.length === 1, 15_000);
    expect(server.sent("login")).toHaveLength(2);
  });
});

/** Stands in for the browser's SharedWorker: one hub per name, reached over a MessageChannel per tab. */
function fakeSharedWorkers(server: FakeServer, opts: { failToStart?: boolean } = {}) {
  const g = globalThis as { SharedWorker?: unknown };
  const had = "SharedWorker" in g;
  const previous = g.SharedWorker;
  g.SharedWorker = function SharedWorker() {};
  const scopes = new Map<string, { onconnect: ((ev: MessageEvent) => void) | null }>();
  let created = 0;
  cleanups.push(() => {
    if (had) g.SharedWorker = previous;
    else delete g.SharedWorker;
  });
  const factory = (name: string) => (): SharedWorker => {
    created++;
    const worker = new EventTarget() as EventTarget & { port: MessagePort };
    const channel = new MessageChannel();
    worker.port = channel.port1;
    cleanups.push(() => {
      channel.port1.close();
      channel.port2.close();
    });
    if (opts.failToStart) {
      setTimeout(() => worker.dispatchEvent(new Event("error")), 20);
    } else {
      let scope = scopes.get(name);
      if (!scope) {
        scope = { onconnect: null };
        const hub = serveSharedWorker(scope, server);
        scopes.set(name, scope);
        cleanups.push(() => hub.stop());
      }
      scope.onconnect!({ ports: [channel.port2] } as unknown as MessageEvent);
    }
    return worker as unknown as SharedWorker;
  };
  return { factory, created: () => created };
}

describe("tabs with a SharedWorker", () => {
  it("connect through the worker's one connection", async () => {
    const server = new FakeServer();
    const name = "worker-" + Math.random();
    const workers = fakeSharedWorkers(server);
    const a = tab(server, name, null, workers.factory(name));
    const b = tab(server, name, null, workers.factory(name));
    expect(a.tabs.role.value).toBe("worker");
    for (const t of [a, b]) {
      await t.engine.open();
      t.client.start();
    }
    await until("both logged in", () => a.client.isConnected && b.client.isConnected, 15_000);
    expect(server.live).toHaveLength(1);
    expect(server.sent("login")).toHaveLength(1);
  });

  it("fall back to electing a tab when the worker never starts", async () => {
    const server = new FakeServer();
    const name = "noworker-" + Math.random();
    const workers = fakeSharedWorkers(server, { failToStart: true });
    const a = tab(server, name, null, workers.factory(name));
    const b = tab(server, name, null, workers.factory(name));
    for (const t of [a, b]) {
      await t.engine.open();
      t.client.start();
    }
    await until("both logged in", () => a.client.isConnected && b.client.isConnected, 15_000);
    expect(workers.created()).toBe(2);
    expect([a.tabs.role.value, b.tabs.role.value].sort()).toEqual(["follower", "leader"]);
    expect(server.live).toHaveLength(1);
  });
});
