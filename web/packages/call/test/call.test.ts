import { ImClient } from "@imkit/client";
import { CallManager, type CallMediaEngine, type CallState, type CallTraceEntry, type MediaJoin, type TabChannel } from "@imkit/call";
import { uidOf } from "@imkit/protocol";
import { afterEach, describe, expect, it } from "vitest";
import { FakeServer, type Frame } from "../../client/test/fake-server.js";
import { until } from "../../client/test/until.js";

const stops: (() => void)[] = [];
afterEach(() => {
  for (const s of stops.splice(0)) s();
});

class FakeMedia implements CallMediaEngine {
  readonly log: string[] = [];
  joins: MediaJoin[] = [];
  failJoin = false;
  async join(p: MediaJoin) {
    this.joins.push(p);
    this.log.push("join");
    if (this.failJoin) throw new Error("no camera");
  }
  async setVideo(on: boolean, q?: string) {
    this.log.push(`video ${on} ${q ?? ""}`.trim());
  }
  setMuted(m: boolean) {
    this.log.push(`muted ${m}`);
  }
  async leave() {
    this.log.push("leave");
  }
}

/** BroadcastChannel semantics: a post reaches every other member, never the sender. */
function tabChannels(n: number): TabChannel[] {
  const members: TabChannel[] = [];
  for (let i = 0; i < n; i++) {
    const me: TabChannel = {
      onmessage: null,
      postMessage: (data) => {
        for (const m of members) if (m !== me) setTimeout(() => m.onmessage?.({ data }), 0);
      },
    };
    members.push(me);
  }
  return members;
}

async function setup(server: FakeServer, opts: { user?: string; channel?: TabChannel | null; clock?: () => number; media?: FakeMedia } = {}) {
  const user = opts.user ?? "alice";
  const client = new ImClient({ endpoints: ["ws://fake"], tokenProvider: () => "tok:" + user, transport: server, requestTimeoutMs: 2_000, watchNetwork: false });
  const media = opts.media ?? new FakeMedia();
  const manager = new CallManager(client, uidOf(user), { media, channel: opts.channel ?? null, endedHoldMs: 50, ...(opts.clock ? { clock: opts.clock } : {}) });
  stops.push(() => {
    manager.close();
    client.stop();
  });
  client.start();
  await client.awaitConnected(5_000);
  return { client, media, manager };
}

const strip = (f: Frame) => {
  const { id: _id, ...rest } = f;
  return rest;
};
const phase = (m: CallManager) => m.state.value.phase;
const bob = uidOf("bob");
const creds = (who: string) => ({ channelId: "ch-1", rtcToken: `rtc-${who}`, appId: "app", videoQuality: "sd", denoiseEnabled: true });

describe("calls", () => {
  it("caller: invite, answered with my own credentials, hang up", async () => {
    const server = new FakeServer();
    server.onFrame = (conn, f) => {
      if (f.action === "call_invite") {
        conn.push({ action: f.action, code: 200, id: f.id, topic: f.topic, callSessionId: "cs-1" });
        setTimeout(() => conn.push({ type: "call_answered", callSessionId: "cs-1", ...creds("caller") }), 20);
        return true;
      }
      if (f.action === "call_hangup") {
        conn.push({ action: f.action, code: 200, id: f.id, callSessionId: f.callSessionId });
        return true;
      }
      return false;
    };
    const { manager, media } = await setup(server);
    const seen: CallState["phase"][] = [];
    manager.state.subscribe((s) => seen.push(s.phase));
    expect(await manager.invite(bob, "voice")).toEqual({ ok: true, callSessionId: "cs-1" });
    await until("active", () => phase(manager) === "active" && !(manager.state.value as { connecting: boolean }).connecting);
    expect(media.joins).toEqual([{ uid: uidOf("alice"), callType: "voice", credentials: creds("caller") }]);
    expect(manager.state.value).toMatchObject({ phase: "active", callSessionId: "cs-1", peer: bob, callType: "voice", muted: false });
    manager.setMuted(true);
    expect(manager.state.value).toMatchObject({ muted: true });
    await manager.hangup();
    expect(manager.state.value).toMatchObject({ phase: "ended", reason: "user_hangup" });
    await until("idle again", () => phase(manager) === "idle");
    expect(seen).toEqual(["outgoing", "outgoing", "active", "active", "active", "ended", "idle"]);
    expect(media.log).toEqual(["join", "muted true", "leave"]);
    expect(server.sent("call_invite").map(strip)).toEqual([{ action: "call_invite", topic: bob, callType: "voice" }]);
    expect(server.sent("call_hangup").map(strip)).toEqual([{ action: "call_hangup", callSessionId: "cs-1" }]);
  });

  it("callee: rings, answers as voice, the other side ends it with an unknown reason", async () => {
    const server = new FakeServer();
    server.onFrame = (conn, f) => {
      if (f.action !== "call_accept") return false;
      conn.push({ action: f.action, code: 200, id: f.id, callSessionId: f.callSessionId, ...creds("callee") });
      return true;
    };
    const { manager, media } = await setup(server, { user: "bob" });
    const conn = server.live[0]!;
    conn.push({ type: "call_ring", callSessionId: "cs-2", callType: "video", callerName: "Alice", ringTimeoutSeconds: 45 });
    await until("ringing", () => phase(manager) === "incoming");
    expect(manager.state.value).toMatchObject({ callType: "video", callerName: "Alice", ringTimeoutSeconds: 45 });
    expect(await manager.accept(true)).toBe(true);
    expect(server.sent("call_accept").map(strip)).toEqual([{ action: "call_accept", callSessionId: "cs-2", acceptAsVoice: true }]);
    expect(media.joins[0]).toEqual({ uid: bob, callType: "voice", credentials: creds("callee") });
    expect(manager.state.value).toMatchObject({ phase: "active", callType: "voice", callerName: "Alice", connecting: false });
    // A call for someone else's session changes nothing.
    conn.push({ type: "call_ended", callSessionId: "cs-other", reason: "user_hangup" });
    conn.push({ type: "call_ended", callSessionId: "cs-2", reason: "host_said_so" });
    await until("ended", () => phase(manager) === "ended");
    expect(manager.state.value).toMatchObject({ reason: "host_said_so" });
    expect(media.log.at(-1)).toBe("leave");
  });

  it("keeps pushes that beat the invite's reply (F49)", async () => {
    const server = new FakeServer();
    server.onFrame = (conn, f) => {
      if (f.action !== "call_invite") return false;
      // The callee answered before this client heard its own call's id.
      conn.push({ type: "call_answered", callSessionId: "cs-3", ...creds("caller") });
      setTimeout(() => conn.push({ action: f.action, code: 200, id: f.id, topic: f.topic, callSessionId: "cs-3" }), 20);
      return true;
    };
    const { manager, media } = await setup(server);
    expect((await manager.invite(bob, "voice")).ok).toBe(true);
    await until("active", () => phase(manager) === "active");
    expect(media.joins).toHaveLength(1);
  });

  it("cancels a call that was cancelled before its id came back", async () => {
    const server = new FakeServer();
    let reply: (() => void) | null = null;
    server.onFrame = (conn, f) => {
      if (f.action === "call_invite") {
        reply = () => conn.push({ action: f.action, code: 200, id: f.id, topic: f.topic, callSessionId: "cs-4" });
        return true;
      }
      if (f.action === "call_cancel") {
        conn.push({ action: f.action, code: 200, id: f.id, callSessionId: f.callSessionId });
        return true;
      }
      return false;
    };
    const { manager } = await setup(server);
    const invited = manager.invite(bob, "video");
    await until("sent", () => reply !== null);
    await manager.cancel();
    expect(manager.state.value).toMatchObject({ phase: "ended", reason: "cancelled" });
    reply!();
    await invited;
    await until("cancel sent", () => server.sent("call_cancel").length === 1);
    expect(server.sent("call_cancel").map(strip)).toEqual([{ action: "call_cancel", callSessionId: "cs-4" }]);
  });

  it("reports refusals and stays idle", async () => {
    const server = new FakeServer();
    server.onFrame = (conn, f) => {
      if (f.action !== "call_invite") return false;
      const reason = f.topic === bob ? "offline" : "not_friends";
      conn.push({ action: f.action, code: f.topic === bob ? 409 : 401, id: f.id, reason });
      return true;
    };
    const { manager } = await setup(server);
    expect(await manager.invite(bob, "voice")).toEqual({ ok: false, code: 409, reason: "offline" });
    expect(phase(manager)).toBe("idle");
    expect(await manager.invite(uidOf("carol"), "voice")).toMatchObject({ ok: false, code: 401, reason: "not_friends" });
  });

  it("switches to video both ways, and ends a call whose media will not start", async () => {
    const server = new FakeServer();
    server.onFrame = (conn, f) => {
      const ok = () => conn.push({ action: f.action, code: 200, id: f.id, callSessionId: f.callSessionId });
      switch (f.action) {
        case "call_accept":
          conn.push({ action: f.action, code: 200, id: f.id, callSessionId: f.callSessionId, ...creds("callee") });
          return true;
        case "call_upgrade":
          ok();
          setTimeout(() => conn.push({ type: "call_upgrade_declined", callSessionId: f.callSessionId }), 10);
          return true;
        case "call_upgrade_accept":
          ok();
          conn.push({ type: "call_upgraded", callSessionId: f.callSessionId, videoQuality: "hd" });
          return true;
        case "call_hangup":
          ok();
          return true;
      }
      return false;
    };
    const { manager, media } = await setup(server, { user: "bob" });
    const conn = server.live[0]!;
    conn.push({ type: "call_ring", callSessionId: "cs-5", callType: "voice", ringTimeoutSeconds: 45 });
    await until("ringing", () => phase(manager) === "incoming");
    await manager.accept();
    expect(await manager.requestVideo()).toBe(true);
    await until("declined", () => (manager.state.value as { upgrade?: string }).upgrade === "declined");
    conn.push({ type: "call_upgrade_request", callSessionId: "cs-5", toType: "video" });
    await until("asked", () => (manager.state.value as { upgrade?: string }).upgrade === "theirs");
    await manager.acceptVideo();
    await until("video", () => (manager.state.value as { callType: string }).callType === "video");
    expect(media.log).toContain("video true hd");
    expect(server.sent("call_upgrade").map(strip)).toEqual([{ action: "call_upgrade", callSessionId: "cs-5", toType: "video" }]);

    // Media that will not start: the call ends for both sides.
    await manager.hangup();
    const broken = new FakeMedia();
    broken.failJoin = true;
    const second = await setup(server, { user: "bob", media: broken });
    server.live.at(-1)!.push({ type: "call_ring", callSessionId: "cs-6", callType: "voice", ringTimeoutSeconds: 45 });
    await until("ringing", () => phase(second.manager) === "incoming");
    await second.manager.accept();
    expect(second.manager.state.value).toMatchObject({ phase: "ended", reason: "failed" });
    expect(server.sent("call_hangup").map((f) => f.callSessionId)).toContain("cs-6");
  });

  // Found in a browser: a manager React built and dropped kept hearing every ring.
  it("built with autoStart false, hears rings only between start and dispose; the trace says what it did", async () => {
    const server = new FakeServer();
    const client = new ImClient({ endpoints: ["ws://fake"], tokenProvider: () => "tok:bob", transport: server, requestTimeoutMs: 2_000, watchNetwork: false });
    const log: CallTraceEntry[] = [];
    const manager = new CallManager(client, bob, { media: new FakeMedia(), channel: null, autoStart: false, log: (e) => log.push(e) });
    stops.push(() => {
      manager.close();
      client.stop();
    });
    client.start();
    await client.awaitConnected(5_000);
    const ring = (id: string) => server.live[0]!.push({ type: "call_ring", callSessionId: id, callType: "voice", ringTimeoutSeconds: 45 });

    ring("cs-a");
    await new Promise((r) => setTimeout(r, 30));
    expect(phase(manager)).toBe("idle");
    expect(manager.trace().filter((e) => e.what === "signal"), "not listening yet").toEqual([]);

    manager.start();
    ring("cs-b");
    await until("rings", () => phase(manager) === "incoming");
    ring("cs-c");
    await until("dropped", () => manager.trace().some((e) => e.what === "drop" && e.callSessionId === "cs-c"));
    manager.dispose();
    ring("cs-d");
    await new Promise((r) => setTimeout(r, 30));

    const lines = manager.trace().map((e) => [e.what, e.callSessionId, e.note].filter(Boolean).join(" "));
    expect(lines).toEqual(["start", "state idle", "signal cs-b ring", "state cs-b incoming", "signal cs-c ring", "drop cs-c busy here (incoming)", "dispose"]);
    expect(log).toEqual(manager.trace());
  });

  it("lets one tab take a ringing call, and keeps the others out of calls meanwhile", async () => {
    const server = new FakeServer();
    server.onFrame = (conn, f) => {
      if (f.action !== "call_accept") return false;
      conn.push({ action: f.action, code: 200, id: f.id, callSessionId: f.callSessionId, ...creds("callee") });
      return true;
    };
    let now = 1_000_000;
    const [c1, c2] = tabChannels(2);
    // Two tabs of one account: one connection would be shared; two fake ones are enough here.
    const one = await setup(server, { user: "bob", channel: c1!, clock: () => now });
    const two = await setup(server, { user: "bob", channel: c2!, clock: () => now });
    for (const conn of server.live) conn.push({ type: "call_ring", callSessionId: "cs-7", callType: "voice", ringTimeoutSeconds: 45 });
    await until("both ring", () => phase(one.manager) === "incoming" && phase(two.manager) === "incoming");
    await one.manager.accept();
    await until("the other stops ringing", () => phase(two.manager) === "idle");
    // While tab one is in the call, tab two neither rings nor calls.
    await new Promise((r) => setTimeout(r, 20)); // the "busy" post arrives
    expect(await two.manager.invite(uidOf("carol"), "voice")).toEqual({ ok: false, code: 409, reason: "busy_here" });
    expect(server.sent("call_invite"), "nothing sent").toHaveLength(0);
    server.live[1]!.push({ type: "call_ring", callSessionId: "cs-8", callType: "voice", ringTimeoutSeconds: 45 });
    await new Promise((r) => setTimeout(r, 30));
    expect(phase(two.manager)).toBe("idle");
    const dropped = two.manager.trace().find((e) => e.what === "drop" && e.callSessionId === "cs-8");
    expect(dropped?.note, "the trace says why it did not ring").toBe("busy in another tab (cs-7)");
    // A tab that went quiet (closed mid-call) stops counting after 90 seconds.
    now += 91_000;
    server.live[1]!.push({ type: "call_ring", callSessionId: "cs-9", callType: "voice", ringTimeoutSeconds: 45 });
    await until("rings again", () => phase(two.manager) === "incoming");
  });
});
