// Call records: recordingCalls wraps the host's call handlers so every call
// they let through is written to a CallRecordStore, for the administrators'
// call-record pages (talkwireAdminRoutes). Plus the paging helpers the stores share.

import type { CallHandlers } from "./host-plugin.js";
import type { CallRecord, CallRecordPage, CallRecordStore } from "./types.js";

export interface RecordingCallsOptions {
  /**
   * A record that could not be written. The call goes on regardless: an
   * administrator's view must never be the reason a call fails. Default:
   * console.error.
   */
  onError?(err: unknown, step: "create" | "answer" | "upgrade" | "settle", callSessionId: string): void;
}

/**
 * The same handlers, recording each call that create accepted: ringing,
 * answered (with the type it was answered as), upgraded to video, and how it
 * ended. A call the handlers refused is not a call and is not recorded.
 */
export function recordingCalls(handlers: CallHandlers, store: CallRecordStore, options: RecordingCallsOptions = {}): CallHandlers {
  const onError =
    options.onError ??
    ((err: unknown, step: string, id: string) => {
      console.error(`talkwire: could not record step ${step} of call ${id}`, err);
    });
  const record = async (step: "create" | "answer" | "upgrade" | "settle", id: string, write: () => Promise<unknown>) => {
    try {
      await write();
    } catch (err) {
      onError(err, step, id);
    }
  };
  return {
    async create(req) {
      const result = await handlers.create(req);
      if (result.ok) {
        await record("create", req.callSessionId, () =>
          store.create({ callSessionId: req.callSessionId, callerUserId: req.callerAppUserId, calleeUserId: req.calleeAppUserId, requestedType: req.callType }),
        );
      }
      return result;
    },
    async answer(req) {
      const result = await handlers.answer(req);
      if (result.ok) await record("answer", req.callSessionId, () => store.answered(req.callSessionId, req.effectiveType));
      return result;
    },
    async upgrade(req) {
      const result = await handlers.upgrade(req);
      if (result.ok) await record("upgrade", req.callSessionId, () => store.upgraded(req.callSessionId));
      return result;
    },
    async settle(req) {
      // The end is a fact whatever the host's billing made of it.
      try {
        await handlers.settle(req);
      } finally {
        await record("settle", req.callSessionId, () => store.ended(req.callSessionId, req.reason));
      }
    },
  };
}

/** Whole seconds from answering to the end; 0 for a call never answered. */
export function durationSec(answeredAt: Date | null, endedAt: Date): number {
  return answeredAt ? Math.max(0, Math.round((endedAt.getTime() - answeredAt.getTime()) / 1000)) : 0;
}

export function newestFirst(a: CallRecord, b: CallRecord): number {
  const t = b.createdAt.getTime() - a.createdAt.getTime();
  return t !== 0 ? t : a.callSessionId < b.callSessionId ? 1 : a.callSessionId > b.callSessionId ? -1 : 0;
}

/** A page cursor: the last record's creation time and id, opaque to callers. */
export function callCursor(r: Pick<CallRecord, "createdAt" | "callSessionId">): string {
  return Buffer.from(`${r.createdAt.getTime()}:${r.callSessionId}`, "utf8").toString("base64url");
}

/** The position a cursor names; null when it is not one. */
export function parseCallCursor(cursor: string): { at: Date; callSessionId: string } | null {
  const text = Buffer.from(cursor, "base64url").toString("utf8");
  const m = /^(\d{1,15}):(.{1,64})$/s.exec(text);
  return m ? { at: new Date(Number(m[1])), callSessionId: m[2]! } : null;
}

/** Whether a record comes after the cursor in newest-first order; everything does without one. */
export function afterCursor(cursor: string | undefined): (r: CallRecord) => boolean {
  if (!cursor) return () => true;
  const c = parseCallCursor(cursor);
  if (!c) throw new Error("invalid cursor");
  return (r) => r.createdAt < c.at || (r.createdAt.getTime() === c.at.getTime() && r.callSessionId < c.callSessionId);
}

/** limit records from limit + 1 fetched, and the cursor for the rest. */
export function pageOf(rows: CallRecord[], limit: number): CallRecordPage {
  const records = rows.slice(0, limit);
  return { records, nextCursor: rows.length > limit ? callCursor(records[records.length - 1]!) : null };
}
