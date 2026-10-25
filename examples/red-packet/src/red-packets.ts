// A host-owned message type: the red packet. The host owns everything about
// it (who may claim, how much each gets, the money); ImKit only carries the
// message that shows the card and the live updates to it.
//
//   1. The sender creates a packet here and gets its id.
//   2. The sender's client publishes an ordinary message carrying the id:
//      {"action":"pub","topic":...,"contentType":"red_packet","content":"[Red packet]","extra":{"redPacketId":...}}
//   3. A member claims it here. The host checks membership with the IM
//      server, records the claim, and pushes the card's new state to the
//      members connected to the conversation (custom-broadcast, kind
//      "red_packet"), who receive {"action":"custom_update","what":"red_packet",...}.
//   4. A client opening the card fetches its state here: updates are not
//      queued for clients that were offline.
//
// The ledger is in memory to keep the example short. A real host debits and
// credits its own balances in its own database, in one transaction with the
// claim, and pushes the update only after that commits.

import { randomUUID } from "node:crypto";
import type { FastifyInstance, FastifyRequest } from "fastify";
import type { ImClient } from "@imkit/host-node";

export interface RedPacketState {
  id: string;
  topic: string;
  senderId: string;
  total: number;
  count: number;
  claims: Array<{ userId: string; amount: number }>;
  createdAt: string;
}

export interface RedPacketOptions {
  im: ImClient;
  /** The signed-in user's host id, or null. */
  authenticate(request: FastifyRequest): Promise<string | null>;
}

/** The card a client shows: the state without who got how much, except the viewer's own share. */
export function cardFor(p: RedPacketState, viewer: string) {
  return {
    id: p.id,
    topic: p.topic,
    senderId: p.senderId,
    total: p.total,
    count: p.count,
    claimed: p.claims.length,
    remaining: p.count - p.claims.length,
    myAmount: p.claims.find((c) => c.userId === viewer)?.amount ?? null,
  };
}

/** Equal shares; the last claim takes what is left. */
function nextShare(p: RedPacketState): number {
  const left = p.total - p.claims.reduce((sum, c) => sum + c.amount, 0);
  const remaining = p.count - p.claims.length;
  return remaining === 1 ? left : Math.floor(p.total / p.count);
}

export async function redPacketRoutes(app: FastifyInstance, options: RedPacketOptions): Promise<void> {
  const packets = new Map<string, RedPacketState>();
  const { im } = options;

  const isMember = async (topic: string, userId: string) => {
    const res = await im.topicMembersResolve(topic, [userId]);
    return res.members[userId] === true;
  };

  app.post("/api/v1/red-packets", async (request, reply) => {
    const me = await options.authenticate(request);
    if (!me) return reply.status(401).send({ error: "unauthorized" });
    const { topic, total, count } = (request.body ?? {}) as { topic?: unknown; total?: unknown; count?: unknown };
    if (typeof topic !== "string" || !topic.startsWith("grp_")) return reply.status(400).send({ error: "topic must be a group" });
    if (!Number.isInteger(count) || (count as number) < 1 || (count as number) > 100) return reply.status(400).send({ error: "count must be 1-100" });
    if (!Number.isInteger(total) || (total as number) < (count as number) || (total as number) > 1_000_000) {
      return reply.status(400).send({ error: "total must be at least count" });
    }
    if (!(await isMember(topic, me))) return reply.status(403).send({ error: "not a member of the group" });
    // A real host debits the sender's balance here.
    const packet: RedPacketState = { id: randomUUID(), topic, senderId: me, total: total as number, count: count as number, claims: [], createdAt: new Date().toISOString() };
    packets.set(packet.id, packet);
    return {
      packet: cardFor(packet, me),
      // What the sender's client publishes to the group next.
      message: { contentType: "red_packet", content: "[Red packet]", extra: { redPacketId: packet.id } },
    };
  });

  app.get("/api/v1/red-packets/:id", async (request, reply) => {
    const me = await options.authenticate(request);
    if (!me) return reply.status(401).send({ error: "unauthorized" });
    const packet = packets.get((request.params as { id: string }).id);
    if (!packet || !(await isMember(packet.topic, me))) return reply.status(404).send({ error: "no such red packet" });
    return cardFor(packet, me);
  });

  app.post("/api/v1/red-packets/:id/claim", async (request, reply) => {
    const me = await options.authenticate(request);
    if (!me) return reply.status(401).send({ error: "unauthorized" });
    const packet = packets.get((request.params as { id: string }).id);
    if (!packet || !(await isMember(packet.topic, me))) return reply.status(404).send({ error: "no such red packet" });
    if (packet.claims.some((c) => c.userId === me)) return reply.status(409).send({ error: "already claimed", card: cardFor(packet, me) });
    if (packet.claims.length >= packet.count) return reply.status(409).send({ error: "all claimed", card: cardFor(packet, me) });
    // Single-threaded here; a real host makes the check and the claim one transaction.
    packet.claims.push({ userId: me, amount: nextShare(packet) });
    // A real host credits the claimer's balance here, then pushes.
    try {
      await im.customBroadcast({ topic: packet.topic }, "red_packet", cardFor(packet, ""));
    } catch (err) {
      // The claim stands; clients that missed the push see it when they fetch the card.
      request.log.warn({ err }, "red packet update not pushed");
    }
    return cardFor(packet, me);
  });
}
