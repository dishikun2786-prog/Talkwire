import assert from "node:assert/strict";
import { describe, test } from "node:test";
import Fastify from "fastify";
import { clearConfirmation, imkitAdminRoutes } from "../src/admin-routes.js";
import { MemoryAuditStore, MemoryCallRecordStore, MemoryProfileStore } from "../src/memory.js";
import type { CallRecordStore } from "../src/types.js";
import { imUid } from "../src/uid.js";
import { stubIm, type Responder } from "./helpers.js";

const BASE = "/api/v1/admin/im";
const P2P = "p2p_aaaa_bbbb";

async function setup(respond?: Responder, calls?: CallRecordStore) {
  const profiles = new MemoryProfileStore();
  const audit = new MemoryAuditStore();
  await profiles.create({ userId: "alice", handle: "alice1", displayName: "Alice", avatarUrl: null });
  await profiles.create({ userId: "bob", handle: "bobby1", displayName: "Bob", avatarUrl: null });
  let clears = 0;
  const { im, seen } = stubIm(
    respond ??
      ((req) => {
        if (req.path.endsWith("/topics")) {
          return { status: 200, body: { topics: [{ topic: P2P, isGroup: false, lastSeq: 2, messageCount: 2, members: [imUid("alice"), imUid("bob")], clearVersion: 0, clearBeforeSeq: 0 }] } };
        }
        if (req.path.endsWith("/topic")) return { status: 200, body: { topic: req.query.topic, isGroup: false, lastSeq: 2, messageCount: 2, members: [imUid("alice"), "uid_strangerXXX"], clearVersion: 0, clearBeforeSeq: 0 } };
        if (req.path.endsWith("/messages")) {
          return { status: 200, body: { hasMore: false, messages: [{ topic: P2P, seq: 1, from: imUid("bob"), content: "hi", contentType: "text/plain", ts: 1 }] } };
        }
        if (req.path.endsWith("/clear")) {
          clears++;
          if (req.body && (req.body as { topic: string }).topic.startsWith("grp_")) return { status: 409, body: { error: "only two-person conversations can be cleared" } };
          return { status: 200, body: { deletedMessages: 2, deletedMedia: 1, clearVersion: 1, clearBeforeSeq: 2 } };
        }
        return { status: 404, body: "404 page not found" };
      }),
  );
  const app = Fastify();
  await app.register(imkitAdminRoutes, {
    im,
    audit,
    profiles,
    calls,
    authenticate: async (req) => {
      const role = req.headers["x-role"];
      return role === "admin" || role === "superadmin" ? { adminId: `id-${role}`, role } : null;
    },
  });
  const as = (role: string | null) => ({
    get: (url: string) => app.inject({ method: "GET", url: BASE + url, headers: role ? { "x-role": role } : {} }),
    post: (url: string, payload: object) => app.inject({ method: "POST", url: BASE + url, payload, headers: role ? { "x-role": role } : {} }),
  });
  return { as, audit, seen, clears: () => clears };
}

describe("admin routes", () => {
  test("reading needs an administrator and shows who the uids are", async () => {
    const { as, seen } = await setup();
    assert.equal((await as(null).get("/users/alice/topics")).statusCode, 401);
    const topics = await as("admin").get("/users/alice/topics?limit=5");
    assert.equal(topics.statusCode, 200);
    assert.equal(topics.json().uid, imUid("alice"));
    assert.deepEqual(topics.json().topics[0].members, [
      { uid: imUid("alice"), userId: "alice", displayName: "Alice" },
      { uid: imUid("bob"), userId: "bob", displayName: "Bob" },
    ]);
    assert.deepEqual(seen[0]!.query, { uid: imUid("alice"), limit: "5" });
    const one = await as("admin").get(`/topics/${P2P}`);
    assert.deepEqual(one.json().members[1], { uid: "uid_strangerXXX", userId: null, displayName: null });
    const msgs = await as("admin").get(`/topics/${P2P}/messages?limit=10&type=text`);
    assert.equal(msgs.json().messages[0].sender.displayName, "Bob");
    assert.equal((await as("admin").get(`/topics/${P2P}/messages?limit=1000`)).statusCode, 400);
  });

  // R13: clearing takes a superadministrator, the typed confirmation, a
  // reason and an idempotency key; the audit keeps metadata only, never the
  // conversation's content or media addresses.
  test("R13_AdminClearNeedsConfirmAndAudit", async () => {
    const { as, audit, clears } = await setup();
    const body = { confirm: clearConfirmation(P2P), reason: "the user asked", idempotencyKey: "clear-0001", targetUserId: "alice" };
    assert.equal((await as("admin").post(`/topics/${P2P}/clear`, body)).statusCode, 403);
    assert.equal((await as("superadmin").post(`/topics/${P2P}/clear`, { ...body, confirm: "CLEAR" })).statusCode, 400);
    assert.equal((await as("superadmin").post(`/topics/${P2P}/clear`, { ...body, reason: " " })).statusCode, 400);
    assert.equal((await as("superadmin").post(`/topics/${P2P}/clear`, { ...body, idempotencyKey: "x" })).statusCode, 400);
    assert.equal(clears(), 0);

    const first = await as("superadmin").post(`/topics/${P2P}/clear`, body);
    assert.equal(first.statusCode, 200);
    assert.equal(first.json().deletedMessages, 2);
    assert.equal(first.json().replayed, false);
    const again = await as("superadmin").post(`/topics/${P2P}/clear`, body);
    assert.equal(again.json().replayed, true, "the same key answers the first outcome");
    assert.equal(again.json().auditId, first.json().auditId);
    assert.equal(clears(), 1, "cleared once");
    assert.equal((await as("superadmin").post(`/topics/p2p_other/clear`, { ...body, confirm: clearConfirmation("p2p_other") })).statusCode, 409, "the key belongs to another request");

    const entry = (await audit.get(first.json().auditId))!;
    assert.equal(entry.status, "success");
    assert.equal(entry.adminId, "id-superadmin");
    assert.equal(entry.reason, "the user asked");
    assert.equal(entry.targetUserId, "alice");
    assert.deepEqual(entry.details, { deletedMessages: 2, deletedMedia: 1, clearVersion: 1, clearBeforeSeq: 2 });
    assert.ok(!JSON.stringify(entry).includes("hi"), "no message content in the audit");

    // An IM server that says more about what it deleted still leaves only the counts.
    const chatty = await setup((req) =>
      req.path.endsWith("/clear")
        ? { status: 200, body: { deletedMessages: 1, deletedMedia: 1, clearVersion: 1, clearBeforeSeq: 1, mediaUrls: ["https://cdn.example/secret.jpg"], messages: [{ content: "secret words" }] } }
        : { status: 404, body: "404 page not found" },
    );
    const done = await chatty.as("superadmin").post(`/topics/${P2P}/clear`, { ...body, idempotencyKey: "clear-0003" });
    assert.equal(done.statusCode, 200);
    const kept = JSON.stringify(await chatty.audit.get(done.json().auditId));
    assert.ok(!kept.includes("secret"), kept);
  });

  test("a refused or failed clear is audited as failed; the key cannot be reused", async () => {
    const { as, audit } = await setup();
    const res = await as("superadmin").post("/topics/grp_team/clear", { confirm: clearConfirmation("grp_team"), reason: "r", idempotencyKey: "clear-0002" });
    assert.equal(res.statusCode, 409);
    assert.match(res.json().error, /two-person/);
    const entry = (await audit.findByIdempotencyKey("clear-0002"))!;
    assert.equal(entry.status, "failed");
    assert.deepEqual(entry.details, { status: 409 });
    const retry = await as("superadmin").post("/topics/grp_team/clear", { confirm: clearConfirmation("grp_team"), reason: "r", idempotencyKey: "clear-0002" });
    assert.equal(retry.statusCode, 409);
    assert.match(retry.json().error, /new idempotencyKey/);
  });

  test("an IM server that does not answer is a 502", async () => {
    const { as } = await setup(() => ({ status: 500, body: { error: "boom" } }));
    assert.equal((await as("admin").get("/users/alice/topics")).statusCode, 502);
  });

  test("call records: who called whom, filters, pages, one call", async () => {
    const calls = new MemoryCallRecordStore();
    const t0 = Date.parse("2026-10-01T10:00:00.000Z");
    await calls.create({ callSessionId: "cs-1", callerUserId: "alice", calleeUserId: "bob", requestedType: "video" }, new Date(t0));
    await calls.answered("cs-1", "voice", new Date(t0 + 2_000));
    await calls.ended("cs-1", "user_hangup", new Date(t0 + 32_000));
    await calls.create({ callSessionId: "cs-2", callerUserId: "bob", calleeUserId: "carol", requestedType: "voice" }, new Date(t0 + 60_000));
    const { as } = await setup(undefined, calls);
    const ids = async (query: string) => {
      const res = await as("admin").get(`/calls${query}`);
      assert.equal(res.statusCode, 200, res.body);
      return (res.json().calls as { callSessionId: string }[]).map((c) => c.callSessionId);
    };

    assert.equal((await as(null).get("/calls")).statusCode, 401);
    const all = (await as("admin").get("/calls")).json();
    assert.deepEqual([all.calls.map((c: { callSessionId: string }) => c.callSessionId), all.nextCursor], [["cs-2", "cs-1"], null]);
    assert.deepEqual(all.calls[1].caller, { uid: imUid("alice"), userId: "alice", displayName: "Alice" });
    assert.deepEqual(all.calls[0].callee, { uid: imUid("carol"), userId: "carol", displayName: null });
    assert.deepEqual([all.calls[1].requestedType, all.calls[1].callType, all.calls[1].durationSec], ["video", "voice", 30]);
    const first = (await as("admin").get("/calls?limit=1")).json();
    assert.deepEqual(await ids(`?limit=1&cursor=${encodeURIComponent(first.nextCursor)}`), ["cs-1"]);
    assert.deepEqual(await ids("?userId=alice"), ["cs-1"]);
    assert.deepEqual(await ids("?status=ringing"), ["cs-2"]);
    assert.deepEqual(await ids("?endReason=user_hangup&callType=voice"), ["cs-1"]);
    assert.deepEqual(await ids(`?fromMs=${t0 + 1_000}`), ["cs-2"]);
    assert.deepEqual(await ids(`?toMs=${t0 + 1_000}`), ["cs-1"]);
    for (const bad of ["?cursor=garbage", "?limit=1000", "?status=missed", "?callType=fax"]) {
      assert.equal((await as("admin").get(`/calls${bad}`)).statusCode, 400, bad);
    }

    const one = await as("admin").get("/calls/cs-1");
    assert.equal(one.statusCode, 200);
    assert.deepEqual([one.json().endReason, one.json().callee.displayName], ["user_hangup", "Bob"]);
    assert.equal((await as("admin").get("/calls/nope")).statusCode, 404);
    assert.equal((await as(null).get("/calls/cs-1")).statusCode, 401);
  });

  test("without a call record store there are no call routes", async () => {
    const { as } = await setup();
    assert.equal((await as("admin").get("/calls")).statusCode, 404);
  });
});
