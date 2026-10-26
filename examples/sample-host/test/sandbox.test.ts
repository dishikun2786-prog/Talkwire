import assert from "node:assert/strict";
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { test } from "node:test";
import { imUid } from "@talkwire/host-node";
import { buildSandboxHost, sandboxUserId, type SandboxLimits } from "../src/sandbox.ts";

const secret = "x".repeat(40);

async function sandbox(limits: Partial<SandboxLimits> = {}, now?: () => number) {
  // No IM server listens here: contact-sync fails and must not break sign-in.
  return buildSandboxHost({
    secret,
    imBaseUrl: "http://127.0.0.1:59999",
    mediaDir: mkdtempSync(path.join(tmpdir(), "talkwire-sandbox-")),
    resetTime: "04:00 Asia/Shanghai",
    limits,
    ...(now ? { now } : {}),
  });
}

const fromIp = (ip: string) => ({ "cf-connecting-ip": ip });

async function newSpace(app: Awaited<ReturnType<typeof sandbox>>, ip = "198.51.100.1"): Promise<string> {
  const res = await app.inject({ method: "POST", url: "/sandbox/spaces", headers: fromIp(ip) });
  assert.equal(res.statusCode, 200);
  const { space } = res.json() as { space: string };
  assert.match(space, /^[a-z0-9]{8}$/);
  return space;
}

async function login(app: Awaited<ReturnType<typeof sandbox>>, name: string, space: string, ip = "198.51.100.1") {
  return app.inject({ method: "POST", url: "/dev/login", headers: fromIp(ip), payload: { name, space } });
}

test("only users of the same space become contacts", async () => {
  const app = await sandbox();
  const one = await newSpace(app);
  const two = await newSpace(app);
  const ann = (await login(app, "Ann", one)).json();
  const bo = (await login(app, "Bo", one)).json();
  const cy = (await login(app, "Cy", two)).json();
  assert.equal(ann.userId, sandboxUserId(one, "Ann"));
  assert.equal(ann.uid, imUid(sandboxUserId(one, "Ann")));
  const contactsOf = async (token: string) =>
    ((await app.inject({ method: "GET", url: "/api/v1/chat/contacts", headers: { authorization: `Bearer ${token}` } })).json().items as Array<{ uid: string }>).map((c) => c.uid);
  assert.deepEqual(await contactsOf(ann.accessToken), [bo.uid]);
  assert.deepEqual(await contactsOf(cy.accessToken), []);
  // The same name in another space is another user.
  const ann2 = (await login(app, "Ann", two)).json();
  assert.notEqual(ann2.uid, ann.uid);
  assert.deepEqual(await contactsOf(ann2.accessToken), [cy.uid]);
  await app.close();
});

test("sign-in needs a space that exists", async () => {
  const app = await sandbox();
  assert.equal((await app.inject({ method: "POST", url: "/dev/login", payload: { name: "Ann" } })).statusCode, 400);
  assert.equal((await login(app, "Ann", "nosuch12")).statusCode, 404);
  assert.equal((await login(app, "A", await newSpace(app))).statusCode, 400);
  await app.close();
});

test("there is no administrator sign-in and no admin routes", async () => {
  const app = await sandbox();
  assert.equal((await app.inject({ method: "POST", url: "/dev/admin-login", payload: { name: "eve", role: "superadmin" } })).statusCode, 404);
  assert.equal((await app.inject({ method: "GET", url: "/api/v1/admin/im/chats" })).statusCode, 404);
  await app.close();
});

test("making spaces and signing in are rate limited per client address", async () => {
  let t = 1_000_000;
  const app = await sandbox({ spacesPerHour: 2, loginsPerMinute: 3 }, () => t);
  await newSpace(app, "203.0.113.7");
  const space = await newSpace(app, "203.0.113.7");
  assert.equal((await app.inject({ method: "POST", url: "/sandbox/spaces", headers: fromIp("203.0.113.7") })).statusCode, 429);
  // Another address has its own budget.
  await newSpace(app, "203.0.113.8");
  for (let i = 0; i < 3; i++) assert.equal((await login(app, "Ann", space, "203.0.113.7")).statusCode, 200);
  assert.equal((await login(app, "Ann", space, "203.0.113.7")).statusCode, 429);
  t += 61_000;
  assert.equal((await login(app, "Ann", space, "203.0.113.7")).statusCode, 200);
  await app.close();
});

test("a space holds a limited number of users; returning users still get in", async () => {
  const app = await sandbox({ usersPerSpace: 2 });
  const space = await newSpace(app);
  assert.equal((await login(app, "Ann", space)).statusCode, 200);
  assert.equal((await login(app, "Bo", space)).statusCode, 200);
  assert.equal((await login(app, "Cy", space)).statusCode, 409);
  assert.equal((await login(app, "Ann", space)).statusCode, 200);
  await app.close();
});

test("info says it is a sandbox and when it is cleared", async () => {
  const app = await sandbox();
  const info = (await app.inject({ method: "GET", url: "/sandbox/info" })).json();
  assert.equal(info.sandbox, true);
  assert.equal(info.resetTime, "04:00 Asia/Shanghai");
  assert.equal(typeof info.limits.maxUploadBytes, "number");
  await app.close();
});

test("uploads over the size limit are refused", async () => {
  const app = await sandbox({ maxUploadBytes: 1024 });
  const ann = (await login(app, "Ann", await newSpace(app))).json();
  const boundary = "----talkwire";
  const big = Buffer.alloc(4096, 1);
  const body = Buffer.concat([
    Buffer.from(`--${boundary}\r\nContent-Disposition: form-data; name="file"; filename="a.png"\r\nContent-Type: image/png\r\n\r\n`),
    big,
    Buffer.from(`\r\n--${boundary}--\r\n`),
  ]);
  const res = await app.inject({
    method: "POST",
    url: "/api/v1/chat/upload",
    headers: { authorization: `Bearer ${ann.accessToken}`, "content-type": `multipart/form-data; boundary=${boundary}` },
    payload: body,
  });
  assert.equal(res.statusCode, 413);
  await app.close();
});
