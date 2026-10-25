import assert from "node:assert/strict";
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { test } from "node:test";
import jwt from "jsonwebtoken";
import { MemoryContactStore, addContact, imUid, signInternalToken } from "@imkit/host-node";
import { buildSampleHost, devUserId, signAccessToken, verifyAccessToken, verifyAdminToken } from "../src/app.ts";
import { demoCalls } from "../src/demo-calls.ts";

const secret = "x".repeat(40);

async function host() {
  // No IM server listens here: contact-sync fails and must not break sign-in.
  return buildSampleHost({ secret, imBaseUrl: "http://127.0.0.1:59999", mediaDir: mkdtempSync(path.join(tmpdir(), "imkit-media-")) });
}

test("dev login issues the access token the IM server accepts", async () => {
  const app = await host();
  const res = await app.inject({ method: "POST", url: "/dev/login", payload: { name: "Alice" } });
  assert.equal(res.statusCode, 200);
  const body = res.json();
  assert.equal(body.userId, "dev-alice");
  assert.equal(body.uid, imUid("dev-alice"));
  const claims = jwt.verify(body.accessToken, secret) as jwt.JwtPayload;
  // The IM server's strict profile takes only access tokens of an ordinary user role.
  assert.equal(claims.sub, "dev-alice");
  assert.equal(claims.type, "access");
  assert.equal(claims.role, "app_user");
  assert.equal(verifyAccessToken(secret, body.accessToken), "dev-alice");
  assert.equal(verifyAccessToken("y".repeat(40), body.accessToken), null);
  await app.close();
});

test("signing in befriends earlier users, both ways", async () => {
  const app = await host();
  const a = (await app.inject({ method: "POST", url: "/dev/login", payload: { name: "ann" } })).json();
  const b = (await app.inject({ method: "POST", url: "/dev/login", payload: { name: "bo" } })).json();
  const list = await app.inject({ method: "GET", url: "/api/v1/chat/contacts", headers: { authorization: `Bearer ${a.accessToken}` } });
  assert.equal(list.statusCode, 200);
  const items = list.json().items as Array<{ uid: string; status: string; displayName: string }>;
  assert.equal(items.length, 1);
  assert.equal(items[0]?.uid, b.uid);
  assert.equal(items[0]?.status, "accepted");
  assert.equal(items[0]?.displayName, "bo");

  // The IM server asks the host whether two users are friends.
  const internal = jwt.sign({ role: "internal_service" }, secret, { algorithm: "HS256", expiresIn: 60 });
  const f = await app.inject({
    method: "GET",
    url: `/api/v1/internal/im/friendship?a=${devUserId("ann")}&b=${devUserId("bo")}`,
    headers: { authorization: `Bearer ${internal}` },
  });
  assert.equal(f.statusCode, 200);
  assert.equal(f.json().friends, true);
  await app.close();
});

test("bad names and missing tokens are refused", async () => {
  const app = await host();
  assert.equal((await app.inject({ method: "POST", url: "/dev/login", payload: { name: "a b" } })).statusCode, 400);
  assert.equal((await app.inject({ method: "GET", url: "/api/v1/chat/contacts" })).statusCode, 401);
  await app.close();
});

test("demo calls: contacts only, each side its own token", async () => {
  const contacts = new MemoryContactStore();
  const calls = demoCalls(contacts);
  const req = { callSessionId: "cs-1", callerAppUserId: "dev-ann", calleeAppUserId: "dev-bo", callType: "voice" as const };
  assert.deepEqual(await calls.create(req), { ok: false, reason: "not_friends" });
  await addContact(contacts, "dev-ann", "dev-bo");
  await addContact(contacts, "dev-bo", "dev-ann");
  assert.deepEqual(await calls.create(req), { ok: true });
  const answer = await calls.answer({ callSessionId: "cs-1", effectiveType: "voice" });
  assert.equal(answer.ok, true);
  assert.ok(answer.callerToken && answer.calleeToken && answer.callerToken !== answer.calleeToken);
  assert.deepEqual(await calls.upgrade({ callSessionId: "cs-1", toType: "video" }), { ok: true, videoQuality: "sd" });
  await calls.settle({ callSessionId: "cs-1", reason: "user_hangup" });
  assert.equal(calls.calls.size, 0);
  assert.deepEqual(await calls.answer({ callSessionId: "cs-1", effectiveType: "voice" }), { ok: false, reason: "not_found" });
});

test("administrators: their own tokens, and every call recorded", async () => {
  const app = await host();
  const a = (await app.inject({ method: "POST", url: "/dev/login", payload: { name: "cat" } })).json();
  const b = (await app.inject({ method: "POST", url: "/dev/login", payload: { name: "dog" } })).json();
  const login = await app.inject({ method: "POST", url: "/dev/admin-login", payload: { name: "root", role: "superadmin" } });
  assert.equal(login.statusCode, 200);
  const admin = login.json();
  assert.deepEqual(verifyAdminToken(secret, admin.token), { adminId: "admin-root", role: "superadmin" });
  // Neither token opens the other's door.
  assert.equal(verifyAccessToken(secret, admin.token), null);
  assert.equal(verifyAdminToken(secret, signAccessToken(secret, "dev-cat")), null);
  assert.equal((await app.inject({ method: "POST", url: "/dev/admin-login", payload: { name: "root", role: "owner" } })).statusCode, 400);
  const records = (token: string) => app.inject({ method: "GET", url: "/api/v1/admin/im/calls", headers: { authorization: `Bearer ${token}` } });
  assert.equal((await records(a.accessToken)).statusCode, 401);

  // A call the IM server reports to the host, as it would: create, answer, settle.
  const internal = signInternalToken(secret);
  const call = (path: string, payload: object) =>
    app.inject({ method: "POST", url: `/api/v1/internal/im/calls/${path}`, payload, headers: { authorization: `Bearer ${internal}` } });
  assert.equal((await call("create", { callSessionId: "cs-1", callerAppUserId: a.userId, calleeAppUserId: b.userId, callType: "voice" })).json().ok, true);
  assert.equal((await call("answer", { callSessionId: "cs-1", effectiveType: "voice" })).json().ok, true);
  assert.equal((await call("settle", { callSessionId: "cs-1", reason: "user_hangup" })).statusCode, 200);
  const list = (await records(admin.token)).json();
  assert.equal(list.calls.length, 1);
  assert.deepEqual(
    [list.calls[0].status, list.calls[0].endReason, list.calls[0].caller.displayName, list.calls[0].callee.displayName],
    ["ended", "user_hangup", "cat", "dog"],
  );
  await app.close();
});
