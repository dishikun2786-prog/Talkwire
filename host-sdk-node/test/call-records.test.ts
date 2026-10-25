import assert from "node:assert/strict";
import { describe, test } from "node:test";
import { recordingCalls } from "../src/call-records.js";
import type { CallHandlers } from "../src/host-plugin.js";
import { MemoryCallRecordStore } from "../src/memory.js";

function handlers(overrides: Partial<CallHandlers> = {}): CallHandlers {
  return {
    create: async (req) => (req.calleeAppUserId === "stranger" ? { ok: false, reason: "not_friends" } : { ok: true }),
    answer: async () => ({ ok: true, channelId: "ch", callerToken: "t1", calleeToken: "t2", appId: "app" }),
    upgrade: async () => ({ ok: true, videoQuality: "hd" }),
    settle: async () => {},
    ...overrides,
  };
}

describe("recordingCalls", () => {
  test("records what the handlers accepted, and passes their answers through", async () => {
    const store = new MemoryCallRecordStore();
    const calls = recordingCalls(handlers(), store);
    assert.deepEqual(await calls.create({ callSessionId: "cs-1", callerAppUserId: "a", calleeAppUserId: "b", callType: "video" }), { ok: true });
    assert.deepEqual(await calls.create({ callSessionId: "cs-2", callerAppUserId: "a", calleeAppUserId: "stranger", callType: "voice" }), {
      ok: false,
      reason: "not_friends",
    });
    assert.equal(await store.get("cs-2"), null);
    assert.equal((await calls.answer({ callSessionId: "cs-1", effectiveType: "voice" })).channelId, "ch");
    assert.equal((await calls.upgrade({ callSessionId: "cs-1", toType: "video" })).videoQuality, "hd");
    await calls.settle({ callSessionId: "cs-1", reason: "user_hangup" });
    const r = (await store.get("cs-1"))!;
    assert.deepEqual([r.requestedType, r.callType, r.status, r.endReason], ["video", "video", "ended", "user_hangup"]);
    assert.ok(r.answeredAt && r.upgradedAt && r.endedAt);
    // Nothing of the credentials is kept.
    assert.doesNotMatch(JSON.stringify(r), /"ch"|t1|t2/);
  });

  test("a refused answer leaves the call ringing; the end is recorded even when settling throws", async () => {
    const store = new MemoryCallRecordStore();
    const calls = recordingCalls(
      handlers({
        answer: async () => ({ ok: false, reason: "insufficient_credits" }),
        settle: async () => {
          throw new Error("billing is down");
        },
      }),
      store,
    );
    await calls.create({ callSessionId: "cs-1", callerAppUserId: "a", calleeAppUserId: "b", callType: "voice" });
    await calls.answer({ callSessionId: "cs-1", effectiveType: "voice" });
    assert.equal((await store.get("cs-1"))!.status, "ringing");
    await assert.rejects(calls.settle({ callSessionId: "cs-1", reason: "failed" }), /billing is down/);
    assert.deepEqual([(await store.get("cs-1"))!.status, (await store.get("cs-1"))!.durationSec], ["ended", 0]);
  });

  test("a store that fails never fails the call", async () => {
    const store = new MemoryCallRecordStore();
    store.create = async () => {
      throw new Error("disk full");
    };
    const errors: string[] = [];
    const calls = recordingCalls(handlers(), store, { onError: (err, step, id) => errors.push(`${step} ${id} ${(err as Error).message}`) });
    assert.deepEqual(await calls.create({ callSessionId: "cs-1", callerAppUserId: "a", calleeAppUserId: "b", callType: "voice" }), { ok: true });
    await calls.settle({ callSessionId: "cs-1", reason: "cancelled" });
    assert.deepEqual(errors, ["create cs-1 disk full"]);
  });
});
