/**
 * Several tabs share one connection (5.3), with Node's BroadcastChannel
 * standing in for the browser's: one real connection, every tab logged in,
 * pushes reach every tab, a tab's own sends reach the others as a second
 * device would see them, and when the leader goes another tab takes over.
 */

import { uidOf } from "@talkwire/protocol";
import { afterEach, describe, expect, it } from "vitest";
import {
  ChatEngine,
  ImClient,
  MemoryChatStore,
  RelayHub,
  RelayTransport,
  createTabTransport,
  serveSharedWorker,
  type RelayLink,
  type RelayMessage,
  type TabTransport,
  type TabTransportOptions,
} from "../src/index.js";
import { FakeServer } from "./fake-server.js";
import { until } from "./replay.js";

const cleanups: (() => void)[] = [];
afterEach(() => {
  for (const c of cleanups.splice(0).reverse()) c();
});

/** Never stopped for good: a client relayed through tabs must never be told it was replaced by its own browser. */
function neverStopped(client: ImClient): () => string[] {
  const stops: string[] = [];
  const off = client.state.subscribe((s) => {
    if (s.kind === "stopped") stops.push(String((s as { reason?: unknown }).reason));
  });
  cleanups.push(off);
  return () => stops;
}

/** `locks`: null forces the heartbeat election; undefined uses the runtime's Web Locks. */
function tab(server: FakeServer, name: string, locks: LockManager | null | undefined, sharedWorker?: TabTransportOptions["sharedWorker"]) {
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
    const a = tab(server, name, locks);
    const b = tab(server, name, locks);
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

/**
 * Stands in for BroadcastChannel where the test controls delivery: `lag`
 * delays a message, and a message `drop` refuses is not delivered to that
 * channel. `tab` labels the channels created while it is set.
 */
class TestChannel extends EventTarget {
  static lag: (m: RelayMessage) => number = () => 1;
  static drop: (m: RelayMessage, to: TestChannel) => boolean = () => false;
  static tab = "";
  static readonly open = new Set<TestChannel>();
  readonly tab = TestChannel.tab;
  private closed = false;

  constructor(readonly name: string) {
    super();
    TestChannel.open.add(this);
  }

  postMessage(data: unknown): void {
    const m = structuredClone(data) as RelayMessage;
    for (const other of TestChannel.open) {
      if (other === this || other.name !== this.name || TestChannel.drop(m, other)) continue;
      setTimeout(() => {
        if (!other.closed) other.dispatchEvent(new MessageEvent("message", { data: m }));
      }, TestChannel.lag(m));
    }
  }

  close(): void {
    this.closed = true;
    TestChannel.open.delete(this);
  }
}

function useTestChannel(): void {
  const g = globalThis as { BroadcastChannel?: unknown };
  const previous = g.BroadcastChannel;
  g.BroadcastChannel = TestChannel;
  cleanups.push(() => {
    g.BroadcastChannel = previous;
    TestChannel.lag = () => 1;
    TestChannel.drop = () => false;
    TestChannel.tab = "";
  });
}

const isAnnouncement = (m: RelayMessage) => m.t === "hub" || m.t === "candidate";

/**
 * Two leaders at once must not mean two real connections: the server keeps
 * one per user and tells the older one it was replaced, which would stop
 * the tabs relayed through it for good.
 */
describe("tabs with two leaders at once (heartbeat election)", () => {
  it("settle on one leader before either connects", async () => {
    useTestChannel();
    // Announcements arrive later than the claims' spread: both tabs claim.
    TestChannel.lag = (m) => (isAnnouncement(m) ? 700 : 1);
    const server = new FakeServer();
    server.replaceOlderLogins = true;
    const name = "split-" + Math.random();
    const a = tab(server, name, null);
    const b = tab(server, name, null);
    const stops = [neverStopped(a.client), neverStopped(b.client)];
    for (const t of [a, b]) {
      await t.engine.open();
      t.client.start();
    }
    await until("both logged in", () => a.client.isConnected && b.client.isConnected, 15_000);
    expect(server.conns, "only one real connection was ever opened").toHaveLength(1);
    expect(server.sent("login")).toHaveLength(1);
    expect([a.tabs.role.value, b.tabs.role.value].sort()).toEqual(["follower", "leader"]);
    expect(stops.flatMap((s) => s())).toEqual([]);
  });

  it("a socket talks only to the hub that answered it first", async () => {
    const server = new FakeServer();
    // Two hubs and one tab's transport on one bus; each message reaches the others.
    const members = new Set<(m: RelayMessage) => void>();
    const join = (deliver: (m: RelayMessage) => void) => {
      members.add(deliver);
      return (m: RelayMessage) => {
        for (const d of members) if (d !== deliver) setTimeout(() => d(m));
      };
    };
    const hubs = [0, 1].map(() => {
      const ref: { hub?: RelayHub } = {};
      const post = join((m) => ref.hub!.receive(m));
      ref.hub = new RelayHub(post, server);
      cleanups.push(() => ref.hub!.stop());
      return ref.hub;
    });
    const sent: RelayMessage[] = [];
    let toTab: (m: RelayMessage) => void = () => undefined;
    const postFromTab = join((m) => toTab(m));
    const link: RelayLink = {
      post: (m) => {
        sent.push(m);
        postFromTab(m);
      },
      onMessage: (fn) => {
        toTab = fn;
        return () => undefined;
      },
    };
    const client = new ImClient({ endpoints: ["ws://fake/ws/im"], tokenProvider: () => "tok:alice", transport: new RelayTransport(link), watchNetwork: false });
    cleanups.push(() => client.stop());
    client.start();
    await until("logged in", () => client.isConnected);
    const login = sent.find((m) => m.t === "send" && m.text.includes('"login"'));
    expect(login && login.t === "send" && login.hub).toBe(hubs[0]!.id);
    expect(sent.some((m) => m.t === "close" && m.hub === hubs[1]!.id), "the other hub is told to forget the socket").toBe(true);
    expect(server.conns, "the other hub opened nothing").toHaveLength(1);
  });

  it("a hub replaced by another hub of the same browser gives way instead of telling the tabs", async () => {
    const server = new FakeServer();
    server.replaceOlderLogins = true;
    const posted: RelayMessage[] = [];
    const hub = new RelayHub((m) => posted.push(m), server);
    cleanups.push(() => hub.stop());
    let rival = true;
    hub.onReplaced = () => rival;
    const login = (conn: string) => {
      hub.receive({ t: "open", conn, url: "ws://fake/ws/im" });
      hub.receive({ t: "send", conn, text: JSON.stringify({ id: "l", action: "login", token: "tok:alice" }), hub: hub.id });
    };
    login("tab:1");
    await until("hub logged in", () => server.live.length === 1 && posted.some((m) => m.t === "frame" && m.text.includes('"code":200')));
    // Another hub's login replaces this one's connection.
    server.open("ws://fake/ws/im", { onOpen: () => undefined, onText: () => undefined, onClosed: () => undefined, onFailure: () => undefined }).send(JSON.stringify({ action: "login", token: "tok:alice" }));
    await until("closed", () => posted.some((m) => m.t === "closed"));
    expect(posted.some((m) => m.t === "frame" && m.text.includes('"code":401')), "the tabs are not told").toBe(false);

    // Without a rival it was another device: the tabs are told, as by any server.
    rival = false;
    posted.length = 0;
    login("tab:2");
    await until("hub logged in again", () => posted.some((m) => m.t === "frame" && m.text.includes('"code":200')));
    server.open("ws://fake/ws/im", { onOpen: () => undefined, onText: () => undefined, onClosed: () => undefined, onFailure: () => undefined }).send(JSON.stringify({ action: "login", token: "tok:alice" }));
    await until("told", () => posted.some((m) => m.t === "frame" && m.text.includes('"code":401')));
  });

  it("a leader that stops being heard gives way when the new leader's login replaces it", async () => {
    useTestChannel();
    const server = new FakeServer();
    server.replaceOlderLogins = true;
    const name = "frozen-" + Math.random();
    TestChannel.tab = "a";
    const a = tab(server, name, null);
    const stops = [neverStopped(a.client)];
    await a.engine.open();
    a.client.start();
    await until("a leads and is logged in", () => a.tabs.role.value === "leader" && a.client.isConnected, 15_000);
    // From now on nothing a's hub sends reaches the other tab (a busy or
    // throttled leader): the other tab elects itself and connects.
    const aHub = await firstAnnouncedHub();
    TestChannel.drop = (m, to) => "hub" in m && m.hub === aHub && to.tab !== "a";
    TestChannel.tab = "b";
    const b = tab(server, name, null);
    stops.push(neverStopped(b.client));
    await b.engine.open();
    b.client.start();
    await until("b leads, both logged in, one connection", () => b.tabs.role.value === "leader" && a.tabs.role.value === "follower" && a.client.isConnected && b.client.isConnected && server.live.length === 1, 20_000);
    expect(stops.flatMap((s) => s())).toEqual([]);
  });
});

/** The id of the next hub announcement on any test channel. */
function firstAnnouncedHub(): Promise<string> {
  return new Promise((resolve) => {
    const post = TestChannel.prototype.postMessage;
    TestChannel.prototype.postMessage = function (this: TestChannel, data: unknown) {
      const m = data as RelayMessage;
      if (m.t === "hub") {
        TestChannel.prototype.postMessage = post;
        resolve(m.hub);
      }
      post.call(this, data);
    };
  });
}

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
