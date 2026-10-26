// The chat app's REST endpoints, with two (or three) signed-in users.

import assert from "node:assert/strict";
import { describe, test } from "node:test";
import Fastify, { type FastifyInstance } from "fastify";
import { ADD_TOKEN_TTL_MS, checkAddToken, issueAddToken } from "../src/add-token.js";
import { talkwireAppRoutes, type AppRoutesOptions } from "../src/app-routes.js";
import { MemoryContactStore, MemoryProfileStore } from "../src/memory.js";
import { imUid } from "../src/uid.js";
import { settle, stubIm } from "./helpers.js";

const BASE = "/api/v1/chat";

async function setup(extra: Partial<AppRoutesOptions> = {}) {
  const profiles = new MemoryProfileStore();
  const contacts = new MemoryContactStore();
  const { im, seen } = stubIm(() => ({ status: 200, body: { ok: true, topic: "p2p_x", uidA: "uid_aaaaaaaaaaa", uidB: "uid_bbbbbbbbbbb" } }));
  const app = Fastify();
  await app.register(talkwireAppRoutes, {
    authenticate: async (req) => (req.headers["x-user"] as string | undefined) ?? null,
    profiles,
    contacts,
    im,
    addTokenSecret: "add-token-secret-0123456789",
    ...extra,
  });
  const as = (user: string) => ({
    get: (url: string) => app.inject({ method: "GET", url: BASE + url, headers: { "x-user": user } }),
    post: (url: string, payload: object) => app.inject({ method: "POST", url: BASE + url, payload, headers: { "x-user": user } }),
    patch: (url: string, payload: object) => app.inject({ method: "PATCH", url: BASE + url, payload, headers: { "x-user": user } }),
    del: (url: string) => app.inject({ method: "DELETE", url: BASE + url, headers: { "x-user": user } }),
  });
  return { app, profiles, contacts, seen, as };
}

/** alice finds bob by handle and asks; bob accepts. Returns the contact id. */
async function befriend(as: (u: string) => ReturnType<Awaited<ReturnType<typeof setup>>["as"]>, a: string, b: string, bHandle: string) {
  const found = await as(a).post("/contacts/search", { handle: bHandle });
  assert.equal(found.statusCode, 200, found.body);
  const add = await as(a).post("/contacts", { contactId: b, addToken: found.json().addToken });
  assert.equal(add.statusCode, 200, add.body);
  const accept = await as(b).patch(`/contacts/${add.json().id}`, { status: "accepted" });
  assert.equal(accept.statusCode, 200, accept.body);
  return add.json().id as string;
}

describe("app routes", () => {
  test("signing in is required", async () => {
    const { app } = await setup();
    for (const [method, url] of [["GET", "/me"], ["GET", "/contacts"], ["POST", "/contacts"], ["PATCH", "/me"]] as const) {
      const res = await (app as FastifyInstance).inject({ method, url: BASE + url, payload: method === "GET" ? undefined : {} });
      assert.equal(res.statusCode, 401, `${method} ${url}`);
    }
  });

  test("my profile: created on first use, handle changed once, names checked", async () => {
    const { as } = await setup({ reservedDisplayNames: ["Admin"] });
    const me = await as("alice").get("/me");
    assert.equal(me.statusCode, 200);
    assert.equal(me.json().uid, imUid("alice"));
    assert.match(me.json().handle, /^u_[0-9a-f]{10}$/);
    assert.equal((await as("alice").patch("/me", { displayName: "admin" })).json().error, "reserved_name");
    assert.equal((await as("alice").patch("/me", { handle: "a!" })).statusCode, 400);
    const renamed = await as("alice").patch("/me", { handle: "Alice01", displayName: "Alice", avatarUrl: "https://cdn/a.png" });
    assert.equal(renamed.json().handle, "Alice01");
    assert.equal((await as("alice").patch("/me", { handle: "Alice02" })).json().error, "handle_change_not_allowed");
    await as("bob").get("/me");
    assert.equal((await as("bob").patch("/me", { handle: "ALICE01" })).statusCode, 409);
    assert.equal((await as("alice").patch("/me", { avatarUrl: "" })).json().avatarUrl, null);
    assert.equal((await as("alice").patch("/me", { extra: 1 })).statusCode, 400);
  });

  test("finding users: by handle or QR code, with an add token bound to both users", async () => {
    const { as } = await setup();
    await as("bob").get("/me");
    await as("bob").patch("/me", { handle: "Bobby1", displayName: "Bob" });
    const byHandle = await as("alice").post("/contacts/search", { handle: "bobby1" });
    assert.equal(byHandle.json().userId, "bob");
    assert.equal(byHandle.json().displayName, "Bob");
    assert.equal(checkAddToken(require_key(), byHandle.json().addToken, "alice", "bob"), "invalid", "the token is not signed with the raw secret");
    const bobsQr = (await as("bob").get("/me/qr")).json().qr;
    assert.match(bobsQr, new RegExp(`^c1\.${imUid("bob")}\.[0-9a-f]{32}$`));
    const byQr = await as("alice").post("/contacts/qr-resolve", { qr: bobsQr });
    assert.equal(byQr.json().userId, "bob");
    assert.equal((await as("alice").post("/contacts/search", { handle: "nobody" })).statusCode, 404);
    const profiles = await as("alice").get(`/profiles?uids=${imUid("bob")},not-a-uid,${imUid("ghost")}`);
    assert.deepEqual(profiles.json().items, [{ uid: imUid("bob"), displayName: "Bob", avatarUrl: null }]);
  });

  // R14: adding needs a token from a search or a QR code, by the same user,
  // for the same target, issued within the last two minutes. The check is
  // stateless: within those minutes the token can be presented again for the
  // same pair, where a second request meets the first one (409).
  test("R14_AddContactRequiresOneTimeToken", async () => {
    const { as } = await setup();
    await as("bob").get("/me");
    await as("carol").get("/me");
    const bob = (await as("bob").get("/me")).json();
    assert.equal((await as("alice").post("/contacts", { contactId: "bob" })).json().error, "add_token_required");
    const forCarol = await as("alice").post("/contacts/qr-resolve", { qr: (await as("carol").get("/me/qr")).json().qr });
    assert.equal((await as("alice").post("/contacts", { contactId: "bob", addToken: forCarol.json().addToken })).statusCode, 403);
    const carolsToken = await as("carol").post("/contacts/search", { handle: bob.handle });
    assert.equal((await as("alice").post("/contacts", { contactId: "bob", addToken: carolsToken.json().addToken })).statusCode, 403);
    const mine = await as("alice").post("/contacts/search", { handle: bob.handle });
    assert.equal((await as("alice").post("/contacts", { contactId: "bob", addToken: mine.json().addToken })).json().status, "pending");
    assert.equal((await as("alice").post("/contacts", { contactId: "bob", addToken: mine.json().addToken })).statusCode, 409, "used: the request exists");
    const token = issueAddToken("k", "alice", "bob", 0);
    assert.equal(checkAddToken("k", token, "alice", "bob", ADD_TOKEN_TTL_MS), "valid");
    assert.equal(checkAddToken("k", token, "alice", "bob", ADD_TOKEN_TTL_MS + 1), "invalid", "expired");
  });

  // R14: a uid -- every group member sees every other member's -- shows a
  // name and an avatar (to draw the chat) and nothing that leads to the user:
  // no host id, no handle, no add token, and a bare uid does not resolve as a
  // QR code. Only a code the user showed (their /me/qr) does.
  test("R14_GroupMembersCannotViewProfiles", async () => {
    const { as } = await setup();
    await as("bob").patch("/me", { displayName: "Bob" });
    const uid = imUid("bob");
    const items = (await as("alice").get(`/profiles?uids=${uid}`)).json().items;
    assert.deepEqual(items, [{ uid, displayName: "Bob", avatarUrl: null }]);
    assert.deepEqual((await as("alice").post("/contacts/qr-resolve", { uid })).json(), { error: "qr_required" });
    for (const qr of [`c1.${uid}.${"0".repeat(32)}`, uid, (await as("carol").get("/me/qr")).json().qr.replace(imUid("carol"), uid)]) {
      const res = await as("alice").post("/contacts/qr-resolve", { qr });
      assert.deepEqual([res.statusCode, res.json()], [400, { error: "invalid_qr" }], qr);
    }
    const shown = (await as("alice").post("/contacts/qr-resolve", { qr: (await as("bob").get("/me/qr")).json().qr })).json();
    assert.equal(shown.userId, "bob");
    assert.ok(shown.addToken);
  });

  test("observe mode accepts requests without a token and reports them", async () => {
    const missing: string[] = [];
    const { as } = await setup({ addTokenMode: "observe", onAddTokenMissing: (u, c, why) => missing.push(`${u}>${c}:${why}`) });
    await as("bob").get("/me");
    assert.equal((await as("alice").post("/contacts", { contactId: "bob" })).json().status, "pending");
    assert.deepEqual(missing, ["alice>bob:absent"]);
    await as("carol").get("/me");
    assert.equal((await as("alice").post("/contacts/qr-resolve", { uid: imUid("carol") })).json().userId, "carol", "a printed bare-uid code still resolves");
    assert.deepEqual(missing, ["alice>bob:absent", "alice>carol:absent"]);
  });

  test("request, accept, events and IM sync", async () => {
    const { as, seen } = await setup();
    await as("alice").get("/me");
    await as("alice").patch("/me", { displayName: "Alice" });
    const bobHandle = (await as("bob").get("/me")).json().handle;
    const id = await befriend(as, "alice", "bob", bobHandle);
    await settle();
    const broadcasts = seen.filter((r) => r.path.endsWith("/custom-broadcast")).map((r) => r.body as { targetUid: string; kind: string; payload: { kind: string } });
    assert.deepEqual(
      broadcasts.map((b) => `${b.targetUid === imUid("alice") ? "alice" : "bob"}:${b.kind}`).sort(),
      ["alice:contact.accepted", "bob:contact.accepted", "bob:contact.request"],
    );
    assert.ok(broadcasts.every((b) => b.kind === b.payload.kind));
    const sync = seen.filter((r) => r.path.endsWith("/contact-sync"));
    assert.deepEqual(sync.map((r) => (r.body as { event: string }).event), ["accepted"]);
    const list = await as("bob").get("/contacts");
    assert.equal(list.json().total, 1);
    assert.equal(list.json().items[0].id, id);
    assert.equal(list.json().items[0].direction, "received");
    assert.equal(list.json().items[0].displayName, "Alice");
    assert.ok(Date.parse(list.json().syncCursor));
  });

  test("remarks are private to their author (two accounts)", async () => {
    const { as } = await setup();
    await as("alice").get("/me");
    await as("alice").patch("/me", { displayName: "Alice" });
    await as("bob").get("/me");
    await as("bob").patch("/me", { displayName: "Bob" });
    const id = await befriend(as, "alice", "bob", (await as("bob").get("/me")).json().handle);
    assert.deepEqual((await as("alice").patch(`/contacts/${id}`, { alias: "Bobby" })).json(), { alias: "Bobby" });
    assert.deepEqual((await as("bob").patch(`/contacts/${id}`, { alias: "Ally" })).json(), { alias: "Ally" });
    const aliceView = (await as("alice").get("/contacts")).json().items[0];
    const bobView = (await as("bob").get("/contacts")).json().items[0];
    assert.equal(aliceView.alias, "Bobby");
    assert.equal(aliceView.displayName, "Bobby");
    assert.equal(aliceView.nickname, "Bob");
    assert.equal(bobView.alias, "Ally");
    assert.equal(bobView.displayName, "Ally");
    assert.equal(bobView.nickname, "Alice");
    assert.equal((await as("carol").patch(`/contacts/${id}`, { alias: "x" })).statusCode, 404);
    assert.deepEqual((await as("alice").patch(`/contacts/${id}`, { alias: null })).json(), { alias: null });
    assert.equal((await as("bob").get("/contacts")).json().items[0].alias, "Ally");
  });

  test("incremental sync with since; removing is idempotent; blocking", async () => {
    const { as, seen } = await setup();
    await as("bob").get("/me");
    await as("carol").get("/me");
    const id = await befriend(as, "alice", "bob", (await as("bob").get("/me")).json().handle);
    const first = (await as("alice").get("/contacts")).json();
    assert.equal((await as("alice").get(`/contacts?since=${encodeURIComponent(first.syncCursor)}`)).json().total, 0);
    await new Promise((r) => setTimeout(r, 5));
    await as("alice").patch(`/contacts/${id}`, { alias: "B" });
    assert.equal((await as("alice").get(`/contacts?since=${encodeURIComponent(first.syncCursor)}`)).json().total, 1);
    assert.equal((await as("alice").get("/contacts?status=bogus")).statusCode, 400);

    const blocked = await as("bob").patch(`/contacts/${id}`, { status: "blocked" });
    assert.equal(blocked.json().status, "blocked");
    const blockedList = await as("bob").get("/contacts?status=blocked");
    assert.equal(blockedList.json().items[0].blockedByMe, true);
    assert.equal((await as("alice").get("/contacts?status=blocked")).json().items[0].blockedByMe, false);
    assert.equal((await as("alice").del(`/contacts/${id}`)).statusCode, 403, "only the blocker lifts a block");
    assert.deepEqual((await as("bob").del(`/contacts/${id}`)).json(), { success: true });
    assert.deepEqual((await as("bob").del(`/contacts/${id}`)).json(), { success: true });
    await settle();
    assert.deepEqual(seen.filter((r) => r.path.endsWith("/contact-sync")).map((r) => (r.body as { event: string }).event), ["accepted", "removed"]);

    const carolHandle = (await as("carol").get("/me")).json().handle;
    const second = await befriend(as, "alice", "carol", carolHandle);
    assert.equal((await as("alice").patch(`/contacts/${second}`, { status: "accepted" })).json().status, "accepted");
    assert.equal((await as("alice").post("/contacts", { contactId: "alice", addToken: "x" })).statusCode, 403);
  });

  test("an inactive user cannot be found or added", async () => {
    const { as } = await setup({ isUserActive: async (u) => u !== "banned", addTokenMode: "off" });
    await as("banned").get("/me");
    const handle = (await as("banned").get("/me")).json().handle;
    assert.equal((await as("alice").post("/contacts/search", { handle })).statusCode, 404);
    assert.equal((await as("alice").post("/contacts", { contactId: "banned" })).statusCode, 404);
    assert.equal((await as("banned").get("/me")).statusCode, 200, "the user still sees their own profile");
  });
});

// The key app-routes derives from addTokenSecret is not the secret itself.
function require_key() {
  return "add-token-secret-0123456789";
}
