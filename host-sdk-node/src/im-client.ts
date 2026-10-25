// A client for the endpoints the IM server exposes to the host
// (contract/host-api/im-internal.openapi.yaml).

import { signInternalToken } from "./token.js";

export interface ImClientOptions {
  /** The IM server's base URL, e.g. http://127.0.0.1:6062. */
  baseUrl: string;
  /** The secret shared with the IM server (its JWT_SECRET). */
  secret: string;
  /** Per-request timeout; default 5 s. */
  timeoutMs?: number;
  /** Override for tests. */
  fetch?: typeof fetch;
}

/** A non-2xx answer, or a body that is not the expected JSON. */
export class ImClientError extends Error {
  constructor(
    readonly status: number,
    readonly body: string,
    message: string,
  ) {
    super(message);
    this.name = "ImClientError";
  }
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
  members: string[] | null;
  clearVersion: number;
  clearBeforeSeq: number;
}

export interface AdminMessage {
  topic: string;
  seq: number;
  from: string;
  content: string;
  contentType: string;
  fileUrl?: string;
  fileName?: string;
  ts: number;
  duration?: number;
  extra?: unknown;
  senderKind?: string;
  forwardFromMsgId?: number;
  forwardFromTopic?: string;
}

export interface AdminMessagesQuery {
  topic: string;
  beforeSeq?: number;
  limit?: number;
  type?: "text" | "image" | "video" | "audio" | "file";
  keyword?: string;
  fromMs?: number;
  toMs?: number;
}

export interface ClearResult {
  deletedMessages: number;
  deletedMedia: number;
  clearVersion: number;
  clearBeforeSeq: number;
}

/** Where a custom update goes: everyone connected to a conversation, or every device of one user. */
export type BroadcastTarget = { topic: string; targetUid?: never } | { targetUid: string; topic?: never };

export class ImClient {
  private readonly base: string;
  private readonly timeoutMs: number;
  private readonly doFetch: typeof fetch;

  constructor(private readonly options: ImClientOptions) {
    this.base = options.baseUrl.replace(/\/$/, "");
    this.timeoutMs = options.timeoutMs ?? 5000;
    this.doFetch = options.fetch ?? fetch;
  }

  private async call<T>(method: "GET" | "POST", path: string, body?: unknown, query?: Record<string, string | number | undefined>): Promise<T> {
    const url = new URL(this.base + path);
    for (const [k, v] of Object.entries(query ?? {})) {
      if (v !== undefined && v !== "") url.searchParams.set(k, String(v));
    }
    const headers: Record<string, string> = { Authorization: `Bearer ${signInternalToken(this.options.secret)}` };
    if (body !== undefined) headers["Content-Type"] = "application/json";
    const res = await this.doFetch(url, {
      method,
      headers,
      body: body === undefined ? undefined : JSON.stringify(body),
      signal: AbortSignal.timeout(this.timeoutMs),
    });
    const text = await res.text();
    if (!res.ok) {
      let reason = text.trim();
      try {
        reason = (JSON.parse(text) as { error?: string }).error ?? reason;
      } catch {
        // plain text body (401, 405)
      }
      throw new ImClientError(res.status, text, `${method} ${path}: ${res.status} ${reason}`);
    }
    try {
      return JSON.parse(text) as T;
    } catch {
      throw new ImClientError(res.status, text, `${method} ${path}: response is not JSON`);
    }
  }

  /** A relationship was accepted or removed. */
  contactSync(appUserIdA: string, appUserIdB: string, event: "accepted" | "removed") {
    return this.call<{ topic: string; uidA: string; uidB: string }>("POST", "/api/v1/internal/im/contact-sync", {
      appUserIdA,
      appUserIdB,
      event,
    });
  }

  /** How many messages still reference url. */
  async mediaRefCount(url: string): Promise<number> {
    const res = await this.call<{ count: number }>("POST", "/api/v1/internal/im/media-ref-count", { url });
    return res.count;
  }

  topicMembersResolve(topic: string, checkAppUserIds: string[] = [], resolveUids: string[] = []) {
    return this.call<{
      members: Record<string, boolean>;
      resolved: Record<string, { appUserId: string | null; isMember: boolean }>;
    }>("POST", "/api/v1/internal/im/topic-members-resolve", { topic, checkAppUserIds, resolveUids });
  }

  /** Relays a host card update; best effort, nothing is queued. */
  async customBroadcast(target: BroadcastTarget, kind: string, payload: unknown): Promise<void> {
    await this.call("POST", "/api/v1/internal/im/custom-broadcast", { ...target, kind, payload });
  }

  /** @deprecated the reference deployment's form; use customBroadcast. */
  async redPacketBroadcast(topic: string, payload: unknown): Promise<void> {
    await this.call("POST", "/api/v1/internal/im/red-packet-broadcast", { topic, payload });
  }

  /** Sends a complete call frame to every device of targetUid; false when none is connected. */
  async callBroadcast(targetUid: string, payload: Record<string, unknown>): Promise<boolean> {
    const res = await this.call<{ delivered: boolean }>("POST", "/api/v1/internal/im/call-broadcast", { targetUid, payload });
    return res.delivered;
  }

  async adminTopics(uid: string, limit?: number): Promise<AdminTopic[]> {
    const res = await this.call<{ topics: AdminTopic[] | null }>("GET", "/api/v1/internal/admin/im/topics", undefined, { uid, limit });
    return res.topics ?? [];
  }

  adminTopic(topic: string) {
    return this.call<AdminTopic>("GET", "/api/v1/internal/admin/im/topic", undefined, { topic });
  }

  async adminMessages(query: AdminMessagesQuery): Promise<{ messages: AdminMessage[]; hasMore: boolean }> {
    const { topic, beforeSeq, limit, type, keyword, fromMs, toMs } = query;
    const res = await this.call<{ messages: AdminMessage[] | null; hasMore: boolean }>("GET", "/api/v1/internal/admin/im/messages", undefined, {
      topic,
      beforeSeq,
      limit,
      type,
      keyword,
      fromMs,
      toMs,
    });
    return { messages: res.messages ?? [], hasMore: res.hasMore };
  }

  /** Clears a two-person conversation for both users. */
  adminClear(topic: string) {
    return this.call<ClearResult>("POST", "/api/v1/internal/admin/im/clear", { topic });
  }
}
