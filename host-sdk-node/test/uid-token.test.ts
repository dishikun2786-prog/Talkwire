import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import path from "node:path";
import { test } from "node:test";
import jwt from "jsonwebtoken";
import { bearerToken, signInternalToken, verifyInternalToken } from "../src/token.js";
import { imUid, isUid } from "../src/uid.js";
import { contractDir } from "./openapi.js";

test("uids match the contract vectors shared with the IM server", () => {
  const { vectors } = JSON.parse(readFileSync(path.join(contractDir, "uid-vectors.json"), "utf8")) as { vectors: Array<{ hostUserId: string; uid: string }> };
  assert.ok(vectors.length >= 5);
  for (const v of vectors) {
    assert.equal(imUid(v.hostUserId), v.uid, v.hostUserId);
    assert.ok(isUid(v.uid));
  }
  assert.ok(!isUid("uid_short") && !isUid("usr_AAAAAAAAAAA") && !isUid("uid_AAAAAAAAAA="));
});

test("internal tokens: HS256, role internal_service, expiry", () => {
  const secret = "token-test-secret-0123456789";
  const token = signInternalToken(secret);
  assert.equal(jwt.decode(token, { complete: true })?.header.alg, "HS256");
  assert.equal((jwt.decode(token) as { role: string }).role, "internal_service");
  assert.ok(verifyInternalToken(secret, token));
  assert.ok(!verifyInternalToken("another-secret-0123456789", token));
  assert.ok(!verifyInternalToken(secret, jwt.sign({ role: "app_user" }, secret, { expiresIn: 60 })));
  assert.ok(!verifyInternalToken(secret, jwt.sign({ role: "internal_service", exp: Math.floor(Date.now() / 1000) - 120 }, secret)));
  assert.ok(!verifyInternalToken(secret, jwt.sign({ role: "internal_service" }, secret, { algorithm: "HS512", expiresIn: 60 })));
  assert.ok(!verifyInternalToken(secret, ""));
  assert.equal(bearerToken("Bearer abc"), "abc");
  assert.equal(bearerToken("Basic abc"), "");
  assert.equal(bearerToken(undefined), "");
});
