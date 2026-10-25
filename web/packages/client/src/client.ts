/**
 * `ImClient`: the connection plus one method per protocol action. Methods
 * send exactly the fields the spec names, nothing else, so a replayed golden
 * transcript can check every request.
 */

import { ImConnection } from "./connection.js";
import { ImError, isOk, type Reply } from "./errors.js";
import { groupRole, type GroupInfo, type GroupMember, type GroupRole } from "./model.js";
import { arr, bool, num, obj, optNum, optStr, parseWireMessage, str, type Frame, type WireMessage } from "./wire.js";

/** The server refused a request whose result the caller needs. */
export class RequestRefused extends Error {
  constructor(readonly reply: Reply) {
    super(`${str(reply.frame, "action")} refused: ${reply.code}${reply.reason ? " " + reply.reason : ""}`);
    this.name = "RequestRefused";
  }
}

export interface PublishFields {
  content?: string;
  contentType?: string;
  fileUrl?: string;
  fileName?: string;
  duration?: number;
  extra?: unknown;
}

export type PublishOutcome =
  | { kind: "stored"; topic: string; seq: number; ts: number }
  /** The server said no; resending the same request will not help when `code` is 4xx. */
  | { kind: "refused"; reply: Reply }
  /** No answer: the server may have stored it. Resend with the same clientMsgId (section 9.1). */
  | { kind: "unknown"; why: "timeout" | "connection_lost" | "not_connected" };

export interface HistoryPage {
  /** The topic as the request named it. */
  topic: string;
  /** Ascending by seq. */
  messages: WireMessage[];
  clearVersion: number;
  clearBeforeSeq: number;
  /** Groups with read receipts on: uid → seq read. */
  readCursors?: Map<string, number>;
  /** strict servers: my own read position in this conversation. */
  readSeq?: number;
}

export interface PresenceSnapshot {
  uid: string;
  online: boolean;
  rev: number;
  /** 0 when unknown. */
  lastSeenAt: number;
}

export interface ClearResult {
  clearVersion: number;
  clearBeforeSeq: number;
  deletedMessages: number;
  deletedMedia: number;
}

export class ImClient extends ImConnection {
  // ---------------------------------------------------------------- messages

  subscribe(topic: string): Promise<Reply> {
    return this.request({ action: "sub", topic });
  }

  /** Cancels this connection's subscription only (section 9.4); single chats need the `p2p_…` topic. */
  leave(topic: string): Promise<Reply> {
    return this.request({ action: "leave", topic });
  }

  async publish(topic: string, fields: PublishFields, clientMsgId?: string): Promise<PublishOutcome> {
    const frame: Frame = { action: "pub", topic };
    if (fields.content !== undefined) frame.content = fields.content;
    if (fields.contentType !== undefined) frame.contentType = fields.contentType;
    if (fields.fileUrl) frame.fileUrl = fields.fileUrl;
    if (fields.fileName) frame.fileName = fields.fileName;
    if (fields.duration) frame.duration = fields.duration;
    if (fields.extra !== undefined && fields.extra !== null) frame.extra = fields.extra;
    if (clientMsgId) frame.clientMsgId = clientMsgId;
    let reply: Reply;
    try {
      reply = await this.request(frame, this.config.publishTimeoutMs);
    } catch (e) {
      if (e instanceof ImError) return { kind: "unknown", why: e.kind };
      throw e;
    }
    if (reply.code === 200) return { kind: "stored", topic: str(reply.frame, "topic") || topic, seq: num(reply.frame, "seq"), ts: num(reply.frame, "ts") };
    return { kind: "refused", reply };
  }

  /** One page of history (section 9.3). Throws RequestRefused on an error reply. */
  async history(topic: string, opts: { limit?: number; beforeSeq?: number; afterSeq?: number } = {}): Promise<HistoryPage> {
    const frame: Frame = { action: "get", topic };
    if (opts.beforeSeq) frame.beforeSeq = opts.beforeSeq;
    if (opts.afterSeq) frame.afterSeq = opts.afterSeq;
    if (opts.limit) frame.limit = opts.limit;
    const reply = await this.request(frame);
    if (reply.code !== 200) throw new RequestRefused(reply);
    const f = reply.frame;
    const page: HistoryPage = {
      topic: str(f, "topic") || topic,
      messages: arr(f, "messages")
        .map((m) => (obj(m) ? parseWireMessage(obj(m)!) : null))
        .filter((m): m is WireMessage => m !== null)
        .sort((a, b) => a.seq - b.seq),
      clearVersion: num(f, "clearVersion"),
      clearBeforeSeq: num(f, "clearBeforeSeq"),
    };
    if (Array.isArray(f.readCursors)) {
      page.readCursors = new Map();
      for (const c of f.readCursors) {
        const o = obj(c);
        if (o && str(o, "uid")) page.readCursors.set(str(o, "uid"), num(o, "seq"));
      }
    }
    const readSeq = optNum(f, "readSeq");
    if (readSeq !== undefined) page.readSeq = readSeq;
    return page;
  }

  /** Recalls an own message (section 9.5). */
  recall(topic: string, seq: number): Promise<Reply> {
    return this.request({ action: "del", topic, seq });
  }

  /** Clears the conversation for everyone, permanently (section 11.2). */
  async clearTopic(topic: string): Promise<ClearResult> {
    const reply = await this.request({ action: "del_topic", topic });
    if (reply.code !== 200) throw new RequestRefused(reply);
    const f = reply.frame;
    return {
      clearVersion: num(f, "clearVersion"),
      clearBeforeSeq: num(f, "clearBeforeSeq"),
      deletedMessages: num(f, "deletedMessages"),
      deletedMedia: num(f, "deletedMedia"),
    };
  }

  /** "Typing…" (no reply). */
  sendTyping(topic: string): boolean {
    return this.sendNoReply({ action: "note", topic, what: "kp" });
  }

  /** Read up to and including `seq` (no reply). */
  sendRead(topic: string, seq: number): boolean {
    return this.sendNoReply({ action: "note", topic, what: "read", seq });
  }

  // ---------------------------------------------------------------- presence

  /** Presence of `uids`; an empty list asks for everyone I share a conversation with (`*`). */
  async queryPresence(uids: readonly string[]): Promise<PresenceSnapshot[]> {
    const frame: Frame = { action: "pres" };
    if (uids.length === 0) frame.topic = "*";
    else if (uids.length === 1) frame.topic = uids[0];
    else frame.topics = [...uids];
    const reply = await this.request(frame);
    if (reply.code !== 200) throw new RequestRefused(reply);
    return arr(reply.frame, "states")
      .map(obj)
      .filter((o): o is Frame => !!o && !!str(o, "uid"))
      .map((o) => ({ uid: str(o, "uid"), online: bool(o, "online"), rev: num(o, "presRev"), lastSeenAt: num(o, "lastSeenAt") }));
  }

  // ---------------------------------------------------------------- groups

  /** Creates a group and returns its topic. */
  async createGroup(name: string, members: readonly string[]): Promise<string> {
    const reply = await this.request({ action: "create_grp", name, members: [...members] });
    if (!isOk(reply)) throw new RequestRefused(reply);
    return str(reply.frame, "topic");
  }

  invite(topic: string, user: string): Promise<Reply> {
    return this.request({ action: "invite", topic, user });
  }

  kick(topic: string, user: string): Promise<Reply> {
    return this.request({ action: "kick", topic, user });
  }

  /** Leaves a group. Never for a single chat (D2). */
  leaveGroup(topic: string): Promise<Reply> {
    return this.request({ action: "leave_grp", topic });
  }

  renameGroup(topic: string, name: string): Promise<Reply> {
    return this.request({ action: "update_grp", topic, name });
  }

  setMemberRole(topic: string, user: string, role: Exclude<GroupRole, "owner">): Promise<Reply> {
    return this.request({ action: "set_member_role", topic, user, memberRole: role });
  }

  transferOwner(topic: string, user: string): Promise<Reply> {
    return this.request({ action: "transfer_owner", topic, user });
  }

  disbandGroup(topic: string): Promise<Reply> {
    return this.request({ action: "disband_grp", topic });
  }

  async getGroups(): Promise<GroupInfo[]> {
    const reply = await this.request({ action: "get_groups" });
    if (reply.code !== 200) throw new RequestRefused(reply);
    return arr(reply.frame, "groups")
      .map(obj)
      .filter((o): o is Frame => !!o && !!str(o, "topic"))
      .map((o) => ({ topic: str(o, "topic"), name: str(o, "name"), owner: str(o, "owner"), memberCount: num(o, "members"), encrypted: bool(o, "encrypted") }));
  }

  async getMembers(topic: string): Promise<GroupMember[]> {
    const reply = await this.request({ action: "get_members", topic });
    if (reply.code !== 200) throw new RequestRefused(reply);
    return arr(reply.frame, "members")
      .map(obj)
      .filter((o): o is Frame => !!o && !!str(o, "uid"))
      .map((o) => ({ uid: str(o, "uid"), role: groupRole(o.role), joinedAt: num(o, "joinedAt") }));
  }

  // ---------------------------------------------------------------- acks and push

  /** Acknowledges a clear delivered while offline, by the id it came with (section 11.3). */
  ackTopicCleared(pendingId: string): boolean {
    return this.sendNoReply({ action: "ack_topic_cleared", id: pendingId });
  }

  /** Acknowledges an offline group event (section 10.10). */
  ackGroupEvent(eventId: number): boolean {
    return this.sendNoReply({ action: "ack_group_event", eventId });
  }

  /** Registers this device's push token: for Web Push, the subscription's JSON (section 17). */
  registerPushToken(token: string, provider: "webpush" | "fcm" | "apns" = "webpush"): boolean {
    return this.sendNoReply({ action: "reg_token", token, provider });
  }

  unregisterPushToken(token: string, provider: "webpush" | "fcm" | "apns" = "webpush"): boolean {
    return this.sendNoReply({ action: "unreg_token", token, provider });
  }

  // ---------------------------------------------------------------- forwarding

  /** Copies messages into another conversation in order (section 9.6). */
  async forwardBundle(sourceTopic: string, seqs: readonly number[], targetTopic: string): Promise<{ ok: boolean; reason?: string; insertedSeqs: number[] }> {
    const f = await this.requestTyped({ action: "forward_bundle", sourceTopic, sourceMsgIds: [...seqs], targetTopic });
    const out: { ok: boolean; reason?: string; insertedSeqs: number[] } = {
      ok: bool(f, "ok"),
      insertedSeqs: arr(f, "insertedSeqs").filter((n): n is number => typeof n === "number"),
    };
    const reason = optStr(f, "reason");
    if (reason) out.reason = reason;
    return out;
  }

  /** Forwards one message to several conversations; targets must be canonical topics (F22). */
  async forwardBroadcast(sourceTopic: string, seq: number, targetTopics: readonly string[]): Promise<{ topic: string; ok: boolean; seq?: number; reason?: string }[]> {
    const f = await this.requestTyped({ action: "forward_broadcast", sourceTopic, sourceMsgId: seq, targetTopics: [...targetTopics] });
    return arr(f, "results")
      .map(obj)
      .filter((o): o is Frame => !!o)
      .map((o) => {
        const r: { topic: string; ok: boolean; seq?: number; reason?: string } = { topic: str(o, "topic"), ok: bool(o, "ok") };
        const s = optNum(o, "seq");
        if (s !== undefined) r.seq = s;
        const reason = optStr(o, "reason");
        if (reason) r.reason = reason;
        return r;
      });
  }
}
