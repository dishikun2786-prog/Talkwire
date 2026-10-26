// A host built from this SDK for the conformance tool: the IM server talks to
// it instead of the tool's fake host, and the tool drives it through the
// conformance control API (tools/conformance/README.md). The golden
// transcripts then check the SDK's host side end to end.
//
//   node --import tsx conformance/host.ts --port 17101 [--store memory|prisma]
//
// The secret is TALKWIRE_HOST_SECRET (the tool's test secret). --store prisma
// needs DATABASE_URL with the tables of prisma/talkwire.prisma. The business
// answers of the call and group assistant modules are the fake host's, since
// they belong to the host, not to the SDK.

import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { parseArgs } from "node:util";
import Fastify, { type FastifyRequest } from "fastify";
import { defaultHandle } from "../src/app-routes.js";
import { talkwireHostPlugin, type CallHandlers } from "../src/host-plugin.js";
import { LocalDiskBlobStore } from "../src/media/local.js";
import { mediaDeleter } from "../src/media/media.js";
import { MemoryContactStore, MemoryProfileStore } from "../src/memory.js";
import { bearerToken, verifyInternalToken } from "../src/token.js";
import type { ConfigSource, ContactStore, ProfileStore } from "../src/types.js";

const { values } = parseArgs({ options: { port: { type: "string", default: "17101" }, store: { type: "string", default: "memory" } } });
const secret = process.env.TALKWIRE_HOST_SECRET;
if (!secret) throw new Error("set TALKWIRE_HOST_SECRET to the conformance tool's secret");
const CONTROL = "/__conformance";

interface Stores {
  profiles: ProfileStore;
  contacts: ContactStore;
  reset(): Promise<void>;
  close(): Promise<void>;
}

async function memoryStores(): Promise<Stores> {
  let profiles = new MemoryProfileStore();
  let contacts = new MemoryContactStore();
  // The plugin keeps the objects it was given; forward to the current ones.
  const forward = <T extends object>(current: () => T): T =>
    new Proxy({} as T, {
      get: (_t, prop) => {
        const target = current() as Record<string | symbol, unknown>;
        const value = target[prop];
        return typeof value === "function" ? (value as (...a: unknown[]) => unknown).bind(target) : value;
      },
    });
  return {
    profiles: forward(() => profiles),
    contacts: forward(() => contacts),
    reset: async () => {
      profiles = new MemoryProfileStore();
      contacts = new MemoryContactStore();
    },
    close: async () => {},
  };
}

async function prismaStores(): Promise<Stores> {
  const { PrismaClient } = await import("@prisma/client");
  const { prismaStores: make } = await import("../src/prisma/index.js");
  const prisma = new PrismaClient();
  const stores = make(prisma);
  return {
    ...stores,
    reset: async () => {
      await prisma.$executeRawUnsafe("TRUNCATE talkwire_profiles, talkwire_contacts, talkwire_admin_audit");
    },
    close: () => prisma.$disconnect(),
  };
}

const stores = values.store === "prisma" ? await prismaStores() : await memoryStores();
let documents: { runtime: Record<string, unknown> | null; e2ee: Record<string, unknown> | null } = { runtime: null, e2ee: null };
const config: ConfigSource = { runtimeConfig: async () => documents.runtime, e2eeConfig: async () => documents.e2ee };

const calls: CallHandlers = {
  create: async (r) => {
    const pair = await stores.contacts.findPair(r.callerAppUserId, r.calleeAppUserId);
    return pair?.status === "accepted" ? { ok: true } : { ok: false, reason: "not_friends" };
  },
  answer: async () => ({
    ok: true,
    channelId: "fake-channel",
    callerToken: "fake-rtc-caller",
    calleeToken: "fake-rtc-callee",
    videoQuality: "sd",
    denoiseEnabled: true,
    appId: "fake-app",
  }),
  settle: async () => {},
  upgrade: async () => ({ ok: true, videoQuality: "hd" }),
};

interface Call {
  method: string;
  path: string;
  query?: Record<string, string>;
  body?: unknown;
  status: number;
}
let recorded: Call[] = [];
const periodic = (p: string) => p.endsWith("/runtime-config") || p.endsWith("/e2ee-config");

const app = Fastify();
app.addHook("onResponse", async (request, reply) => {
  const pathname = request.url.split("?")[0]!;
  if (pathname.startsWith(CONTROL) || periodic(pathname)) return;
  const call: Call = { method: request.method, path: pathname, status: reply.statusCode };
  const query = Object.entries((request.query ?? {}) as Record<string, string | string[]>)
    .map(([k, v]) => [k, Array.isArray(v) ? v[0]! : v] as const)
    .sort(([a], [b]) => (a < b ? -1 : 1));
  if (query.length) call.query = Object.fromEntries(query);
  if (request.body !== undefined && request.body !== null) call.body = request.body;
  recorded.push(call);
});

await app.register(talkwireHostPlugin, {
  secret,
  contacts: stores.contacts,
  profiles: stores.profiles,
  config,
  deleteMedia: mediaDeleter(new LocalDiskBlobStore(mkdtempSync(path.join(tmpdir(), "talkwire-conformance-media-")))),
  calls,
  groupAi: async (q) => (q.question.includes("quota") ? { ok: false, reason: "quota_exceeded" } : { ok: true, answer: "fake answer" }),
});

// -- the control API --

const control = (request: FastifyRequest) => verifyInternalToken(secret, bearerToken(request.headers.authorization));
const route = <B>(name: string, handle: (body: B) => Promise<unknown>) =>
  app.post(`${CONTROL}/${name}`, async (request, reply) => {
    if (!control(request)) return reply.status(401).send({ error: "unauthorized" });
    return (await handle((request.body ?? {}) as B)) ?? {};
  });

route("reset", async () => {
  await stores.reset();
  documents = { runtime: null, e2ee: null };
  recorded = [];
});

route<{ a: string; b: string; friends: boolean }>("friends", async ({ a, b, friends }) => {
  await stores.contacts.withPair(a, b, async () => {
    const pair = await stores.contacts.findPair(a, b);
    if (friends && !pair) await stores.contacts.create({ requesterId: a, recipientId: b, status: "accepted" });
    else if (friends && pair) await stores.contacts.update(pair.id, { status: "accepted" });
    else if (!friends && pair) await stores.contacts.delete(pair.id);
  });
});

route<{ user: string; displayName: string; avatarUrl: string }>("profile", async ({ user, displayName, avatarUrl }) => {
  const existing = await stores.profiles.get(user);
  if (existing) await stores.profiles.update(user, { displayName, avatarUrl: avatarUrl || null });
  else await stores.profiles.create({ userId: user, handle: defaultHandle(user), displayName, avatarUrl: avatarUrl || null });
});

route<{ document: Record<string, unknown> | null }>("runtime-config", async ({ document }) => {
  documents.runtime = document;
});

route<{ document: Record<string, unknown> | null }>("e2ee-config", async ({ document }) => {
  documents.e2ee = document;
});

route("calls", async () => {
  const out = recorded;
  recorded = [];
  return { calls: out };
});

const close = async () => {
  await app.close();
  await stores.close();
  process.exit(0);
};
process.on("SIGINT", close);
process.on("SIGTERM", close);

await app.listen({ port: Number(values.port), host: "127.0.0.1" });
console.log(`conformance host (${values.store}) listening on http://127.0.0.1:${values.port}`);
