// The host side of the IM contract: the endpoints the IM server calls
// (contract/host-api/host.openapi.yaml), as a Fastify plugin.

import type { FastifyInstance, FastifyReply, FastifyRequest } from "fastify";
import { z } from "zod";
import { bearerToken, verifyInternalToken } from "./token.js";
import type { ConfigSource, ContactStore, ProfileStore } from "./types.js";
import { aliasFor } from "./contacts.js";

/** The call module's host side; see the calls/* operations of the contract. */
export interface CallHandlers {
  create(req: { callSessionId: string; callerAppUserId: string; calleeAppUserId: string; callType: "voice" | "video" }): Promise<{ ok: boolean; reason?: string }>;
  answer(req: { callSessionId: string; effectiveType: "voice" | "video" }): Promise<{
    ok: boolean;
    reason?: string;
    channelId?: string;
    callerToken?: string;
    calleeToken?: string;
    videoQuality?: string;
    denoiseEnabled?: boolean;
    appId?: string;
  }>;
  settle(req: { callSessionId: string; reason: string }): Promise<void>;
  upgrade(req: { callSessionId: string; toType: "video" }): Promise<{ ok: boolean; reason?: string; videoQuality?: string }>;
}

export interface GroupAiRequest {
  groupTopic: string;
  askerImUid: string;
  question: string;
  groupOwnerAppUserId: string;
  kbIds: string[];
  docIds: string[];
}

/**
 * The group assistant's host side (the bot hook): the host decides the answer
 * and what it costs, and the IM server posts it in the group as the
 * assistant. On a business failure answer ok=false with a reason; `message`,
 * when given, is posted instead of the IM server's configured notice for the
 * reason (runtime config `groupAi`). The IM server waits 10 seconds, then
 * posts the "busy" notice.
 */
export type GroupAiHandler = (req: GroupAiRequest) => Promise<{ ok: boolean; answer?: string; reason?: string; message?: string }>;

export interface HostPluginOptions {
  /** The secret shared with the IM server (its JWT_SECRET). */
  secret: string;
  contacts: ContactStore;
  profiles: ProfileStore;
  /** Deletes the blobs behind media URLs; returns how many it deleted. Absent: nothing is deleted. */
  deleteMedia?: (urls: string[]) => Promise<number>;
  /** Absent: both configuration documents answer 404 and the IM server keeps its defaults. */
  config?: ConfigSource;
  /** The call module's host side; absent: the calls/* endpoints answer 404. */
  calls?: CallHandlers;
  /** The group assistant; absent: group-ai-qa answers 404. */
  groupAi?: GroupAiHandler;
  /** Where the endpoints live; the IM server calls MAIN_API_BASE_URL + this. */
  basePath?: string;
}

const hostUserId = z.string().min(1).max(128);
const callType = z.enum(["voice", "video"]);

const schemas = {
  friendship: z.object({ a: hostUserId, b: hostUserId }),
  profile: z.object({ viewer: hostUserId, sender: hostUserId }),
  deleteMedia: z.object({ urls: z.array(z.string()).max(500) }),
  callCreate: z.object({ callSessionId: z.string().min(1).max(64), callerAppUserId: hostUserId, calleeAppUserId: hostUserId, callType }),
  callAnswer: z.object({ callSessionId: z.string().min(1).max(64), effectiveType: callType }),
  callSettle: z.object({
    callSessionId: z.string().min(1).max(64),
    reason: z.enum(["user_hangup", "no_answer", "rejected", "cancelled", "disconnected", "failed"]),
  }),
  callUpgrade: z.object({ callSessionId: z.string().min(1).max(64), toType: z.literal("video") }),
  groupAi: z.object({
    groupTopic: z.string().regex(/^grp_/),
    askerImUid: z.string().min(1),
    question: z.string(),
    groupOwnerAppUserId: hostUserId,
    kbIds: z.array(z.string()),
    docIds: z.array(z.string()),
  }),
};

function parse<S extends z.ZodTypeAny>(schema: S, value: unknown, reply: FastifyReply): z.output<S> | undefined {
  const parsed = schema.safeParse(value);
  if (!parsed.success) {
    const first = parsed.error.issues[0];
    void reply.status(400).send({ error: `invalid request: ${first ? `${first.path.join(".") || "body"}: ${first.message}` : "bad input"}` });
    return undefined;
  }
  return parsed.data;
}

/**
 * Registers the endpoints the IM server calls. Every request must carry an
 * internal_service token signed with options.secret; anything else gets 401.
 */
export async function talkwireHostPlugin(app: FastifyInstance, options: HostPluginOptions): Promise<void> {
  const base = (options.basePath ?? "/api/v1/internal/im").replace(/\/$/, "");
  const { contacts, profiles } = options;
  // The largest request is delete-media with 500 URLs. A small limit also
  // bounds what reaches validation (zod 3 can use much memory on a huge
  // invalid array before it checks the array's length).
  app.addHook("onRoute", (route) => {
    route.bodyLimit ??= 256 * 1024;
  });

  const guard = async (request: FastifyRequest, reply: FastifyReply) => {
    if (!verifyInternalToken(options.secret, bearerToken(request.headers.authorization))) {
      return reply.status(401).send({ error: "unauthorized" });
    }
  };
  const route = { preHandler: guard };

  app.get(`${base}/friendship`, route, async (request, reply) => {
    const q = parse(schemas.friendship, request.query, reply);
    if (!q) return reply;
    const pair = await contacts.findPair(q.a, q.b);
    return { friends: pair?.status === "accepted" };
  });

  app.get(`${base}/profile`, route, async (request, reply) => {
    const q = parse(schemas.profile, request.query, reply);
    if (!q) return reply;
    const [pair, profile] = await Promise.all([contacts.findPair(q.viewer, q.sender), profiles.get(q.sender)]);
    // Only an accepted relationship carries a remark worth showing.
    const alias = pair?.status === "accepted" ? aliasFor(pair, q.viewer) : null;
    // An unknown sender answers empty strings: the IM server then uses its
    // own configurable fallback name.
    return { displayName: alias || profile?.displayName || "", avatarUrl: profile?.avatarUrl ?? "" };
  });

  const configRoute = (path: string, read: (c: ConfigSource) => Promise<Record<string, unknown> | null>) =>
    app.get(`${base}/${path}`, route, async (_request, reply) => {
      const doc = options.config ? await read(options.config) : null;
      if (!doc) return reply.status(404).send({ error: "not configured" });
      return doc;
    });
  configRoute("runtime-config", (c) => c.runtimeConfig());
  configRoute("e2ee-config", (c) => c.e2eeConfig());

  app.post(`${base}/delete-media`, route, async (request, reply) => {
    const body = parse(schemas.deleteMedia, request.body, reply);
    if (!body) return reply;
    const deleted = options.deleteMedia ? await options.deleteMedia(body.urls) : 0;
    return { deleted };
  });

  const notServed = (reply: FastifyReply, module: string) => reply.status(404).send({ error: `${module} module not served` });

  app.post(`${base}/calls/create`, route, async (request, reply) => {
    if (!options.calls) return notServed(reply, "call");
    const body = parse(schemas.callCreate, request.body, reply);
    if (!body) return reply;
    return options.calls.create(body);
  });
  app.post(`${base}/calls/answer`, route, async (request, reply) => {
    if (!options.calls) return notServed(reply, "call");
    const body = parse(schemas.callAnswer, request.body, reply);
    if (!body) return reply;
    return options.calls.answer(body);
  });
  app.post(`${base}/calls/settle`, route, async (request, reply) => {
    if (!options.calls) return notServed(reply, "call");
    const body = parse(schemas.callSettle, request.body, reply);
    if (!body) return reply;
    await options.calls.settle(body);
    return { ok: true };
  });
  app.post(`${base}/calls/upgrade`, route, async (request, reply) => {
    if (!options.calls) return notServed(reply, "call");
    const body = parse(schemas.callUpgrade, request.body, reply);
    if (!body) return reply;
    return options.calls.upgrade(body);
  });

  app.post(`${base}/group-ai-qa`, route, async (request, reply) => {
    if (!options.groupAi) return notServed(reply, "groupai");
    const body = parse(schemas.groupAi, request.body, reply);
    if (!body) return reply;
    return options.groupAi(body);
  });
}
