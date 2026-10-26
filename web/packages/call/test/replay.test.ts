/**
 * The call manager against the golden transcript `module/call`: the replay
 * plays the server and fails unless the client sends exactly the scripted
 * requests. A is the caller, B the callee; the step numbers are the
 * transcript's. Requests the manager would never send (a second answer, a
 * hang-up after the end) go through its signaling directly.
 */

import { ImClient } from "@talkwire/client";
import { CallManager, NoMediaEngine, type CallState } from "@talkwire/call";
import { afterEach, describe, expect, it } from "vitest";
import { RecordingTransport, bindings, replayBuilt, replaying, until, type Replay } from "../../client/test/replay.js";

const stops: (() => void)[] = [];
afterEach(() => {
  for (const s of stops.splice(0)) s();
});

async function connect(replay: Replay, user: string, me: string): Promise<{ manager: CallManager; media: NoMediaEngine }> {
  const client = new ImClient({
    endpoints: [replay.url],
    tokenProvider: () => `token-of-${user}`,
    transport: new RecordingTransport(),
    requestTimeoutMs: 5_000,
    watchNetwork: false,
  });
  const media = new NoMediaEngine();
  const manager = new CallManager(client, me, { media, channel: null, endedHoldMs: 20 });
  stops.push(() => {
    manager.close();
    client.stop();
  });
  client.start();
  expect(await client.awaitConnected(10_000)).not.toBeNull();
  return { manager, media };
}

const is = (m: CallManager, phase: CallState["phase"], extra: (s: CallState) => boolean = () => true) => () => m.state.value.phase === phase && extra(m.state.value);

describe.skipIf(!replayBuilt)("calls, replayed", () => {
  it("the caller (module/call as A)", async () => {
    const b = bindings("module/call");
    const [cs1, cs2, cs3] = [b["cs1"]!, b["cs2"]!, b["cs3"]!];
    await replaying("module/call", "A", undefined, async (replay) => {
      const { manager, media } = await connect(replay, "A", b["uid:A"]!);
      expect(await manager.invite(b["uid:D"]!, "voice")).toMatchObject({ ok: false, code: 409, reason: "offline" }); // 7
      expect(await manager.invite(b["uid:C"]!, "voice")).toMatchObject({ ok: false, code: 401, reason: "not_friends" }); // 8
      expect(await manager.invite(b["uid:A"]!, "voice")).toMatchObject({ ok: false, code: 400 }); // 9
      expect((await manager.signaling.invite(b["uid:B"]!, "fax")).code).toBe(400); // 10
      expect(await manager.invite(b["uid:B"]!, "voice", { clientCallId: "cc-1" })).toEqual({ ok: true, callSessionId: cs1 }); // 11

      await until("answered", is(manager, "active", (s) => s.phase === "active" && !s.connecting)); // 13
      expect(media.log).toEqual(["join voice fake-channel"]);
      expect(manager.state.value).toMatchObject({ callSessionId: cs1, peer: b["uid:B"] });

      expect(await manager.requestVideo()).toBe(true); // 15
      await until("declined", is(manager, "active", (s) => s.phase === "active" && s.upgrade === "declined")); // 16
      expect(await manager.requestVideo()).toBe(true); // 17
      await until("video", is(manager, "active", (s) => s.phase === "active" && s.callType === "video")); // 18
      expect(media.log).toContain("video true");
      expect((await manager.signaling.upgrade(cs1)).code).toBe(409); // 19: already video
      await manager.hangup(); // 20
      expect(manager.state.value).toMatchObject({ phase: "ended", reason: "user_hangup" });

      expect(await manager.invite(b["uid:B"]!, "video")).toEqual({ ok: true, callSessionId: cs2 }); // 22
      await manager.cancel(); // 23
      expect(await manager.invite(b["uid:B"]!, "video")).toEqual({ ok: true, callSessionId: cs3 }); // 24
      await until("rejected", is(manager, "ended", (s) => s.phase === "ended" && s.reason === "rejected")); // 26
    });
  });

  it("the callee (module/call as B)", async () => {
    const b = bindings("module/call");
    const [cs1, cs2, cs3] = [b["cs1"]!, b["cs2"]!, b["cs3"]!];
    await replaying("module/call", "B", undefined, async (replay) => {
      const { manager, media } = await connect(replay, "B", b["uid:B"]!);
      // Pushes arrive faster than a test can look: every state, from the start.
      const seen: string[] = [];
      manager.state.subscribe((s) => seen.push(s.phase === "incoming" ? `ring ${s.callSessionId}` : s.phase === "ended" ? `ended ${s.reason}` : s.phase));
      await until("ringing", is(manager, "incoming")); // 11
      expect(manager.state.value).toMatchObject({ callSessionId: cs1, callType: "voice", callerName: "Alice", callerAvatar: "https://example.test/a.png", ringTimeoutSeconds: 45 });
      expect(await manager.accept()).toBe(true); // 13
      expect(media.log).toEqual(["join voice fake-channel"]);
      expect((await manager.signaling.accept(cs1)).code).toBe(409); // 14: already answered

      await until("asked for video", is(manager, "active", (s) => s.phase === "active" && s.upgrade === "theirs")); // 15
      await manager.declineVideo(); // 16
      await until("asked again", is(manager, "active", (s) => s.phase === "active" && s.upgrade === "theirs")); // 17
      await manager.acceptVideo(); // 18
      // The replay sends A's hang-up (20) right behind call_upgraded: the camera went on in between.
      await until("camera on", () => media.log.includes("video true"));

      await until("hung up", is(manager, "ended", (s) => s.phase === "ended" && s.reason === "user_hangup")); // 20
      expect(media.log.at(-1)).toBe("leave");
      expect((await manager.signaling.hangup(cs1)).code).toBeGreaterThanOrEqual(400); // 21: already ended

      // 22–24 arrive back to back: a ring, its cancel, another ring.
      await until("rings a third time", is(manager, "incoming", (s) => s.phase === "incoming" && s.callSessionId === cs3)); // 24
      const after = seen.slice(seen.lastIndexOf("ended user_hangup") + 1).filter((x) => x !== "idle");
      expect(after).toEqual([`ring ${cs2}`, "ended cancelled", `ring ${cs3}`]);
      await manager.reject(); // 26
      expect((await manager.signaling.accept("nonexistent-session")).code).toBe(404); // 27
    });
  });
});
