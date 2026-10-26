// The public test service (sandbox) for third-party developers: the sample
// host with what a public network needs. Same chat routes as app.ts, plus:
//
//   spaces          POST /sandbox/spaces makes a space and returns its code.
//                   /dev/login takes a name and a space code; only users of
//                   the same space become contacts, so developers testing at
//                   the same time do not see each other.
//   no admin        /dev/admin-login and the admin routes are not registered:
//                   on the open sample host anyone can become a superadmin.
//   limits          per-client rate limits on making spaces and signing in, a
//                   cap on users per space, uploads limited in size, number
//                   and total disk use.
//   /sandbox/info   what a client shows: that this is a test service, when
//                   data is cleared, the limits.
//
// Data lives in memory and in the media directory; a daily job restarts the
// host and the IM server with an empty database and media directory, so
// nothing here is kept (deploy/imtalk/).
//
// The client's address comes from CF-Connecting-IP: the sandbox runs behind
// Cloudflare, and the proxy in front of the host refuses connections that do
// not come from Cloudflare, so the header cannot be forged (deploy/imtalk/).

import { randomInt } from "node:crypto";
import { readdir, stat } from "node:fs/promises";
import path from "node:path";
import Fastify, { type FastifyInstance, type FastifyRequest } from "fastify";
import {
  ImClient,
  LocalDiskBlobStore,
  MemoryContactStore,
  MemoryProfileStore,
  addContact,
  bearerToken,
  defaultHandle,
  imUid,
  mediaDeleter,
  talkwireAppRoutes,
  talkwireHostPlugin,
  talkwireMediaRoutes,
} from "@talkwire/host-node";
import { redPacketRoutes } from "../../red-packet/src/red-packets.ts";
import { signAccessToken, verifyAccessToken } from "./app.ts";
import { demoCalls } from "./demo-calls.ts";

export interface SandboxLimits {
  /** Spaces one client address may make per hour. */
  spacesPerHour: number;
  /** Spaces in all; the daily reset empties them. */
  maxSpaces: number;
  /** Sign-ins one client address may make per minute. */
  loginsPerMinute: number;
  /** Users in one space. */
  usersPerSpace: number;
  /** Largest upload in bytes. */
  maxUploadBytes: number;
  /** Uploads one user may make per hour. */
  uploadsPerHour: number;
  /** All uploaded files together, in bytes; uploads stop when it is reached. */
  mediaQuotaBytes: number;
}

export const DEFAULT_SANDBOX_LIMITS: SandboxLimits = {
  spacesPerHour: 10,
  maxSpaces: 2000,
  loginsPerMinute: 30,
  usersPerSpace: 50,
  maxUploadBytes: 10 * 1024 * 1024,
  uploadsPerHour: 60,
  mediaQuotaBytes: 2 * 1024 * 1024 * 1024,
};

export interface SandboxOptions {
  /** Shared with the IM server (its JWT_SECRET). */
  secret: string;
  /** The IM server, e.g. http://127.0.0.1:6062. */
  imBaseUrl: string;
  mediaDir: string;
  limits?: Partial<SandboxLimits>;
  /** When the daily reset runs, as shown to clients, e.g. "04:00 Asia/Shanghai". */
  resetTime?: string;
  /** Where the sandbox is documented. */
  docsUrl?: string;
  /** Clock, for tests. */
  now?(): number;
}

// Lowercase letters and digits without the ones that are easy to mix up (0/o, 1/l/i).
const SPACE_ALPHABET = "abcdefghjkmnpqrstuvwxyz23456789";
const SPACE = /^[a-z0-9]{8}$/;
const NAME = /^[A-Za-z0-9_]{2,20}$/;

function newSpaceCode(): string {
  let code = "";
  for (let i = 0; i < 8; i++) code += SPACE_ALPHABET[randomInt(SPACE_ALPHABET.length)];
  return code;
}

/** A sandbox user's host id: the space and the name, so the same name in two spaces is two users. */
export function sandboxUserId(space: string, name: string): string {
  return `sbx-${space}-${name.toLowerCase()}`;
}

/** Fixed-window counters per key; enough for a single-process sandbox. */
class RateLimiter {
  private readonly windows = new Map<string, { count: number; resetAt: number }>();
  constructor(
    private readonly limit: number,
    private readonly windowMs: number,
    private readonly now: () => number,
  ) {}

  /** Counts one hit; false when the key is over its limit for this window. */
  hit(key: string): boolean {
    const t = this.now();
    let w = this.windows.get(key);
    if (!w || w.resetAt <= t) {
      if (this.windows.size > 50_000) this.sweep(t);
      w = { count: 0, resetAt: t + this.windowMs };
      this.windows.set(key, w);
    }
    w.count++;
    return w.count <= this.limit;
  }

  private sweep(t: number): void {
    for (const [k, w] of this.windows) if (w.resetAt <= t) this.windows.delete(k);
  }
}

/** Total size of the files directly in a directory; 0 when it does not exist. */
async function dirBytes(dir: string): Promise<number> {
  let total = 0;
  let names: string[];
  try {
    names = await readdir(dir);
  } catch {
    return 0;
  }
  for (const n of names) {
    try {
      const s = await stat(path.join(dir, n));
      if (s.isFile()) total += s.size;
    } catch {
      // removed meanwhile
    }
  }
  return total;
}

export async function buildSandboxHost(options: SandboxOptions): Promise<FastifyInstance> {
  const limits: SandboxLimits = { ...DEFAULT_SANDBOX_LIMITS, ...options.limits };
  const now = options.now ?? Date.now;
  const app = Fastify({ logger: { level: "info" }, bodyLimit: 64 * 1024 });
  const profiles = new MemoryProfileStore();
  const contacts = new MemoryContactStore();
  const im = new ImClient({ baseUrl: options.imBaseUrl, secret: options.secret });
  const blobs = new LocalDiskBlobStore(options.mediaDir);
  const authenticate = async (request: FastifyRequest) => verifyAccessToken(options.secret, bearerToken(request.headers.authorization));

  // space code -> the host ids signed in to it
  const spaces = new Map<string, Set<string>>();
  const spaceLimiter = new RateLimiter(limits.spacesPerHour, 3600_000, now);
  const loginLimiter = new RateLimiter(limits.loginsPerMinute, 60_000, now);
  const uploadLimiter = new RateLimiter(limits.uploadsPerHour, 3600_000, now);
  const clientIp = (request: FastifyRequest) => {
    const cf = request.headers["cf-connecting-ip"];
    return (Array.isArray(cf) ? cf[0] : cf) ?? request.ip;
  };

  // Disk use, measured at most every 30 seconds: an upload may overshoot the
  // quota by what arrives within that time, which the daily reset clears.
  let mediaBytes = 0;
  let measuredAt = -Infinity;
  const mediaUse = async () => {
    if (now() - measuredAt > 30_000) {
      mediaBytes = await dirBytes(options.mediaDir);
      measuredAt = now();
    }
    return mediaBytes;
  };

  await app.register(talkwireHostPlugin, {
    secret: options.secret,
    contacts,
    profiles,
    deleteMedia: mediaDeleter(blobs),
    calls: demoCalls(contacts, (msg) => app.log.info(msg)),
  });
  await app.register(talkwireAppRoutes, {
    authenticate,
    profiles,
    contacts,
    im,
    addTokenSecret: options.secret + ":add",
    initialProfile: async (userId) => ({ displayName: userId.replace(/^sbx-[a-z0-9]{8}-/, "") }),
  });

  // Upload limits run before the media routes see the request.
  app.addHook("onRequest", async (request, reply) => {
    if (request.method !== "POST" || request.url.split("?")[0] !== "/api/v1/chat/upload") return;
    const userId = await authenticate(request);
    if (!userId) return; // the media routes answer 401
    if (!uploadLimiter.hit(userId)) {
      return reply.status(429).send({ error: `at most ${limits.uploadsPerHour} uploads an hour in the sandbox` });
    }
    if ((await mediaUse()) >= limits.mediaQuotaBytes) {
      return reply.status(507).send({ error: "the sandbox's upload space is full until the daily reset" });
    }
  });
  await app.register(talkwireMediaRoutes, { authenticate, store: blobs, im, maxBytes: limits.maxUploadBytes });
  await app.register(redPacketRoutes, { im, authenticate });

  app.get("/sandbox/info", async () => ({
    sandbox: true,
    resetTime: options.resetTime ?? null,
    docsUrl: options.docsUrl ?? null,
    limits: {
      usersPerSpace: limits.usersPerSpace,
      maxUploadBytes: limits.maxUploadBytes,
      uploadsPerHour: limits.uploadsPerHour,
    },
  }));

  app.post("/sandbox/spaces", async (request, reply) => {
    if (!spaceLimiter.hit(clientIp(request))) {
      return reply.status(429).send({ error: `at most ${limits.spacesPerHour} new spaces an hour from one address` });
    }
    if (spaces.size >= limits.maxSpaces) {
      return reply.status(503).send({ error: "no more spaces until the daily reset" });
    }
    let code = newSpaceCode();
    while (spaces.has(code)) code = newSpaceCode();
    spaces.set(code, new Set());
    return { space: code };
  });

  app.post("/dev/login", async (request, reply) => {
    if (!loginLimiter.hit(clientIp(request))) {
      return reply.status(429).send({ error: "too many sign-ins, try again in a minute" });
    }
    const body = request.body as { name?: unknown; space?: unknown } | undefined;
    const name = body?.name;
    const space = typeof body?.space === "string" ? body.space.trim().toLowerCase() : "";
    if (typeof name !== "string" || !NAME.test(name)) {
      return reply.status(400).send({ error: "name: 2-20 letters, digits or _" });
    }
    if (!SPACE.test(space)) {
      return reply.status(400).send({ error: "space: the 8-character code from POST /sandbox/spaces" });
    }
    const members = spaces.get(space);
    if (!members) {
      return reply.status(404).send({ error: "unknown space: it may have been cleared by the daily reset; make a new one" });
    }
    const userId = sandboxUserId(space, name);
    if (!members.has(userId) && members.size >= limits.usersPerSpace) {
      return reply.status(409).send({ error: `a space holds at most ${limits.usersPerSpace} users` });
    }
    if (!(await profiles.get(userId))) {
      await profiles.create({ userId, handle: defaultHandle(userId), displayName: name, avatarUrl: null });
    }
    // Everyone who signed in to this space before becomes a contact, both ways accepted.
    for (const other of members) {
      if (other === userId) continue;
      const first = await addContact(contacts, userId, other);
      const second = await addContact(contacts, other, userId);
      if (first.ok && second.ok && second.value.contact.status === "accepted") {
        im.contactSync(userId, other, "accepted").catch((err: unknown) => request.log.warn({ err }, "contact-sync failed"));
      }
    }
    members.add(userId);
    return { userId, uid: imUid(userId), name, space, accessToken: signAccessToken(options.secret, userId, 24 * 3600) };
  });

  return app;
}
