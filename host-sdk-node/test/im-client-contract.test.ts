// im-internal.openapi.yaml in both directions: ImClient's requests and the
// real IM server's answers, every operation exercised. The host the IM server
// calls back into is this SDK's host plugin.

import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import { after, before, describe, test } from "node:test";
import Fastify from "fastify";
import jwt from "jsonwebtoken";
import { talkwireHostPlugin } from "../src/host-plugin.js";
import { ImClient, ImClientError } from "../src/im-client.js";
import { MemoryContactStore, MemoryProfileStore } from "../src/memory.js";
import { imUid } from "../src/uid.js";
import { SECRET } from "./helpers.js";
import { imServerSkip, startImServer, type RunningServer } from "./im-server.js";
import { Contract, errorsOf } from "./openapi.js";

const contract = new Contract("im-internal.openapi.yaml");
const skip = imServerSkip();

interface Exchange {
  method: string;
  path: string;
  query: Record<string, string>;
  body: unknown;
  status: number;
  text: string;
}

describe("IM server internal API against the contract", { skip }, () => {
  let server: RunningServer;
  const host = Fastify();
  const exchanges: Exchange[] = [];

  // A fetch that records each raw request and answer, so the contract is
  // checked on the wire, not on ImClient's parsed results.
  const recording = (async (input: string | URL | Request, init?: RequestInit) => {
    const url = new URL(String(input));
    const res = await fetch(input, init);
    const text = await res.text();
    exchanges.push({
      method: init?.method ?? "GET",
      path: url.pathname,
      query: Object.fromEntries(url.searchParams),
      body: init?.body ? JSON.parse(String(init.body)) : undefined,
      status: res.status,
      text,
    });
    return new Response(text, { status: res.status, headers: res.headers });
  }) as typeof fetch;

  let im: ImClient;

  before(async () => {
    await host.register(talkwireHostPlugin, { secret: SECRET, contacts: new MemoryContactStore(), profiles: new MemoryProfileStore() });
    const hostUrl = await host.listen({ port: 0, host: "127.0.0.1" });
    server = await startImServer({ secret: SECRET, hostUrl });
    im = new ImClient({ baseUrl: server.base, secret: SECRET, fetch: recording });
  });

  after(async () => {
    await server?.stop();
    await host.close();
  });

  /** Logs userId in over WebSocket and returns the open socket and its frames. */
  async function connect(userId: string) {
    const token = jwt.sign({ sub: userId, type: "access", role: "app_user" }, SECRET, { algorithm: "HS256", expiresIn: 3600 });
    const ws = new WebSocket(`ws://127.0.0.1:${server.port}/ws/im`);
    const frames: Array<Record<string, unknown>> = [];
    ws.addEventListener("message", (e) => frames.push(JSON.parse(String(e.data))));
    await new Promise((resolve, reject) => {
      ws.addEventListener("open", resolve, { once: true });
      ws.addEventListener("error", reject, { once: true });
    });
    let n = 0;
    const request = async (msg: Record<string, unknown>) => {
      const id = `q${++n}`;
      ws.send(JSON.stringify({ ...msg, id }));
      return waitFor((f) => f.id === id || (msg.action === "login" && f.action === "login"));
    };
    const waitFor = async (pred: (f: Record<string, unknown>) => boolean, ms = 3000) => {
      const deadline = Date.now() + ms;
      while (Date.now() < deadline) {
        const i = frames.findIndex(pred);
        if (i >= 0) return frames.splice(i, 1)[0]!;
        await new Promise((r) => setTimeout(r, 10));
      }
      return null;
    };
    const login = await request({ action: "login", token });
    assert.equal(login?.code, 200, JSON.stringify(login));
    return { ws, request, waitFor };
  }

  test("every exchange matches the contract, and every operation is used", async () => {
    const a = `cc-a-${randomUUID()}`;
    const b = `cc-b-${randomUUID()}`;
    const alice = await connect(a);

    const sync = await im.contactSync(a, b, "accepted");
    assert.equal(sync.uidA, imUid(a));
    assert.equal(sync.uidB, imUid(b));
    await assert.rejects(im.contactSync(a, b, "maybe" as "accepted"), (e: unknown) => e instanceof ImClientError && e.status === 400);

    assert.equal(await im.mediaRefCount("/media/none.png"), 0);

    const created = await alice.request({ action: "create_grp", name: "contract", members: [] });
    const group = created?.topic as string;
    assert.match(group, /^grp_/);
    await alice.request({ action: "sub", topic: group });
    const resolved = await im.topicMembersResolve(group, [a, b], [imUid(a), "uid_nobodynobod"]);
    assert.deepEqual(resolved.members, { [a]: true, [b]: false });
    assert.equal(resolved.resolved[imUid(a)]?.isMember, true);
    assert.equal(resolved.resolved["uid_nobodynobod"]?.appUserId, null);

    await im.customBroadcast({ topic: group }, "red_packet", { id: "p1", left: 2 });
    const card = await alice.waitFor((f) => f.action === "custom_update");
    assert.deepEqual(card, { action: "custom_update", topic: group, what: "red_packet", extra: { id: "p1", left: 2 } });
    await im.customBroadcast({ targetUid: imUid(a) }, "contact.request", { from: b });
    assert.deepEqual(await alice.waitFor((f) => f.action === "custom_update"), { action: "custom_update", what: "contact.request", extra: { from: b } });
    await assert.rejects(im.customBroadcast({ topic: group }, "Not-A-Kind", 1), (e: unknown) => e instanceof ImClientError && e.status === 400);

    await im.redPacketBroadcast(group, { id: "p1", left: 1 });
    assert.ok(await alice.waitFor((f) => f.action === "red_packet_update"));
    assert.equal(await im.callBroadcast(imUid(b), { type: "credit_balance", credits: 1 }), false);
    assert.equal(await im.callBroadcast(imUid(a), { type: "credit_balance", credits: 1 }), true);

    const topics = await im.adminTopics(imUid(a), 10);
    assert.ok(topics.some((t) => t.topic === sync.topic) && topics.some((t) => t.topic === group));
    assert.equal((await im.adminTopic(sync.topic)).isGroup, false);
    assert.deepEqual((await im.adminMessages({ topic: sync.topic, limit: 5 })).messages, []);
    const cleared = await im.adminClear(sync.topic);
    assert.equal(cleared.deletedMessages, 0);
    await assert.rejects(im.adminClear(group), (e: unknown) => e instanceof ImClientError && e.status === 409);
    await assert.rejects(im.adminTopic("p2p_does_not_exist"), (e: unknown) => e instanceof ImClientError);

    const stranger = new ImClient({ baseUrl: server.base, secret: "a-different-secret-0123456789", fetch: recording });
    await assert.rejects(stranger.mediaRefCount("/x"), (e: unknown) => e instanceof ImClientError && e.status === 401);
    alice.ws.close();

    const used = new Set<string>();
    for (const x of exchanges) {
      const where = `${x.method} ${x.path} -> ${x.status} ${x.text.trim().slice(0, 200)}`;
      used.add(`${x.method} ${x.path}`);
      const q = contract.query(x.method, x.path);
      assert.ok(q(x.query), `${where}: query ${errorsOf(q)}`);
      const body = contract.requestBody(x.method, x.path);
      if (body && x.status !== 401) assert.ok(body(x.body) || x.status === 400, `${where}: request ${errorsOf(body)}`);
      const res = contract.response(x.method, x.path, x.status);
      if (res) assert.ok(res(JSON.parse(x.text)), `${where}: response ${errorsOf(res)}`);
    }
    const unused = contract.operations().map(([m, p]) => `${m} ${p}`).filter((op) => !used.has(op));
    assert.deepEqual(unused, []);
  });
});
