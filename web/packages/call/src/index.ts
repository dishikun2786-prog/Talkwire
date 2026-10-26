/**
 * Voice and video calls (protocol section 16), the web side of the `call`
 * module, in three parts:
 *
 * - `CallSignaling`: one method per call action and the call pushes, sending
 *   exactly the fields the spec names (a replayed golden transcript checks
 *   every request).
 * - `CallManager`: the state of this tab's call — ringing, active, ended —
 *   driving a `CallMediaEngine` and agreeing with the account's other tabs
 *   on which of them takes a call.
 * - `CallMediaEngine`: what an RTC adapter implements. Media never goes
 *   through the IM server; the host's RTC service carries it, with the
 *   credentials the server hands each side (see examples/ for an adapter).
 *
 * A side's `rtcToken` is its own and is never sent to the other side.
 */

import { Emitter, Signal, isOk, num, optStr, str, type Frame, type ImClient, type ImEvent, type ReadonlySignal, type Reply, type Unsubscribe } from "@talkwire/client";

export type CallType = "voice" | "video";

/**
 * Why a call ended. Clients must tolerate reasons not listed here (section
 * 16.4): the host may end calls for reasons of its own.
 */
export type CallEndReason =
  | "user_hangup"
  | "no_answer"
  | "rejected"
  | "cancelled"
  | "disconnected"
  | "failed"
  | "answered_elsewhere"
  | "credit_exhausted"
  | "max_duration_reached"
  | (string & {});

/** One side's credentials for the RTC channel. */
export interface RtcCredentials {
  channelId: string;
  rtcToken: string;
  appId?: string;
  videoQuality?: string;
  denoiseEnabled: boolean;
}

export type CallSignal =
  | { kind: "ring"; callSessionId: string; callType: CallType; callerName?: string; callerAvatar?: string; ringTimeoutSeconds: number }
  /** To the caller: the callee answered; the caller's own credentials. */
  | { kind: "answered"; callSessionId: string; credentials: RtcCredentials }
  | { kind: "ended"; callSessionId: string; reason: CallEndReason }
  | { kind: "upgradeRequest"; callSessionId: string; toType: "video" }
  | { kind: "upgraded"; callSessionId: string; videoQuality?: string }
  | { kind: "upgradeDeclined"; callSessionId: string };

const callTypeOf = (v: unknown): CallType => (v === "video" ? "video" : "voice");

/** The credentials in a `call_accept` reply or a `call_answered` push. */
export function credentialsOf(f: Frame): RtcCredentials {
  const c: RtcCredentials = { channelId: str(f, "channelId"), rtcToken: str(f, "rtcToken"), denoiseEnabled: f.denoiseEnabled === true };
  const appId = optStr(f, "appId");
  if (appId) c.appId = appId;
  const videoQuality = optStr(f, "videoQuality");
  if (videoQuality) c.videoQuality = videoQuality;
  return c;
}

/** The call actions and pushes, one to one with section 16. */
export class CallSignaling {
  readonly signals = new Emitter<CallSignal>();
  private off: Unsubscribe | null = null;

  /** Listens to the client's pushes from the start, unless `autoStart` is false. */
  constructor(
    readonly client: ImClient,
    opts: { autoStart?: boolean } = {},
  ) {
    if (opts.autoStart !== false) this.start();
  }

  /** Listens again after `dispose`; does nothing while listening. */
  start(): void {
    this.off ??= this.client.events.on((ev) => this.onEvent(ev));
  }

  dispose(): void {
    this.off?.();
    this.off = null;
  }

  /** `topic` is the callee's uid. The reply carries `callSessionId`; the callee rings first (F49). */
  invite(peer: string, callType: string, clientCallId?: string): Promise<Reply> {
    const frame: Frame = { action: "call_invite", topic: peer, callType };
    if (clientCallId) frame.clientCallId = clientCallId;
    return this.client.request(frame);
  }

  /** The reply carries this side's credentials (`credentialsOf`). */
  accept(callSessionId: string, acceptAsVoice?: boolean): Promise<Reply> {
    const frame: Frame = { action: "call_accept", callSessionId };
    if (acceptAsVoice) frame.acceptAsVoice = true;
    return this.client.request(frame);
  }

  reject(callSessionId: string): Promise<Reply> {
    return this.client.request({ action: "call_reject", callSessionId });
  }

  cancel(callSessionId: string): Promise<Reply> {
    return this.client.request({ action: "call_cancel", callSessionId });
  }

  hangup(callSessionId: string): Promise<Reply> {
    return this.client.request({ action: "call_hangup", callSessionId });
  }

  upgrade(callSessionId: string): Promise<Reply> {
    return this.client.request({ action: "call_upgrade", callSessionId, toType: "video" });
  }

  upgradeAccept(callSessionId: string): Promise<Reply> {
    return this.client.request({ action: "call_upgrade_accept", callSessionId });
  }

  upgradeReject(callSessionId: string): Promise<Reply> {
    return this.client.request({ action: "call_upgrade_reject", callSessionId });
  }

  private onEvent(ev: ImEvent): void {
    if (ev.kind !== "module") return;
    const f = ev.frame;
    const callSessionId = str(f, "callSessionId");
    if (!callSessionId) return;
    let s: CallSignal | null = null;
    switch (str(f, "type")) {
      case "call_ring": {
        const ring: CallSignal & { kind: "ring" } = { kind: "ring", callSessionId, callType: callTypeOf(f.callType), ringTimeoutSeconds: num(f, "ringTimeoutSeconds") || 45 };
        const name = optStr(f, "callerName");
        if (name) ring.callerName = name;
        const avatar = optStr(f, "callerAvatar");
        if (avatar) ring.callerAvatar = avatar;
        s = ring;
        break;
      }
      case "call_answered":
        s = { kind: "answered", callSessionId, credentials: credentialsOf(f) };
        break;
      case "call_ended":
        s = { kind: "ended", callSessionId, reason: str(f, "reason") || "user_hangup" };
        break;
      case "call_upgrade_request":
        s = { kind: "upgradeRequest", callSessionId, toType: "video" };
        break;
      case "call_upgraded": {
        const up: CallSignal & { kind: "upgraded" } = { kind: "upgraded", callSessionId };
        const q = optStr(f, "videoQuality");
        if (q) up.videoQuality = q;
        s = up;
        break;
      }
      case "call_upgrade_declined":
        s = { kind: "upgradeDeclined", callSessionId };
        break;
    }
    if (s) this.signals.emit(s);
  }
}

// ------------------------------------------------------------------ media

export interface MediaJoin {
  /** The local user's uid, for adapters that name the participant. */
  uid: string;
  callType: CallType;
  credentials: RtcCredentials;
}

/**
 * What an RTC adapter implements. Every method may be called while a
 * previous one is still running; `leave` must work at any time, even when
 * `join` failed or never finished.
 */
export interface CallMediaEngine {
  join(params: MediaJoin): Promise<void>;
  /** Turns the camera on after the call became a video call. */
  setVideo(on: boolean, videoQuality?: string): Promise<void>;
  setMuted(muted: boolean): void;
  leave(): Promise<void>;
  /** Shows the local camera in `el`; null detaches. */
  attachLocalVideo?(el: HTMLElement | null): void;
  /** Shows the other side's video in `el`; null detaches. */
  attachRemoteVideo?(el: HTMLElement | null): void;
}

/** Joins nothing: signaling without media, for tests and demos. */
export class NoMediaEngine implements CallMediaEngine {
  readonly log: string[] = [];
  async join(p: MediaJoin): Promise<void> {
    this.log.push(`join ${p.callType} ${p.credentials.channelId}`);
  }
  async setVideo(on: boolean): Promise<void> {
    this.log.push(`video ${on}`);
  }
  setMuted(muted: boolean): void {
    this.log.push(`muted ${muted}`);
  }
  async leave(): Promise<void> {
    this.log.push("leave");
  }
}

// ------------------------------------------------------------------ manager

export type CallState =
  | { phase: "idle" }
  /** Calling `peer`; `callSessionId` once the server acknowledged. */
  | { phase: "outgoing"; peer: string; callType: CallType; callSessionId?: string; since: number }
  | { phase: "incoming"; callSessionId: string; callType: CallType; callerName?: string; callerAvatar?: string; ringTimeoutSeconds: number; since: number }
  | {
      phase: "active";
      callSessionId: string;
      callType: CallType;
      /** The callee's uid on the caller's side; unknown to the callee (the ring has no uid). */
      peer?: string;
      callerName?: string;
      /** Joining the RTC channel. */
      connecting: boolean;
      muted: boolean;
      /** A switch to video: asked by me, asked by them, or just declined by them. */
      upgrade?: "mine" | "theirs" | "declined";
      since: number;
    }
  | { phase: "ended"; callSessionId?: string; reason: CallEndReason; since: number };

export type InviteResult =
  | { ok: true; callSessionId: string }
  /**
   * `reason` is the server's: "offline", "busy", "not_friends", a host reason,
   * or a text for people. "busy_here" when this tab or another is in a call.
   */
  | { ok: false; code: number; reason: string; errCode?: string };

/** A BroadcastChannel, or a stand-in for tests. */
export interface TabChannel {
  postMessage(message: unknown): void;
  onmessage: ((ev: { data: unknown }) => void) | null;
  close?(): void;
}

export interface CallManagerOptions {
  /** The RTC adapter. */
  media: CallMediaEngine;
  /** How the other tabs of this account hear which tab took a call; null for none. Default: a BroadcastChannel per account. */
  channel?: TabChannel | null;
  /** How long an ended call stays on screen before the state goes back to idle. */
  endedHoldMs?: number;
  clock?: () => number;
  /**
   * Listen from construction; default true. False waits for `start()`: for a
   * framework that may build an instance and drop it unused (React StrictMode
   * runs `useMemo` twice), so the dropped one never hears a ring.
   */
  autoStart?: boolean;
  /** Called with each trace entry as it happens (see `trace()`); for a debug log. */
  log?: (entry: CallTraceEntry) => void;
}

/**
 * One line of a manager's trace: what it heard and what it did with it, for
 * finding out afterwards why a call did not ring. Holds call ids and phases,
 * never names or credentials.
 */
export interface CallTraceEntry {
  at: number;
  what: "start" | "dispose" | "connection" | "signal" | "drop" | "tab" | "state";
  callSessionId?: string;
  /** The signal kind, the reason for a drop, the tab message, the new phase. */
  note?: string;
}

/** How many trace entries a manager keeps. */
const TRACE_SIZE = 100;

type TabMessage = { t: "took"; callSessionId: string } | { t: "busy"; callSessionId: string } | { t: "free"; callSessionId: string };

/** How long pushes about a call this tab has just asked for are kept until the server tells its id (F49). */
const EARLY_SIGNAL_MS = 10_000;
/** A tab in a call says so this often; the others forget a tab that went quiet (closed mid-call). */
const BUSY_HEARTBEAT_MS = 30_000;
const BUSY_EXPIRY_MS = 90_000;

export class CallManager {
  readonly signaling: CallSignaling;
  private readonly _state = new Signal<CallState>({ phase: "idle" });
  readonly state: ReadonlySignal<CallState> = this._state;
  /** The RTC adapter, for the call screen to attach video elements. */
  readonly media: CallMediaEngine;
  /** Null until `start` when the manager opens its own (the default), and again after `dispose`. */
  private channel: TabChannel | null;
  private readonly ownChannel: boolean;
  private readonly clock: () => number;
  private readonly log: ((entry: CallTraceEntry) => void) | undefined;
  private readonly traced: CallTraceEntry[] = [];
  private offConnection: Unsubscribe | null = null;
  private readonly endedHoldMs: number;
  private readonly offSignals: Unsubscribe;
  private readonly early: { at: number; signal: CallSignal }[] = [];
  /** Calls another tab of this account is in, with when it last said so. */
  private readonly busyElsewhere = new Map<string, number>();
  private timer: ReturnType<typeof setTimeout> | null = null;
  private heartbeat: ReturnType<typeof setInterval> | null = null;
  /** cancel() came before the server told the call's id. */
  private cancelBeforeAck = false;
  private disposed = true;

  constructor(
    readonly client: ImClient,
    readonly me: string,
    options: CallManagerOptions,
  ) {
    this.signaling = new CallSignaling(client, { autoStart: false });
    this.media = options.media;
    this.clock = options.clock ?? Date.now;
    this.log = options.log;
    this.endedHoldMs = options.endedHoldMs ?? 3_000;
    this.ownChannel = options.channel === undefined;
    this.channel = options.channel ?? null;
    this.offSignals = this.signaling.signals.on((s) => void this.onSignal(s));
    this.offState = this._state.subscribe((s) => this.onState(s));
    if (options.autoStart !== false) this.start();
  }

  private readonly offState: Unsubscribe;
  private lastPhase: CallState["phase"] | null = null;

  /** Listens again after `dispose`; does nothing while listening (React StrictMode mounts twice). */
  start(): void {
    if (this.disposed) this.note("start");
    this.disposed = false;
    this.signaling.start();
    if (this.ownChannel) this.channel ??= defaultChannel(this.me);
    this.listenToTabs(true);
    this.offConnection ??= this.client.events.on((ev) => {
      if (ev.kind === "connected") this.note("connection", undefined, "connected");
      else if (ev.kind === "disconnected") this.note("connection", undefined, ev.willReconnect ? "lost, reconnecting" : "lost");
    });
    this.onState(this.current);
  }

  /**
   * Stops listening to the server and the other tabs, leaving any call in
   * progress (the server ends it after the disconnect grace period). `start`
   * undoes it.
   */
  dispose(): void {
    if (!this.disposed) this.note("dispose");
    this.disposed = true;
    this.signaling.dispose();
    this.listenToTabs(false);
    if (this.ownChannel) {
      this.channel?.close?.();
      this.channel = null;
    }
    this.offConnection?.();
    this.offConnection = null;
    this.clearTimer();
    if (this.heartbeat) clearInterval(this.heartbeat);
    this.heartbeat = null;
  }

  /** Disposes for good and closes the channel to the other tabs. */
  close(): void {
    this.dispose();
    this.offSignals();
    this.offState();
    this.channel?.close?.();
  }

  /** The last 100 things this manager heard and did, oldest first. */
  trace(): CallTraceEntry[] {
    return [...this.traced];
  }

  private note(what: CallTraceEntry["what"], callSessionId?: string, note?: string): void {
    const e: CallTraceEntry = { at: this.clock(), what };
    if (callSessionId) e.callSessionId = callSessionId;
    if (note) e.note = note;
    this.traced.push(e);
    if (this.traced.length > TRACE_SIZE) this.traced.shift();
    try {
      this.log?.(e);
    } catch {
      // a debug log must not break calls
    }
  }

  private listenToTabs(on: boolean): void {
    if (this.channel) this.channel.onmessage = on ? (ev) => this.onTabMessage(ev.data as TabMessage) : null;
  }

  private get current(): CallState {
    return this._state.value;
  }

  private get free(): boolean {
    return this.whyBusy() === null;
  }

  /** Why this tab cannot take a call now; null when it can. */
  private whyBusy(): string | null {
    const phase = this.current.phase;
    if (phase !== "idle" && phase !== "ended") return `busy here (${phase})`;
    const now = this.clock();
    for (const [id, at] of this.busyElsewhere) if (now - at >= BUSY_EXPIRY_MS) this.busyElsewhere.delete(id);
    return this.busyElsewhere.size === 0 ? null : `busy in another tab (${[...this.busyElsewhere.keys()].join(", ")})`;
  }

  /** While this tab is in a call with a known id, the account's other tabs hear so every 30 seconds. */
  private onState(s: CallState): void {
    if (s.phase !== this.lastPhase) {
      this.lastPhase = s.phase;
      this.note("state", "callSessionId" in s ? s.callSessionId : undefined, s.phase === "ended" ? `ended: ${s.reason}` : s.phase);
    }
    if (this.disposed) return;
    const id = s.phase === "active" || (s.phase === "outgoing" && s.callSessionId) ? s.callSessionId! : null;
    if (id && !this.heartbeat) {
      this.heartbeat = setInterval(() => {
        const c = this.current;
        if ("callSessionId" in c && c.callSessionId && (c.phase === "active" || c.phase === "outgoing")) this.post({ t: "busy", callSessionId: c.callSessionId });
      }, BUSY_HEARTBEAT_MS);
    } else if (!id && this.heartbeat) {
      clearInterval(this.heartbeat);
      this.heartbeat = null;
    }
  }

  // ---------------------------------------------------------------- actions

  /** Calls `peer` (a uid). Fails at once when this tab or another is in a call. */
  async invite(peer: string, callType: CallType, opts: { clientCallId?: string } = {}): Promise<InviteResult> {
    if (!this.free) return { ok: false, code: 409, reason: "busy_here" };
    this.clearTimer();
    this.early.length = 0;
    this.cancelBeforeAck = false;
    this._state.set({ phase: "outgoing", peer, callType, since: this.clock() });
    let reply: Reply;
    try {
      reply = await this.signaling.invite(peer, callType, opts.clientCallId);
    } catch {
      this.endedIfOutgoing(undefined, "failed");
      return { ok: false, code: 0, reason: "connection" };
    }
    if (!isOk(reply)) {
      if (this.current.phase === "outgoing" && !this.current.callSessionId) this._state.set({ phase: "idle" });
      const r: InviteResult = { ok: false, code: reply.code, reason: reply.reason ?? "" };
      if (reply.errCode) r.errCode = reply.errCode;
      return r;
    }
    const callSessionId = str(reply.frame, "callSessionId");
    if (this.cancelBeforeAck) {
      // Cancelled while the invite was on its way: the callee is ringing now.
      this.cancelBeforeAck = false;
      this.early.length = 0;
      await this.signaling.cancel(callSessionId).catch(() => null);
      return { ok: true, callSessionId };
    }
    const s = this.current;
    if (s.phase === "outgoing" && !s.callSessionId) {
      this._state.set({ ...s, callSessionId });
      this.post({ t: "busy", callSessionId });
      // Pushes that arrived before the acknowledgement (F49).
      const now = this.clock();
      for (const e of this.early.splice(0)) if (e.signal.callSessionId === callSessionId && now - e.at < EARLY_SIGNAL_MS) await this.onSignal(e.signal);
    }
    return { ok: true, callSessionId };
  }

  /** Answers the ringing call; a video call answered as voice stays voice. */
  async accept(asVoice = false): Promise<boolean> {
    const s = this.current;
    if (s.phase !== "incoming") return false;
    this.post({ t: "took", callSessionId: s.callSessionId });
    let reply: Reply;
    try {
      reply = await this.signaling.accept(s.callSessionId, asVoice);
    } catch {
      this.endedIf(s.callSessionId, "incoming", "failed");
      return false;
    }
    if (!isOk(reply)) {
      // Cancelled or answered meanwhile; a call_ended may follow.
      this.endedIf(s.callSessionId, "incoming", "failed");
      return false;
    }
    if (this.current.phase !== "incoming" || this.current.callSessionId !== s.callSessionId) return false;
    const callType: CallType = asVoice ? "voice" : s.callType;
    const active: CallState = { phase: "active", callSessionId: s.callSessionId, callType, connecting: true, muted: false, since: this.clock() };
    if (s.callerName) active.callerName = s.callerName;
    this._state.set(active);
    this.post({ t: "busy", callSessionId: s.callSessionId });
    await this.joinMedia(s.callSessionId, callType, credentialsOf(reply.frame));
    return true;
  }

  /** Declines the ringing call. */
  async reject(): Promise<void> {
    const s = this.current;
    if (s.phase !== "incoming") return;
    this.post({ t: "took", callSessionId: s.callSessionId });
    this.ended(s.callSessionId, "rejected");
    await this.signaling.reject(s.callSessionId).catch(() => null);
  }

  /** Stops calling before the other side answers. */
  async cancel(): Promise<void> {
    const s = this.current;
    if (s.phase !== "outgoing") return;
    if (!s.callSessionId) {
      // Not acknowledged yet: invite() cancels once it is.
      this.cancelBeforeAck = true;
      this._state.set({ phase: "ended", reason: "cancelled", since: this.clock() });
      this.scheduleIdle();
      return;
    }
    this.ended(s.callSessionId, "cancelled");
    await this.signaling.cancel(s.callSessionId).catch(() => null);
  }

  /** Ends the active call. */
  async hangup(): Promise<void> {
    const s = this.current;
    if (s.phase !== "active") return;
    this.ended(s.callSessionId, "user_hangup");
    await this.media.leave().catch(() => {});
    await this.signaling.hangup(s.callSessionId).catch(() => null);
  }

  /** Whatever ends the call from here: decline, cancel or hang up. */
  end(): Promise<void> {
    switch (this.current.phase) {
      case "incoming":
        return this.reject();
      case "outgoing":
        return this.cancel();
      case "active":
        return this.hangup();
      default:
        return Promise.resolve();
    }
  }

  /** Asks the other side to switch to video. */
  async requestVideo(): Promise<boolean> {
    const s = this.current;
    if (s.phase !== "active" || s.callType === "video" || s.upgrade === "mine") return false;
    this._state.set({ ...s, upgrade: "mine" });
    const reply = await this.signaling.upgrade(s.callSessionId).catch(() => null);
    if (reply && isOk(reply)) return true;
    this.patchActive(s.callSessionId, (a) => ({ ...a, upgrade: undefined }));
    return false;
  }

  /** Agrees to the other side's switch to video; `call_upgraded` turns the camera on. */
  async acceptVideo(): Promise<void> {
    const s = this.current;
    if (s.phase !== "active" || s.upgrade !== "theirs") return;
    this.patchActive(s.callSessionId, (a) => ({ ...a, upgrade: undefined }));
    await this.signaling.upgradeAccept(s.callSessionId).catch(() => null);
  }

  async declineVideo(): Promise<void> {
    const s = this.current;
    if (s.phase !== "active" || s.upgrade !== "theirs") return;
    this.patchActive(s.callSessionId, (a) => ({ ...a, upgrade: undefined }));
    await this.signaling.upgradeReject(s.callSessionId).catch(() => null);
  }

  setMuted(muted: boolean): void {
    const s = this.current;
    if (s.phase !== "active") return;
    this.media.setMuted(muted);
    this._state.set({ ...s, muted });
  }

  // ---------------------------------------------------------------- pushes

  private async onSignal(sig: CallSignal): Promise<void> {
    this.note("signal", sig.callSessionId, sig.kind);
    if (this.disposed) {
      this.note("drop", sig.callSessionId, "stopped");
      return;
    }
    const s = this.current;
    const mine = "callSessionId" in s && s.callSessionId === sig.callSessionId;
    if (sig.kind === "ring") {
      const busy = this.whyBusy();
      if (busy) {
        this.note("drop", sig.callSessionId, busy);
        return;
      }
      this.clearTimer();
      const ring: CallState = { phase: "incoming", callSessionId: sig.callSessionId, callType: sig.callType, ringTimeoutSeconds: sig.ringTimeoutSeconds, since: this.clock() };
      if (sig.callerName) ring.callerName = sig.callerName;
      if (sig.callerAvatar) ring.callerAvatar = sig.callerAvatar;
      this._state.set(ring);
      // The server ends an unanswered call; this only covers a lost connection.
      this.timer = setTimeout(() => this.endedIf(sig.callSessionId, "incoming", "no_answer"), (sig.ringTimeoutSeconds + 5) * 1000);
      return;
    }
    if (!mine) {
      // A push for the call this tab asked for, ahead of its id (F49).
      if (s.phase === "outgoing" && !s.callSessionId) {
        this.early.push({ at: this.clock(), signal: sig });
        if (this.early.length > 20) this.early.shift();
        this.note("drop", sig.callSessionId, "kept until the invite reply");
      } else {
        this.note("drop", sig.callSessionId, "not this tab's call");
      }
      return;
    }
    switch (sig.kind) {
      case "answered": {
        if (s.phase !== "outgoing") return;
        const active: CallState = { phase: "active", callSessionId: sig.callSessionId, callType: s.callType, peer: s.peer, connecting: true, muted: false, since: this.clock() };
        this._state.set(active);
        await this.joinMedia(sig.callSessionId, s.callType, sig.credentials);
        return;
      }
      case "ended": {
        const wasActive = s.phase === "active";
        this.ended(sig.callSessionId, sig.reason);
        if (wasActive) await this.media.leave().catch(() => {});
        return;
      }
      case "upgradeRequest":
        this.patchActive(sig.callSessionId, (a) => ({ ...a, upgrade: "theirs" }));
        return;
      case "upgradeDeclined":
        this.patchActive(sig.callSessionId, (a) => ({ ...a, upgrade: "declined" }));
        return;
      case "upgraded":
        if (s.phase !== "active") return;
        this.patchActive(sig.callSessionId, (a) => ({ ...a, callType: "video", upgrade: undefined }));
        await this.media.setVideo(true, sig.videoQuality).catch(() => {});
        return;
    }
  }

  private onTabMessage(m: TabMessage): void {
    if (!m || typeof m !== "object") return;
    this.note("tab", m.callSessionId, m.t);
    const s = this.current;
    if (m.t === "took") {
      // Another tab answered or declined the call ringing here too.
      if (s.phase === "incoming" && s.callSessionId === m.callSessionId) {
        this.clearTimer();
        this._state.set({ phase: "idle" });
      }
    } else if (m.t === "busy") {
      if (!("callSessionId" in s) || s.callSessionId !== m.callSessionId) this.busyElsewhere.set(m.callSessionId, this.clock());
    } else if (m.t === "free") {
      this.busyElsewhere.delete(m.callSessionId);
    }
  }

  // ---------------------------------------------------------------- helpers

  private async joinMedia(callSessionId: string, callType: CallType, credentials: RtcCredentials): Promise<void> {
    try {
      await this.media.join({ uid: this.me, callType, credentials });
    } catch {
      // No media, no call: end it for both sides.
      this.ended(callSessionId, "failed");
      await this.media.leave().catch(() => {});
      await this.signaling.hangup(callSessionId).catch(() => null);
      return;
    }
    this.patchActive(callSessionId, (a) => ({ ...a, connecting: false }));
  }

  private patchActive(callSessionId: string, fn: (a: CallState & { phase: "active" }) => CallState): void {
    const s = this.current;
    if (s.phase === "active" && s.callSessionId === callSessionId) this._state.set(fn(s));
  }

  private ended(callSessionId: string, reason: CallEndReason): void {
    this.clearTimer();
    this._state.set({ phase: "ended", callSessionId, reason, since: this.clock() });
    this.post({ t: "free", callSessionId });
    this.scheduleIdle();
  }

  private endedIf(callSessionId: string, phase: CallState["phase"], reason: CallEndReason): void {
    const s = this.current;
    if (s.phase === phase && "callSessionId" in s && s.callSessionId === callSessionId) this.ended(callSessionId, reason);
  }

  private endedIfOutgoing(callSessionId: string | undefined, reason: CallEndReason): void {
    if (this.current.phase !== "outgoing") return;
    this.clearTimer();
    const e: CallState = { phase: "ended", reason, since: this.clock() };
    if (callSessionId) e.callSessionId = callSessionId;
    this._state.set(e);
    this.scheduleIdle();
  }

  private scheduleIdle(): void {
    const s = this.current;
    this.timer = setTimeout(() => {
      this.timer = null;
      if (this.current === s) this._state.set({ phase: "idle" });
    }, this.endedHoldMs);
  }

  private clearTimer(): void {
    if (this.timer) clearTimeout(this.timer);
    this.timer = null;
  }

  private post(m: TabMessage): void {
    try {
      this.channel?.postMessage(m);
    } catch {
      // the channel is closed
    }
  }
}

// ------------------------------------------------------------------ ringing

/** Plays the ring of an incoming call and the ringback of an outgoing one, until stopped. */
export interface CallRinger {
  ring(): void;
  ringback(): void;
  stop(): void;
}

/** The sound a call state wants: a ring while a call rings here, a ringback while the other side has not answered. */
export function ringToneFor(state: CallState): "ring" | "ringback" | null {
  if (state.phase === "incoming") return "ring";
  if (state.phase === "outgoing") return "ringback";
  return null;
}

/**
 * Keeps `ringer` in step with a manager's state: starts a sound when the
 * wanted one changes (or another call rings), stops it when the call is
 * answered, refused or over. Returns the unsubscribe, which also stops it.
 */
export function ringWith(source: { readonly state: ReadonlySignal<CallState> }, ringer: CallRinger): Unsubscribe {
  let playing: string | null = null;
  const apply = (s: CallState) => {
    const tone = ringToneFor(s);
    const key = tone === "ring" && s.phase === "incoming" ? `ring:${s.callSessionId}` : tone;
    if (key === playing) return;
    if (playing) ringer.stop();
    playing = key;
    if (tone === "ring") ringer.ring();
    else if (tone === "ringback") ringer.ringback();
  };
  apply(source.state.value);
  const off = source.state.subscribe(apply);
  return () => {
    off();
    if (playing) ringer.stop();
    playing = null;
  };
}

/** What `ToneRinger` needs of Web Audio; a stand-in in tests. */
export interface ToneContext {
  readonly currentTime: number;
  readonly state: string;
  readonly destination: unknown;
  resume(): Promise<void>;
  createOscillator(): { frequency: { value: number }; connect(to: unknown): unknown; start(at: number): void; stop(at: number): void };
  createGain(): { gain: { setValueAtTime(v: number, at: number): unknown; linearRampToValueAtTime(v: number, at: number): unknown }; connect(to: unknown): unknown };
}

export interface ToneRingerOptions {
  /** 0..1; default 0.25. */
  volume?: number;
  /** Vibrate with the ring where the browser can (phones). Default true. */
  vibrate?: boolean;
  /** Makes the audio context, on the first sound; default a new AudioContext, none where there is no Web Audio. */
  audioContext?: () => ToneContext | null;
}

/**
 * A ringer that synthesizes its sounds with Web Audio, so the kit ships no
 * audio files: a two-note chime twice every three seconds for the ring, a
 * 425 Hz tone one second in five for the ringback (the usual Chinese and
 * European cadence). Browsers keep audio silent until the page has had a
 * click or key press; a call to an untouched tab rings without sound.
 */
export class ToneRinger implements CallRinger {
  private ctx: ToneContext | null | undefined;
  private timer: ReturnType<typeof setInterval> | null = null;
  private oscillators: { stop(at: number): void }[] = [];
  private readonly volume: number;
  private readonly vibrate: boolean;
  private readonly makeContext: () => ToneContext | null;

  constructor(options: ToneRingerOptions = {}) {
    this.volume = options.volume ?? 0.25;
    this.vibrate = options.vibrate ?? true;
    this.makeContext =
      options.audioContext ??
      (() => {
        const AC = (globalThis as { AudioContext?: new () => ToneContext }).AudioContext;
        return AC ? new AC() : null;
      });
  }

  ring(): void {
    this.loop(3_000, (at) => {
      // Two rising notes, twice.
      this.note(659.25, at, 0.18);
      this.note(783.99, at + 0.2, 0.25);
      this.note(659.25, at + 0.6, 0.18);
      this.note(783.99, at + 0.8, 0.25);
      if (this.vibrate) (globalThis.navigator as { vibrate?: (p: number[]) => boolean } | undefined)?.vibrate?.([400, 200, 400]);
    });
  }

  ringback(): void {
    this.loop(5_000, (at) => this.note(425, at, 1));
  }

  stop(): void {
    if (this.timer) clearInterval(this.timer);
    this.timer = null;
    const now = this.ctx?.currentTime ?? 0;
    for (const o of this.oscillators.splice(0)) {
      try {
        o.stop(now);
      } catch {
        // already stopped
      }
    }
    (globalThis.navigator as { vibrate?: (p: number) => boolean } | undefined)?.vibrate?.(0);
  }

  private loop(periodMs: number, cycle: (at: number) => void): void {
    this.stop();
    if (this.ctx === undefined) {
      try {
        this.ctx = this.makeContext();
      } catch {
        this.ctx = null;
      }
    }
    const ctx = this.ctx;
    if (!ctx) return;
    if (ctx.state === "suspended") void ctx.resume().catch(() => {});
    const once = () => {
      this.oscillators = this.oscillators.slice(-16);
      cycle(ctx.currentTime + 0.05);
    };
    once();
    this.timer = setInterval(once, periodMs);
  }

  private note(freq: number, at: number, seconds: number): void {
    const ctx = this.ctx!;
    const osc = ctx.createOscillator();
    const gain = ctx.createGain();
    osc.frequency.value = freq;
    // Short fades so the tone starts and ends without a click.
    gain.gain.setValueAtTime(0, at);
    gain.gain.linearRampToValueAtTime(this.volume, at + 0.02);
    gain.gain.setValueAtTime(this.volume, at + seconds - 0.03);
    gain.gain.linearRampToValueAtTime(0, at + seconds);
    osc.connect(gain);
    gain.connect(ctx.destination);
    osc.start(at);
    osc.stop(at + seconds);
    this.oscillators.push(osc);
  }
}

function defaultChannel(me: string): TabChannel | null {
  return typeof BroadcastChannel === "undefined" ? null : (new BroadcastChannel(`talkwire-call:${me}`) as unknown as TabChannel);
}
