/**
 * A client for the host's administrator endpoints (talkwireAdminRoutes in
 * @talkwire/host-node): a user's conversations, their messages, clearing a
 * two-person conversation, and call records. The host signs its
 * administrators in; pass their credentials through `headers`.
 */

export interface Person {
  uid: string;
  /** The host's id for the user; null for a uid the host does not know. */
  userId: string | null;
  displayName: string | null;
}

export interface AdminTopic {
  topic: string;
  isGroup: boolean;
  name?: string;
  lastSeq: number;
  lastContent?: string;
  lastContentType?: string;
  lastMessageAt?: number;
  messageCount: number;
  members: Person[];
  clearVersion: number;
  clearBeforeSeq: number;
}

export interface AdminMessage {
  topic: string;
  seq: number;
  from: string;
  sender: Person;
  content: string;
  contentType: string;
  fileUrl?: string;
  fileName?: string;
  ts: number;
  duration?: number;
  senderKind?: string;
}

export type MessageType = "text" | "image" | "video" | "audio" | "file";

export interface MessagesQuery {
  beforeSeq?: number;
  limit?: number;
  type?: MessageType;
  keyword?: string;
  fromMs?: number;
  toMs?: number;
}

export interface ClearRequest {
  /** Exactly clearConfirmation(topic). */
  confirm: string;
  reason: string;
  /** The same key again answers the first attempt instead of clearing twice. */
  idempotencyKey: string;
  /** The user whose records the administrator was looking at, for the audit. */
  targetUserId?: string;
}

export interface ClearResult {
  deletedMessages: number;
  deletedMedia: number;
  clearVersion: number;
  clearBeforeSeq: number;
  auditId: string;
  /** True when this answered an earlier attempt with the same key. */
  replayed: boolean;
}

export type CallStatus = "ringing" | "active" | "ended";
export type CallType = "voice" | "video";

export interface CallRecord {
  callSessionId: string;
  callerUserId: string;
  calleeUserId: string;
  caller: Person;
  callee: Person;
  requestedType: CallType;
  callType: CallType;
  status: CallStatus;
  /** ISO 8601. */
  createdAt: string;
  answeredAt: string | null;
  upgradedAt: string | null;
  endedAt: string | null;
  endReason: string | null;
  durationSec: number | null;
}

export interface CallsQuery {
  userId?: string;
  status?: CallStatus;
  callType?: CallType;
  endReason?: string;
  fromMs?: number;
  toMs?: number;
  cursor?: string;
  limit?: number;
}

/** What a superadministrator types to clear a conversation; the server checks the same text. */
export function clearConfirmation(topic: string): string {
  return `CLEAR ${topic}`;
}

/** A refused request: the server's status and its error text. */
export class ImAdminError extends Error {
  constructor(
    readonly status: number,
    message: string,
    /** For a failed clear: the audit entry that recorded it. */
    readonly auditId?: string,
  ) {
    super(message);
    this.name = "ImAdminError";
  }
}

export interface ImAdminApiOptions {
  /** Where talkwireAdminRoutes live; default /api/v1/admin/im. */
  baseUrl?: string;
  /** Headers for every request, typically the administrator's Authorization. */
  headers?: () => Record<string, string> | Promise<Record<string, string>>;
  fetch?: typeof fetch;
}

type Query = Record<string, string | number | undefined>;

export class ImAdminApi {
  private readonly base: string;
  private readonly doFetch: typeof fetch;

  constructor(private readonly options: ImAdminApiOptions = {}) {
    this.base = (options.baseUrl ?? "/api/v1/admin/im").replace(/\/$/, "");
    this.doFetch = options.fetch ?? ((...args) => globalThis.fetch(...args));
  }

  userTopics(userId: string, limit?: number): Promise<{ uid: string; topics: AdminTopic[] }> {
    return this.get(`/users/${encodeURIComponent(userId)}/topics`, { limit });
  }

  topic(topic: string): Promise<AdminTopic> {
    return this.get(`/topics/${encodeURIComponent(topic)}`);
  }

  messages(topic: string, query: MessagesQuery = {}): Promise<{ hasMore: boolean; messages: AdminMessage[] }> {
    return this.get(`/topics/${encodeURIComponent(topic)}/messages`, { ...query });
  }

  clear(topic: string, request: ClearRequest): Promise<ClearResult> {
    return this.send("POST", `/topics/${encodeURIComponent(topic)}/clear`, undefined, request);
  }

  calls(query: CallsQuery = {}): Promise<{ calls: CallRecord[]; nextCursor: string | null }> {
    return this.get("/calls", { ...query });
  }

  call(callSessionId: string): Promise<CallRecord> {
    return this.get(`/calls/${encodeURIComponent(callSessionId)}`);
  }

  private get<T>(path: string, query?: Query): Promise<T> {
    return this.send("GET", path, query);
  }

  private async send<T>(method: string, path: string, query?: Query, body?: unknown): Promise<T> {
    const params = new URLSearchParams();
    for (const [k, v] of Object.entries(query ?? {})) if (v !== undefined && v !== "") params.set(k, String(v));
    const qs = params.toString();
    const url = this.base + path + (qs ? `?${qs}` : "");
    const headers: Record<string, string> = { ...(await this.options.headers?.()) };
    if (body !== undefined) headers["content-type"] = "application/json";
    const res = await this.doFetch(url, { method, headers, body: body === undefined ? undefined : JSON.stringify(body) });
    const text = await res.text();
    let json: unknown;
    try {
      json = text ? JSON.parse(text) : undefined;
    } catch {
      json = undefined;
    }
    if (!res.ok) {
      const err = (json ?? {}) as { error?: unknown; auditId?: unknown };
      const message = typeof err.error === "string" ? err.error : text.slice(0, 200) || `HTTP ${res.status}`;
      throw new ImAdminError(res.status, message, typeof err.auditId === "string" ? err.auditId : undefined);
    }
    return json as T;
  }
}
