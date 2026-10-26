// A development host for the Talkwire sample apps. Everything a real host does
// with its own accounts is replaced by one route, POST /dev/login, that signs
// anyone in by name: never expose this on a network.
//
//   /dev/login                    a name in, an IM access token out; signing in
//                                 befriends every user who signed in before
//   /api/v1/internal/im/*         the endpoints the IM server calls (talkwireHostPlugin)
//   /api/v1/chat/*                profiles and contacts (talkwireAppRoutes)
//   /api/v1/chat/upload, /media   media (talkwireMediaRoutes)
//   /api/v1/red-packets           the red packet example
//   calls                         demo credentials, signaling only (demo-calls.ts),
//                                 each call recorded for the administrators
//   /dev/admin-login              a name and a role in, an administrator token out
//   /api/v1/admin/im/*            chat and call records (talkwireAdminRoutes)
//
// Stores are in memory: restart the host and every user signs in again.

import Fastify, { type FastifyInstance, type FastifyRequest } from "fastify";
import jwt from "jsonwebtoken";
import {
  ImClient,
  LocalDiskBlobStore,
  MemoryAuditStore,
  MemoryCallRecordStore,
  MemoryContactStore,
  MemoryProfileStore,
  addContact,
  bearerToken,
  defaultHandle,
  imUid,
  talkwireAdminRoutes,
  talkwireAppRoutes,
  talkwireHostPlugin,
  talkwireMediaRoutes,
  mediaDeleter,
  recordingCalls,
  type AdminIdentity,
} from "@talkwire/host-node";
import { redPacketRoutes } from "../../red-packet/src/red-packets.ts";
import { demoCalls } from "./demo-calls.ts";

export interface SampleHostOptions {
  /** Shared with the IM server (its JWT_SECRET). */
  secret: string;
  /** The IM server, e.g. http://127.0.0.1:6062. */
  imBaseUrl: string;
  mediaDir: string;
  /** Secret for add tokens. */
  addTokenSecret?: string;
}

/** What the IM server accepts at login: an access token of an ordinary user (red line R18). */
export function signAccessToken(secret: string, userId: string, ttlSeconds = 30 * 24 * 3600): string {
  return jwt.sign({ type: "access", role: "app_user" }, secret, { algorithm: "HS256", subject: userId, expiresIn: ttlSeconds });
}

// Administrator tokens use their own key: the IM server must never take one
// for a user's, and an ordinary user's token must never open the records.
const adminKey = (secret: string) => secret + ":admin";

export function signAdminToken(secret: string, name: string, role: AdminIdentity["role"], ttlSeconds = 12 * 3600): string {
  return jwt.sign({ type: "admin_access", role }, adminKey(secret), { algorithm: "HS256", subject: "admin-" + name.toLowerCase(), expiresIn: ttlSeconds });
}

export function verifyAdminToken(secret: string, token: string): AdminIdentity | null {
  if (!token) return null;
  try {
    const c = jwt.verify(token, adminKey(secret), { algorithms: ["HS256"] });
    if (typeof c !== "object" || c === null || c.type !== "admin_access" || typeof c.sub !== "string") return null;
    return c.role === "admin" || c.role === "superadmin" ? { adminId: c.sub, role: c.role } : null;
  } catch {
    return null;
  }
}

export function verifyAccessToken(secret: string, token: string): string | null {
  if (!token) return null;
  try {
    const c = jwt.verify(token, secret, { algorithms: ["HS256"] });
    if (typeof c !== "object" || c === null || c.type !== "access" || typeof c.sub !== "string") return null;
    return c.sub;
  } catch {
    return null;
  }
}

/** A dev user's host id: stable per name, so signing in again is the same user. */
export function devUserId(name: string): string {
  return "dev-" + name.toLowerCase();
}

const NAME = /^[A-Za-z0-9_]{2,20}$/;

export async function buildSampleHost(options: SampleHostOptions): Promise<FastifyInstance> {
  const app = Fastify({ logger: { level: "info" }, bodyLimit: 64 * 1024 });
  const profiles = new MemoryProfileStore();
  const contacts = new MemoryContactStore();
  const im = new ImClient({ baseUrl: options.imBaseUrl, secret: options.secret });
  const blobs = new LocalDiskBlobStore(options.mediaDir);
  const authenticate = async (request: FastifyRequest) => verifyAccessToken(options.secret, bearerToken(request.headers.authorization));
  const users = new Set<string>();

  const callRecords = new MemoryCallRecordStore();
  const calls = recordingCalls(
    demoCalls(contacts, (msg) => app.log.info(msg)),
    callRecords,
    { onError: (err, step, id) => app.log.error({ err, step, callSessionId: id }, "could not record a call step") },
  );
  await app.register(talkwireHostPlugin, { secret: options.secret, contacts, profiles, deleteMedia: mediaDeleter(blobs), calls });
  await app.register(talkwireAppRoutes, {
    authenticate,
    profiles,
    contacts,
    im,
    addTokenSecret: options.addTokenSecret ?? options.secret + ":add",
    initialProfile: async (userId) => ({ displayName: userId.replace(/^dev-/, "") }),
  });
  await app.register(talkwireMediaRoutes, { authenticate, store: blobs, im });
  await app.register(redPacketRoutes, { im, authenticate });
  await app.register(talkwireAdminRoutes, {
    authenticate: async (request) => verifyAdminToken(options.secret, bearerToken(request.headers.authorization)),
    im,
    audit: new MemoryAuditStore(),
    profiles,
    calls: callRecords,
  });

  app.post("/dev/admin-login", async (request, reply) => {
    const body = request.body as { name?: unknown; role?: unknown } | undefined;
    if (typeof body?.name !== "string" || !NAME.test(body.name)) {
      return reply.status(400).send({ error: "name: 2-20 letters, digits or _" });
    }
    if (body.role !== "admin" && body.role !== "superadmin") return reply.status(400).send({ error: "role: admin or superadmin" });
    return { adminId: "admin-" + body.name.toLowerCase(), role: body.role, token: signAdminToken(options.secret, body.name, body.role) };
  });

  app.post("/dev/login", async (request, reply) => {
    const name = (request.body as { name?: unknown } | undefined)?.name;
    if (typeof name !== "string" || !NAME.test(name)) {
      return reply.status(400).send({ error: "name: 2-20 letters, digits or _" });
    }
    const userId = devUserId(name);
    if (!(await profiles.get(userId))) {
      await profiles.create({ userId, handle: defaultHandle(userId), displayName: name, avatarUrl: null });
    }
    // Everyone who signed in before becomes a contact, both ways accepted.
    for (const other of users) {
      if (other === userId) continue;
      const first = await addContact(contacts, userId, other);
      const second = await addContact(contacts, other, userId);
      if (first.ok && second.ok && second.value.contact.status === "accepted") {
        im.contactSync(userId, other, "accepted").catch((err: unknown) => request.log.warn({ err }, "contact-sync failed"));
      }
    }
    users.add(userId);
    return { userId, uid: imUid(userId), name, accessToken: signAccessToken(options.secret, userId) };
  });

  return app;
}
