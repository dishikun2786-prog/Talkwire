// In-memory stores: for tests, demos and single-process prototypes. Nothing
// survives a restart.

import { randomUUID } from "node:crypto";
import {
  DuplicateKeyError,
  HandleTakenError,
  PairExistsError,
  type AuditEntry,
  type AuditStore,
  type CallRecord,
  type CallRecordQuery,
  type CallRecordStore,
  type CallType,
  type ConfigSource,
  type Contact,
  type ContactListQuery,
  type ContactPatch,
  type ContactStore,
  type NewAuditEntry,
  type NewCallRecord,
  type NewContact,
  type NewProfile,
  type Profile,
  type ProfilePatch,
  type ProfileStore,
} from "./types.js";
import { imUid } from "./uid.js";
import { afterCursor, durationSec, newestFirst, pageOf } from "./call-records.js";

export function pairKey(a: string, b: string): string {
  return a < b ? `${a}\u0000${b}` : `${b}\u0000${a}`;
}

/** Serializes async work per key. */
export class KeyedMutex {
  private tails = new Map<string, Promise<unknown>>();

  async run<T>(key: string, fn: () => Promise<T>): Promise<T> {
    const previous = this.tails.get(key) ?? Promise.resolve();
    let release!: () => void;
    const mine = new Promise<void>((resolve) => (release = resolve));
    const tail = previous.then(() => mine);
    this.tails.set(key, tail);
    await previous;
    try {
      return await fn();
    } finally {
      release();
      if (this.tails.get(key) === tail) this.tails.delete(key);
    }
  }
}

const copy = <T extends object>(row: T): T => ({ ...row });

export class MemoryProfileStore implements ProfileStore {
  private rows = new Map<string, Profile>();

  async get(userId: string) {
    const row = this.rows.get(userId);
    return row ? copy(row) : null;
  }

  async getMany(userIds: string[]) {
    return userIds.flatMap((id) => {
      const row = this.rows.get(id);
      return row ? [copy(row)] : [];
    });
  }

  async findByHandle(handle: string) {
    const wanted = handle.toLowerCase();
    for (const row of this.rows.values()) {
      if (row.handle.toLowerCase() === wanted) return copy(row);
    }
    return null;
  }

  async findByUid(uid: string) {
    for (const row of this.rows.values()) {
      if (row.uid === uid) return copy(row);
    }
    return null;
  }

  async findManyByUid(uids: string[]) {
    const wanted = new Set(uids);
    return [...this.rows.values()].filter((row) => wanted.has(row.uid)).map(copy);
  }

  private assertHandleFree(handle: string, owner: string) {
    const wanted = handle.toLowerCase();
    for (const row of this.rows.values()) {
      if (row.userId !== owner && row.handle.toLowerCase() === wanted) throw new HandleTakenError(handle);
    }
  }

  async create(profile: NewProfile) {
    if (this.rows.has(profile.userId)) throw new Error(`profile exists: ${profile.userId}`);
    this.assertHandleFree(profile.handle, profile.userId);
    const now = new Date();
    const row: Profile = { ...profile, uid: imUid(profile.userId), handleChangedAt: null, createdAt: now, updatedAt: now };
    this.rows.set(row.userId, row);
    return copy(row);
  }

  async update(userId: string, patch: ProfilePatch) {
    const row = this.rows.get(userId);
    if (!row) throw new Error(`no profile: ${userId}`);
    if (patch.handle !== undefined) this.assertHandleFree(patch.handle, userId);
    const next: Profile = { ...row, ...patch, updatedAt: new Date() };
    this.rows.set(userId, next);
    return copy(next);
  }
}

export class MemoryContactStore implements ContactStore {
  private rows = new Map<string, Contact>();
  private mutex = new KeyedMutex();

  async get(id: string) {
    const row = this.rows.get(id);
    return row ? copy(row) : null;
  }

  async findPair(a: string, b: string) {
    const key = pairKey(a, b);
    for (const row of this.rows.values()) {
      if (pairKey(row.requesterId, row.recipientId) === key) return copy(row);
    }
    return null;
  }

  async list(userId: string, query: ContactListQuery) {
    return [...this.rows.values()]
      .filter((row) => {
        if (row.status !== query.status) return false;
        const sent = row.requesterId === userId;
        const received = row.recipientId === userId;
        if (query.direction === "sent" ? !sent : query.direction === "received" ? !received : !(sent || received)) return false;
        if (query.updatedAfter && !(row.updatedAt > query.updatedAfter)) return false;
        if (query.updatedUntil && row.updatedAt > query.updatedUntil) return false;
        return true;
      })
      .sort((x, y) => y.updatedAt.getTime() - x.updatedAt.getTime())
      .map(copy);
  }

  async create(contact: NewContact) {
    if (await this.findPair(contact.requesterId, contact.recipientId)) throw new PairExistsError();
    const now = new Date();
    const row: Contact = {
      ...contact,
      id: randomUUID(),
      requesterAlias: null,
      recipientAlias: null,
      blockedBy: null,
      createdAt: now,
      updatedAt: now,
    };
    this.rows.set(row.id, row);
    return copy(row);
  }

  async update(id: string, patch: ContactPatch) {
    const row = this.rows.get(id);
    if (!row) throw new Error(`no contact: ${id}`);
    const next: Contact = { ...row, ...patch, updatedAt: new Date() };
    if (next.status !== "blocked") next.blockedBy = null;
    this.rows.set(id, next);
    return copy(next);
  }

  async delete(id: string) {
    this.rows.delete(id);
  }

  withPair<T>(a: string, b: string, fn: () => Promise<T>) {
    return this.mutex.run(pairKey(a, b), fn);
  }
}

export class MemoryAuditStore implements AuditStore {
  private rows = new Map<string, AuditEntry>();

  async create(entry: NewAuditEntry) {
    if (entry.idempotencyKey && (await this.findByIdempotencyKey(entry.idempotencyKey))) {
      throw new DuplicateKeyError(entry.idempotencyKey);
    }
    const row: AuditEntry = {
      id: randomUUID(),
      adminId: entry.adminId,
      action: entry.action,
      status: entry.status,
      targetUserId: entry.targetUserId ?? null,
      topic: entry.topic ?? null,
      reason: entry.reason ?? null,
      idempotencyKey: entry.idempotencyKey ?? null,
      details: entry.details ?? null,
      createdAt: new Date(),
      completedAt: null,
    };
    this.rows.set(row.id, row);
    return copy(row);
  }

  async finish(id: string, status: "success" | "failed", details: Record<string, unknown> | null) {
    const row = this.rows.get(id);
    if (!row) throw new Error(`no audit entry: ${id}`);
    const next: AuditEntry = { ...row, status, details: details ?? row.details, completedAt: new Date() };
    this.rows.set(id, next);
    return copy(next);
  }

  async get(id: string) {
    const row = this.rows.get(id);
    return row ? copy(row) : null;
  }

  async findByIdempotencyKey(key: string) {
    for (const row of this.rows.values()) {
      if (row.idempotencyKey === key) return copy(row);
    }
    return null;
  }
}

export class MemoryCallRecordStore implements CallRecordStore {
  private rows = new Map<string, CallRecord>();

  async create(call: NewCallRecord, at = new Date()) {
    const existing = this.rows.get(call.callSessionId);
    if (existing) return { ...existing };
    const row: CallRecord = {
      ...call,
      callType: call.requestedType,
      status: "ringing",
      createdAt: at,
      answeredAt: null,
      upgradedAt: null,
      endedAt: null,
      endReason: null,
      durationSec: null,
    };
    this.rows.set(row.callSessionId, row);
    return { ...row };
  }

  async answered(callSessionId: string, callType: CallType, at = new Date()) {
    return this.move(callSessionId, (r) => (r.status === "ringing" ? { status: "active", callType, answeredAt: at } : null));
  }

  async upgraded(callSessionId: string, at = new Date()) {
    return this.move(callSessionId, (r) => (r.status === "active" && r.callType === "voice" ? { callType: "video", upgradedAt: at } : null));
  }

  async ended(callSessionId: string, reason: string, at = new Date()) {
    return this.move(callSessionId, (r) =>
      r.status === "ended" ? null : { status: "ended", endedAt: at, endReason: reason, durationSec: durationSec(r.answeredAt, at) },
    );
  }

  async get(callSessionId: string) {
    const row = this.rows.get(callSessionId);
    return row ? { ...row } : null;
  }

  async list(q: CallRecordQuery) {
    const after = afterCursor(q.cursor);
    const rows = [...this.rows.values()]
      .filter(
        (r) =>
          (!q.userId || r.callerUserId === q.userId || r.calleeUserId === q.userId) &&
          (!q.status || r.status === q.status) &&
          (!q.callType || r.callType === q.callType) &&
          (!q.endReason || r.endReason === q.endReason) &&
          (!q.from || r.createdAt >= q.from) &&
          (!q.to || r.createdAt <= q.to) &&
          after(r),
      )
      .sort(newestFirst);
    return pageOf(rows.slice(0, q.limit + 1).map((r) => ({ ...r })), q.limit);
  }

  private move(callSessionId: string, step: (r: CallRecord) => Partial<CallRecord> | null): CallRecord | null {
    const row = this.rows.get(callSessionId);
    if (!row) return null;
    const patch = step(row);
    if (!patch) return { ...row };
    const next = { ...row, ...patch };
    this.rows.set(callSessionId, next);
    return { ...next };
  }
}

/** A ConfigSource answering fixed documents; an absent one answers 404. */
export function staticConfig(docs: { runtime?: Record<string, unknown>; e2ee?: Record<string, unknown> }): ConfigSource {
  return {
    runtimeConfig: async () => docs.runtime ?? null,
    e2eeConfig: async () => docs.e2ee ?? null,
  };
}
