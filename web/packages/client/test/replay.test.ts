/**
 * The web client against the golden transcripts: `replay` plays the server
 * and fails unless the client sends exactly the scripted requests. The same
 * cases as android/talkwire-core's ReplayScenarioTest, one per client red line
 * a fake server could get wrong in the same way as the client.
 *
 * The transcripts come from the legacy-profile reference server: the login
 * reply has no `protocol`, and presence snapshots have no `id`.
 */

import { p2pTopic, uidOf } from "@talkwire/protocol";
import { afterEach, describe, expect, it } from "vitest";
import { ChatEngine, ImClient, MemoryChatStore, type ChatEvent, type ImEvent, type ImTransport } from "../src/index.js";
import { RecordingTransport, bindings, replayBuilt, replaying, until, type Replay } from "./replay.js";

const clients: ImClient[] = [];
const engines: ChatEngine[] = [];

afterEach(() => {
  for (const c of clients.splice(0)) c.stop();
  for (const e of engines.splice(0)) e.close();
});

function client(replay: Replay, user: string, opts: { transport?: ImTransport; requestTimeoutMs?: number; publishTimeoutMs?: number; reconnectDelayMs?: number } = {}): ImClient {
  const delay = opts.reconnectDelayMs ?? 100;
  const c = new ImClient({
    endpoints: [replay.url],
    tokenProvider: () => `token-of-${user}`, // the replay does not check tokens
    transport: opts.transport ?? new RecordingTransport(),
    requestTimeoutMs: opts.requestTimeoutMs ?? 5_000,
    publishTimeoutMs: opts.publishTimeoutMs ?? 20_000,
    reconnectBaseDelayMs: delay,
    reconnectMaxDelayMs: delay * 2,
    watchNetwork: false,
  });
  clients.push(c);
  return c;
}

function engine(c: ImClient, user: string, pageSize = 50, store = new MemoryChatStore()): ChatEngine {
  // No automatic sync: the transcript scripts every request.
  const e = new ChatEngine(c, store, { appUserId: `conf-${user}`, autoSync: false, textContentType: "text/plain", pageSize });
  engines.push(e);
  return e;
}

const seqs = (e: ChatEngine, conv: string) => e.messages(conv).value.flatMap((m) => (m.seq === null ? [] : [m.seq]));

describe.skipIf(!replayBuilt)("replayed transcripts", () => {
  it("bindings use the harness user ids", () => {
    const b = bindings("core/p2p");
    expect(b["uid:A"]).toBe(uidOf("conf-A"));
    expect(b["p2p:A,B"]).toBe(p2pTopic(uidOf("conf-A"), uidOf("conf-B")));
  });

  /**
   * R19: the legacy server answers the handshake login and the presence
   * snapshot without an id and the client takes them by action; every other
   * reply goes by id, so the refused second login (step 3, with an id)
   * reaches the request that sent it.
   */
  it("R19_CorrelateLegacyRepliesByAction", async () => {
    const uidA = bindings("core/login")["uid:A"];
    await replaying("core/login", "A", undefined, async (replay) => {
      // Superseded at step 5 by a plain close; it must not come back within the test.
      const first = client(replay, "A", { reconnectDelayMs: 60_000 });
      first.start();
      const session = await first.awaitConnected(10_000); // step 2: no id
      expect(session?.uid).toBe(uidA);
      expect(session?.protocol).toBe(0);

      const again = await first.request({ action: "login", token: "token-of-A" }); // step 3
      expect(again.code).toBe(400);
      expect(first.session?.uid, "still logged in").toBe(uidA);

      const second = client(replay, "A");
      second.start();
      expect((await second.awaitConnected(10_000))?.uid).toBe(uidA); // steps 4-5
      await until("the first connection closed", () => first.state.value.kind !== "connected");

      expect(await second.queryPresence([]), "the snapshot lists nobody").toEqual([]); // step 6: no id
    });
  });

  /**
   * R7: B is offline while A clears the chat. On B's next login the pending
   * clear comes after the login reply; B empties the conversation and acks the
   * clear by its id. The next login brings nothing.
   */
  it("R7_ClearOfflineReplay", async () => {
    const withA = bindings("core/clear-offline")["uid:A"]!;
    await replaying("core/clear-offline", "B", undefined, async (replay) => {
      const transport = new RecordingTransport();
      const c = client(replay, "B", { transport });
      const store = new MemoryChatStore();
      const e = engine(c, "B", 50, store);
      const cleared: ChatEvent[] = [];
      e.events.on((ev) => {
        if (ev.kind === "conversationCleared") cleared.push(ev);
      });
      await e.open();
      const window = e.messages(withA);
      c.start();
      expect(await c.awaitConnected(10_000)).not.toBeNull();
      await until("the message", () => window.value.map((m) => m.content).join() === "secret"); // step 6

      c.stop(); // step 7: B goes offline; A clears (steps 8-9)
      c.start(); // steps 10-11
      await until("the pending clear", () => cleared.length > 0);
      expect(window.value).toEqual([]);
      expect(await store.clearState(withA)).toEqual({ clearVersion: 1, clearBeforeSeq: 1 });
      await until("the ack", () => transport.sentActions("ack_topic_cleared").length > 0); // step 12
      expect(transport.sentActions("ack_topic_cleared")[0]!.id).toBe("1");

      c.stop(); // step 13
      c.start(); // steps 14-15
      expect(await c.awaitConnected(10_000)).not.toBeNull();
      await new Promise((r) => setTimeout(r, 500));
      expect(cleared.length, "not delivered again").toBe(1);
      expect(transport.sentActions("ack_topic_cleared").length).toBe(1);
    });
  });

  /** C1: an injected disconnect is noticed, the client logs in again by itself and carries on. C5: presence follows pushes. */
  it("C1_ReconnectAfterInjectedDisconnect", async () => {
    const b = bindings("core/p2p");
    const uidA = b["uid:A"]!;
    const uidB = b["uid:B"]!;
    await replaying("core/p2p", "A", '[{"kind":"disconnect","step":9}]', async (replay) => {
      const c = client(replay, "A");
      const e = engine(c, "A");
      const log: ImEvent[] = [];
      c.events.on((ev) => log.push(ev));
      await e.open();
      c.start();
      const first = await c.awaitConnected(10_000);
      expect(first?.uid).toBe(uidA);
      expect(first?.protocol, "legacy login reply carries no protocol").toBe(0);

      expect((await c.subscribe(uidB)).code).toBe(200); // step 7
      await until("B online", () => e.presence.state(uidB) === "online");

      // Before step 9 the replay closes the socket and waits for a new login.
      await until("second login", () => log.filter((ev) => ev.kind === "connected").length === 2);
      expect(log.some((ev) => ev.kind === "disconnected" && ev.willReconnect)).toBe(true);

      const stored = await c.publish(uidB, { content: "hey", contentType: "text/plain" }, "m-1"); // step 9
      expect(stored).toMatchObject({ kind: "stored", seq: 1 });

      // Step 11: the legacy snapshot has no id and is matched by action.
      expect((await c.queryPresence([])).find((s) => s.uid === uidB)?.online).toBe(true);

      await until("B offline", () => e.presence.state(uidB) === "offline"); // step 14
      const [last] = await c.queryPresence([uidB]); // step 15
      expect(last?.online).toBe(false);
      expect(last!.lastSeenAt).toBeGreaterThan(0);
    });
  });

  /** C1: every frame duplicated, replies put last: duplicates of replies, logins and pushes are ignored. */
  it("C1_IgnoresDuplicatedFrames", async () => {
    const uidB = bindings("core/p2p")["uid:B"]!;
    await replaying(
      "core/p2p",
      "A",
      '[{"kind":"duplicate","step":0}]',
      async (replay) => {
        const c = client(replay, "A");
        const e = engine(c, "A");
        await e.open();
        c.start();
        expect(await c.awaitConnected(10_000)).not.toBeNull();
        expect((await c.subscribe(uidB)).code).toBe(200);
        expect(await c.publish(uidB, { content: "hey", contentType: "text/plain" }, "m-1")).toMatchObject({ kind: "stored", seq: 1 });
        expect((await c.queryPresence([])).find((s) => s.uid === uidB)?.online).toBe(true);
        await until("B offline", () => e.presence.state(uidB) === "offline");
        expect((await c.queryPresence([uidB]))[0]?.online).toBe(false);
        expect(c.state.value.kind, "still the first connection").toBe("connected");
      },
      "-order",
      "last",
    );
  });

  /**
   * C2: the reply to a publish is lost while the connection stays up. The
   * message stays "sending" and is not sent again on its own; a retry uses
   * the same clientMsgId (the replay checks the mapping) and the server's
   * de-duplication answers with the same seq.
   */
  it("C2_LostPublishReplyStaysSending", async () => {
    const b = bindings("core/group-messaging");
    const uidB = b["uid:B"]!;
    await replaying("core/group-messaging", "A", '[{"kind":"drop","step":8}]', async (replay) => {
      const transport = new RecordingTransport();
      const c = client(replay, "A", { transport, publishTimeoutMs: 1_000 });
      const e = engine(c, "A");
      await e.open();
      c.start();
      expect(await c.awaitConnected(10_000)).not.toBeNull();

      const topic = await c.createGroup("team", [uidB]); // step 5
      expect(topic).toBe(b.grp);
      const window = e.messages(topic); // open it before anything is stored
      expect((await e.subscribe(topic)).code).toBe(200); // step 6

      await e.send(topic, { content: "hello" }); // step 8; the reply is dropped
      await new Promise((r) => setTimeout(r, 2_500)); // the publish timeout passes, then some more
      expect(transport.sentActions("pub").length, "no resend on a live connection").toBe(1);
      const pending = window.value[0]!;
      expect(window.value.length).toBe(1);
      expect(pending.status).toBe("sending");
      expect(pending.seq).toBeNull();

      await e.retry(pending); // step 9
      await until("stored as seq 1", () => window.value.some((m) => m.seq === 1 && m.status === "sent"));
      const ids = transport.sentActions("pub").map((f) => f.clientMsgId);
      expect(ids.length).toBe(2);
      expect(ids[0]).toBe(pending.localId);
      expect(ids[1]).toBe(ids[0]);

      await until("B's message", () => seqs(e, topic).join() === "1,2"); // step 10
      expect(e.sendTyping(topic)).toBe(true); // step 12
      expect((await c.history(topic, { limit: 20 })).messages.map((m) => m.seq)).toEqual([1, 2]); // step 13
      expect((await e.refreshGroups())?.[0]?.name).toBe("team"); // step 14
      expect(await e.myRole(topic)).toBe("owner"); // step 15
      expect(window.value.length, "stored once").toBe(2);
    });
  });

  /**
   * C3: a message seen live after a gap must not move the resume point: with
   * 1 and 3 held, the pull asks for everything after 1 (the scripted
   * request), then fills in 2.
   */
  it("C3_ResumeFromContiguousEnd", async () => {
    const b = bindings("core/resubscribe");
    const uidA = b["uid:A"]!;
    const group = b.grp!;
    await replaying("core/resubscribe", "B", undefined, async (replay) => {
      const c = client(replay, "B");
      const e = engine(c, "B", 10);
      await e.open();
      c.start();
      expect(await c.awaitConnected(10_000)).not.toBeNull();
      await until("g1", () => seqs(e, group).join() === "1"); // steps 5-6

      c.stop(); // step 7
      c.start(); // steps 8-9: a new connection
      expect(await c.awaitConnected(10_000)).not.toBeNull();
      await until("p1", () => e.messages(uidA).value.map((m) => m.content).join() === "p1"); // step 11

      expect((await e.subscribe(group)).code).toBe(200); // step 12
      await until("g3 live", () => seqs(e, group).join() === "1,3"); // step 13: g2 was missed
      await e.syncConversation(group); // step 14: afterSeq 1, not 3
      expect(seqs(e, group)).toEqual([1, 2, 3]);
    });
  });

  /**
   * C9: my role comes from the roster; grp_sync marks it stale, so the next
   * look fetches it again.
   */
  it("C9_RoleFromRoster", async () => {
    const b = bindings("core/group-guards");
    const [uidA, uidB, uidC, uidD] = ["A", "B", "C", "D"].map((u) => b[`uid:${u}`]!);
    await replaying("core/group-guards", "A", undefined, async (replay) => {
      const c = client(replay, "A");
      const e = engine(c, "A");
      await e.open();
      c.start();
      expect(await c.awaitConnected(10_000)).not.toBeNull();

      const topic = await c.createGroup("guards", [uidB!, uidC!, uidD!]); // step 9
      expect((await c.setMemberRole(topic, uidB!, "admin")).code).toBe(200); // step 10
      expect((await c.setMemberRole(topic, uidC!, "admin")).code).toBe(200); // step 11
      expect((await c.kick(topic, uidA!)).code).toBe(400); // step 12: not even the owner kicks himself
      await until("two grp_sync", () => e.rosterEpoch(topic) === 2);

      expect(await e.myRole(topic)).toBe("owner"); // step 18: first fetch
      const roster = await e.members(topic); // cached: no request
      expect(Object.fromEntries(roster!.map((m) => [m.uid, m.role]))).toEqual({ [uidA!]: "owner", [uidB!]: "admin", [uidC!]: "admin", [uidD!]: "member" });

      expect((await c.kick(topic, uidC!)).code).toBe(200); // step 19, with a grp_sync
      await until("grp_sync after the kick", () => e.rosterEpoch(topic) === 3);
      expect(e.rosterStale(topic)).toBe(true);
      expect(await e.myRole(topic)).toBe("owner"); // step 20: fetched again
      expect(new Set((await e.members(topic))!.map((m) => m.uid))).toEqual(new Set([uidA, uidB, uidD]));
    });
  });

  /** C9: when the roster cannot be fetched I am treated as a plain member; the next fetch restores the role. */
  it("C9_UnknownRosterMeansMember", async () => {
    const b = bindings("core/group-guards");
    const [uidA, uidB, uidC, uidD] = ["A", "B", "C", "D"].map((u) => b[`uid:${u}`]!);
    await replaying("core/group-guards", "A", '[{"kind":"drop","step":18}]', async (replay) => {
      const c = client(replay, "A", { requestTimeoutMs: 1_000 });
      const e = engine(c, "A");
      await e.open();
      c.start();
      expect(await c.awaitConnected(10_000)).not.toBeNull();

      const topic = await c.createGroup("guards", [uidB!, uidC!, uidD!]);
      await c.setMemberRole(topic, uidB!, "admin");
      await c.setMemberRole(topic, uidC!, "admin");
      await c.kick(topic, uidA!);
      await until("two grp_sync", () => e.rosterEpoch(topic) === 2);

      expect(await e.myRole(topic), "no roster: fail closed").toBe("member"); // step 18, reply dropped
      expect(e.rosterStale(topic)).toBeNull();
      expect((await c.kick(topic, uidC!)).code).toBe(200); // step 19
      expect(await e.myRole(topic)).toBe("owner"); // step 20
    });
  });
});
