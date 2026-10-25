import assert from "node:assert/strict";
import { test } from "node:test";
import { buildAppToken, readAppToken } from "../src/app-token.js";

// Output of Alibaba Cloud's Node.js demo (AliRTCSample, Server/nodejs) for
// these fixed inputs; the keys are made up.
const GOLDEN = [
  {
    input: {
      appId: "test-app-id-123",
      appKey: "test-app-key-secret-value-456",
      channelId: "imcall-abc123def456",
      userId: "uid_test_user_789",
      issuedAt: 1757500000,
      salt: 123456789,
      expiresAt: 1757500000 + 86400,
    },
    token:
      "000eJxjYGBQSD1SZpuoG3TakVta2sVCZ985nVm2P1jPNMyIEtaQvPA2lYGBgb8ktbhEN7GgQDczRdfQyDjjYFACe/RZ0YxDix8wMDAIZ+YmJ+bk6CYmJRsaGaekppmYmjEwMAiWZqbEg3TGlxanFsWbW1gyMDKMgsEEAIROJKw=",
  },
  {
    input: { appId: "a", appKey: "b", channelId: "c", userId: "d", issuedAt: 1, salt: 0, expiresAt: 1 + 86400 },
    token: "000eJxjYGBQOPbZJ8t0ubflj9j/cRciErPzl3me+s6vYbOKtyQk3zRiJwMDA2MiiGAAAcbARhCZDCJSoGKjYKgCADqAEVw=",
  },
];

test("matches Alibaba Cloud's demo byte for byte", () => {
  for (const g of GOLDEN) assert.equal(buildAppToken(g.input), g.token);
});

test("reads back what it was built from", () => {
  for (const g of GOLDEN) {
    const { appKey: _, ...rest } = g.input;
    assert.deepEqual(readAppToken(g.token), rest);
  }
  const token = buildAppToken({ appId: "app", appKey: "k", channelId: "imkit-x", userId: "uid_K9gGyX8OAK8", expiresAt: 2_000_000_000 });
  const read = readAppToken(token);
  assert.equal(read.userId, "uid_K9gGyX8OAK8");
  assert.ok(Math.abs(read.issuedAt - Date.now() / 1000) < 5);
  assert.ok(read.salt >= 0 && read.salt < read.issuedAt);
  assert.throws(() => readAppToken("001abc"), /version 000/);
});

test("the key and every field change the token", () => {
  const base = GOLDEN[1]!.input;
  const tokens = new Set([
    buildAppToken(base),
    buildAppToken({ ...base, appKey: "B" }),
    buildAppToken({ ...base, channelId: "C" }),
    buildAppToken({ ...base, userId: "D" }),
    buildAppToken({ ...base, salt: 1 }),
    buildAppToken({ ...base, expiresAt: base.expiresAt + 1 }),
  ]);
  assert.equal(tokens.size, 6);
});
