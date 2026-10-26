/**
 * The scan-to-join client against the golden transcripts `module/qr-join`
 * and `module/qr-join-review`: the replay plays the server and fails unless
 * the client sends exactly the scripted requests. Each case is one scenario
 * user; the step numbers are the transcript's.
 */

import { ImClient, RequestRefused } from "@talkwire/client";
import { GroupJoinClient, type GroupJoinEvent } from "@talkwire/group-join";
import { afterEach, describe, expect, it } from "vitest";
import { RecordingTransport, bindings, replayBuilt, replaying, until, type Replay } from "../../client/test/replay.js";

const stops: (() => void)[] = [];
afterEach(() => {
  for (const s of stops.splice(0)) s();
});

async function connect(replay: Replay, user: string): Promise<{ client: ImClient; api: GroupJoinClient }> {
  const client = new ImClient({
    endpoints: [replay.url],
    tokenProvider: () => `token-of-${user}`,
    transport: new RecordingTransport(),
    requestTimeoutMs: 5_000,
    watchNetwork: false,
  });
  const api = new GroupJoinClient(client);
  stops.push(() => {
    api.dispose();
    client.stop();
  });
  client.start();
  expect(await client.awaitConnected(10_000)).not.toBeNull();
  return { client, api };
}

const refused = (code: number) => (e: unknown) => e instanceof RequestRefused && e.reply.code === code;

describe.skipIf(!replayBuilt)("scan to join, replayed", () => {
  it("a newcomer previews, joins and joins again (module/qr-join as D)", async () => {
    const b = bindings("module/qr-join");
    const grp = b["grp"]!;
    await replaying("module/qr-join", "D", undefined, async (replay) => {
      const { client, api } = await connect(replay, "D");
      await expect(api.qrToken(grp)).rejects.toSatisfy(refused(401)); // step 11: not a member
      expect(await api.resolve(b["qr1"]!)).toBeNull(); // 15: replaced by the refresh
      expect(await api.resolve(b["qr2"]!)).toEqual({ topic: grp, name: "scan", memberCount: 2, alreadyMember: false, isFull: false, requireApproval: false }); // 16
      expect(await api.resolve("no-such-token")).toBeNull(); // 17
      expect(await api.join(b["qr2"]!)).toEqual({ topic: grp, status: "joined" }); // 18
      expect(await api.join(b["qr2"]!)).toEqual({ topic: grp, status: "joined" }); // 19: already a member
      expect((await client.getMembers(grp)).map((m) => m.uid)).toContain(b["uid:D"]); // 20
      expect(await api.requireApproval(grp)).toBe(true); // 24
      await expect(api.requests(grp)).rejects.toSatisfy(refused(401)); // 27: a plain member
    });
  });

  it("an admin shares, refreshes and reviews (module/qr-join as B)", async () => {
    const b = bindings("module/qr-join");
    const grp = b["grp"]!;
    await replaying("module/qr-join", "B", undefined, async (replay) => {
      const { api } = await connect(replay, "B");
      const seen: GroupJoinEvent[] = [];
      api.events.on((e) => seen.push(e));
      // Steps 9 and 10 make B a member and an admin; the replay paces itself.
      expect((await api.qrToken(grp)).qrToken).toBe(b["qr1"]); // 13: the same current token
      const fresh = await api.qrToken(grp, { refresh: true }); // 14
      expect(fresh.qrToken).toBe(b["qr2"]);
      expect(fresh.expiresAt).toBeGreaterThan(Date.UTC(2020, 0));
      await expect(api.setRequireApproval(grp, true)).rejects.toSatisfy(refused(401)); // 22: the owner only
      await until("the request reaches the admins", () => seen.length === 1); // 25
      expect(seen[0]).toEqual({ kind: "request", topic: grp, name: "scan", from: b["uid:E"] });
      const pending = await api.requests(grp); // 28
      expect(pending).toMatchObject([{ id: 1, requesterUid: b["uid:E"], message: "please let me in" }]);
      expect(await api.review(grp, pending[0]!.id, true)).toBe("approved"); // 29
      expect(await api.requests(grp)).toEqual([]); // 31: nothing pending, the field omitted
    });
  });

  it("an applicant asks twice and hears the answer (module/qr-join as E)", async () => {
    const b = bindings("module/qr-join");
    const grp = b["grp"]!;
    await replaying("module/qr-join", "E", undefined, async (replay) => {
      const { api } = await connect(replay, "E");
      const seen: GroupJoinEvent[] = [];
      api.events.on((e) => seen.push(e));
      await expect(api.requireApproval(grp)).rejects.toSatisfy(refused(401)); // 21: not a member
      expect(await api.join(b["qr2"]!, "please let me in")).toEqual({ topic: grp, status: "pending_approval" }); // 25
      expect(await api.join(b["qr2"]!, "again")).toEqual({ topic: grp, status: "pending_approval" }); // 26: no second notice
      await until("the review result", () => seen.length === 1); // 29
      expect(seen[0]).toEqual({ kind: "reviewed", topic: grp, name: "scan", status: "approved" });
    });
  });

  it("a rejected applicant asks again (module/qr-join-review as E)", async () => {
    const b = bindings("module/qr-join-review");
    const g2 = b["g2"]!;
    await replaying("module/qr-join-review", "E", undefined, async (replay) => {
      const { client, api } = await connect(replay, "E");
      const seen: GroupJoinEvent[] = [];
      api.events.on((e) => seen.push(e));
      expect((await api.join(b["qr"]!, "first try")).status).toBe("pending_approval"); // 13
      await until("rejected", () => seen.length === 1); // 15
      expect(seen[0]).toMatchObject({ kind: "reviewed", topic: g2, status: "rejected" });
      await expect(client.getMembers(g2)).rejects.toThrow(); // 16: not a member
      expect((await api.join(b["qr"]!, "second try")).status).toBe("pending_approval"); // 18: the same token works again (F56)
      await client.getMembers(g2).catch(() => null); // 22: on the legacy server E never joins (D8)
    });
  });
});
