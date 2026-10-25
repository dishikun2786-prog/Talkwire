import { p2pTopic, uidOf } from "@imkit/protocol";
import { afterEach, describe, expect, it } from "vitest";
import { AI_ASSISTANT_UID, ChatEngine, ImClient, MemoryChatStore, PresenceTracker, RECALLED_CONTENT_TYPE, recalledWhileAway, resumeSeq, type EngineConfig } from "../src/index.js";
import { FakeServer, type Frame } from "./fake-server.js";
import { until } from "./replay.js";

const clients: ImClient[] = [];
const engines: ChatEngine[] = [];
afterEach(() => {
  for (const c of clients.splice(0)) c.stop();
  for (const e of engines.splice(0)) e.close();
});

function device(server: FakeServer, appUserId: string, opts: { publishTimeoutMs?: number; autoSync?: boolean; clock?: () => number } = {}) {
  const client = new ImClient({
    endpoints: ["ws://fake/ws/im"],
    tokenProvider: () => "tok:" + appUserId,
    transport: server,
    publishTimeoutMs: opts.publishTimeoutMs ?? 5_000,
    requestTimeoutMs: 2_000,
    reconnectBaseDelayMs: 20,
    reconnectMaxDelayMs: 40,
    watchNetwork: false,
  });
  const store = new MemoryChatStore();
  const config: EngineConfig = { appUserId, autoSync: opts.autoSync ?? false, pageSize: 10 };
  if (opts.clock) config.clock = opts.clock;
  const engine = new ChatEngine(client, store, config);
  clients.push(client);
  engines.push(engine);
  return { client, store, engine };
}

describe("sync math", () => {
  it("resumes from the end of the contiguous run (C3)", () => {
    expect(resumeSeq([], [], 0)).toBe(0);
    expect(resumeSeq([1, 3], [], 0)).toBe(1);
    expect(resumeSeq([5, 6, 7], [], 0)).toBe(7);
    expect(resumeSeq([1, 3], [2], 0), "a recalled seq is not a hole").toBe(3);
    expect(resumeSeq([9], [], 7), "the clear watermark starts the run").toBe(7);
    expect(resumeSeq([8, 9], [], 7)).toBe(9);
    expect(resumeSeq([], [], 7)).toBe(7);
  });

  it("finds messages recalled while away", () => {
    expect(recalledWhileAway([1, 2, 3, 4], 1, [2, 4], false)).toEqual([3]);
    expect(recalledWhileAway([1, 2, 3, 4], 1, [2], true), "beyond a full page nothing is known").toEqual([]);
    expect(recalledWhileAway([1, 2, 3, 4], 1, [2], false), "a short page is the end of history").toEqual([3, 4]);
    expect(recalledWhileAway([1, 2], 0, [], false), "the newest page proves nothing").toEqual([]);
  });
});

describe("presence", () => {
  /** C5: online, offline and unknown; a record expires back to unknown; one tracker serves every screen. */
  it("C5_PresenceTriStateWithExpiry", async () => {
    let now = 1_000;
    const p = new PresenceTracker(60_000, () => now);
    expect(p.state("uid_x")).toBe("unknown");
    p.update("uid_x", true, 5);
    expect(p.state("uid_x")).toBe("online");
    expect(p.update("uid_x", false, 4), "an older presRev is dropped").toBe(false);
    expect(p.state("uid_x")).toBe("online");
    p.update("uid_x", false, 6, 900);
    expect(p.state("uid_x")).toBe("offline");
    expect(p.record("uid_x")?.lastSeenAt).toBe(900);
    now += 60_000;
    expect(p.state("uid_x"), "expired").toBe("unknown");
    p.dispose();

    // The engine has exactly one tracker; pushes and snapshots both land in it.
    const server = new FakeServer();
    const bob = uidOf("bob");
    server.onFrame = (conn, f) => {
      if (f.action !== "pres") return false;
      conn.push({ action: "pres", code: 200, id: f.id, what: "snapshot", states: [{ uid: bob, online: true, presRev: 3 }] });
      return true;
    };
    const { client, engine } = device(server, "alice");
    await engine.open();
    client.start();
    await client.awaitConnected(5_000);
    await engine.refreshPresence([bob]);
    expect(engine.presence.state(bob)).toBe("online");
    server.live[0]!.push({ action: "pres", topic: bob, what: "off", presRev: 2 });
    server.live[0]!.push({ action: "pres", topic: bob, what: "off", presRev: 4 });
    await until("offline", () => engine.presence.state(bob) === "offline");
    expect(engine.presence.record(bob)?.rev).toBe(4);
  });
});

describe("engine", () => {
  /**
   * C2: a media message whose publish reply was lost is pulled back from
   * history (which has no clientMsgId) and matched to the pending row by
   * fileUrl: not sent again, not shown twice.
   */
  it("C2_MediaReconciledByUrl", async () => {
    const server = new FakeServer();
    const me = uidOf("alice");
    const group = "grp_media0000001";
    const stored: Frame[] = [];
    server.onFrame = (conn, f) => {
      if (f.action === "pub") {
        // Stored, but the reply never arrives.
        stored.push({ topic: group, seq: stored.length + 1, from: me, ts: 5_000, content: f.content, contentType: f.contentType, fileUrl: f.fileUrl, fileName: f.fileName });
        return true;
      }
      if (f.action === "get") {
        conn.push({ action: "get", code: 200, id: f.id, topic: f.topic, messages: stored });
        return true;
      }
      return false;
    };
    const { client, engine } = device(server, "alice", { publishTimeoutMs: 200 });
    await engine.open();
    client.start();
    await client.awaitConnected(5_000);
    const window = engine.messages(group);
    const pending = await engine.send(group, { contentType: "image/png", fileUrl: "https://media.example/a.png", fileName: "a.png" });
    await until("publish timed out", () => server.sent("pub").length === 1);
    await new Promise((r) => setTimeout(r, 400));
    expect(window.value.map((m) => m.status)).toEqual(["sending"]);

    await engine.syncConversation(group);
    expect(window.value).toHaveLength(1);
    expect(window.value[0]).toMatchObject({ seq: 1, status: "sent", localId: pending.localId, fileUrl: "https://media.example/a.png" });
    expect(server.sent("pub"), "not sent again").toHaveLength(1);
  });

  it("files single-chat frames under the peer whatever the spelling (section 5.5)", async () => {
    const server = new FakeServer();
    const me = uidOf("alice");
    const bob = uidOf("bob");
    const { client, engine } = device(server, "alice");
    await engine.open();
    await engine.ensureConversation(bob, "Bob");
    client.start();
    await client.awaitConnected(5_000);
    const conn = server.live[0]!;
    conn.push({ action: "data", topic: bob, from: bob, seq: 1, ts: 1, content: "hi", contentType: "text" });
    // My other device's message: topic is the peer, from is me.
    conn.push({ action: "data", topic: bob, from: me, seq: 2, ts: 2, content: "yo", contentType: "text", clientMsgId: "x" });
    // A recall written with my uid as the topic (D6).
    conn.push({ action: "recalled", topic: me, from: bob, seq: 1 });
    // A read on the canonical topic.
    conn.push({ action: "info", topic: p2pTopic(me, bob), from: bob, what: "read", seq: 2 });
    await until("frames applied", () => engine.conversation(bob)?.peerReadSeq === 2);
    const window = engine.messages(bob);
    await engine.idle(); // the window loads from the store
    // Bob's recalled message stays as a tombstone: his, no content, not unread.
    expect(window.value.map((m) => [m.seq, m.status, m.contentType])).toEqual([
      [1, "received", RECALLED_CONTENT_TYPE],
      [2, "sent", "text"],
    ]);
    expect(window.value[0]).toMatchObject({ from: bob, content: "" });
    expect(engine.conversation(bob)?.unread).toBe(0);
  });

  it("ignores a clear that is not newer and acknowledges pending ones", async () => {
    const server = new FakeServer();
    const bob = uidOf("bob");
    const { client, engine, store } = device(server, "alice");
    await engine.open();
    client.start();
    await client.awaitConnected(5_000);
    const conn = server.live[0]!;
    for (const seq of [1, 2, 3]) conn.push({ action: "data", topic: bob, from: bob, seq, ts: seq, content: "m" + seq, contentType: "text" });
    conn.push({ action: "topic_cleared", topic: bob, from: bob, clearVersion: 2, clearBeforeSeq: 2, id: "7" });
    conn.push({ action: "topic_cleared", topic: bob, from: bob, clearVersion: 1, clearBeforeSeq: 3 });
    await until("ack", () => server.sent("ack_topic_cleared").length === 1);
    await engine.idle();
    expect(server.sent("ack_topic_cleared")[0]!.id).toBe("7");
    expect(await store.seqs(bob)).toEqual([3]);
    expect(await store.clearState(bob)).toEqual({ clearVersion: 2, clearBeforeSeq: 2 });
  });

  it("marks groups missing from get_groups as left (section 8 step 2)", async () => {
    const server = new FakeServer();
    server.onFrame = (conn, f) => {
      if (f.action !== "get_groups") return false;
      conn.push({ action: "get_groups", code: 200, id: f.id, groups: [{ topic: "grp_keep00000001", name: "keep", owner: "uid_x", members: 2 }] });
      return true;
    };
    const { client, engine } = device(server, "alice");
    await engine.open();
    await engine.ensureConversation("grp_gone00000001");
    const removed: string[] = [];
    engine.events.on((e) => {
      if (e.kind === "removedFromGroup") removed.push(e.conversation);
    });
    client.start();
    await client.awaitConnected(5_000);
    await engine.refreshGroups();
    expect(removed).toEqual(["grp_gone00000001"]);
    expect(engine.conversation("grp_gone00000001")?.left).toBe(true);
    expect(engine.conversation("grp_keep00000001")?.title).toBe("keep");
  });

  it("shows the group assistant typing until its reply (section 15.2)", async () => {
    const server = new FakeServer();
    const carol = uidOf("carol");
    const group = "grp_askai0000001";
    const now = 1_000_000;
    const { client, engine } = device(server, "alice", { clock: () => now });
    await engine.open();
    await engine.ensureConversation(group, "ask");
    client.start();
    await client.awaitConnected(5_000);
    const conn = server.live[0]!;
    conn.push({ action: "info", topic: group, from: carol, what: "kp" });
    // A group this device does not know is not invented by a typing note.
    conn.push({ type: "ai_typing", topic: "grp_unknown00001" });
    conn.push({ type: "ai_typing", topic: group });
    await until("the assistant types", () => engine.typingIn(group).includes(AI_ASSISTANT_UID));
    expect(engine.typingIn(group).sort()).toEqual([AI_ASSISTANT_UID, carol].sort());
    expect(engine.conversation("grp_unknown00001")).toBeUndefined();
    // It outlasts the server's 10-second wait for the host, then ends on its own.
    expect(engine.typingIn(group, now + 14_000)).toContain(AI_ASSISTANT_UID);
    expect(engine.typingIn(group, now + 15_001)).not.toContain(AI_ASSISTANT_UID);

    // Its reply ends it; Carol is still typing.
    conn.push({ action: "data", topic: group, from: AI_ASSISTANT_UID, senderKind: "ai_assistant", seq: 1, ts: 1, content: "42", contentType: "text" });
    await until("the reply ends it", () => !engine.typingIn(group).includes(AI_ASSISTANT_UID));
    expect(engine.typingIn(group)).toEqual([carol]);
    // So does a message from anyone else who was typing.
    conn.push({ action: "data", topic: group, from: carol, seq: 2, ts: 2, content: "thanks", contentType: "text" });
    await until("Carol's message ends hers", () => engine.typingIn(group).length === 0);
    expect(engine.typing.value.has(group)).toBe(false);
  });

  it("lists a group it created under its name at once", async () => {
    const server = new FakeServer();
    const topic = "grp_trip00000001";
    let groupsAsked = 0;
    server.onFrame = (conn, f) => {
      if (f.action === "create_grp") {
        conn.push({ action: "create_grp", code: 200, id: f.id, topic });
        return true;
      }
      if (f.action === "get_groups") {
        groupsAsked++;
        conn.push({ action: "get_groups", code: 200, id: f.id, groups: [{ topic, name: "Trip", owner: uidOf("alice"), members: 2 }] });
        return true;
      }
      return false;
    };
    const { client, engine } = device(server, "alice");
    await engine.open();
    client.start();
    await client.awaitConnected(5_000);
    expect(await engine.createGroup("Trip", [uidOf("bob")])).toBe(topic);
    expect(engine.conversation(topic)?.title, "named before any refresh").toBe("Trip");
    expect(engine.conversations.value.some((c) => c.id === topic && c.isGroup)).toBe(true);
    expect(server.sent("create_grp")[0]).toMatchObject({ name: "Trip", members: [uidOf("bob")] });
    await until("groups refreshed", () => groupsAsked === 1);
    expect(engine.conversation(topic)?.left).toBe(false);
  });

  it("stops for good when the same device logs in elsewhere (section 6.2)", async () => {
    const server = new FakeServer();
    const { client } = device(server, "alice");
    client.start();
    await client.awaitConnected(5_000);
    server.live[0]!.push({ action: "login", code: 401, reason: "logged in from another device" });
    await until("stopped", () => client.state.value.kind === "stopped");
    expect(client.state.value).toEqual({ kind: "stopped", reason: "replaced" });
    await new Promise((r) => setTimeout(r, 200));
    expect(server.conns).toHaveLength(1);
  });
});
