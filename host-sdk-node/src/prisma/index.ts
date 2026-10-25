// Prisma implementations of the storage interfaces, over the models in
// prisma/imkit.prisma. Pass your generated PrismaClient; the stores only use
// the three imkit* models, $transaction and $executeRaw.

import { AsyncLocalStorage } from "node:async_hooks";
import {
  DuplicateKeyError,
  HandleTakenError,
  PairExistsError,
  type AuditEntry,
  type AuditStore,
  type Contact,
  type ContactListQuery,
  type ContactPatch,
  type ContactStatus,
  type ContactStore,
  type NewAuditEntry,
  type NewContact,
  type NewProfile,
  type Profile,
  type ProfilePatch,
  type ProfileStore,
} from "../types.js";
import { imUid } from "../uid.js";

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
  delete(args: Loose): Promise<Loose>;
  deleteMany(args: Loose): Promise<{ count: number }>;
}

/** The part of a generated transaction client the stores use. */
export interface ImkitPrismaTx {
  imkitProfile: Delegate;
  imkitContact: Delegate;
  imkitAdminAudit: Delegate;
  $executeRaw(query: TemplateStringsArray, ...values: unknown[]): Promise<number>;
}

/** The part of a generated PrismaClient the stores use. */
export interface ImkitPrisma extends ImkitPrismaTx {
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
  constructor(private readonly prisma: ImkitPrismaTx) {}

  async get(userId: string) {
    const row: ProfileRow | null = await this.prisma.imkitProfile.findUnique({ where: { userId } });
    return row ? toProfile(row) : null;
  }

  async getMany(userIds: string[]) {
    if (userIds.length === 0) return [];
    const rows: ProfileRow[] = await this.prisma.imkitProfile.findMany({ where: { userId: { in: userIds } } });
    return rows.map(toProfile);
  }

  async findByHandle(handle: string) {
    const row: ProfileRow | null = await this.prisma.imkitProfile.findUnique({ where: { handleKey: handle.toLowerCase() } });
    return row ? toProfile(row) : null;
  }

  async findByUid(uid: string) {
    const row: ProfileRow | null = await this.prisma.imkitProfile.findUnique({ where: { uid } });
    return row ? toProfile(row) : null;
  }

  async findManyByUid(uids: string[]) {
    if (uids.length === 0) return [];
    const rows: ProfileRow[] = await this.prisma.imkitProfile.findMany({ where: { uid: { in: uids } } });
    return rows.map(toProfile);
  }

  async create(profile: NewProfile) {
    try {
      const row: ProfileRow = await this.prisma.imkitProfile.create({
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
      const row: ProfileRow = await this.prisma.imkitProfile.update({ where: { userId }, data });
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
  private readonly tx = new AsyncLocalStorage<ImkitPrismaTx>();
  private readonly timeout: number;

  constructor(
    private readonly prisma: ImkitPrisma,
    options: PrismaContactStoreOptions = {},
  ) {
    this.timeout = options.transactionTimeoutMs ?? 10_000;
  }

  private db(): ImkitPrismaTx {
    return this.tx.getStore() ?? this.prisma;
  }

  async get(id: string) {
    // The column is a uuid: anything else cannot exist, and Postgres would reject it.
    if (!UUID.test(id)) return null;
    const row: ContactRow | null = await this.db().imkitContact.findUnique({ where: { id } });
    return row ? toContact(row) : null;
  }

  async findPair(a: string, b: string) {
    const row: ContactRow | null = await this.db().imkitContact.findUnique({ where: { pairKey: pairKeyOf(a, b) } });
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
    const rows: ContactRow[] = await this.db().imkitContact.findMany({
      where: { status: query.status, ...side, ...(Object.keys(updatedAt).length ? { updatedAt } : {}) },
      orderBy: { updatedAt: "desc" },
    });
    return rows.map(toContact);
  }

  async create(contact: NewContact) {
    try {
      const row: ContactRow = await this.db().imkitContact.create({
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
    const row: ContactRow = await this.db().imkitContact.update({ where: { id }, data });
    return toContact(row);
  }

  async delete(id: string) {
    if (!UUID.test(id)) return;
    await this.db().imkitContact.deleteMany({ where: { id } });
  }

  withPair<T>(a: string, b: string, fn: () => Promise<T>): Promise<T> {
    if (this.tx.getStore()) return fn();
    const key = pairKeyOf(a, b);
    return this.prisma.$transaction(
      async (tx: ImkitPrismaTx) => {
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
  constructor(private readonly prisma: ImkitPrismaTx) {}

  async create(entry: NewAuditEntry) {
    try {
      const row: AuditRow = await this.prisma.imkitAdminAudit.create({
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
    const row: AuditRow = await this.prisma.imkitAdminAudit.update({
      where: { id },
      data: { status, completedAt: new Date(), ...(details ? { details } : {}) },
    });
    return toAudit(row);
  }

  async get(id: string) {
    if (!UUID.test(id)) return null;
    const row: AuditRow | null = await this.prisma.imkitAdminAudit.findUnique({ where: { id } });
    return row ? toAudit(row) : null;
  }

  async findByIdempotencyKey(key: string) {
    const row: AuditRow | null = await this.prisma.imkitAdminAudit.findUnique({ where: { idempotencyKey: key } });
    return row ? toAudit(row) : null;
  }
}

/** The three stores over one client. */
export function prismaStores(prisma: ImkitPrisma, options: PrismaContactStoreOptions = {}) {
  return {
    profiles: new PrismaProfileStore(prisma),
    contacts: new PrismaContactStore(prisma, options),
    audit: new PrismaAuditStore(prisma),
  };
}
