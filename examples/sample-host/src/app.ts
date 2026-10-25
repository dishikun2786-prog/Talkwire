// A development host for the ImKit sample apps. Everything a real host does
// with its own accounts is replaced by one route, POST /dev/login, that signs
// anyone in by name: never expose this on a network.
//
//   /dev/login                    a name in, an IM access token out; signing in
//                                 befriends every user who signed in before
//   /api/v1/internal/im/*         the endpoints the IM server calls (imkitHostPlugin)
//   /api/v1/chat/*                profiles and contacts (imkitAppRoutes)
//   /api/v1/chat/upload, /media   media (imkitMediaRoutes)
//   /api/v1/red-packets           the red packet example
//
// Stores are in memory: restart the host and every user signs in again.

import Fastify, { type FastifyInstance, type FastifyRequest } from "fastify";
import jwt from "jsonwebtoken";
import {
  ImClient,
  LocalDiskBlobStore,
  MemoryContactStore,
  MemoryProfileStore,
  addContact,
  bearerToken,
  defaultHandle,
  imUid,
  imkitAppRoutes,
  imkitHostPlugin,
  imkitMediaRoutes,
  mediaDeleter,
} from "@imkit/host-node";
import { redPacketRoutes } from "../../red-packet/src/red-packets.ts";

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

  await app.register(imkitHostPlugin, { secret: options.secret, contacts, profiles, deleteMedia: mediaDeleter(blobs) });
  await app.register(imkitAppRoutes, {
    authenticate,
    profiles,
    contacts,
    im,
    addTokenSecret: options.addTokenSecret ?? options.secret + ":add",
    initialProfile: async (userId) => ({ displayName: userId.replace(/^dev-/, "") }),
  });
  await app.register(imkitMediaRoutes, { authenticate, store: blobs, im });
  await app.register(redPacketRoutes, { im, authenticate });

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
