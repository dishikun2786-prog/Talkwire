/**
 * Presence with three states and an expiry (C5): what the server last said
 * is trusted for `ttlMs`, then the state is "unknown" again. There is one
 * tracker per engine; the conversation list and the chat screen both read
 * it, so they cannot disagree.
 *
 * Frames with a smaller `presRev` than the one held are stale and dropped
 * (section 12.1).
 */

import type { PresenceState } from "./model.js";
import { Signal, type ReadonlySignal } from "./util.js";

export interface PresenceRecord {
  online: boolean;
  /** The server's `presRev`; 0 when the frame had none. */
  rev: number;
  receivedAt: number;
  /** 0 when unknown. */
  lastSeenAt: number;
}

export class PresenceTracker {
  private readonly records = new Map<string, PresenceRecord>();
  /** Bumped on every change, for UIs that re-read `state`. */
  private readonly _version = new Signal(0);
  readonly version: ReadonlySignal<number> = this._version;
  private timer: ReturnType<typeof setTimeout> | null = null;

  constructor(
    readonly ttlMs = 180_000,
    private readonly clock: () => number = Date.now,
  ) {}

  /** Applies a push or a snapshot entry; returns false when it was older than what is held. */
  update(uid: string, online: boolean, rev: number, lastSeenAt = 0): boolean {
    const held = this.records.get(uid);
    if (held && rev > 0 && held.rev > rev) return false;
    this.records.set(uid, { online, rev: Math.max(rev, held?.rev ?? 0), receivedAt: this.clock(), lastSeenAt: lastSeenAt || held?.lastSeenAt || 0 });
    this.changed();
    return true;
  }

  state(uid: string): PresenceState {
    const r = this.records.get(uid);
    if (!r || this.clock() - r.receivedAt >= this.ttlMs) return "unknown";
    return r.online ? "online" : "offline";
  }

  record(uid: string): PresenceRecord | undefined {
    return this.records.get(uid);
  }

  /** After a disconnect nothing the server said can be trusted any more. */
  forgetAll(): void {
    if (this.records.size === 0) return;
    this.records.clear();
    this.changed();
  }

  dispose(): void {
    if (this.timer) clearTimeout(this.timer);
    this.timer = null;
  }

  private changed(): void {
    this._version.update((v) => v + 1);
    this.scheduleExpiry();
  }

  /** Bumps the version when the next record expires, so readers see it turn "unknown". */
  private scheduleExpiry(): void {
    if (this.timer) clearTimeout(this.timer);
    this.timer = null;
    let next = Infinity;
    const now = this.clock();
    for (const r of this.records.values()) {
      const at = r.receivedAt + this.ttlMs;
      if (at > now) next = Math.min(next, at);
    }
    if (next === Infinity) return;
    this.timer = setTimeout(() => {
      this.timer = null;
      this._version.update((v) => v + 1);
      this.scheduleExpiry();
    }, Math.max(0, next - now));
  }
}
