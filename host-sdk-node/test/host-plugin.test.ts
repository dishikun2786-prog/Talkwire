// The endpoints the IM server calls, checked against host.openapi.yaml.

import assert from "node:assert/strict";
import { describe, test } from "node:test";
import Fastify, { type FastifyInstance, type LightMyRequestResponse } from "fastify";
import jwt from "jsonwebtoken";
import { imkitHostPlugin, type CallHandlers } from "../src/host-plugin.js";
import { MemoryContactStore, MemoryProfileStore, staticConfig } from "../src/memory.js";
import { signInternalToken } from "../src/token.js";
import { addContact, respondToContact, setAlias } from "../src/contacts.js";
import { Contract, errorsOf } from "./openapi.js";
import { SECRET, internalAuth } from "./helpers.js";

const contract = new Contract("host.openapi.yaml");
const BASE = "/api/v1/internal/im";
const covered = new Set<string>();

/** Asserts the response is one the contract allows for this operation. */
function conforms(method: string, route: string, res: LightMyRequestResponse) {
  covered.add(`${method} ${route}`);
  const validate = contract.response(method, route, res.statusCode);
  if (validate) assert.ok(validate(res.json()), `${method} ${route} ${res.statusCode}: ${errorsOf(validate)} in ${res.body}`);
}

async function app(options: { calls?: CallHandlers; groupAi?: boolean; config?: boolean; deleted?: string[] } = {}) {
  const contacts = new MemoryContactStore();
  const profiles = new MemoryProfileStore();
  const a = Fastify();
  await a.register(imkitHostPlugin, {
    secret: SECRET,
    contacts,
    profiles,
    config: options.config ? staticConfig({ runtime: { messageRateLimitPer10s: 30 }, e2ee: { e2eeEnabled: true } }) : undefined,
    deleteMedia: options.deleted
      ? async (urls) => {
          options.deleted!.push(...urls);
          return urls.length;
        }
      : undefined,
    calls: options.calls,
    groupAi: options.groupAi
      ? async (q) =>
          q.question.includes("tomorrow")
            ? { ok: false, reason: "quota_exceeded", message: "Come back tomorrow" }
            : q.question.includes("quota")
              ? { ok: false, reason: "quota_exceeded" }
              : { ok: true, answer: "42" }
      : undefined,
  });
  return { app: a, contacts, profiles };
}

const get = (a: FastifyInstance, path: string, query: Record<string, string>, headers: Record<string, string> = internalAuth()) =>
  a.inject({ method: "GET", url: path, query, headers });
const post = (a: FastifyInstance, path: string, payload: unknown, headers: Record<string, string> = internalAuth()) =>
  a.inject({ method: "POST", url: path, payload: payload as object, headers });

describe("host plugin", () => {
  // R13: every request needs an unexpired internal_service token signed with
  // the shared secret: missing, not a JWT, another secret, expired and another
  // role are all refused.
  test("R13_HostEndpointsRejectBadTokens", async () => {
    const { app: a } = await app({ config: true, deleted: [] });
    const route = "/api/v1/internal/im/friendship";
    const bad: Array<Record<string, string>> = [
      {},
      { authorization: "Bearer nonsense" },
      { authorization: `Bearer ${signInternalToken("another-secret-0123456789")}` },
      { authorization: `Bearer ${signInternalToken(SECRET, -60)}` },
      { authorization: `Bearer ${jwt.sign({ role: "app_user" }, SECRET, { algorithm: "HS256", expiresIn: 60 })}` },
    ];
    for (const headers of bad) {
      const res = await get(a, route, { a: "x", b: "y" }, headers);
      assert.equal(res.statusCode, 401);
      conforms("GET", route, res);
    }
  });

  test("friendship follows the contact store", async () => {
    const { app: a, contacts } = await app();
    const route = `${BASE}/friendship`;
    let res = await get(a, route, { a: "alice", b: "bob" });
    assert.deepEqual(res.json(), { friends: false });
    conforms("GET", route, res);
    const req = await addContact(contacts, "alice", "bob");
    assert.ok(req.ok);
    res = await get(a, route, { a: "bob", b: "alice" });
    assert.deepEqual(res.json(), { friends: false }, "pending is not friends");
    await respondToContact(contacts, "bob", req.value.contact.id, "accepted");
    res = await get(a, route, { a: "bob", b: "alice" });
    assert.deepEqual(res.json(), { friends: true });
    conforms("GET", route, res);
    res = await get(a, route, { a: "alice" });
    assert.equal(res.statusCode, 400);
    conforms("GET", route, res);
  });

  // R12: the profile of a sender as one viewer sees it: the viewer's own
  // remark first, then the sender's name; unknown senders get empty strings.
  // Remarks go by direction: neither side sees the other's.
  test("R12_ProfileAliasByDirection", async () => {
    const { app: a, contacts, profiles } = await app();
    const route = `${BASE}/profile`;
    let res = await get(a, route, { viewer: "bob", sender: "alice" });
    assert.deepEqual(res.json(), { displayName: "", avatarUrl: "" });
    conforms("GET", route, res);
    await profiles.create({ userId: "alice", handle: "alice", displayName: "Alice", avatarUrl: "https://cdn/a.png" });
    res = await get(a, route, { viewer: "bob", sender: "alice" });
    assert.deepEqual(res.json(), { displayName: "Alice", avatarUrl: "https://cdn/a.png" });
    const req = await addContact(contacts, "alice", "bob");
    assert.ok(req.ok);
    await respondToContact(contacts, "bob", req.value.contact.id, "accepted");
    await setAlias(contacts, "bob", req.value.contact.id, "My Alice");
    await setAlias(contacts, "alice", req.value.contact.id, "Bobby");
    res = await get(a, route, { viewer: "bob", sender: "alice" });
    assert.equal(res.json().displayName, "My Alice", "bob sees his remark for alice");
    await profiles.create({ userId: "bob", handle: "bob", displayName: "Bob", avatarUrl: null });
    res = await get(a, route, { viewer: "alice", sender: "bob" });
    assert.equal(res.json().displayName, "Bobby", "alice sees her remark for bob, not his for her");
    res = await get(a, route, { viewer: "carol", sender: "alice" });
    assert.equal(res.json().displayName, "Alice", "nobody else sees bob's remark");
    conforms("GET", route, res);
  });

  test("configuration documents answer 404 until the host has one", async () => {
    for (const config of [false, true]) {
      const { app: a } = await app({ config });
      for (const name of ["runtime-config", "e2ee-config"]) {
        const res = await get(a, `${BASE}/${name}`, {});
        assert.equal(res.statusCode, config ? 200 : 404);
        conforms("GET", `${BASE}/${name}`, res);
      }
    }
  });

  test("delete-media hands the URLs to the host", async () => {
    const deleted: string[] = [];
    const { app: a } = await app({ deleted });
    const route = `${BASE}/delete-media`;
    const res = await post(a, route, { urls: ["/media/x", "/media/y"] });
    assert.deepEqual(res.json(), { deleted: 2 });
    assert.deepEqual(deleted, ["/media/x", "/media/y"]);
    conforms("POST", route, res);
    const bad = await post(a, route, { urls: "x" });
    assert.equal(bad.statusCode, 400);
    conforms("POST", route, bad);
    const { app: none } = await app();
    assert.deepEqual((await post(none, route, { urls: ["/media/x"] })).json(), { deleted: 0 });
  });

  test("call module: answered by the host's handlers, 404 when not served", async () => {
    const calls: CallHandlers = {
      create: async (r) => (r.callerAppUserId === "blocked" ? { ok: false, reason: "not_friends" } : { ok: true }),
      answer: async () => ({ ok: true, channelId: "ch", callerToken: "t1", calleeToken: "t2", videoQuality: "sd", denoiseEnabled: true, appId: "app" }),
      settle: async () => {},
      upgrade: async () => ({ ok: true, videoQuality: "hd" }),
    };
    const { app: a } = await app({ calls });
    const cases: Array<[string, unknown]> = [
      [`${BASE}/calls/create`, { callSessionId: "cs1", callerAppUserId: "alice", calleeAppUserId: "bob", callType: "voice" }],
      [`${BASE}/calls/create`, { callSessionId: "cs1", callerAppUserId: "blocked", calleeAppUserId: "bob", callType: "video" }],
      [`${BASE}/calls/answer`, { callSessionId: "cs1", effectiveType: "voice" }],
      [`${BASE}/calls/settle`, { callSessionId: "cs1", reason: "user_hangup" }],
      [`${BASE}/calls/upgrade`, { callSessionId: "cs1", toType: "video" }],
    ];
    for (const [route, body] of cases) {
      const res = await post(a, route, body);
      assert.equal(res.statusCode, 200, `${route}: ${res.body}`);
      conforms("POST", route, res);
      const bad = await post(a, route, {});
      assert.equal(bad.statusCode, 400);
      conforms("POST", route, bad);
    }
    const { app: without } = await app();
    const res = await post(without, `${BASE}/calls/create`, cases[0]![1]);
    assert.equal(res.statusCode, 404);
    conforms("POST", `${BASE}/calls/create`, res);
  });

  test("group assistant: an answer, or a business refusal with or without the host's wording", async () => {
    const { app: a } = await app({ groupAi: true });
    const route = `${BASE}/group-ai-qa`;
    const q = { groupTopic: "grp_x", askerImUid: "uid_aaaaaaaaaaa", question: "why?", groupOwnerAppUserId: "owner", kbIds: [], docIds: [] };
    let res = await post(a, route, q);
    assert.deepEqual(res.json(), { ok: true, answer: "42" });
    conforms("POST", route, res);
    res = await post(a, route, { ...q, question: "over quota" });
    assert.deepEqual(res.json(), { ok: false, reason: "quota_exceeded" });
    conforms("POST", route, res);
    // A refusal in the host's own words.
    res = await post(a, route, { ...q, question: "again tomorrow?" });
    assert.deepEqual(res.json(), { ok: false, reason: "quota_exceeded", message: "Come back tomorrow" });
    conforms("POST", route, res);
    const { app: without } = await app();
    res = await post(without, route, q);
    assert.equal(res.statusCode, 404);
    conforms("POST", route, res);
  });

  test("every operation in the contract was exercised", () => {
    const all = contract.operations().map(([m, p]) => `${m} ${p}`);
    assert.deepEqual(all.filter((op) => !covered.has(op)), []);
  });
});

test("request bodies are limited", async () => {
  const { app: a } = await app({ deleted: [] });
  const huge = { urls: Array.from({ length: 30_000 }, (_, i) => `/media/${i}-${"x".repeat(20)}`) };
  const res = await post(a, `${BASE}/delete-media`, huge);
  assert.equal(res.statusCode, 413);
});
