/**
 * What the client keeps. A conversation is keyed by the **peer's uid** for a
 * single chat and by the `grp_…` topic for a group (protocol section 5.5);
 * the canonical `p2p_…` topic only appears in history replies and is mapped
 * back to the peer before anything is stored.
 */

import { isGroupTopic, isUserAddress } from "@imkit/protocol";

/** A group topic, or the peer's uid for a single chat. */
export type ConversationId = string;

export const isGroupConversation = (id: ConversationId): boolean => isGroupTopic(id);

export function directConversation(peerUid: string): ConversationId {
  if (!isUserAddress(peerUid)) throw new Error("not a user uid: " + peerUid);
  return peerUid;
}

export type MessageStatus =
  /** Own message, published, no reply yet (or the reply timed out on a live connection). */
  | "sending"
  /** Own message the server stored: it has a seq. */
  | "sent"
  /** Own message that did not reach the server, or that the server refused. */
  | "failed"
  /** Someone else's message. */
  | "received";

/**
 * What a message is, decided by its `contentType` alone. The text in
 * `content` never decides the kind: a media message carries fallback text
 * there only for clients that do not know the type.
 */
export type MessageKind = "text" | "image" | "video" | "voice" | "file" | "system" | "custom";

export function messageKind(contentType: string, fileUrl?: string | null): MessageKind {
  const t = contentType.toLowerCase();
  if (t === "" || t === "text" || t.startsWith("text/")) return "text";
  if (t === "system") return "system";
  if (t === "image" || t.startsWith("image/")) return "image";
  if (t === "video" || t.startsWith("video/")) return "video";
  if (t === "voice" || t === "audio" || t.startsWith("audio/")) return "voice";
  if (t === "file" || (t.startsWith("application/") && fileUrl)) return "file";
  return "custom";
}

/** Why a send failed. `code` is the server's code; 0 means there was no server reply. */
export interface SendFailure {
  code: number;
  errCode?: string;
  reason?: string;
}

/** The server decided; sending the same request again will not change it. */
export const isDeterministicFailure = (f: SendFailure): boolean => f.code >= 400 && f.code <= 499;

export const SENDER_USER = "user";
export const SENDER_AI = "ai_assistant";
/** The reserved `from` of the group assistant's messages (section 9.2). */
export const AI_ASSISTANT_UID = "assistant_xiaotaozi";

export interface ChatMessage {
  conversation: ConversationId;
  /** Server sequence number; `null` until the server stored the message. */
  seq: number | null;
  /** The `clientMsgId` of an own message; stays the same across retries. */
  localId: string | null;
  from: string;
  content: string;
  contentType: string;
  fileUrl?: string;
  fileName?: string;
  /** Seconds of audio or video; 0 when not media. */
  duration: number;
  extra?: unknown;
  senderKind: string;
  forwardFromMsgId?: number;
  forwardFromTopic?: string;
  /** Server time for stored messages; local creation time for pending ones. */
  ts: number;
  status: MessageStatus;
  failure?: SendFailure;
}

/** A key that stays unique in a list: the seq once stored, the local id before. */
export const messageKey = (m: ChatMessage): string => (m.seq !== null ? "s:" + m.seq : "l:" + m.localId);

export const isPending = (m: ChatMessage): boolean => m.seq === null && (m.status === "sending" || m.status === "failed");

/** Timeline order: stored messages by seq, pending ones after all stored ones, by creation time. */
export function timelineOrder(a: ChatMessage, b: ChatMessage): number {
  if (a.seq !== null && b.seq !== null) return a.seq - b.seq;
  if (a.seq !== null) return -1;
  if (b.seq !== null) return 1;
  return a.ts - b.ts || (a.localId ?? "").localeCompare(b.localId ?? "");
}

export type PresenceState = "online" | "offline" | "unknown";

export interface GroupInfo {
  topic: string;
  name: string;
  owner: string;
  memberCount: number;
  encrypted: boolean;
}

export type GroupRole = "owner" | "admin" | "member";

/** Unknown or missing roles fall back to the least privileged one. */
export function groupRole(wire: unknown): GroupRole {
  return wire === "owner" || wire === "admin" ? wire : "member";
}

export const canManage = (role: GroupRole): boolean => role !== "member";

export interface GroupMember {
  uid: string;
  role: GroupRole;
  joinedAt: number;
}

/** Per-conversation local state, as the store keeps it. */
export interface StoredConversation {
  id: ConversationId;
  /** Group name; undefined for single chats (the host names people). */
  title?: string;
  /** My read position: messages up to and including this seq are read. */
  lastReadSeq: number;
  /** Single chats: how far the peer has read. */
  peerReadSeq: number;
  draft?: string;
  pinned: boolean;
  muted: boolean;
  /** A group I am no longer in (kicked, left, disbanded). */
  left: boolean;
  /** Last activity, for ordering the conversation list. */
  updatedAt: number;
}

export function newConversation(id: ConversationId, title?: string): StoredConversation {
  const c: StoredConversation = { id, lastReadSeq: 0, peerReadSeq: 0, pinned: false, muted: false, left: false, updatedAt: 0 };
  if (title !== undefined) c.title = title;
  return c;
}

/** A conversation as the list shows it. */
export interface ConversationSummary extends StoredConversation {
  isGroup: boolean;
  lastMessage?: ChatMessage;
  unread: number;
}

/** The clear watermark of a conversation (section 11.3). */
export interface ClearState {
  clearVersion: number;
  clearBeforeSeq: number;
}
