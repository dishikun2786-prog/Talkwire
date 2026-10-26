import assert from "node:assert/strict";
import { test } from "node:test";
import Fastify from "fastify";
import { ImClient } from "@talkwire/host-node";
import { redPacketRoutes } from "../src/red-packets.js";

const GROUP = "grp_lucky";
const MEMBERS = new Set(["alice", "bob", "carol"]);

/** An IM server stand-in: answers membership, records broadcasts. */
function fakeIm() {
  const broadcasts: Array<{ topic?: string; kind: string; payload: Record<string, unknown> }> = [];
  const fetchStub = (async (input: string | URL | Request, init?: RequestInit) => {
    const path = new URL(String(input)).pathname;
    const body = JSON.parse(String(init?.body ?? "{}"));
    if (path.endsWith("/topic-members-resolve")) {
      const members = Object.fromEntries((body.checkAppUserIds as string[]).map((u) => [u, body.topic === GROUP && MEMBERS.has(u)]));
      return Response.json({ members, resolved: {} });
    }
    if (path.endsWith("/custom-broadcast")) {
      broadcasts.push(body);
      return Response.json({ ok: true });
    }
    return new Response("404 page not found", { status: 404 });
  }) as typeof fetch;
  return { im: new ImClient({ baseUrl: "http://im.test", secret: "example-secret-0123456789", fetch: fetchStub }), broadcasts };
}

async function setup() {
  const { im, broadcasts } = fakeIm();
  const app = Fastify();
  await app.register(redPacketRoutes, { im, authenticate: async (req) => (req.headers["x-user"] as string | undefined) ?? null });
  const as = (user: string) => ({
    post: (url: string, payload: object = {}) => app.inject({ method: "POST", url, payload, headers: { "x-user": user } }),
    get: (url: string) => app.inject({ method: "GET", url, headers: { "x-user": user } }),
  });
  return { as, broadcasts };
}

test("send, publish, claim: every claim pushes the card's new state", async () => {
  const { as, broadcasts } = await setup();
  const created = await as("alice").post("/api/v1/red-packets", { topic: GROUP, total: 100, count: 3 });
  assert.equal(created.statusCode, 200, created.body);
  const { packet, message } = created.json();
  assert.deepEqual(message, { contentType: "red_packet", content: "[Red packet]", extra: { redPacketId: packet.id } });

  const bob = await as("bob").post(`/api/v1/red-packets/${packet.id}/claim`);
  assert.equal(bob.json().myAmount, 33);
  assert.equal((await as("bob").post(`/api/v1/red-packets/${packet.id}/claim`)).json().error, "already claimed");
  await as("carol").post(`/api/v1/red-packets/${packet.id}/claim`);
  const last = await as("alice").post(`/api/v1/red-packets/${packet.id}/claim`);
  assert.equal(last.json().myAmount, 34, "the last share takes the remainder");
  assert.equal(last.json().remaining, 0);

  assert.equal(broadcasts.length, 3);
  assert.ok(broadcasts.every((b) => b.topic === GROUP && b.kind === "red_packet"));
  assert.deepEqual(broadcasts.map((b) => b.payload.claimed), [1, 2, 3]);
  assert.ok(broadcasts.every((b) => b.payload.myAmount === null), "the broadcast does not reveal anyone's share");
});

test("a client opening the card fetches its state", async () => {
  const { as } = await setup();
  const { packet } = (await as("alice").post("/api/v1/red-packets", { topic: GROUP, total: 10, count: 2 })).json();
  await as("bob").post(`/api/v1/red-packets/${packet.id}/claim`);
  assert.equal((await as("bob").get(`/api/v1/red-packets/${packet.id}`)).json().myAmount, 5);
  assert.equal((await as("carol").get(`/api/v1/red-packets/${packet.id}`)).json().myAmount, null);
  assert.equal((await as("mallory").get(`/api/v1/red-packets/${packet.id}`)).statusCode, 404, "not a member");
});

test("refused: outsiders, bad amounts, all claimed", async () => {
  const { as } = await setup();
  assert.equal((await as("mallory").post("/api/v1/red-packets", { topic: GROUP, total: 10, count: 2 })).statusCode, 403);
  assert.equal((await as("alice").post("/api/v1/red-packets", { topic: "p2p_x", total: 10, count: 2 })).statusCode, 400);
  assert.equal((await as("alice").post("/api/v1/red-packets", { topic: GROUP, total: 1, count: 2 })).statusCode, 400);
  const { packet } = (await as("alice").post("/api/v1/red-packets", { topic: GROUP, total: 2, count: 1 })).json();
  await as("bob").post(`/api/v1/red-packets/${packet.id}/claim`);
  assert.equal((await as("carol").post(`/api/v1/red-packets/${packet.id}/claim`)).json().error, "all claimed");
  assert.equal((await as("mallory").post(`/api/v1/red-packets/${packet.id}/claim`)).statusCode, 404);
});
