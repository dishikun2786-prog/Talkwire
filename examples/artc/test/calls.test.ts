import assert from "node:assert/strict";
import { test } from "node:test";
import { MemoryContactStore, imUid } from "@imkit/host-node";
import { buildAppToken, readAppToken } from "../src/app-token.js";
import { artcCalls, friendsOnly, type ArtcCall } from "../src/calls.js";

const NOW = 1_800_000_000_000;
const options = { appId: "app-1", appKey: "secret-key", now: () => NOW };

/** Whether the token was signed with this key: rebuilt from what it says, it must come out the same. */
function signedWith(token: string, appKey: string) {
  const t = readAppToken(token);
  return buildAppToken({ ...t, appKey }) === token;
}

test("one channel per call; each side's own token, for its IM uid", async () => {
  const calls = artcCalls(options);
  assert.deepEqual(await calls.create({ callSessionId: "s1", callerAppUserId: "alice", calleeAppUserId: "bob", callType: "video" }), { ok: true });
  await calls.create({ callSessionId: "s2", callerAppUserId: "alice", calleeAppUserId: "carol", callType: "voice" });
  const a = await calls.answer({ callSessionId: "s1", effectiveType: "voice" });
  const b = await calls.answer({ callSessionId: "s2", effectiveType: "voice" });
  assert.equal(a.ok, true);
  assert.match(a.channelId!, /^imkit-[0-9a-f]{24}$/);
  assert.notEqual(a.channelId, b.channelId);
  assert.equal(a.appId, "app-1");
  assert.equal(a.videoQuality, "sd");
  assert.equal(a.denoiseEnabled, true);

  const caller = readAppToken(a.callerToken!);
  const callee = readAppToken(a.calleeToken!);
  assert.equal(caller.userId, imUid("alice"));
  assert.equal(callee.userId, imUid("bob"));
  for (const t of [caller, callee]) {
    assert.equal(t.appId, "app-1");
    assert.equal(t.channelId, a.channelId);
    assert.equal(t.expiresAt, NOW / 1000 + 86400);
  }
  assert.ok(signedWith(a.callerToken!, "secret-key"));
  assert.ok(!signedWith(a.callerToken!, "other-key"));
  // Answered as voice: what was asked for gives way to what was accepted.
  assert.equal(calls.calls.get("s1")?.callType, "voice");
  assert.equal(calls.calls.get("s1")?.answeredAt, NOW);
});

test("the policy decides who may call; friendsOnly accepts accepted contacts only", async () => {
  const contacts = new MemoryContactStore();
  await contacts.create({ requesterId: "alice", recipientId: "bob", status: "accepted" });
  await contacts.create({ requesterId: "alice", recipientId: "dave", status: "pending" });
  const calls = artcCalls({ ...options, canCall: friendsOnly(contacts) });
  const create = (callee: string, id: string) => calls.create({ callSessionId: id, callerAppUserId: "alice", calleeAppUserId: callee, callType: "voice" });
  assert.deepEqual(await create("bob", "s1"), { ok: true });
  assert.deepEqual(await create("dave", "s2"), { ok: false, reason: "not_friends" });
  assert.deepEqual(await create("erin", "s3"), { ok: false, reason: "not_friends" });
  assert.deepEqual([...calls.calls.keys()], ["s1"]);
  // In the other direction too: a contact is one row per pair.
  assert.deepEqual(
    await calls.create({ callSessionId: "s4", callerAppUserId: "bob", calleeAppUserId: "alice", callType: "voice" }),
    { ok: true },
  );
});

test("upgrade and settle; settle bills once and forgets the call", async () => {
  const settled: Array<[ArtcCall, string]> = [];
  const calls = artcCalls({ ...options, videoQuality: () => "hd", denoiseEnabled: false, onSettle: (c, r) => void settled.push([c, r]) });
  await calls.create({ callSessionId: "s1", callerAppUserId: "alice", calleeAppUserId: "bob", callType: "voice" });
  const a = await calls.answer({ callSessionId: "s1", effectiveType: "voice" });
  assert.equal(a.denoiseEnabled, false);
  assert.deepEqual(await calls.upgrade({ callSessionId: "s1", toType: "video" }), { ok: true, videoQuality: "hd" });
  await calls.settle({ callSessionId: "s1", reason: "hangup" });
  await calls.settle({ callSessionId: "s1", reason: "hangup" });
  assert.equal(settled.length, 1);
  assert.equal(settled[0]![0].callType, "video");
  assert.equal(settled[0]![1], "hangup");
  assert.equal(calls.calls.size, 0);
  assert.deepEqual(await calls.answer({ callSessionId: "s1", effectiveType: "voice" }), { ok: false, reason: "not_found" });
  assert.deepEqual(await calls.upgrade({ callSessionId: "s1", toType: "video" }), { ok: false, reason: "not_found" });
});

test("the host's own RTC user ids, and tokens never outlive 24 hours", async () => {
  const calls = artcCalls({ ...options, rtcUserId: (u) => "rtc-" + u, tokenSeconds: 7 * 86400 });
  await calls.create({ callSessionId: "s1", callerAppUserId: "alice", calleeAppUserId: "bob", callType: "voice" });
  const a = await calls.answer({ callSessionId: "s1", effectiveType: "voice" });
  assert.equal(readAppToken(a.calleeToken!).userId, "rtc-bob");
  assert.equal(readAppToken(a.calleeToken!).expiresAt, NOW / 1000 + 86400);
  const short = artcCalls({ ...options, tokenSeconds: 600 });
  await short.create({ callSessionId: "s1", callerAppUserId: "alice", calleeAppUserId: "bob", callType: "voice" });
  assert.equal(readAppToken((await short.answer({ callSessionId: "s1", effectiveType: "voice" })).callerToken!).expiresAt, NOW / 1000 + 600);
});
