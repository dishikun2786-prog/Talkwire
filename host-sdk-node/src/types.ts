// Data the host keeps for ImKit, and the storage interfaces the SDK uses to
// reach it. A host implements them over its own database, or uses the
// reference implementations: memory (tests, demos) and Prisma.
//
// Host user ids are opaque strings chosen by the host. The IM server maps
// each to a wire uid (see uid.ts); the SDK never needs the reverse mapping
// except through ProfileStore.findByUid.

/** A user's chat profile. */
export interface Profile {
  userId: string;
  /** The wire uid of userId (imUid), stored so a uid can be looked up. */
  uid: string;
  /** The public handle other users search for; unique, compared case-insensitively. */
  handle: string;
  /** When the user last changed their handle; null if never. */
  handleChangedAt: Date | null;
  displayName: string;
  avatarUrl: string | null;
  createdAt: Date;
  updatedAt: Date;
}

export type NewProfile = Pick<Profile, "userId" | "handle" | "displayName" | "avatarUrl">;
export type ProfilePatch = Partial<Pick<Profile, "handle" | "handleChangedAt" | "displayName" | "avatarUrl">>;

export interface ProfileStore {
  get(userId: string): Promise<Profile | null>;
  getMany(userIds: string[]): Promise<Profile[]>;
  /** Case-insensitive. */
  findByHandle(handle: string): Promise<Profile | null>;
  findByUid(uid: string): Promise<Profile | null>;
  findManyByUid(uids: string[]): Promise<Profile[]>;
  /** Creates the profile; throws HandleTakenError when the handle is taken. */
  create(profile: NewProfile): Promise<Profile>;
  /** Updates it; throws HandleTakenError when a new handle is taken. */
  update(userId: string, patch: ProfilePatch): Promise<Profile>;
}

export type ContactStatus = "pending" | "accepted" | "rejected" | "blocked";

/**
 * The relationship between two users. There is at most one per pair of
 * users, whoever asked first: requesterId asked recipientId. Remarks are
 * private and directional: requesterAlias is the requester's remark for the
 * recipient, recipientAlias the recipient's remark for the requester. A user
 * must only ever see their own.
 */
export interface Contact {
  id: string;
  requesterId: string;
  recipientId: string;
  status: ContactStatus;
  requesterAlias: string | null;
  recipientAlias: string | null;
  /** Who blocked, when status is blocked. */
  blockedBy: string | null;
  createdAt: Date;
  updatedAt: Date;
}

export type NewContact = Pick<Contact, "requesterId" | "recipientId" | "status">;
export type ContactPatch = Partial<
  Pick<Contact, "requesterId" | "recipientId" | "status" | "requesterAlias" | "recipientAlias" | "blockedBy">
>;

export interface ContactListQuery {
  status: ContactStatus;
  /** sent: the user is the requester; received: the recipient; all: either. */
  direction: "sent" | "received" | "all";
  /** Only rows updated after this instant. */
  updatedAfter?: Date;
  /** Only rows updated at or before this instant. */
  updatedUntil?: Date;
}

export interface ContactStore {
  get(id: string): Promise<Contact | null>;
  /** The relationship between a and b, in either direction. */
  findPair(a: string, b: string): Promise<Contact | null>;
  /** Newest first. */
  list(userId: string, query: ContactListQuery): Promise<Contact[]>;
  /** Creates it; throws PairExistsError when the pair already has one. */
  create(contact: NewContact): Promise<Contact>;
  update(id: string, patch: ContactPatch): Promise<Contact>;
  delete(id: string): Promise<void>;
  /**
   * Runs fn with no other withPair of the same pair running, so the
   * read-then-write of a contact change cannot interleave with another.
   */
  withPair<T>(a: string, b: string, fn: () => Promise<T>): Promise<T>;
}

/** One administrator action on chat records. Metadata only: never message content. */
export interface AuditEntry {
  id: string;
  adminId: string;
  action: string;
  targetUserId: string | null;
  topic: string | null;
  reason: string | null;
  status: "pending" | "success" | "failed";
  idempotencyKey: string | null;
  details: Record<string, unknown> | null;
  createdAt: Date;
  completedAt: Date | null;
}

export type NewAuditEntry = Pick<AuditEntry, "adminId" | "action" | "status"> &
  Partial<Pick<AuditEntry, "targetUserId" | "topic" | "reason" | "idempotencyKey" | "details">>;

export interface AuditStore {
  /** Throws DuplicateKeyError when idempotencyKey is already used. */
  create(entry: NewAuditEntry): Promise<AuditEntry>;
  /** Records the outcome; details replaces the entry's details, null keeps them. */
  finish(id: string, status: "success" | "failed", details: Record<string, unknown> | null): Promise<AuditEntry>;
  get(id: string): Promise<AuditEntry | null>;
  findByIdempotencyKey(key: string): Promise<AuditEntry | null>;
}

/** The host's live settings for the IM server; null answers 404 ("no document"). */
export interface ConfigSource {
  runtimeConfig(): Promise<Record<string, unknown> | null>;
  e2eeConfig(): Promise<Record<string, unknown> | null>;
}

export type CallType = "voice" | "video";

/**
 * ringing until the callee answers, active while they talk, ended once the
 * IM server settled it (endReason says how: user_hangup, no_answer, rejected,
 * cancelled, disconnected or failed).
 */
export type CallStatus = "ringing" | "active" | "ended";

/**
 * One call as the host's call handlers saw it (see recordingCalls). Metadata
 * only: no channel ids or RTC credentials, nothing said in the call.
 */
export interface CallRecord {
  callSessionId: string;
  callerUserId: string;
  calleeUserId: string;
  /** What the caller asked for. */
  requestedType: CallType;
  /** What the call was in the end: a video call answered as voice is voice, an upgraded one is video. */
  callType: CallType;
  status: CallStatus;
  createdAt: Date;
  answeredAt: Date | null;
  upgradedAt: Date | null;
  endedAt: Date | null;
  endReason: string | null;
  /** Seconds from answering to the end: null until it ended, 0 if never answered. */
  durationSec: number | null;
}

export type NewCallRecord = Pick<CallRecord, "callSessionId" | "callerUserId" | "calleeUserId" | "requestedType">;

export interface CallRecordQuery {
  /** Calls this user made or received. */
  userId?: string;
  status?: CallStatus;
  callType?: CallType;
  endReason?: string;
  /** Created at or after. */
  from?: Date;
  /** Created at or before. */
  to?: Date;
  /** A nextCursor from the previous page. */
  cursor?: string;
  limit: number;
}

export interface CallRecordPage {
  /** Newest first. */
  records: CallRecord[];
  /** Pass as cursor for the next page; null on the last one. */
  nextCursor: string | null;
}

/**
 * Call records for administrators. Every step is idempotent and only moves a
 * call forward: answering an ended call, or ending it twice, changes nothing
 * and answers the record as it is (null when there is none).
 */
export interface CallRecordStore {
  /** Records a ringing call; the same callSessionId again answers the existing record. */
  create(call: NewCallRecord, at?: Date): Promise<CallRecord>;
  answered(callSessionId: string, callType: CallType, at?: Date): Promise<CallRecord | null>;
  upgraded(callSessionId: string, at?: Date): Promise<CallRecord | null>;
  ended(callSessionId: string, reason: string, at?: Date): Promise<CallRecord | null>;
  get(callSessionId: string): Promise<CallRecord | null>;
  list(query: CallRecordQuery): Promise<CallRecordPage>;
}

export class HandleTakenError extends Error {
  constructor(handle: string) {
    super(`handle taken: ${handle}`);
    this.name = "HandleTakenError";
  }
}

export class PairExistsError extends Error {
  constructor() {
    super("the pair already has a relationship");
    this.name = "PairExistsError";
  }
}

export class DuplicateKeyError extends Error {
  constructor(key: string) {
    super(`duplicate key: ${key}`);
    this.name = "DuplicateKeyError";
  }
}
