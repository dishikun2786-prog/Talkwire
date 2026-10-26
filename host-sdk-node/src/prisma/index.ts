// Prisma implementations of the storage interfaces, over the models in
// prisma/talkwire.prisma. Pass your generated PrismaClient; the stores only use
// the talkwire* models, $transaction and $executeRaw.

import { AsyncLocalStorage } from "node:async_hooks";
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
  type Contact,
  type ContactListQuery,
  type ContactPatch,
  type ContactStatus,
  type ContactStore,
  type NewAuditEntry,
  type NewCallRecord,
  type NewContact,
  type NewProfile,
  type Profile,
  type ProfilePatch,
  type ProfileStore,
} from "../types.js";
import { imUid } from "../uid.js";
import { durationSec, pageOf, parseCallCursor } from "../call-records.js";

/* eslint-disable @typescript-eslint/no-explicit-any -- the generated client's
   delegate methods are generic over the host's own schema; the stores are
   typed against the rows they read instead. */
type Loose = any;

interface Delegate {
  findUnique(args: Loose): Promise<Loose>;
  findFirst(args: Loose): Promise<Loose>;
  findMany(args: Loose): Promise<Loose[]>;
  create(args: Loose): Promise<Loose>;
  update(args: Loose): Promise<Loose>;
  updateMany(args: Loose): Promise<{ count: number }>;
  delete(args: Loose): Promise<Loose>;
  deleteMany(args: Loose): Promise<{ count: number }>;
}

/** The part of a generated transaction client the stores use. */
export interface TalkwirePrismaTx {
  talkwireProfile: Delegate;
  talkwireContact: Delegate;
  talkwireAdminAudit: Delegate;
  /** Only for PrismaCallRecordStore. */
  talkwireCallRecord?: Delegate;
  $executeRaw(query: TemplateStringsArray, ...values: unknown[]): Promise<number>;
}

/** The part of a generated PrismaClient the stores use. */
export interface TalkwirePrisma extends TalkwirePrismaTx {
  $transaction<T>(fn: (tx: Loose) => Promise<T>, options?: { timeout?: number; maxWait?: number }): Promise<T>;
}

// A unique constraint failed. Which one is not reliably reported (Prisma 6
// gives meta.target null with PostgreSQL), so callers look it up again.
function uniqueViolation(err: unknown): boolean {
  return (err as { code?: string })?.code === "P2002";
}

interface ProfileRow {
  userId: string;
  uid: string;
  handle: string;
  handleKey: string;
  handleChangedAt: Date | null;
  displayName: string;
  avatarUrl: string | null;
  createdAt: Date;
  updatedAt: Date;
}

const toProfile = (r: ProfileRow): Profile => ({
  userId: r.userId,
  uid: r.uid,
  handle: r.handle,
  handleChangedAt: r.handleChangedAt,
  displayName: r.displayName,
  avatarUrl: r.avatarUrl,
  createdAt: r.createdAt,
  updatedAt: r.updatedAt,
});

export class PrismaProfileStore implements ProfileStore {
  constructor(private readonly prisma: TalkwirePrismaTx) {}

  async get(userId: string) {
    const row: ProfileRow | null = await this.prisma.talkwireProfile.findUnique({ where: { userId } });
    return row ? toProfile(row) : null;
  }

  async getMany(userIds: string[]) {
    if (userIds.length === 0) return [];
    const rows: ProfileRow[] = await this.prisma.talkwireProfile.findMany({ where: { userId: { in: userIds } } });
    return rows.map(toProfile);
  }

  async findByHandle(handle: string) {
    const row: ProfileRow | null = await this.prisma.talkwireProfile.findUnique({ where: { handleKey: handle.toLowerCase() } });
    return row ? toProfile(row) : null;
  }

  async findByUid(uid: string) {
    const row: ProfileRow | null = await this.prisma.talkwireProfile.findUnique({ where: { uid } });
    return row ? toProfile(row) : null;
  }

  async findManyByUid(uids: string[]) {
    if (uids.length === 0) return [];
    const rows: ProfileRow[] = await this.prisma.talkwireProfile.findMany({ where: { uid: { in: uids } } });
    return rows.map(toProfile);
  }

  async create(profile: NewProfile) {
    try {
      const row: ProfileRow = await this.prisma.talkwireProfile.create({
        data: {
          userId: profile.userId,
          uid: imUid(profile.userId),
          handle: profile.handle,
          handleKey: profile.handle.toLowerCase(),
          displayName: profile.displayName,
          avatarUrl: profile.avatarUrl,
        },
      });
      return toProfile(row);
    } catch (err) {
      if (uniqueViolation(err) && (await this.handleOwner(profile.handle)) !== profile.userId) throw new HandleTakenError(profile.handle);
      throw err;
    }
  }

  async update(userId: string, patch: ProfilePatch) {
    const data: Record<string, unknown> = { ...patch };
    if (patch.handle !== undefined) data.handleKey = patch.handle.toLowerCase();
    try {
      const row: ProfileRow = await this.prisma.talkwireProfile.update({ where: { userId }, data });
      return toProfile(row);
    } catch (err) {
      if (patch.handle !== undefined && uniqueViolation(err) && (await this.handleOwner(patch.handle)) !== userId) {
        throw new HandleTakenError(patch.handle);
      }
      throw err;
    }
  }

  private async handleOwner(handle: string): Promise<string | null> {
    return (await this.findByHandle(handle))?.userId ?? null;
  }
}

/** The same key for (a, b) and (b, a). */
export function pairKeyOf(a: string, b: string): string {
  return JSON.stringify(a < b ? [a, b] : [b, a]);
}

interface ContactRow {
  id: string;
  pairKey: string;
  requesterId: string;
  recipientId: string;
  status: string;
  requesterAlias: string | null;
  recipientAlias: string | null;
  blockedBy: string | null;
  createdAt: Date;
  updatedAt: Date;
}

const toContact = (r: ContactRow): Contact => ({
  id: r.id,
  requesterId: r.requesterId,
  recipientId: r.recipientId,
  status: r.status as ContactStatus,
  requesterAlias: r.requesterAlias,
  recipientAlias: r.recipientAlias,
  blockedBy: r.blockedBy,
  createdAt: r.createdAt,
  updatedAt: r.updatedAt,
});

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

export interface PrismaContactStoreOptions {
  /** How long withPair may hold its transaction; default 10 s. */
  transactionTimeoutMs?: number;
}

/**
 * withPair runs its work in a transaction holding a PostgreSQL advisory lock
 * on the pair, so several processes serialize too; the store's methods called
 * inside it use that transaction.
 */
export class PrismaContactStore implements ContactStore {
  private readonly tx = new AsyncLocalStorage<TalkwirePrismaTx>();
  private readonly timeout: number;

  constructor(
    private readonly prisma: TalkwirePrisma,
    options: PrismaContactStoreOptions = {},
  ) {
    this.timeout = options.transactionTimeoutMs ?? 10_000;
  }

  private db(): TalkwirePrismaTx {
    return this.tx.getStore() ?? this.prisma;
  }

  async get(id: string) {
    // The column is a uuid: anything else cannot exist, and Postgres would reject it.
    if (!UUID.test(id)) return null;
    const row: ContactRow | null = await this.db().talkwireContact.findUnique({ where: { id } });
    return row ? toContact(row) : null;
  }

  async findPair(a: string, b: string) {
    const row: ContactRow | null = await this.db().talkwireContact.findUnique({ where: { pairKey: pairKeyOf(a, b) } });
    return row ? toContact(row) : null;
  }

  async list(userId: string, query: ContactListQuery) {
    const side =
      query.direction === "sent"
        ? { requesterId: userId }
        : query.direction === "received"
          ? { recipientId: userId }
          : { OR: [{ requesterId: userId }, { recipientId: userId }] };
    const updatedAt: Record<string, Date> = {};
    if (query.updatedAfter) updatedAt.gt = query.updatedAfter;
    if (query.updatedUntil) updatedAt.lte = query.updatedUntil;
    const rows: ContactRow[] = await this.db().talkwireContact.findMany({
      where: { status: query.status, ...side, ...(Object.keys(updatedAt).length ? { updatedAt } : {}) },
      orderBy: { updatedAt: "desc" },
    });
    return rows.map(toContact);
  }

  async create(contact: NewContact) {
    try {
      const row: ContactRow = await this.db().talkwireContact.create({
        data: { ...contact, pairKey: pairKeyOf(contact.requesterId, contact.recipientId) },
      });
      return toContact(row);
    } catch (err) {
      // Inside withPair the failed insert has aborted the transaction, so do
      // not query again there; the only other unique column is a random uuid.
      if (uniqueViolation(err) && (this.tx.getStore() || (await this.findPair(contact.requesterId, contact.recipientId)))) {
        throw new PairExistsError();
      }
      throw err;
    }
  }

  async update(id: string, patch: ContactPatch) {
    const data: Record<string, unknown> = { ...patch };
    if (patch.status !== undefined && patch.status !== "blocked") data.blockedBy = null;
    const row: ContactRow = await this.db().talkwireContact.update({ where: { id }, data });
    return toContact(row);
  }

  async delete(id: string) {
    if (!UUID.test(id)) return;
    await this.db().talkwireContact.deleteMany({ where: { id } });
  }

  withPair<T>(a: string, b: string, fn: () => Promise<T>): Promise<T> {
    if (this.tx.getStore()) return fn();
    const key = pairKeyOf(a, b);
    return this.prisma.$transaction(
      async (tx: TalkwirePrismaTx) => {
        await tx.$executeRaw`SELECT pg_advisory_xact_lock(hashtextextended(${key}, 0))`;
        return this.tx.run(tx, fn);
      },
      { timeout: this.timeout },
    );
  }
}

interface AuditRow {
  id: string;
  adminId: string;
  action: string;
  targetUserId: string | null;
  topic: string | null;
  reason: string | null;
  status: string;
  idempotencyKey: string | null;
  details: unknown;
  createdAt: Date;
  completedAt: Date | null;
}

const toAudit = (r: AuditRow): AuditEntry => ({
  id: r.id,
  adminId: r.adminId,
  action: r.action,
  targetUserId: r.targetUserId,
  topic: r.topic,
  reason: r.reason,
  status: r.status as AuditEntry["status"],
  idempotencyKey: r.idempotencyKey,
  details: (r.details ?? null) as Record<string, unknown> | null,
  createdAt: r.createdAt,
  completedAt: r.completedAt,
});

export class PrismaAuditStore implements AuditStore {
  constructor(private readonly prisma: TalkwirePrismaTx) {}

  async create(entry: NewAuditEntry) {
    try {
      const row: AuditRow = await this.prisma.talkwireAdminAudit.create({
        data: {
          adminId: entry.adminId,
          action: entry.action,
          status: entry.status,
          targetUserId: entry.targetUserId ?? null,
          topic: entry.topic ?? null,
          reason: entry.reason ?? null,
          idempotencyKey: entry.idempotencyKey ?? null,
          ...(entry.details ? { details: entry.details } : {}),
        },
      });
      return toAudit(row);
    } catch (err) {
      if (entry.idempotencyKey && uniqueViolation(err) && (await this.findByIdempotencyKey(entry.idempotencyKey))) {
        throw new DuplicateKeyError(entry.idempotencyKey);
      }
      throw err;
    }
  }

  async finish(id: string, status: "success" | "failed", details: Record<string, unknown> | null) {
    const row: AuditRow = await this.prisma.talkwireAdminAudit.update({
      where: { id },
      data: { status, completedAt: new Date(), ...(details ? { details } : {}) },
    });
    return toAudit(row);
  }

  async get(id: string) {
    if (!UUID.test(id)) return null;
    const row: AuditRow | null = await this.prisma.talkwireAdminAudit.findUnique({ where: { id } });
    return row ? toAudit(row) : null;
  }

  async findByIdempotencyKey(key: string) {
    const row: AuditRow | null = await this.prisma.talkwireAdminAudit.findUnique({ where: { idempotencyKey: key } });
    return row ? toAudit(row) : null;
  }
}

interface CallRow {
  callSessionId: string;
  callerUserId: string;
  calleeUserId: string;
  requestedType: string;
  callType: string;
  status: string;
  createdAt: Date;
  answeredAt: Date | null;
  upgradedAt: Date | null;
  endedAt: Date | null;
  endReason: string | null;
  durationSec: number | null;
}

const toCall = (r: CallRow): CallRecord => ({
  callSessionId: r.callSessionId,
  callerUserId: r.callerUserId,
  calleeUserId: r.calleeUserId,
  requestedType: r.requestedType as CallType,
  callType: r.callType as CallType,
  status: r.status as CallRecord["status"],
  createdAt: r.createdAt,
  answeredAt: r.answeredAt,
  upgradedAt: r.upgradedAt,
  endedAt: r.endedAt,
  endReason: r.endReason,
  durationSec: r.durationSec,
});

/**
 * Each step is one conditional update, so the IM server retrying a request,
 * or two processes handling the same call, cannot move a call backwards.
 */
export class PrismaCallRecordStore implements CallRecordStore {
  private readonly rows: Delegate;

  constructor(prisma: TalkwirePrismaTx) {
    if (!prisma.talkwireCallRecord) throw new Error("the Prisma client has no TalkwireCallRecord model: copy it from prisma/talkwire.prisma");
    this.rows = prisma.talkwireCallRecord;
  }

  async create(call: NewCallRecord, at = new Date()) {
    try {
      const row: CallRow = await this.rows.create({
        data: { ...call, callType: call.requestedType, status: "ringing", createdAt: at },
      });
      return toCall(row);
    } catch (err) {
      const existing = uniqueViolation(err) ? await this.get(call.callSessionId) : null;
      if (existing) return existing;
      throw err;
    }
  }

  async answered(callSessionId: string, callType: CallType, at = new Date()) {
    await this.rows.updateMany({ where: { callSessionId, status: "ringing" }, data: { status: "active", callType, answeredAt: at } });
    return this.get(callSessionId);
  }

  async upgraded(callSessionId: string, at = new Date()) {
    await this.rows.updateMany({ where: { callSessionId, status: "active", callType: "voice" }, data: { callType: "video", upgradedAt: at } });
    return this.get(callSessionId);
  }

  async ended(callSessionId: string, reason: string, at = new Date()) {
    // The duration needs answeredAt, so the update is conditional on the
    // status it was computed from; a call answered in between is read again.
    for (let attempt = 0; attempt < 3; attempt++) {
      const row = await this.get(callSessionId);
      if (!row || row.status === "ended") return row;
      const { count } = await this.rows.updateMany({
        where: { callSessionId, status: row.status },
        data: { status: "ended", endedAt: at, endReason: reason, durationSec: durationSec(row.answeredAt, at) },
      });
      if (count === 1) return this.get(callSessionId);
    }
    return this.get(callSessionId);
  }

  async get(callSessionId: string) {
    const row: CallRow | null = await this.rows.findUnique({ where: { callSessionId } });
    return row ? toCall(row) : null;
  }

  async list(q: CallRecordQuery) {
    const and: Loose[] = [];
    if (q.userId) and.push({ OR: [{ callerUserId: q.userId }, { calleeUserId: q.userId }] });
    if (q.status) and.push({ status: q.status });
    if (q.callType) and.push({ callType: q.callType });
    if (q.endReason) and.push({ endReason: q.endReason });
    if (q.from) and.push({ createdAt: { gte: q.from } });
    if (q.to) and.push({ createdAt: { lte: q.to } });
    if (q.cursor) {
      const c = parseCallCursor(q.cursor);
      if (!c) throw new Error("invalid cursor");
      and.push({ OR: [{ createdAt: { lt: c.at } }, { createdAt: c.at, callSessionId: { lt: c.callSessionId } }] });
    }
    const rows: CallRow[] = await this.rows.findMany({
      where: { AND: and },
      orderBy: [{ createdAt: "desc" }, { callSessionId: "desc" }],
      take: q.limit + 1,
    });
    return pageOf(rows.map(toCall), q.limit);
  }
}

/**
 * The stores over one client: the three every host needs, plus call records
 * when the client has the TalkwireCallRecord model.
 */
export function prismaStores(prisma: TalkwirePrisma, options: PrismaContactStoreOptions = {}) {
  return {
    profiles: new PrismaProfileStore(prisma),
    contacts: new PrismaContactStore(prisma, options),
    audit: new PrismaAuditStore(prisma),
    calls: prisma.talkwireCallRecord ? new PrismaCallRecordStore(prisma) : undefined,
  };
}
