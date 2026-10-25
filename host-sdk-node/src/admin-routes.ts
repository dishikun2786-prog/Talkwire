// Chat records for the host's administrators: list a user's conversations,
// read their messages, and clear a two-person conversation for both users.
// With a CallRecordStore, also the call records (see recordingCalls).
//
// Reading is for any administrator. Clearing deletes messages and media for
// good, so it takes a superadministrator, a typed confirmation naming the
// conversation, a reason and an idempotency key, and it is audited. The audit
// keeps metadata only (who, which conversation, why, how many were deleted),
// never message content.

import type { FastifyInstance, FastifyReply, FastifyRequest } from "fastify";
import { z } from "zod";
import { ImClientError, type AdminMessage, type AdminTopic, type ImClient } from "./im-client.js";
import { parseCallCursor } from "./call-records.js";
import { DuplicateKeyError, type AuditStore, type CallRecord, type CallRecordStore, type Profile, type ProfileStore } from "./types.js";
import { imUid } from "./uid.js";

export interface AdminIdentity {
  adminId: string;
  role: "admin" | "superadmin";
}

export interface AdminRoutesOptions {
  /** The signed-in administrator, or null when the request is not from one. */
  authenticate(request: FastifyRequest): Promise<AdminIdentity | null>;
  im: ImClient;
  audit: AuditStore;
  profiles: ProfileStore;
  /** Call records; absent: the calls routes are not there (404). */
  calls?: CallRecordStore;
  /** Where the routes live; default /api/v1/admin/im. */
  basePath?: string;
}

/** What a superadministrator must type to clear topic. */
export function clearConfirmation(topic: string): string {
  return `CLEAR ${topic}`;
}

interface Person {
  uid: string;
  userId: string | null;
  displayName: string | null;
}

const topicParam = z.object({ topic: z.string().min(1).max(128) });

export async function imkitAdminRoutes(app: FastifyInstance, options: AdminRoutesOptions): Promise<void> {
  const base = (options.basePath ?? "/api/v1/admin/im").replace(/\/$/, "");
  const { im, audit, profiles } = options;
  app.addHook("onRoute", (route) => {
    route.bodyLimit ??= 16 * 1024;
  });

  const admin = async (request: FastifyRequest, reply: FastifyReply): Promise<AdminIdentity | null> => {
    const who = await options.authenticate(request);
    if (!who) void reply.status(401).send({ error: "unauthorized" });
    return who;
  };

  const parse = <S extends z.ZodTypeAny>(schema: S, value: unknown, reply: FastifyReply): z.output<S> | undefined => {
    const parsed = schema.safeParse(value ?? {});
    if (!parsed.success) {
      const first = parsed.error.issues[0];
      void reply.status(400).send({ error: `invalid request: ${first ? `${first.path.join(".") || "body"}: ${first.message}` : "bad input"}` });
      return undefined;
    }
    return parsed.data;
  };

  // The IM server knows users by uid only; show administrators who they are.
  const people = async (uids: string[]): Promise<Map<string, Person>> => {
    const unique = [...new Set(uids)];
    const found = await profiles.findManyByUid(unique);
    const byUid = new Map<string, Profile>(found.map((p) => [p.uid, p]));
    return new Map(unique.map((uid) => [uid, { uid, userId: byUid.get(uid)?.userId ?? null, displayName: byUid.get(uid)?.displayName ?? null }]));
  };

  const imFailed = (reply: FastifyReply, err: unknown) => {
    if (err instanceof ImClientError && (err.status === 400 || err.status === 404 || err.status === 409)) {
      let error = err.body.trim();
      try {
        error = (JSON.parse(err.body) as { error?: string }).error ?? error;
      } catch {
        // plain text
      }
      return reply.status(err.status).send({ error });
    }
    reply.log.error({ err }, "imkit: IM server call failed");
    return reply.status(502).send({ error: "the IM server did not answer" });
  };

  const withMembers = async (topics: AdminTopic[]) => {
    const who = await people(topics.flatMap((t) => t.members ?? []));
    return topics.map((t) => ({ ...t, members: (t.members ?? []).map((uid) => who.get(uid)!) }));
  };

  app.get(`${base}/users/:userId/topics`, async (request, reply) => {
    if (!(await admin(request, reply))) return reply;
    const params = parse(z.object({ userId: z.string().min(1).max(128) }), request.params, reply);
    const q = parse(z.object({ limit: z.coerce.number().int().min(1).max(500).optional() }), request.query, reply);
    if (!params || !q) return reply;
    try {
      const topics = await im.adminTopics(imUid(params.userId), q.limit);
      return { uid: imUid(params.userId), topics: await withMembers(topics) };
    } catch (err) {
      return imFailed(reply, err);
    }
  });

  app.get(`${base}/topics/:topic`, async (request, reply) => {
    if (!(await admin(request, reply))) return reply;
    const params = parse(topicParam, request.params, reply);
    if (!params) return reply;
    try {
      const [topic] = await withMembers([await im.adminTopic(params.topic)]);
      return topic;
    } catch (err) {
      return imFailed(reply, err);
    }
  });

  const messagesQuery = z.object({
    beforeSeq: z.coerce.number().int().min(1).optional(),
    limit: z.coerce.number().int().min(1).max(200).optional(),
    type: z.enum(["text", "image", "video", "audio", "file"]).optional(),
    keyword: z.string().max(100).optional(),
    fromMs: z.coerce.number().int().min(0).optional(),
    toMs: z.coerce.number().int().min(0).optional(),
  });

  app.get(`${base}/topics/:topic/messages`, async (request, reply) => {
    if (!(await admin(request, reply))) return reply;
    const params = parse(topicParam, request.params, reply);
    const q = parse(messagesQuery, request.query, reply);
    if (!params || !q) return reply;
    try {
      const page = await im.adminMessages({ topic: params.topic, ...q });
      const who = await people(page.messages.map((m) => m.from));
      return {
        hasMore: page.hasMore,
        messages: page.messages.map((m: AdminMessage) => ({ ...m, sender: who.get(m.from)! })),
      };
    } catch (err) {
      return imFailed(reply, err);
    }
  });

  const clearBody = z.object({
    confirm: z.string(),
    reason: z.string().trim().min(1).max(500),
    idempotencyKey: z.string().min(8).max(128),
    /** The user whose records the administrator was looking at; kept in the audit. */
    targetUserId: z.string().min(1).max(128).optional(),
  });

  app.post(`${base}/topics/:topic/clear`, async (request, reply) => {
    const who = await admin(request, reply);
    if (!who) return reply;
    if (who.role !== "superadmin") return reply.status(403).send({ error: "clearing needs a superadministrator" });
    const params = parse(topicParam, request.params, reply);
    const body = parse(clearBody, request.body, reply);
    if (!params || !body) return reply;
    if (body.confirm !== clearConfirmation(params.topic)) {
      return reply.status(400).send({ error: `type "${clearConfirmation(params.topic)}" to confirm` });
    }

    // The same key again answers the first attempt's outcome, never clears twice.
    const replay = async () => {
      const prior = await audit.findByIdempotencyKey(body.idempotencyKey);
      if (!prior) return undefined;
      if (prior.action !== "im.clear_topic" || prior.topic !== params.topic || prior.adminId !== who.adminId) {
        return reply.status(409).send({ error: "idempotencyKey already used for another request" });
      }
      if (prior.status === "success") return { ...prior.details, auditId: prior.id, replayed: true };
      if (prior.status === "pending") return reply.status(409).send({ error: "this clear is still running" });
      return reply.status(409).send({ error: "this clear failed; retry with a new idempotencyKey", auditId: prior.id });
    };
    const earlier = await replay();
    if (earlier) return earlier;

    let entry;
    try {
      entry = await audit.create({
        adminId: who.adminId,
        action: "im.clear_topic",
        status: "pending",
        topic: params.topic,
        reason: body.reason,
        idempotencyKey: body.idempotencyKey,
        targetUserId: body.targetUserId,
      });
    } catch (err) {
      // A concurrent request with the same key got there first.
      if (err instanceof DuplicateKeyError) return (await replay()) ?? reply.status(409).send({ error: "this clear is still running" });
      throw err;
    }
    try {
      const result = await im.adminClear(params.topic);
      const details = {
        deletedMessages: result.deletedMessages,
        deletedMedia: result.deletedMedia,
        clearVersion: result.clearVersion,
        clearBeforeSeq: result.clearBeforeSeq,
      };
      await audit.finish(entry.id, "success", details);
      return { ...details, auditId: entry.id, replayed: false };
    } catch (err) {
      await audit.finish(entry.id, "failed", { status: err instanceof ImClientError ? err.status : 0 });
      return imFailed(reply, err);
    }
  });

  const calls = options.calls;
  if (!calls) return;

  // Records hold host user ids; show administrators who they are.
  const withParties = async (records: CallRecord[]) => {
    const ids = [...new Set(records.flatMap((r) => [r.callerUserId, r.calleeUserId]))];
    const found = new Map((await profiles.getMany(ids)).map((p) => [p.userId, p]));
    const person = (userId: string): Person => ({ uid: imUid(userId), userId, displayName: found.get(userId)?.displayName ?? null });
    return records.map((r) => ({ ...r, caller: person(r.callerUserId), callee: person(r.calleeUserId) }));
  };

  const callsQuery = z.object({
    userId: z.string().min(1).max(128).optional(),
    status: z.enum(["ringing", "active", "ended"]).optional(),
    callType: z.enum(["voice", "video"]).optional(),
    endReason: z.string().min(1).max(32).optional(),
    fromMs: z.coerce.number().int().min(0).optional(),
    toMs: z.coerce.number().int().min(0).optional(),
    cursor: z
      .string()
      .max(200)
      .refine((c) => parseCallCursor(c) !== null, "not a cursor from this list")
      .optional(),
    limit: z.coerce.number().int().min(1).max(100).default(50),
  });

  app.get(`${base}/calls`, async (request, reply) => {
    if (!(await admin(request, reply))) return reply;
    const q = parse(callsQuery, request.query, reply);
    if (!q) return reply;
    const page = await calls.list({
      userId: q.userId,
      status: q.status,
      callType: q.callType,
      endReason: q.endReason,
      from: q.fromMs === undefined ? undefined : new Date(q.fromMs),
      to: q.toMs === undefined ? undefined : new Date(q.toMs),
      cursor: q.cursor,
      limit: q.limit,
    });
    return { calls: await withParties(page.records), nextCursor: page.nextCursor };
  });

  app.get(`${base}/calls/:callSessionId`, async (request, reply) => {
    if (!(await admin(request, reply))) return reply;
    const params = parse(z.object({ callSessionId: z.string().min(1).max(64) }), request.params, reply);
    if (!params) return reply;
    const record = await calls.get(params.callSessionId);
    if (!record) return reply.status(404).send({ error: "no such call" });
    const [call] = await withParties([record]);
    return call;
  });
}
