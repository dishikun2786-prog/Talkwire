/**
 * The connection state machine (C1) and request correlation (section 3.3):
 *
 * - the session is cleared before a new socket is opened, and the client is
 *   "connected" only once the server accepted its login;
 * - one connection at a time: every socket belongs to an attempt with a
 *   generation number, and frames from an older socket are dropped;
 * - replies are matched by `id`; a successful `login` and a `pres` snapshot
 *   may come without one (legacy servers) and are matched by action, and
 *   type-family replies by `reqId`;
 * - after a drop the client reconnects with exponential backoff and ±20%
 *   jitter, and stops for good when another login of the same device took
 *   the session (section 6.2).
 */

import { ImError, replyOf, type Reply } from "./errors.js";
import { WebSocketTransport, type ImSocket, type ImSocketListener, type ImTransport } from "./transport.js";
import { Emitter, Signal, deferred, firstValue, randomId, withTimeout, type Deferred, type ReadonlySignal } from "./util.js";
import { arr, num, obj, optNum, optStr, parseWireMessage, str, type Frame, type WireMessage } from "./wire.js";

/** Returns the host's access token; `forceRefresh` after the server refused the last one. Null when signed out. */
export type TokenProvider = (forceRefresh: boolean) => Promise<string | null> | string | null;

export interface ImClientConfig {
  /** WebSocket URLs of the IM server (`…/ws/im`), tried in order; the last one that worked goes first. */
  endpoints: string[];
  tokenProvider: TokenProvider;
  /**
   * This device's stable name for multi-device login (section 6.2): 1–64 of
   * `A–Z a–z 0–9 . _ : -`, the same on every login of this browser profile.
   * Servers without multi-device ignore it.
   */
  deviceName?: string;
  transport?: ImTransport;
  requestTimeoutMs?: number;
  publishTimeoutMs?: number;
  loginTimeoutMs?: number;
  reconnectBaseDelayMs?: number;
  reconnectMaxDelayMs?: number;
  /** Logins refused in a row before giving up (the token provider is asked to refresh each time). */
  maxAuthFailures?: number;
  random?: () => number;
  /** Prefix of request ids; tabs sharing one connection need different ones. Random by default. */
  idPrefix?: string;
  /** Reconnect at once when the browser reports the network is back. Default true where `window` exists. */
  watchNetwork?: boolean;
}

const DEVICE = /^[A-Za-z0-9._:-]{1,64}$/;
/** A connection that lived this long resets the backoff. */
const STABLE_CONNECTION_MS = 30_000;

export interface ImSession {
  uid: string;
  /** 0 from a legacy server (no `protocol` in the login reply). */
  protocol: number;
  capabilities: string[];
  /** The Web Push VAPID key, when the server has one. */
  vapidKey?: string;
  endpoint: string;
}

export type StopReason =
  | "stopped"
  /** Another login of the same device took the session; reconnecting would steal it back. */
  | "replaced"
  | "auth_rejected"
  | "no_credentials"
  | "too_many_devices"
  | "invalid_device";

export type ConnectionState =
  | { kind: "idle" }
  | { kind: "connecting"; endpoint: string; round: number }
  | { kind: "connected"; session: ImSession }
  | { kind: "waiting"; round: number; delayMs: number; lastError?: string }
  | { kind: "stopped"; reason: StopReason };

/** Server pushes and connection changes. */
export type ImEvent =
  | { kind: "connected"; session: ImSession }
  | { kind: "disconnected"; willReconnect: boolean }
  | { kind: "message"; message: WireMessage }
  | { kind: "recalled"; topic: string; seq: number; from: string }
  | { kind: "topicCleared"; topic: string; from: string; clearVersion: number; clearBeforeSeq: number; pendingId?: string }
  | { kind: "presence"; uid: string; online: boolean; rev: number }
  | { kind: "typing"; topic: string; from: string }
  | { kind: "read"; topic: string; from: string; seq: number }
  | { kind: "groupSync"; topic: string; name?: string; from?: string; eventId?: number }
  | { kind: "groupRemoved"; topic: string; from?: string; eventId?: number }
  | { kind: "contactSync"; what: string; from: string }
  | { kind: "custom"; topic?: string; what: string; extra: unknown }
  /** A frame of an optional module (calls, E2EE, group join, group AI), or an unknown push. */
  | { kind: "module"; frame: Frame; eventId?: number };

interface Attempt {
  gen: number;
  url: string;
  opened: Deferred<void>;
  gone: Deferred<string>;
  login: Deferred<Frame>;
  replaced: boolean;
  loggedInAt: number;
}

interface Pending {
  gen: number;
  action: string;
  reply: Deferred<Frame>;
  /** A `pres` query, which a legacy server answers without an id. */
  presFallback: boolean;
}

type Outcome =
  | { kind: "ended"; replaced: boolean; livedMs: number }
  | { kind: "failed"; error: string }
  | { kind: "auth" }
  | { kind: "fatal"; reason: StopReason };

export class ImConnection {
  readonly config: Required<Omit<ImClientConfig, "deviceName">> & { deviceName?: string };
  private readonly _state = new Signal<ConnectionState>({ kind: "idle" });
  readonly state: ReadonlySignal<ConnectionState> = this._state;
  readonly events = new Emitter<ImEvent>();

  private run = 0;
  private stopping = true;
  private gen = 0;
  private attempt: Attempt | null = null;
  private socket: ImSocket | null = null;
  private _session: ImSession | null = null;
  private preferred: string | null = null;
  private wake: (() => void) | null = null;
  private idSeq = 0;
  private readonly pending = new Map<string, Pending>();
  private readonly typed = new Map<string, Pending>();
  private networkListener: (() => void) | null = null;

  constructor(config: ImClientConfig) {
    if (config.endpoints.length === 0) throw new Error("no endpoints");
    if (config.deviceName !== undefined && !DEVICE.test(config.deviceName)) throw new Error("invalid device name");
    this.config = {
      transport: new WebSocketTransport(),
      requestTimeoutMs: 10_000,
      publishTimeoutMs: 15_000,
      loginTimeoutMs: 15_000,
      reconnectBaseDelayMs: 1_000,
      reconnectMaxDelayMs: 30_000,
      maxAuthFailures: 3,
      random: Math.random,
      idPrefix: randomId(6) + "-",
      watchNetwork: typeof window !== "undefined",
      ...config,
    };
  }

  get session(): ImSession | null {
    return this._session;
  }

  get isConnected(): boolean {
    return this._session !== null;
  }

  /** Starts connecting; returns at once. The state is "connecting" when this returns. */
  start(): void {
    if (!this.stopping) return;
    this.stopping = false;
    const run = ++this.run;
    this._state.set({ kind: "connecting", endpoint: this.orderedEndpoints()[0]!, round: 0 });
    if (this.config.watchNetwork && typeof window !== "undefined" && !this.networkListener) {
      this.networkListener = () => this.onNetworkAvailable();
      window.addEventListener("online", this.networkListener);
    }
    void this.runLoop(run);
  }

  /** Disconnects and stays disconnected until `start` is called again. */
  stop(): void {
    if (this.networkListener && typeof window !== "undefined") window.removeEventListener("online", this.networkListener);
    this.networkListener = null;
    this.stopWith("stopped", this.run);
  }

  /** Skips the current reconnect delay, e.g. when the network came back. */
  onNetworkAvailable(): void {
    this.wake?.();
  }

  /** The session once connected; null when the client stopped first or `timeoutMs` passed. */
  async awaitConnected(timeoutMs: number): Promise<ImSession | null> {
    const s = await firstValue(this.state, (v) => v.kind === "connected" || v.kind === "stopped", timeoutMs);
    return s?.kind === "connected" ? s.session : null;
  }

  /** Exponential backoff with ±20% jitter so clients dropped together do not come back together. */
  backoffDelay(round: number): number {
    const base = Math.min(this.config.reconnectMaxDelayMs, this.config.reconnectBaseDelayMs * 2 ** Math.min(round, 5));
    return Math.round(base * (0.8 + this.config.random() * 0.4));
  }

  // ------------------------------------------------------------------ loop

  private alive(run: number): boolean {
    return run === this.run && !this.stopping;
  }

  private stopWith(reason: StopReason, run: number): void {
    if (run !== this.run) return;
    const wasStopping = this.stopping;
    this.stopping = true;
    this.run++;
    const old = this.socket;
    const a = this.attempt;
    const had = this._session;
    this.socket = null;
    this._session = null;
    this.attempt = null;
    this.gen++;
    old?.close(1000, "bye");
    a?.gone.resolve("stopped");
    this.failAll(new ImError("connection_lost"));
    this.wake?.();
    if (wasStopping && this._state.value.kind === "stopped") return;
    this._state.set({ kind: "stopped", reason });
    if (had) this.events.emit({ kind: "disconnected", willReconnect: false });
  }

  private async runLoop(run: number): Promise<void> {
    let round = 0;
    let authFailures = 0;
    let lastError: string | undefined;
    while (this.alive(run)) {
      let outcome: Outcome = { kind: "failed", error: "no endpoint" };
      for (const url of this.orderedEndpoints()) {
        outcome = await this.connectOnce(run, url, round, authFailures > 0);
        if (!this.alive(run)) return;
        if (outcome.kind !== "failed") break;
        lastError = outcome.error;
      }
      switch (outcome.kind) {
        case "ended":
          if (outcome.replaced) {
            this.stopWith("replaced", run);
            return;
          }
          authFailures = 0;
          lastError = "connection closed";
          // A connection that dies right after login keeps the backoff growing.
          if (outcome.livedMs >= STABLE_CONNECTION_MS) round = 0;
          break;
        case "auth":
          authFailures++;
          lastError = "token refused";
          if (authFailures >= this.config.maxAuthFailures) {
            this.stopWith("auth_rejected", run);
            return;
          }
          break;
        case "fatal":
          this.stopWith(outcome.reason, run);
          return;
        case "failed":
          break;
      }
      const delayMs = this.backoffDelay(round);
      round++;
      const s: ConnectionState = { kind: "waiting", round, delayMs };
      if (lastError) s.lastError = lastError;
      this._state.set(s);
      await new Promise<void>((resolve) => {
        const t = setTimeout(done, delayMs);
        function done() {
          clearTimeout(t);
          resolve();
        }
        this.wake = done;
      });
      this.wake = null;
    }
  }

  private orderedEndpoints(): string[] {
    const p = this.preferred;
    const all = this.config.endpoints;
    return p && all.includes(p) ? [p, ...all.filter((e) => e !== p)] : all;
  }

  private async connectOnce(run: number, url: string, round: number, forceRefresh: boolean): Promise<Outcome> {
    let token: string | null;
    try {
      token = await this.config.tokenProvider(forceRefresh);
    } catch (e) {
      return { kind: "failed", error: "token provider failed: " + String(e) };
    }
    if (!this.alive(run)) return { kind: "fatal", reason: "stopped" };
    if (!token) return { kind: "fatal", reason: "no_credentials" };

    // The identity is cleared before the new socket exists (C1).
    const old = this.socket;
    this.socket = null;
    this._session = null;
    const a: Attempt = { gen: ++this.gen, url, opened: deferred(), gone: deferred(), login: deferred(), replaced: false, loggedInAt: 0 };
    this.attempt = a;
    old?.close(1000, "reconnect");
    this.failAll(new ImError("connection_lost"));
    this._state.set({ kind: "connecting", endpoint: url, round });

    let sock: ImSocket;
    try {
      sock = this.config.transport.open(url, this.listenerFor(a));
    } catch (e) {
      if (this.attempt === a) this.attempt = null;
      return { kind: "failed", error: `cannot open ${url}: ${String(e)}` };
    }
    if (this.attempt !== a) {
      sock.close(1000, "superseded");
      return { kind: "failed", error: "superseded" };
    }
    this.socket = sock;

    const opened = await withTimeout(
      Promise.race([a.opened.promise.then(() => true), a.gone.promise.then(() => false)]),
      this.config.loginTimeoutMs,
    );
    if (!opened) {
      const closed = a.gone.settled;
      this.teardown(a);
      return { kind: "failed", error: closed ? `closed before open: ${url}` : `open timed out: ${url}` };
    }
    const login: Frame = { id: `${this.config.idPrefix}login-${a.gen}`, action: "login", token };
    if (this.config.deviceName) login.device = this.config.deviceName;
    if (!sock.send(JSON.stringify(login))) {
      this.teardown(a);
      return { kind: "failed", error: "cannot send login" };
    }
    const reply = await withTimeout(
      Promise.race([a.login.promise, a.gone.promise.then(() => null)]),
      this.config.loginTimeoutMs,
    );
    if (!reply) {
      const closed = a.gone.settled;
      this.teardown(a);
      return { kind: "failed", error: closed ? "closed during login" : `no login reply from ${url}` };
    }
    const code = num(reply, "code");
    if (code === 200) {
      this.preferred = url;
      await a.gone.promise;
      return { kind: "ended", replaced: a.replaced, livedMs: Date.now() - a.loggedInAt };
    }
    this.teardown(a);
    const reason = str(reply, "reason");
    const errCode = optStr(reply, "errCode");
    if (code === 401) return { kind: "auth" };
    if (code === 409) return { kind: "fatal", reason: "too_many_devices" };
    if (errCode === "invalid_device" || reason === "invalid device") return { kind: "fatal", reason: "invalid_device" };
    return { kind: "failed", error: `login refused: ${code} ${reason}` };
  }

  private teardown(a: Attempt): void {
    if (this.attempt !== a) return;
    const s = this.socket;
    this.socket = null;
    this._session = null;
    s?.close(1000, "bye");
    a.gone.resolve("torn down");
    this.failGen(a.gen, new ImError("connection_lost"));
  }

  private listenerFor(a: Attempt): ImSocketListener {
    return {
      onOpen: () => {
        if (this.attempt === a) a.opened.resolve();
      },
      onText: (text) => {
        if (this.attempt !== a) return;
        let value: unknown;
        try {
          value = JSON.parse(text);
        } catch {
          return;
        }
        const f = obj(value);
        if (f) this.onFrame(a, f);
      },
      onClosed: () => this.onGone(a),
      onFailure: () => this.onGone(a),
    };
  }

  private onGone(a: Attempt): void {
    if (this.attempt !== a || a.gone.settled) return;
    this.socket = null;
    const had = this._session;
    this._session = null;
    a.gone.resolve("gone");
    this.failGen(a.gen, new ImError("connection_lost"));
    if (had) {
      const willReconnect = !this.stopping && !a.replaced;
      if (willReconnect) this._state.set({ kind: "waiting", round: 0, delayMs: 0, lastError: "connection closed" });
      this.events.emit({ kind: "disconnected", willReconnect });
    }
  }

  // ---------------------------------------------------------------- inbound

  private onFrame(a: Attempt, f: Frame): void {
    // Section 3.1: look at `type` first.
    const type = optStr(f, "type");
    if (type) {
      const reqId = optStr(f, "reqId");
      const p = reqId ? this.typed.get(reqId) : undefined;
      if (p && p.gen === a.gen) {
        this.typed.delete(reqId!);
        p.reply.resolve(f);
      } else {
        this.emitModule(f);
      }
      return;
    }
    const action = optStr(f, "action");
    if (!action) return;
    const id = optStr(f, "id");
    if (id) {
      const p = this.pending.get(id);
      if (p && p.gen === a.gen && p.action === action) {
        this.pending.delete(id);
        p.reply.resolve(f);
        return;
      }
    }
    // The handshake's reply: legacy servers send it without an id (R19).
    if (action === "login") {
      this.onLoginFrame(a, f);
      return;
    }
    if (action === "pres" && f.what === "snapshot" && !id) {
      for (const [k, p] of this.pending) {
        if (p.presFallback && p.gen === a.gen) {
          this.pending.delete(k);
          p.reply.resolve(f);
          break;
        }
      }
      return;
    }
    const push = this.parsePush(action, f);
    if (push) this.events.emit(push);
  }

  private onLoginFrame(a: Attempt, f: Frame): void {
    const code = num(f, "code");
    const loggedIn = this._session !== null;
    if (!a.login.settled && !loggedIn) {
      if (code === 200) {
        const uid = optStr(f, "uid");
        if (!uid) {
          a.login.resolve({ ...f, code: 502 });
          return;
        }
        const session: ImSession = {
          uid,
          protocol: num(f, "protocol"),
          capabilities: arr(f, "capabilities").filter((c): c is string => typeof c === "string"),
          endpoint: a.url,
        };
        const vapid = optStr(f, "vapidKey");
        if (vapid) session.vapidKey = vapid;
        this._session = session;
        a.loggedInAt = Date.now();
        this._state.set({ kind: "connected", session });
        this.events.emit({ kind: "connected", session });
      }
      a.login.resolve(f);
      return;
    }
    if (code === 401 && loggedIn) {
      // "logged in from another device": reconnecting would only take the session back (section 6.2).
      a.replaced = true;
      const s = this.socket;
      this.socket = null;
      s?.close(1000, "replaced");
      this.onGone(a);
    }
  }

  private emitModule(f: Frame): void {
    const ev: ImEvent = { kind: "module", frame: f };
    const eventId = optNum(f, "eventId");
    if (eventId !== undefined) ev.eventId = eventId;
    this.events.emit(ev);
  }

  private parsePush(action: string, f: Frame): ImEvent | null {
    const topic = str(f, "topic");
    const from = str(f, "from");
    const eventId = optNum(f, "eventId");
    switch (action) {
      case "data": {
        const message = parseWireMessage(f);
        return message ? { kind: "message", message } : null;
      }
      case "recalled": {
        const seq = num(f, "seq");
        return seq > 0 ? { kind: "recalled", topic, seq, from } : null;
      }
      case "topic_cleared": {
        const ev: ImEvent = { kind: "topicCleared", topic, from, clearVersion: num(f, "clearVersion"), clearBeforeSeq: num(f, "clearBeforeSeq") };
        // The pending-record id of a delivery the client was offline for: a number as a string (F9).
        const pendingId = typeof f.id === "number" ? String(f.id) : optStr(f, "id");
        if (pendingId) ev.pendingId = pendingId;
        return ev;
      }
      case "pres":
        if (f.what === "on" || f.what === "off") return { kind: "presence", uid: topic, online: f.what === "on", rev: num(f, "presRev") };
        return null;
      case "info":
        if (f.what === "kp") return { kind: "typing", topic, from };
        if (f.what === "read") {
          const seq = num(f, "seq");
          return seq > 0 ? { kind: "read", topic, from, seq } : null;
        }
        return null;
      case "grp_sync": {
        const ev: ImEvent = { kind: "groupSync", topic };
        const name = optStr(f, "name");
        if (name) ev.name = name;
        if (from) ev.from = from;
        if (eventId !== undefined) ev.eventId = eventId;
        return ev;
      }
      case "grp_disband": {
        const ev: ImEvent = { kind: "groupRemoved", topic };
        if (from) ev.from = from;
        if (eventId !== undefined) ev.eventId = eventId;
        return ev;
      }
      case "contact_sync":
        return { kind: "contactSync", what: str(f, "what"), from };
      case "custom_update": {
        const what = optStr(f, "what");
        if (!what) return null;
        const ev: ImEvent = { kind: "custom", what, extra: f.extra ?? null };
        if (topic) ev.topic = topic;
        return ev;
      }
      case "red_packet_update": {
        const ev: ImEvent = { kind: "custom", what: "red_packet", extra: f.extra ?? null };
        if (topic) ev.topic = topic;
        return ev;
      }
      default: {
        // A late reply nobody waits for is dropped; anything else belongs to a module.
        if ("code" in f) return null;
        const ev: ImEvent = { kind: "module", frame: f };
        if (eventId !== undefined) ev.eventId = eventId;
        return ev;
      }
    }
  }

  // ---------------------------------------------------------------- requests

  private nextId(): string {
    return this.config.idPrefix + ++this.idSeq;
  }

  /**
   * Sends an action-family request and waits for its reply. Resolves with
   * any reply the server sends, success or error; rejects with an ImError
   * when not connected, when the connection drops, or on timeout.
   */
  async request(frame: Frame, timeoutMs = this.config.requestTimeoutMs): Promise<Reply> {
    const a = this.attempt;
    const sock = this.socket;
    if (!this._session || !a || !sock) throw new ImError("not_connected");
    const action = str(frame, "action");
    const id = this.nextId();
    const p: Pending = { gen: a.gen, action, reply: deferred(), presFallback: action === "pres" };
    this.pending.set(id, p);
    if (!sock.send(JSON.stringify({ ...frame, id }))) {
      this.pending.delete(id);
      throw new ImError("connection_lost");
    }
    const f = await withTimeout(p.reply.promise, timeoutMs);
    if (f === undefined) {
      this.pending.delete(id);
      throw new ImError("timeout");
    }
    return replyOf(f);
  }

  /** Sends a request answered by a type-family frame, matched by `reqId` (added when missing). */
  async requestTyped(frame: Frame, timeoutMs = this.config.requestTimeoutMs): Promise<Frame> {
    const a = this.attempt;
    const sock = this.socket;
    if (!this._session || !a || !sock) throw new ImError("not_connected");
    const reqId = optStr(frame, "reqId") ?? this.nextId();
    const p: Pending = { gen: a.gen, action: str(frame, "action"), reply: deferred(), presFallback: false };
    this.typed.set(reqId, p);
    if (!sock.send(JSON.stringify({ ...frame, reqId }))) {
      this.typed.delete(reqId);
      throw new ImError("connection_lost");
    }
    const f = await withTimeout(p.reply.promise, timeoutMs);
    if (f === undefined) {
      this.typed.delete(reqId);
      throw new ImError("timeout");
    }
    return f;
  }

  /** Sends a request that has no reply (note, acks, push tokens). False when not connected. */
  sendNoReply(frame: Frame): boolean {
    const sock = this.socket;
    if (!this._session || !sock) return false;
    return sock.send(JSON.stringify(frame));
  }

  private failAll(e: ImError): void {
    for (const p of this.pending.values()) p.reply.reject(e);
    for (const p of this.typed.values()) p.reply.reject(e);
    this.pending.clear();
    this.typed.clear();
  }

  private failGen(gen: number, e: ImError): void {
    for (const [k, p] of this.pending) {
      if (p.gen === gen) {
        this.pending.delete(k);
        p.reply.reject(e);
      }
    }
    for (const [k, p] of this.typed) {
      if (p.gen === gen) {
        this.typed.delete(k);
        p.reply.reject(e);
      }
    }
  }
}
