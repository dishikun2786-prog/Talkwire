/**
 * Scan-to-join (protocol section 14), the web side of the `groupjoin`
 * module: a group's QR token, joining with it, and reviewing join requests.
 *
 * One method per protocol action, sending exactly the fields the spec names
 * (a replayed golden transcript checks every request), so `join` does not
 * subscribe to the group: on the legacy server the joiner is not subscribed
 * and nobody is told (D7), and the caller follows a `joined` with
 * `afterJoined`. The React components in `@talkwire/group-join/react` do.
 */

import {
  Emitter,
  RequestRefused,
  arr,
  bool,
  isOk,
  num,
  obj,
  optNum,
  optStr,
  str,
  type ChatEngine,
  type Frame,
  type ImClient,
  type ImEvent,
  type Unsubscribe,
} from "@talkwire/client";

export interface QrToken {
  topic: string;
  qrToken: string;
  /** Epoch milliseconds. */
  expiresAt: number;
}

/** What a QR token leads to, before joining. */
export interface QrPreview {
  topic: string;
  name: string;
  memberCount: number;
  alreadyMember: boolean;
  isFull: boolean;
  requireApproval: boolean;
}

export type JoinStatus = "joined" | "pending_approval";

export interface JoinRequest {
  id: number;
  requesterUid: string;
  /** The host's name for the applicant, as the reviewer sees them; their uid when unknown (F46). */
  requesterName: string;
  message?: string;
  /** Epoch milliseconds. */
  createdAt: number;
}

export type GroupJoinEvent =
  /** Someone asked to join a group I manage. */
  | { kind: "request"; topic: string; name: string; from: string }
  /** A manager decided on my request; on approval a `grp_sync` follows. */
  | { kind: "reviewed"; topic: string; name: string; status: "approved" | "rejected" };

/** `joinMessage` is cut to this many code points by the server; the UI limits input to it. */
export const JOIN_MESSAGE_MAX = 60;

export class GroupJoinClient {
  readonly events = new Emitter<GroupJoinEvent>();
  private off: Unsubscribe | null = null;

  /** Listens to the client's pushes from the start. */
  constructor(readonly client: ImClient) {
    this.start();
  }

  /** Listens again after `dispose`; does nothing while listening (React StrictMode mounts twice). */
  start(): void {
    this.off ??= this.client.events.on((ev) => this.onEvent(ev));
  }

  /** Stops listening to the client's pushes. */
  dispose(): void {
    this.off?.();
    this.off = null;
  }

  /**
   * The group's current QR token; the same one until a manager refreshes it.
   * `refresh` (managers only) replaces it, and the old one stops working at
   * once. Members only: others get a RequestRefused with code 401.
   */
  async qrToken(topic: string, opts: { refresh?: boolean } = {}): Promise<QrToken> {
    const frame: Frame = { action: "group_qr_token", topic };
    if (opts.refresh) frame.refreshQr = true;
    const reply = await this.client.request(frame);
    if (!isOk(reply)) throw new RequestRefused(reply);
    return { topic: str(reply.frame, "topic"), qrToken: str(reply.frame, "qrToken"), expiresAt: num(reply.frame, "expiresAt") };
  }

  /** The group behind a token; null when the token is unknown, expired or replaced (404). */
  async resolve(qrToken: string): Promise<QrPreview | null> {
    const reply = await this.client.request({ action: "group_qr_resolve", qrToken });
    if (reply.code === 404) return null;
    if (!isOk(reply)) throw new RequestRefused(reply);
    const f = reply.frame;
    return {
      topic: str(f, "topic"),
      name: str(f, "name"),
      memberCount: num(f, "memberCount"),
      alreadyMember: bool(f, "alreadyMember"),
      isFull: bool(f, "isFull"),
      requireApproval: bool(f, "requireApproval"),
    };
  }

  /**
   * Joins with a token, no friendship needed. `joined` also when already a
   * member; `pending_approval` when a manager must approve (asking twice
   * does not tell them twice). A full group is a RequestRefused with 409.
   */
  async join(qrToken: string, message?: string): Promise<{ topic: string; status: JoinStatus }> {
    const frame: Frame = { action: "group_join", qrToken };
    if (message) frame.joinMessage = message;
    const reply = await this.client.request(frame);
    if (!isOk(reply)) throw new RequestRefused(reply);
    const status = str(reply.frame, "joinStatus") === "pending_approval" ? "pending_approval" : "joined";
    return { topic: str(reply.frame, "topic"), status };
  }

  /** Pending requests of a group, oldest first as the server lists them. Managers only. */
  async requests(topic: string): Promise<JoinRequest[]> {
    const reply = await this.client.request({ action: "group_join_requests_list", topic });
    if (!isOk(reply)) throw new RequestRefused(reply);
    return arr(reply.frame, "joinRequests")
      .map(obj)
      .filter((o): o is Frame => !!o && optNum(o, "id") !== undefined)
      .map((o) => {
        const r: JoinRequest = { id: num(o, "id"), requesterUid: str(o, "requesterUid"), requesterName: str(o, "requesterName") || str(o, "requesterUid"), createdAt: num(o, "createdAt") };
        const message = optStr(o, "message");
        if (message) r.message = message;
        return r;
      });
  }

  /**
   * Approves or rejects one request. `topic` must be the request's own group,
   * from the push or the list. Already handled: RequestRefused with 409.
   */
  async review(topic: string, joinRequestId: number, approve: boolean): Promise<"approved" | "rejected"> {
    const reply = await this.client.request({ action: "group_join_review", topic, joinRequestId, approve });
    if (!isOk(reply)) throw new RequestRefused(reply);
    return str(reply.frame, "joinStatus") === "approved" ? "approved" : "rejected";
  }

  /** Whether joining by QR needs a manager's approval. Members only. */
  async requireApproval(topic: string): Promise<boolean> {
    const reply = await this.client.request({ action: "get_group_join_settings", topic });
    if (!isOk(reply)) throw new RequestRefused(reply);
    return bool(reply.frame, "requireApproval");
  }

  /** Turns approval on or off; the owner only. Returns the new value. */
  async setRequireApproval(topic: string, requireApproval: boolean): Promise<boolean> {
    const reply = await this.client.request({ action: "update_group_join_settings", topic, requireApproval });
    if (!isOk(reply)) throw new RequestRefused(reply);
    return bool(reply.frame, "requireApproval");
  }

  private onEvent(ev: ImEvent): void {
    if (ev.kind !== "module") return;
    const f = ev.frame;
    const action = str(f, "action");
    if (action === "group_join_request") {
      this.events.emit({ kind: "request", topic: str(f, "topic"), name: str(f, "name"), from: str(f, "from") });
    } else if (action === "group_join_reviewed") {
      const status = str(f, "joinStatus") === "approved" ? "approved" : "rejected";
      this.events.emit({ kind: "reviewed", topic: str(f, "topic"), name: str(f, "name"), status });
    } else {
      return;
    }
    // Delivered from the offline queue (strict servers, section 10.10).
    if (ev.eventId !== undefined) this.client.ackGroupEvent(ev.eventId);
  }
}

/**
 * After `joined`: subscribes to the group and lists it. The legacy server
 * neither subscribes the joiner nor tells anyone (D7); on a strict server
 * this is harmless.
 */
export async function afterJoined(engine: ChatEngine, topic: string, name?: string): Promise<void> {
  await engine.ensureConversation(topic, name);
  await engine.subscribe(topic).catch(() => {});
  await engine.refreshGroups().catch(() => null);
}
