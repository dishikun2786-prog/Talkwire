// The REST endpoints a chat app calls on its host: the user's own profile,
// other users' profiles, finding users, and contacts. The IM server never
// calls these; the app does, with the host's own user credentials.

import { createHash, createHmac } from "node:crypto";
import type { FastifyInstance, FastifyReply, FastifyRequest } from "fastify";
import { z } from "zod";
import { checkAddToken, checkContactQr, issueAddToken, issueContactQr, type AddTokenMode } from "./add-token.js";
import {
  addContact,
  aliasFor,
  directionFor,
  otherUser,
  removeContact,
  respondToContact,
  setAlias,
  type ContactError,
  type ContactResult,
} from "./contacts.js";
import type { ImClient } from "./im-client.js";
import { HandleTakenError, type Contact, type ContactStore, type Profile, type ProfileStore } from "./types.js";
import { imUid, isUid } from "./uid.js";

/** A realtime notice for one user about their profile or contacts. */
export type AppEvent =
  | { kind: "contact.request"; fromUserId: string; uid: string; displayName: string; avatarUrl: string | null }
  | { kind: "contact.accepted"; userId: string; uid: string }
  | { kind: "contact.removed"; userId: string; uid: string }
  | { kind: "contact.alias"; userId: string; uid: string; alias: string | null }
  | { kind: "profile.updated"; userId: string; uid: string; displayName: string; avatarUrl: string | null };

export interface AppRoutesOptions {
  /** The signed-in user's host id, or null when the request is not signed in. */
  authenticate(request: FastifyRequest): Promise<string | null>;
  profiles: ProfileStore;
  contacts: ContactStore;
  /** Keeps the IM server in step with contacts, and delivers events when notify is absent. */
  im?: ImClient;
  /** Secret for add tokens; use one only the host knows. */
  addTokenSecret: string;
  /** Default "require". "observe" suits a migration from clients that do not send tokens yet. */
  addTokenMode?: AddTokenMode;
  /** Called for a request without a valid token in "observe" mode. */
  onAddTokenMissing?(userId: string, contactId: string, check: "absent" | "invalid"): void;
  /** Delivers an event to every device of userId. Default: a custom_update through the IM server. */
  notify?(userId: string, event: AppEvent): Promise<void> | void;
  /** Whether a user may be found and added; default every user. */
  isUserActive?(userId: string): Promise<boolean>;
  /** The profile a user starts with. Default: a handle derived from the id and no name. */
  initialProfile?(userId: string): Promise<{ handle?: string; displayName?: string; avatarUrl?: string | null }>;
  /** How often a user may choose their handle: "once" (default), "never" or "always". */
  handleChange?: "once" | "never" | "always";
  /** Display names nobody may take, compared case-insensitively. */
  reservedDisplayNames?: string[];
  /** Where the routes live; default /api/v1/chat. */
  basePath?: string;
  /** Errors from background work (IM sync, notifications); default: the Fastify logger. */
  onError?(err: unknown, what: string): void;
}

const HANDLE_MIN = 4;
const HANDLE_MAX = 16;
const CUSTOM_HANDLE = /^[A-Za-z0-9]+$/;
const SEARCH_HANDLE = /^[A-Za-z0-9_]+$/;

/** The default handle of a user: "u_" and 10 characters derived from the id. */
export function defaultHandle(userId: string): string {
  return "u_" + createHash("sha256").update(userId, "utf8").digest("hex").slice(0, 10);
}

const errorStatus: Record<ContactError, number> = {
  cannot_add_self: 400,
  already_friends: 409,
  already_requested: 409,
  you_blocked: 403,
  blocked_by_them: 403,
  not_found: 404,
  not_allowed: 403,
  not_friends: 400,
};

export async function talkwireAppRoutes(app: FastifyInstance, options: AppRoutesOptions): Promise<void> {
  const base = (options.basePath ?? "/api/v1/chat").replace(/\/$/, "");
  const { profiles, contacts, im } = options;
  // Every request here is a few small fields; see host-plugin.ts on why it is limited.
  app.addHook("onRoute", (route) => {
    route.bodyLimit ??= 16 * 1024;
  });
  const addTokenMode = options.addTokenMode ?? "require";
  const handleChange = options.handleChange ?? "once";
  const reserved = new Set((options.reservedDisplayNames ?? []).map((n) => n.toLowerCase()));
  const isActive = options.isUserActive ?? (async () => true);
  // A key of its own, so an add token can never pass for any other HMAC the
  // host makes with the same secret.
  const addTokenKey = createHmac("sha256", options.addTokenSecret).update("talkwire add token").digest("hex");
  const contactQrKey = createHmac("sha256", options.addTokenSecret).update("talkwire contact qr").digest("hex");
  const reportError = (err: unknown, what: string) =>
    options.onError ? options.onError(err, what) : app.log.warn({ err }, `talkwire: ${what} failed`);

  const notify = (userId: string, event: AppEvent) => {
    const send = options.notify
      ? async () => options.notify!(userId, event)
      : im
        ? () => im.customBroadcast({ targetUid: imUid(userId) }, event.kind, event)
        : undefined;
    if (send) void Promise.resolve().then(send).catch((err) => reportError(err, `notify ${event.kind}`));
  };
  const syncIm = (a: string, b: string, event: "accepted" | "removed") => {
    if (im) void im.contactSync(a, b, event).catch((err) => reportError(err, `contact-sync ${event}`));
  };

  const ensureProfile = async (userId: string): Promise<Profile> => {
    const existing = await profiles.get(userId);
    if (existing) return existing;
    const initial = options.initialProfile ? await options.initialProfile(userId) : {};
    const handle = initial.handle ?? defaultHandle(userId);
    try {
      return await profiles.create({ userId, handle, displayName: initial.displayName ?? "", avatarUrl: initial.avatarUrl ?? null });
    } catch (err) {
      // Created meanwhile by a concurrent request, or the handle is taken.
      const raced = await profiles.get(userId);
      if (raced) return raced;
      if (err instanceof HandleTakenError) {
        return profiles.create({ userId, handle: `${handle}${Date.now() % 1000}`, displayName: initial.displayName ?? "", avatarUrl: initial.avatarUrl ?? null });
      }
      throw err;
    }
  };

  const nameOf = (profile: Profile | null | undefined) => profile?.displayName || profile?.handle || "";

  const publicProfile = (p: Profile) => ({
    userId: p.userId,
    uid: p.uid,
    handle: p.handle,
    displayName: nameOf(p),
    avatarUrl: p.avatarUrl,
  });

  const contactDto = (c: Contact, me: string, other: Profile | undefined) => {
    const alias = aliasFor(c, me);
    const otherId = otherUser(c, me);
    return {
      id: c.id,
      contactId: otherId,
      uid: imUid(otherId),
      direction: directionFor(c, me),
      status: c.status,
      blockedByMe: c.status === "blocked" ? c.blockedBy === me : undefined,
      alias,
      handle: other?.handle ?? null,
      displayName: alias || nameOf(other),
      // The other user's own name, without my remark: the remark dialog
      // shows it as what clearing the remark goes back to.
      nickname: nameOf(other),
      avatarUrl: other?.avatarUrl ?? null,
      createdAt: c.createdAt.toISOString(),
      updatedAt: c.updatedAt.toISOString(),
    };
  };

  const signedIn = async (request: FastifyRequest, reply: FastifyReply): Promise<string | undefined> => {
    const userId = await options.authenticate(request);
    if (!userId) {
      void reply.status(401).send({ error: "unauthorized" });
      return undefined;
    }
    return userId;
  };

  const parse = <S extends z.ZodTypeAny>(schema: S, value: unknown, reply: FastifyReply): z.output<S> | undefined => {
    const parsed = schema.safeParse(value ?? {});
    if (!parsed.success) {
      void reply.status(400).send({ error: "invalid_request", detail: parsed.error.issues[0]?.message });
      return undefined;
    }
    return parsed.data;
  };

  const failed = (reply: FastifyReply, result: ContactResult<unknown> & { ok: false }) =>
    reply.status(errorStatus[result.error]).send({ error: result.error });

  // -- the user's own profile --

  app.get(`${base}/me`, async (request, reply) => {
    const me = await signedIn(request, reply);
    if (!me) return reply;
    return publicProfile(await ensureProfile(me));
  });

  const patchMe = z
    .object({
      handle: z.string().trim().min(HANDLE_MIN).max(HANDLE_MAX).regex(CUSTOM_HANDLE).optional(),
      displayName: z.string().trim().min(1).max(50).optional(),
      avatarUrl: z.string().trim().max(500).nullable().optional(),
    })
    .strict();

  app.patch(`${base}/me`, async (request, reply) => {
    const me = await signedIn(request, reply);
    if (!me) return reply;
    const body = parse(patchMe, request.body, reply);
    if (!body) return reply;
    if (body.displayName && reserved.has(body.displayName.toLowerCase())) {
      return reply.status(400).send({ error: "reserved_name" });
    }
    const profile = await ensureProfile(me);
    const patch: Parameters<ProfileStore["update"]>[1] = {};
    if (body.handle !== undefined && body.handle !== profile.handle) {
      if (handleChange === "never" || (handleChange === "once" && profile.handleChangedAt)) {
        return reply.status(400).send({ error: "handle_change_not_allowed" });
      }
      patch.handle = body.handle;
      patch.handleChangedAt = new Date();
    }
    if (body.displayName !== undefined) patch.displayName = body.displayName;
    if (body.avatarUrl !== undefined) patch.avatarUrl = body.avatarUrl || null;
    let updated: Profile;
    try {
      updated = Object.keys(patch).length ? await profiles.update(me, patch) : profile;
    } catch (err) {
      if (err instanceof HandleTakenError) return reply.status(409).send({ error: "handle_taken" });
      throw err;
    }
    if (patch.displayName !== undefined || patch.avatarUrl !== undefined) {
      const peers = await contacts.list(me, { status: "accepted", direction: "all" });
      for (const c of peers) {
        notify(otherUser(c, me), { kind: "profile.updated", userId: me, uid: updated.uid, displayName: nameOf(updated), avatarUrl: updated.avatarUrl });
      }
    }
    return publicProfile(updated);
  });

  // The user's own contact QR payload: what their QR code shows.
  app.get(`${base}/me/qr`, async (request, reply) => {
    const me = await signedIn(request, reply);
    if (!me) return reply;
    return { qr: issueContactQr(contactQrKey, (await ensureProfile(me)).uid) };
  });

  // -- other users --

  app.get(`${base}/profiles`, async (request, reply) => {
    const me = await signedIn(request, reply);
    if (!me) return reply;
    const raw = (request.query as { uids?: unknown }).uids;
    const uids = (typeof raw === "string" ? raw.split(",") : [])
      .map((s) => s.trim())
      .filter(isUid)
      .slice(0, 200);
    const found = uids.length ? await profiles.findManyByUid(uids) : [];
    return { items: found.map((p) => ({ uid: p.uid, displayName: nameOf(p), avatarUrl: p.avatarUrl })) };
  });

  const found = async (me: string, profile: Profile | null, reply: FastifyReply) => {
    if (!profile || (profile.userId !== me && !(await isActive(profile.userId)))) {
      return reply.status(404).send({ error: "user_not_found" });
    }
    return { ...publicProfile(profile), addToken: issueAddToken(addTokenKey, me, profile.userId) };
  };

  app.post(`${base}/contacts/search`, async (request, reply) => {
    const me = await signedIn(request, reply);
    if (!me) return reply;
    const body = parse(z.object({ handle: z.string().trim().min(1).max(HANDLE_MAX).regex(SEARCH_HANDLE) }), request.body, reply);
    if (!body) return reply;
    return found(me, await profiles.findByHandle(body.handle), reply);
  });

  // A contact QR code: the signed payload of /me/qr. A bare uid is refused
  // under addTokenMode "require" (anyone in a group knows the other members'
  // uids); "observe" accepts it and reports it, for codes printed before.
  app.post(`${base}/contacts/qr-resolve`, async (request, reply) => {
    const me = await signedIn(request, reply);
    if (!me) return reply;
    const body = parse(
      z.object({ qr: z.string().trim().min(1).max(128).optional(), uid: z.string().trim().min(1).max(64).optional() }).refine((b) => b.qr || b.uid),
      request.body,
      reply,
    );
    if (!body) return reply;
    if (body.qr) {
      const uid = checkContactQr(contactQrKey, body.qr);
      if (!uid) return reply.status(400).send({ error: "invalid_qr" });
      return found(me, await profiles.findByUid(uid), reply);
    }
    if (addTokenMode === "require") return reply.status(400).send({ error: "qr_required" });
    const profile = await profiles.findByUid(body.uid!);
    if (profile && profile.userId !== me && addTokenMode === "observe") options.onAddTokenMissing?.(me, profile.userId, "absent");
    return found(me, profile, reply);
  });

  // -- contacts --

  app.post(`${base}/contacts`, async (request, reply) => {
    const me = await signedIn(request, reply);
    if (!me) return reply;
    const body = parse(z.object({ contactId: z.string().min(1).max(128), addToken: z.string().max(128).optional() }), request.body, reply);
    if (!body) return reply;
    if (addTokenMode !== "off") {
      const check = checkAddToken(addTokenKey, body.addToken, me, body.contactId);
      if (check !== "valid") {
        if (addTokenMode === "require") return reply.status(403).send({ error: "add_token_required" });
        options.onAddTokenMissing?.(me, body.contactId, check);
      }
    }
    if (body.contactId !== me && !(await isActive(body.contactId))) {
      return reply.status(404).send({ error: "user_not_found" });
    }
    const result = await addContact(contacts, me, body.contactId);
    if (!result.ok) return failed(reply, result);
    const { contact } = result.value;
    if (contact.status === "pending") {
      const mine = await ensureProfile(me);
      notify(body.contactId, { kind: "contact.request", fromUserId: me, uid: mine.uid, displayName: nameOf(mine), avatarUrl: mine.avatarUrl });
    } else if (contact.status === "accepted") {
      notify(me, { kind: "contact.accepted", userId: body.contactId, uid: imUid(body.contactId) });
      notify(body.contactId, { kind: "contact.accepted", userId: me, uid: imUid(me) });
      syncIm(me, body.contactId, "accepted");
    }
    return { status: contact.status, id: contact.id };
  });

  const listQuery = z.object({
    status: z.enum(["pending", "accepted", "rejected", "blocked"]).default("accepted"),
    direction: z.enum(["sent", "received", "all"]).default("all"),
    since: z.string().datetime().optional(),
  });

  app.get(`${base}/contacts`, async (request, reply) => {
    const me = await signedIn(request, reply);
    if (!me) return reply;
    const q = parse(listQuery, request.query, reply);
    if (!q) return reply;
    // The cursor is fixed before reading: a row changed while this request
    // runs is either in this answer or after the cursor, never lost.
    const syncCursor = new Date();
    const rows = await contacts.list(me, {
      status: q.status,
      direction: q.direction,
      updatedAfter: q.since ? new Date(q.since) : undefined,
      updatedUntil: syncCursor,
    });
    const others = await profiles.getMany(rows.map((c) => otherUser(c, me)));
    const byId = new Map(others.map((p) => [p.userId, p]));
    return {
      items: rows.map((c) => contactDto(c, me, byId.get(otherUser(c, me)))),
      total: rows.length,
      syncCursor: syncCursor.toISOString(),
    };
  });

  const patchContact = z.union([
    z.object({ status: z.enum(["accepted", "rejected", "blocked"]) }).strict(),
    z.object({ alias: z.string().trim().max(50).nullable() }).strict(),
  ]);

  app.patch(`${base}/contacts/:id`, async (request, reply) => {
    const me = await signedIn(request, reply);
    if (!me) return reply;
    const { id } = request.params as { id: string };
    const body = parse(patchContact, request.body, reply);
    if (!body) return reply;
    if ("alias" in body) {
      const result = await setAlias(contacts, me, id, body.alias);
      if (!result.ok) return failed(reply, result);
      const other = otherUser(result.value, me);
      const alias = aliasFor(result.value, me);
      notify(me, { kind: "contact.alias", userId: other, uid: imUid(other), alias });
      return { alias };
    }
    const result = await respondToContact(contacts, me, id, body.status);
    if (!result.ok) return failed(reply, result);
    const { contact, previous } = result.value;
    const other = otherUser(contact, me);
    if (previous !== contact.status) {
      if (contact.status === "accepted") {
        notify(me, { kind: "contact.accepted", userId: other, uid: imUid(other) });
        notify(other, { kind: "contact.accepted", userId: me, uid: imUid(me) });
        syncIm(me, other, "accepted");
      } else if (previous === "accepted") {
        // Blocking a contact ends the conversation on the IM side.
        notify(me, { kind: "contact.removed", userId: other, uid: imUid(other) });
        notify(other, { kind: "contact.removed", userId: me, uid: imUid(me) });
        syncIm(me, other, "removed");
      }
    }
    return { status: contact.status };
  });

  app.delete(`${base}/contacts/:id`, async (request, reply) => {
    const me = await signedIn(request, reply);
    if (!me) return reply;
    const { id } = request.params as { id: string };
    const result = await removeContact(contacts, me, id);
    if (!result.ok) {
      // Already gone: removing is idempotent.
      if (result.error === "not_found" && !(await contacts.get(id))) return { success: true };
      return failed(reply, result);
    }
    const contact = result.value;
    const other = otherUser(contact, me);
    notify(me, { kind: "contact.removed", userId: other, uid: imUid(other) });
    notify(other, { kind: "contact.removed", userId: me, uid: imUid(me) });
    if (contact.status === "accepted") syncIm(me, other, "removed");
    return { success: true };
  });
}
