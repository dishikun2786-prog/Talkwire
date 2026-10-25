import assert from "node:assert/strict";
import { describe, test } from "node:test";
import Fastify from "fastify";
import { clearConfirmation, imkitAdminRoutes } from "../src/admin-routes.js";
import { MemoryAuditStore, MemoryProfileStore } from "../src/memory.js";
import { imUid } from "../src/uid.js";
import { stubIm, type Responder } from "./helpers.js";

const BASE = "/api/v1/admin/im";
const P2P = "p2p_aaaa_bbbb";

async function setup(respond?: Responder) {
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

  test("clearing: superadministrator, typed confirmation, reason, idempotency key", async () => {
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
});
