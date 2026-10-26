#!/usr/bin/env node
// End-to-end check of a running Talkwire sandbox from the public internet,
// with the real web client (web/packages/client and web/packages/call, built
// with `npm run build` in web/). Every step goes through the public address,
// so a pass means the whole path works: DNS, Cloudflare when the record is
// proxied, Caddy, the sandbox host and the IM server.
//
//   node tools/sandbox-check/check.mjs [--base https://imtalk.openedskill.com]
//
// It makes a fresh space with three users, Ann and Bo in it and Cy in another
// space, then checks: the sandbox's info and web page, space isolation,
// login and first sync, a single-chat message, a media upload sent as a
// message and downloaded again, a group round trip, joining that group by
// its QR code from the other space with the owner's approval, recall, read
// receipts, two devices of one user at once, call signaling (invite, ring,
// accept, answered, hang up, ended), clearing a chat for both sides, offline
// catch-up after both of Bo's devices were away, and presence. It stops at
// the first failure and exits 1.
//
// Everything it makes stays in its own space and is cleared by the daily reset.

import { createHash, randomBytes } from "node:crypto";
import { CallSignaling } from "../../web/packages/call/dist/index.js";
import { GroupJoinClient } from "../../web/packages/group-join/dist/index.js";
import { ChatEngine, ImClient, MediaSender, MediaUploader, MemoryChatStore, WebSocketTransport, isRecalled } from "../../web/packages/client/dist/index.js";

const argv = process.argv.slice(2);
const arg = (name, fallback) => {
  const i = argv.indexOf(name);
  return i >= 0 && argv[i + 1] ? argv[i + 1] : fallback;
};
const BASE = arg("--base", "https://imtalk.openedskill.com").replace(/\/+$/, "");
const WS_URL = BASE.replace(/^http/, "ws") + "/ws/im";
const UA = "talkwire-sandbox-check/1.0";
const TIMEOUT = Number(arg("--timeout", "20000"));

// Cloudflare refuses requests without a user agent; Node's WebSocket sends none by default.
// Node ignores a TLS client certificate request, which Chrome's WebSocket does
// not: use the diagnostic page (deploy/imtalk/ws-diag.html) for that case.
class AgentWebSocket extends WebSocket {
  constructor(url) {
    super(url, { headers: { "user-agent": UA } });
  }
}

async function until(what, check, timeoutMs = TIMEOUT) {
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    if (await check()) return;
    if (Date.now() > deadline) throw new Error("timed out waiting for " + what);
    await new Promise((r) => setTimeout(r, 25));
  }
}

function assert(ok, message) {
  if (!ok) throw new Error(message);
}

async function http(method, path, { body, token, raw } = {}) {
  const headers = { "user-agent": UA };
  if (body !== undefined) headers["content-type"] = "application/json";
  if (token) headers.authorization = `Bearer ${token}`;
  const resp = await fetch(path.startsWith("http") ? path : BASE + path, { method, headers, body: body === undefined ? undefined : JSON.stringify(body) });
  if (raw) return resp;
  const text = await resp.text();
  let json = null;
  try {
    json = JSON.parse(text);
  } catch {
    // not JSON
  }
  return { status: resp.status, json, text, headers: resp.headers };
}

const results = [];
async function step(name, fn) {
  const started = Date.now();
  try {
    const note = await fn();
    results.push({ name, ok: true, ms: Date.now() - started, note: note ?? "" });
    console.log(`  ok    ${name}  (${Date.now() - started} ms)${note ? "  " + note : ""}`);
  } catch (e) {
    results.push({ name, ok: false, ms: Date.now() - started, note: e instanceof Error ? e.message : String(e) });
    console.log(`  FAIL  ${name}  (${Date.now() - started} ms)\n        ${e instanceof Error ? e.message : String(e)}`);
    throw e;
  }
}

function device(user, deviceName) {
  const client = new ImClient({
    endpoints: [WS_URL],
    tokenProvider: () => user.accessToken,
    transport: new WebSocketTransport(AgentWebSocket),
    requestTimeoutMs: 10_000,
    publishTimeoutMs: 10_000,
    reconnectBaseDelayMs: 500,
    reconnectMaxDelayMs: 2_000,
    watchNetwork: false,
    deviceName,
  });
  const store = new MemoryChatStore();
  const engine = new ChatEngine(client, store, { appUserId: user.userId, pageSize: 20 });
  const events = [];
  engine.events.on((e) => events.push(e));
  return { user, client, store, engine, events, synced: () => events.filter((e) => e.kind === "synced").length };
}

async function sendAndConfirm(d, conversation, text) {
  const window = d.engine.messages(conversation);
  const pending = await d.engine.send(conversation, { content: text });
  await until(`"${text}" stored`, () => window.value.some((m) => m.localId === pending.localId && m.status === "sent"));
  return window.value.find((m) => m.localId === pending.localId);
}

const has = (d, conversation, pred) => d.engine.messages(conversation).value.some(pred);

// A 1x1 PNG with a random text chunk, so every run uploads different bytes.
function tinyPng() {
  const crcTable = Array.from({ length: 256 }, (_, n) => {
    let c = n;
    for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
    return c >>> 0;
  });
  const crc = (buf) => {
    let c = 0xffffffff;
    for (const b of buf) c = crcTable[(c ^ b) & 0xff] ^ (c >>> 8);
    return (c ^ 0xffffffff) >>> 0;
  };
  const chunk = (type, data) => {
    const len = Buffer.alloc(4);
    len.writeUInt32BE(data.length);
    const td = Buffer.concat([Buffer.from(type), data]);
    const sum = Buffer.alloc(4);
    sum.writeUInt32BE(crc(td));
    return Buffer.concat([len, td, sum]);
  };
  const ihdr = Buffer.from([0, 0, 0, 1, 0, 0, 0, 1, 8, 2, 0, 0, 0]);
  const idat = Buffer.from("789c63f8cfc0f01f0005000201", "hex");
  const text = Buffer.from("Comment\0talkwire-check-" + randomBytes(6).toString("hex"));
  return Buffer.concat([Buffer.from("89504e470d0a1a0a", "hex"), chunk("IHDR", ihdr), chunk("tEXt", text), chunk("IDAT", idat), chunk("IEND", Buffer.alloc(0))]);
}

const devices = [];
const signalings = [];

async function main() {
  console.log(`Talkwire sandbox check against ${BASE}\n`);
  const run = randomBytes(2).toString("hex");
  let space, otherSpace, ann, bo, cy;
  let annD, boD, boTablet, cyD, groupTopic;

  await step("sandbox info", async () => {
    const r = await http("GET", "/sandbox/info");
    assert(r.status === 200 && r.json?.sandbox === true, `GET /sandbox/info: ${r.status} ${r.text.slice(0, 120)}`);
    const ray = r.headers.get("cf-ray");
    return `${ray ? "through Cloudflare (" + ray + ")" : "direct, not through Cloudflare"}, reset ${r.json.resetTime ?? "?"}`;
  });

  await step("web page served", async () => {
    const r = await http("GET", "/");
    assert(r.status === 200 && /<div id="root"/.test(r.text), `GET /: ${r.status}`);
    const script = /src="(\/assets\/[^"]+\.js)"/.exec(r.text)?.[1];
    assert(script, "no script in the page");
    const js = await http("GET", script, { raw: true });
    assert(js.status === 200, `GET ${script}: ${js.status}`);
    await js.arrayBuffer();
    return script;
  });

  await step("no admin sign-in or admin routes", async () => {
    const a = await http("POST", "/dev/admin-login", { body: { name: "eve", role: "superadmin" } });
    const b = await http("GET", "/api/v1/admin/im/chats");
    assert(a.status === 404 && b.status === 404, `admin-login ${a.status}, admin route ${b.status}`);
  });

  await step("make two spaces and sign in three users", async () => {
    const s1 = await http("POST", "/sandbox/spaces");
    const s2 = await http("POST", "/sandbox/spaces");
    assert(s1.status === 200 && s2.status === 200, `POST /sandbox/spaces: ${s1.status} ${s1.text.slice(0, 100)}`);
    space = s1.json.space;
    otherSpace = s2.json.space;
    const login = async (name, sp) => {
      const r = await http("POST", "/dev/login", { body: { name, space: sp } });
      assert(r.status === 200 && r.json?.accessToken, `login ${name}: ${r.status} ${r.text.slice(0, 120)}`);
      return r.json;
    };
    ann = await login(`Ann_${run}`, space);
    bo = await login(`Bo_${run}`, space);
    cy = await login(`Cy_${run}`, otherSpace);
    const unknown = await http("POST", "/dev/login", { body: { name: "Dee", space: "zzzzzzzz" } });
    assert(unknown.status === 404, `unknown space gave ${unknown.status}`);
    return `space ${space}, other ${otherSpace}`;
  });

  await step("contacts only within a space", async () => {
    const uids = async (u) => (await http("GET", "/api/v1/chat/contacts?status=accepted", { token: u.accessToken })).json.items.map((c) => c.uid);
    const a = await uids(ann);
    const c = await uids(cy);
    assert(a.length === 1 && a[0] === bo.uid, `Ann's contacts: ${JSON.stringify(a)}`);
    assert(c.length === 0, `Cy's contacts: ${JSON.stringify(c)}`);
  });

  await step("login and first sync over WebSocket", async () => {
    annD = device(ann, "check-web");
    boD = device(bo, "check-web");
    devices.push(annD, boD);
    for (const d of [annD, boD]) await d.engine.open();
    await annD.engine.ensureConversation(bo.uid, "Bo");
    await boD.engine.ensureConversation(ann.uid, "Ann");
    annD.client.start();
    boD.client.start();
    await until("first sync", () => annD.synced() >= 1 && boD.synced() >= 1);
    const s = annD.client.session;
    assert(s?.uid === ann.uid, `session uid ${s?.uid}`);
    return `protocol ${s.protocol}`;
  });

  await step("single-chat message", async () => {
    const m = await sendAndConfirm(annD, bo.uid, "hello from the check");
    await until("Bo receives", () => has(boD, ann.uid, (x) => x.content === "hello from the check" && x.status === "received"));
    return `seq ${m.seq}`;
  });

  await step("another space cannot message in", async () => {
    cyD = device(cy, "check-web");
    devices.push(cyD);
    await cyD.engine.open();
    cyD.client.start();
    await until("Cy synced", () => cyD.synced() >= 1);
    const reply = await cyD.client.publish(ann.uid, { content: "should not arrive", contentType: "text" }, "check-" + run + "-cy");
    assert(reply.kind === "refused", `Cy's message to Ann: ${JSON.stringify(reply)}`);
    return `refused with ${reply.reply.code}`;
  });

  await step("media upload sent as a message and downloaded", async () => {
    const png = tinyPng();
    const sender = new MediaSender(annD.engine, new MediaUploader(BASE, () => ann.accessToken, { fetch: (u, init) => fetch(u, { ...init, headers: { ...init?.headers, "user-agent": UA } }) }));
    const res = await sender.sendFile(bo.uid, new File([png], "check.png", { type: "image/png" }));
    assert(res.kind === "sent", `sendFile: ${res.kind} ${res.error instanceof Error ? res.error.message : ""}`);
    await until("Bo receives the image", () => has(boD, ann.uid, (x) => x.fileUrl && x.status === "received"));
    const got = boD.engine.messages(ann.uid).value.find((x) => x.fileUrl);
    const r = await fetch(got.fileUrl, { headers: { "user-agent": UA, authorization: `Bearer ${bo.accessToken}` } });
    assert(r.status === 200, `GET ${got.fileUrl}: ${r.status}`);
    const bytes = Buffer.from(await r.arrayBuffer());
    const same = createHash("sha256").update(bytes).digest("hex") === createHash("sha256").update(png).digest("hex");
    assert(same, `downloaded ${bytes.length} bytes, sent ${png.length}`);
    return `${png.length} bytes, ${got.contentType}`;
  });

  await step("group round trip", async () => {
    const topic = await annD.client.createGroup(`check ${run}`, [bo.uid]);
    await until("Bo learns of the group", () => boD.engine.conversations.value.some((c) => c.id === topic));
    await sendAndConfirm(annD, topic, "to the group");
    await until("Bo receives the group message", () => has(boD, topic, (x) => x.content === "to the group"));
    const roles = [await annD.engine.myRole(topic), await boD.engine.myRole(topic)];
    assert(roles[0] === "owner" && roles[1] === "member", `roles ${roles}`);
    groupTopic = topic;
    return topic;
  });

  await step("join the group by QR code from the other space, with approval", async () => {
    // A group QR code is shared on purpose, so it works across spaces; no friendship needed.
    const annJoin = new GroupJoinClient(annD.client);
    const cyJoin = new GroupJoinClient(cyD.client);
    signalings.push(annJoin, cyJoin);
    const requests = [];
    annJoin.events.on((e) => requests.push(e));
    assert((await annJoin.setRequireApproval(groupTopic, true)) === true, "approval not turned on");
    const { qrToken } = await annJoin.qrToken(groupTopic);
    const preview = await cyJoin.resolve(qrToken);
    assert(preview && preview.topic === groupTopic && preview.requireApproval && !preview.alreadyMember, `preview ${JSON.stringify(preview)}`);
    const asked = await cyJoin.join(qrToken, "from the check");
    assert(asked.status === "pending_approval", `join: ${asked.status}`);
    await until("Ann is asked", () => requests.some((e) => e.kind === "request" && e.topic === groupTopic));
    const pending = await annJoin.requests(groupTopic);
    const mine = pending.find((r) => r.requesterUid === cy.uid);
    assert(mine, `no request from Cy in ${JSON.stringify(pending)}`);
    assert((await annJoin.review(groupTopic, mine.id, true)) === "approved", "not approved");
    await until("Cy learns of the group", () => cyD.engine.conversations.value.some((c) => c.id === groupTopic));
    await sendAndConfirm(cyD, groupTopic, "hello from the other space");
    await until("Ann receives Cy's group message", () => has(annD, groupTopic, (x) => x.content === "hello from the other space"));
    return `${preview.memberCount} members before`;
  });

  await step("recall", async () => {
    const target = await sendAndConfirm(annD, bo.uid, "to be recalled");
    await until("Bo has it", () => has(boD, ann.uid, (x) => x.seq === target.seq));
    await annD.engine.recall(target);
    await until("recall reaches Bo", () => has(boD, ann.uid, (x) => x.seq === target.seq && isRecalled(x)));
  });

  await step("read receipts", async () => {
    await boD.engine.markRead(ann.uid);
    const seqs = boD.engine.messages(ann.uid).value.flatMap((m) => (m.seq === null ? [] : [m.seq]));
    const max = Math.max(...seqs);
    await until("Ann sees the read position", async () => (await annD.store.conversation(bo.uid))?.peerReadSeq === max);
    assert(boD.engine.conversation(ann.uid)?.unread === 0, "Bo still has unread");
    return `read up to ${max}`;
  });

  await step("two devices of one user at once", async () => {
    boTablet = device(bo, "check-tablet");
    devices.push(boTablet);
    await boTablet.engine.open();
    await boTablet.engine.ensureConversation(ann.uid, "Ann");
    boTablet.client.start();
    await until("tablet sync", () => boTablet.synced() >= 1);
    assert(boD.client.isConnected, "the first device was dropped when the second signed in");
    await sendAndConfirm(annD, bo.uid, "to both devices");
    await until("both devices receive", () => has(boD, ann.uid, (x) => x.content === "to both devices") && has(boTablet, ann.uid, (x) => x.content === "to both devices"));
  });

  await step("call signaling: invite, ring, accept, answered, hang up, ended", async () => {
    const annCalls = new CallSignaling(annD.client);
    const boCalls = new CallSignaling(boD.client);
    signalings.push(annCalls, boCalls);
    const annSignals = [];
    const boSignals = [];
    annCalls.signals.on((s) => annSignals.push(s));
    boCalls.signals.on((s) => boSignals.push(s));
    const invite = await annCalls.invite(bo.uid, "voice", "check-" + run);
    assert(invite.code === 200, `call_invite: ${invite.code} ${invite.errCode ?? ""} ${invite.reason ?? ""}`);
    const id = invite.frame.callSessionId;
    assert(typeof id === "string" && id, "no callSessionId");
    await until("Bo rings", () => boSignals.some((s) => s.kind === "ring" && s.callSessionId === id));
    const accept = await boCalls.accept(id);
    assert(accept.code === 200, `call_accept: ${accept.code} ${accept.errCode ?? ""} ${accept.reason ?? ""}`);
    await until("Ann is answered", () => annSignals.some((s) => s.kind === "answered" && s.callSessionId === id));
    const hang = await annCalls.hangup(id);
    assert(hang.code === 200, `call_hangup: ${hang.code}`);
    await until("Bo is told it ended", () => boSignals.some((s) => s.kind === "ended" && s.callSessionId === id));
    return id;
  });

  await step("clear a chat for both sides", async () => {
    await annD.engine.clear(bo.uid);
    assert(annD.engine.messages(bo.uid).value.length === 0, "Ann still has messages");
    await until("the clear reaches Bo", () => boD.engine.messages(ann.uid).value.length === 0);
    await until("Bo is told", () => boD.events.some((e) => e.kind === "conversationCleared" && e.conversation === ann.uid));
    await sendAndConfirm(annD, bo.uid, "after the clear");
    await until("Bo receives after the clear", () => boD.engine.messages(ann.uid).value.map((m) => m.content).join() === "after the clear");
  });

  await step("offline catch-up and presence", async () => {
    await annD.engine.subscribe(bo.uid);
    boD.client.stop();
    boTablet.client.stop();
    await until("Bo offline for Ann", () => annD.engine.presence.state(bo.uid) === "offline");
    for (let i = 0; i < 3; i++) await sendAndConfirm(annD, bo.uid, `while away ${i}`);
    const before = boD.synced();
    boD.client.start();
    await until("Bo's sync", () => boD.synced() > before);
    await until("Bo has what was sent while away", () => [0, 1, 2].every((i) => has(boD, ann.uid, (x) => x.content === `while away ${i}`)));
    await until("Bo online for Ann", () => annD.engine.presence.state(bo.uid) === "online");
  });
}

let failed = false;
try {
  await main();
} catch {
  failed = true;
} finally {
  for (const s of signalings) s.dispose();
  for (const d of devices) {
    d.engine.close();
    d.client.stop();
  }
}
const ok = results.filter((r) => r.ok).length;
console.log(`\n${ok}/${results.length} steps passed${failed ? " (stopped at the first failure)" : ""}`);
process.exit(failed ? 1 : 0);
