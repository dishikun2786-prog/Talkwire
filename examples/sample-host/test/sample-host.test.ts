import assert from "node:assert/strict";
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { test } from "node:test";
import jwt from "jsonwebtoken";
import { imUid } from "@imkit/host-node";
import { buildSampleHost, devUserId, verifyAccessToken } from "../src/app.ts";

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
