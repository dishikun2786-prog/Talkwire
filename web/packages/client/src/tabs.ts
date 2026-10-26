/**
 * Several tabs of one app share one WebSocket (5.3). A relay hub owns the
 * real connection and every tab's `ImClient` talks to it through a
 * `RelayTransport`, as if it had a socket of its own. The hub runs in a
 * SharedWorker when the app provides one, otherwise in the tab elected
 * leader over BroadcastChannel (Web Locks when the browser has them, a
 * heartbeat election otherwise). When the leader tab closes, another tab is
 * elected and every client reconnects through it.
 *
 * Two leaders at once (the heartbeat election, when announcements arrive
 * late) must not mean two real connections: the server keeps one per user
 * and device, and tells the older one it was replaced, which stops a client
 * for good. So a newly elected tab first stands as a candidate and serves no
 * one until it has heard no other leader for a while; each socket talks to
 * the one hub it chose; and a hub replaced while another hub is around steps
 * down instead of passing the replacement on.
 *
 * The clients stay unchanged: each tab logs in, syncs and correlates its own
 * replies (request ids carry a per-tab prefix). The hub opens the real socket
 * with the first login it receives and answers later logins from the
 * session it already has. Pushes go to every tab. A tab's own actions are
 * not echoed by the server to the same connection, so the hub echoes them to
 * the other tabs in the form a second device would see them (section 6.2):
 * `data` with `from` = me for a stored publish, `recalled`, `topic_cleared`
 * and `info read` likewise.
 *
 * Limit: a legacy server answers a presence query without an id, so two tabs
 * querying presence at the same moment may each take the other's snapshot.
 */

import { WebSocketTransport, type ImSocket, type ImSocketListener, type ImTransport } from "./transport.js";
import { Signal, randomId, type ReadonlySignal } from "./util.js";
import { num, obj, optStr, str, type Frame } from "./wire.js";

/** Messages between the hub and the tabs' transports. */
export type RelayMessage =
  | { t: "open"; conn: string; url: string }
  /** `hub`: the hub the socket chose; other hubs ignore it (absent from older tabs: any hub that knows `conn`). */
  | { t: "send"; conn: string; text: string; hub?: string }
  | { t: "close"; conn: string; hub?: string }
  | { t: "hello" }
  | { t: "hub"; hub: string }
  /** A tab elected by heartbeat, not serving yet (see `electedTransport`). */
  | { t: "candidate"; hub: string }
  | { t: "opened"; hub: string; conn: string }
  /** `conn` set: for that connection only; otherwise for every logged-in connection except `except`. */
  | { t: "frame"; hub: string; text: string; conn?: string; except?: string }
  /** `conn` set: that connection closed; otherwise all of them. */
  | { t: "closed"; hub: string; conn?: string };

/** A two-way message pipe: a BroadcastChannel, a MessagePort, or anything with the same shape. */
export interface RelayLink {
  post(message: RelayMessage): void;
  onMessage(listener: (message: RelayMessage) => void): () => void;
}

export function channelLink(channel: BroadcastChannel | MessagePort): RelayLink {
  return {
    post: (m) => channel.postMessage(m),
    onMessage: (fn) => {
      const h = (ev: MessageEvent) => fn(ev.data as RelayMessage);
      channel.addEventListener("message", h as EventListener);
      if ("start" in channel) channel.start();
      return () => channel.removeEventListener("message", h as EventListener);
    },
  };
}

// ------------------------------------------------------------------------ hub

interface RealConnection {
  socket: ImSocket;
  state: "opening" | "login" | "ready" | "closed";
  /** The successful login reply, answered again to every later login. */
  loginReply?: Frame;
  uid?: string;
  /** Logins waiting for the real one: conn → the login frame's id. */
  waiting: Map<string, string | undefined>;
  firstLogin: string;
}

/** How long the real connection stays up after the last tab let go, for a tab that is just reconnecting. */
const IDLE_CLOSE_MS = 1_500;

export class RelayHub {
  readonly id = randomId(10);
  private readonly conns = new Map<string, { url: string; loggedIn: boolean }>();
  private real: RealConnection | null = null;
  /** Requests whose success the other tabs should hear about: request id → conn and frame. */
  private readonly echoes = new Map<string, { conn: string; frame: Frame }>();
  private idleTimer: ReturnType<typeof setTimeout> | null = null;
  private stopped = false;

  /**
   * Called when the server says the real connection was replaced ("logged in
   * from another device"). Return true when that was this browser's own
   * second hub: then the tabs are not told, the connection just closes and
   * they reconnect. Unset, or false: the tabs hear it, as from any server.
   */
  onReplaced?: () => boolean;

  constructor(
    private readonly post: (m: RelayMessage) => void,
    private readonly transport: ImTransport = new WebSocketTransport(),
  ) {}

  announce(): void {
    this.post({ t: "hub", hub: this.id });
  }

  /** Stops the hub: the real connection closes and every tab reconnects elsewhere. */
  stop(): void {
    this.stopped = true;
    this.closeReal();
    this.conns.clear();
  }

  receive(m: RelayMessage): void {
    if (this.stopped) return;
    switch (m.t) {
      case "hello":
        this.announce();
        return;
      case "open":
        this.conns.set(m.conn, { url: m.url, loggedIn: false });
        if (this.idleTimer) clearTimeout(this.idleTimer);
        this.idleTimer = null;
        this.post({ t: "opened", hub: this.id, conn: m.conn });
        return;
      case "close":
        if (m.hub && m.hub !== this.id) return;
        this.dropConn(m.conn);
        return;
      case "send":
        if (m.hub && m.hub !== this.id) return;
        this.onSend(m.conn, m.text);
        return;
      default:
        return;
    }
  }

  private dropConn(conn: string): void {
    if (!this.conns.delete(conn)) return;
    this.real?.waiting.delete(conn);
    if (this.conns.size === 0 && this.real && !this.idleTimer) {
      this.idleTimer = setTimeout(() => {
        this.idleTimer = null;
        if (this.conns.size === 0) this.closeReal();
      }, IDLE_CLOSE_MS);
    }
  }

  private onSend(conn: string, text: string): void {
    const c = this.conns.get(conn);
    if (!c) return;
    let frame: Frame | undefined;
    try {
      frame = obj(JSON.parse(text));
    } catch {
      return;
    }
    if (!frame) return;
    if (frame.action === "login") return this.onLogin(conn, c.url, frame, text);
    const real = this.real;
    if (!real || real.state !== "ready" || !c.loggedIn) return; // as if the socket dropped it
    if (!real.socket.send(text)) return;
    const id = optStr(frame, "id");
    const action = str(frame, "action");
    if (id && (action === "pub" || action === "del" || action === "del_topic")) this.echoes.set(id, { conn, frame });
    if (action === "note" && frame.what === "read" && real.uid) {
      this.echo(conn, { action: "info", topic: str(frame, "topic"), from: real.uid, what: "read", seq: num(frame, "seq") });
    }
  }

  private onLogin(conn: string, url: string, frame: Frame, text: string): void {
    const id = optStr(frame, "id");
    const real = this.real;
    if (real && real.state === "ready" && real.loginReply) {
      this.conns.get(conn)!.loggedIn = true;
      this.post({ t: "frame", hub: this.id, conn, text: JSON.stringify(withId(real.loginReply, id)) });
      return;
    }
    if (real && (real.state === "opening" || real.state === "login")) {
      real.waiting.set(conn, id);
      return;
    }
    // No real connection: this login opens one.
    let socket: ImSocket;
    const r: RealConnection = { socket: null as unknown as ImSocket, state: "opening", waiting: new Map([[conn, id]]), firstLogin: conn };
    this.real = r;
    try {
      socket = this.transport.open(url, {
        onOpen: () => {
          if (this.real !== r) return;
          r.state = "login";
          if (!socket.send(text)) this.realGone(r);
        },
        onText: (t) => this.onRealText(r, t),
        onClosed: () => this.realGone(r),
        onFailure: () => this.realGone(r),
      });
    } catch {
      this.real = null;
      this.post({ t: "closed", hub: this.id, conn });
      return;
    }
    r.socket = socket;
  }

  private onRealText(r: RealConnection, text: string): void {
    if (this.real !== r) return;
    let f: Frame | undefined;
    try {
      f = obj(JSON.parse(text));
    } catch {
      return;
    }
    if (!f) return;
    if (r.state === "login" && f.action === "login" && !f.type) {
      const code = num(f, "code");
      const waiting = [...r.waiting];
      r.waiting.clear();
      if (code === 200) {
        r.state = "ready";
        r.loginReply = f;
        const uid = optStr(f, "uid");
        if (uid) r.uid = uid;
        for (const [conn, id] of waiting) {
          const c = this.conns.get(conn);
          if (!c) continue;
          c.loggedIn = true;
          this.post({ t: "frame", hub: this.id, conn, text: conn === r.firstLogin ? text : JSON.stringify(withId(f, id)) });
        }
      } else {
        for (const [conn, id] of waiting) this.post({ t: "frame", hub: this.id, conn, text: JSON.stringify(withId(f, id)) });
        this.closeReal();
      }
      return;
    }
    if (f.action === "login" && num(f, "code") === 401 && this.onReplaced?.()) {
      this.closeReal();
      return;
    }
    this.post({ t: "frame", hub: this.id, text });
    const id = optStr(f, "id");
    const pending = id ? this.echoes.get(id) : undefined;
    if (pending && f.action === pending.frame.action) {
      this.echoes.delete(id!);
      if (num(f, "code") === 200 && r.uid) this.echoReply(pending.conn, pending.frame, f, r.uid);
    }
  }

  /** What another device would be told about a successful request (section 6.2). */
  private echoReply(conn: string, req: Frame, reply: Frame, me: string): void {
    const topic = str(req, "topic");
    switch (req.action) {
      case "pub": {
        const data: Frame = { action: "data", topic, from: me, seq: num(reply, "seq"), ts: num(reply, "ts"), senderKind: "user" };
        for (const k of ["content", "contentType", "fileUrl", "fileName", "duration", "extra", "clientMsgId"]) if (req[k] !== undefined) data[k] = req[k];
        this.echo(conn, data);
        return;
      }
      case "del":
        this.echo(conn, { action: "recalled", topic, seq: num(req, "seq"), from: me });
        return;
      case "del_topic":
        this.echo(conn, { action: "topic_cleared", topic, from: me, clearVersion: num(reply, "clearVersion"), clearBeforeSeq: num(reply, "clearBeforeSeq") });
        return;
    }
  }

  private echo(except: string, frame: Frame): void {
    this.post({ t: "frame", hub: this.id, text: JSON.stringify(frame), except });
  }

  private realGone(r: RealConnection): void {
    if (this.real !== r) return;
    r.state = "closed";
    this.real = null;
    this.echoes.clear();
    for (const c of this.conns.values()) c.loggedIn = false;
    this.conns.clear();
    this.post({ t: "closed", hub: this.id });
  }

  private closeReal(): void {
    const r = this.real;
    if (!r) return;
    r.socket.close(1000, "relay idle");
    this.realGone(r);
  }
}

function withId(f: Frame, id: string | undefined): Frame {
  const out = { ...f };
  if (id) out.id = id;
  else delete out.id;
  return out;
}

// ------------------------------------------------------------------ transport

interface VirtualSocket {
  conn: string;
  url: string;
  listener: ImSocketListener;
  hub: string | null;
  opened: boolean;
  loggedIn: boolean;
  loginId: string | undefined;
  closed: boolean;
}

/** An `ImTransport` whose sockets go through a relay hub. */
export class RelayTransport implements ImTransport {
  readonly tab = randomId(8);
  private n = 0;
  private readonly sockets = new Map<string, VirtualSocket>();
  private readonly off: () => void;

  constructor(private readonly link: RelayLink) {
    this.off = link.onMessage((m) => this.onMessage(m));
  }

  dispose(): void {
    for (const s of [...this.sockets.values()]) this.closeSocket(s, true);
    this.off();
  }

  /** Closes every socket, e.g. when the page goes away. */
  closeAll(): void {
    for (const s of [...this.sockets.values()]) this.closeSocket(s, true);
  }

  /** Reports every socket as dropped, so its client reconnects (through whatever transport it uses next). */
  failAll(): void {
    for (const s of [...this.sockets.values()]) this.gone(s);
  }

  open(url: string, listener: ImSocketListener): ImSocket {
    const s: VirtualSocket = { conn: `${this.tab}:${++this.n}`, url, listener, hub: null, opened: false, loggedIn: false, loginId: undefined, closed: false };
    this.sockets.set(s.conn, s);
    this.link.post({ t: "open", conn: s.conn, url });
    return {
      send: (text) => {
        if (s.closed || !s.opened) return false;
        try {
          const f = obj(JSON.parse(text));
          if (f?.action === "login") s.loginId = optStr(f, "id");
        } catch {
          return false;
        }
        this.link.post({ t: "send", conn: s.conn, text, ...(s.hub ? { hub: s.hub } : {}) });
        return true;
      },
      close: () => this.closeSocket(s, true),
    };
  }

  private closeSocket(s: VirtualSocket, tellHub: boolean): void {
    if (s.closed) return;
    s.closed = true;
    this.sockets.delete(s.conn);
    if (tellHub) this.link.post({ t: "close", conn: s.conn, ...(s.hub ? { hub: s.hub } : {}) });
  }

  private gone(s: VirtualSocket): void {
    if (s.closed) return;
    this.closeSocket(s, false);
    s.listener.onClosed(1006, "relay closed");
  }

  private onMessage(m: RelayMessage): void {
    switch (m.t) {
      case "hub":
        for (const s of [...this.sockets.values()]) {
          if (s.hub && s.hub !== m.hub) this.gone(s); // a new leader: reconnect through it
          else if (!s.hub) this.link.post({ t: "open", conn: s.conn, url: s.url }); // asked before any hub existed
        }
        return;
      case "opened": {
        const s = this.sockets.get(m.conn);
        if (!s) return;
        if (s.opened) {
          // A second hub answered (two leaders for a moment): only the first serves this socket.
          if (s.hub !== m.hub) this.link.post({ t: "close", conn: s.conn, hub: m.hub });
          return;
        }
        s.opened = true;
        s.hub = m.hub;
        s.listener.onOpen();
        return;
      }
      case "frame": {
        if (m.conn !== undefined) {
          const s = this.sockets.get(m.conn);
          if (!s || s.hub !== m.hub) return;
          try {
            const f = obj(JSON.parse(m.text));
            if (f?.action === "login" && num(f, "code") === 200) s.loggedIn = true;
          } catch {
            return;
          }
          s.listener.onText(m.text);
          return;
        }
        for (const s of [...this.sockets.values()]) {
          if (s.hub === m.hub && s.loggedIn && s.conn !== m.except) s.listener.onText(m.text);
        }
        return;
      }
      case "closed":
        for (const s of [...this.sockets.values()]) {
          if (s.hub === m.hub && (m.conn === undefined || m.conn === s.conn)) this.gone(s);
        }
        return;
      default:
        return;
    }
  }
}

// ------------------------------------------------------------------ election

export type TabRole = "leader" | "follower" | "worker" | "solo";

export interface TabTransportOptions {
  /** Tabs with the same name share a connection; use one per account. */
  name?: string;
  /** A SharedWorker running `serveSharedWorker`; preferred where SharedWorker exists. */
  sharedWorker?: () => SharedWorker;
  /** The real transport, in the leader tab. */
  transport?: ImTransport;
  /** Web Locks; null forces the heartbeat election. Defaults to `navigator.locks`. */
  locks?: LockManager | null;
}

export interface TabTransport {
  readonly transport: ImTransport;
  readonly role: ReadonlySignal<TabRole>;
  /** Leaves the group: the hub (if here) stops and this tab's sockets close. */
  close(): void;
}

const HEARTBEAT_MS = 1_000;
const LEADER_TIMEOUT_MS = 3_500;
/** How long a tab elected by heartbeat waits, hearing no other leader, before it serves; longer than a heartbeat. */
const SETTLE_MS = 1_500;

type Globals = { SharedWorker?: unknown; BroadcastChannel?: typeof BroadcastChannel; navigator?: { locks?: LockManager } };

/** Hands each new socket to whichever transport is current; lets a tab change how it connects. */
class SwitchingTransport implements ImTransport {
  constructor(public current: ImTransport) {}

  open(url: string, listener: ImSocketListener): ImSocket {
    return this.current.open(url, listener);
  }
}

/** Chooses how this tab reaches the server: through a SharedWorker, an elected leader tab, or directly. */
export function createTabTransport(options: TabTransportOptions = {}): TabTransport {
  const name = "talkwire:" + (options.name ?? "default");
  const role = new Signal<TabRole>("solo");
  const g = globalThis as Globals;
  if (options.sharedWorker && typeof g.SharedWorker === "function") return workerTransport(options.sharedWorker, name, role, options, g);
  return electedTransport(name, role, options, g);
}

/**
 * Connects through the SharedWorker. If the worker never comes up (it fails
 * to load, or the browser blocks it), the tab falls back to electing a leader
 * tab, and its sockets reconnect that way. Every tab of the account sees the
 * same failure, so they all end up in the same election.
 */
function workerTransport(make: () => SharedWorker, name: string, role: Signal<TabRole>, options: TabTransportOptions, g: Globals): TabTransport {
  let worker: SharedWorker | null = null;
  try {
    worker = make();
  } catch {
    return electedTransport(name, role, options, g);
  }
  const relay = new RelayTransport(channelLink(worker.port));
  const switching = new SwitchingTransport(relay);
  let fallback: TabTransport | null = null;
  let heardHub = false;
  let closed = false;
  role.set("worker");
  worker.port.addEventListener("message", (ev) => {
    if ((ev.data as RelayMessage | undefined)?.t === "hub") heardHub = true;
  });
  worker.addEventListener("error", () => {
    // Only a worker that never answered; a running worker's later errors are its own business.
    if (heardHub || fallback || closed) return;
    fallback = electedTransport(name, role, options, g);
    switching.current = fallback.transport;
    relay.failAll();
    relay.dispose();
    worker?.port.close();
  });
  worker.port.postMessage({ t: "hello" } satisfies RelayMessage);
  const onHide = () => {
    if (!fallback) relay.closeAll();
  };
  globalThis.addEventListener?.("pagehide", onHide);
  return {
    transport: switching,
    role,
    close() {
      if (closed) return;
      closed = true;
      globalThis.removeEventListener?.("pagehide", onHide);
      if (fallback) fallback.close();
      else {
        relay.dispose();
        worker?.port.close();
        role.set("solo");
      }
    },
  };
}

/** Elects one tab to hold the connection, over BroadcastChannel (Web Locks where available, a heartbeat otherwise). */
function electedTransport(name: string, role: Signal<TabRole>, options: TabTransportOptions, g: Globals): TabTransport {
  if (typeof g.BroadcastChannel !== "function") {
    role.set("solo");
    return { transport: options.transport ?? new WebSocketTransport(), role, close() {} };
  }

  const clientChannel = new BroadcastChannel(name);
  const transport = new RelayTransport(channelLink(clientChannel));
  let hub: RelayHub | null = null;
  /** Whether `hub` serves the tabs yet; under the heartbeat election it stands as a candidate first. */
  let serving = false;
  /** When another hub (leader or candidate) was last heard. */
  let rivalAt = 0;
  let hubChannel: BroadcastChannel | null = null;
  let heartbeat: ReturnType<typeof setInterval> | null = null;
  let settleTimer: ReturnType<typeof setTimeout> | null = null;
  let watchdog: ReturnType<typeof setInterval> | null = null;
  let releaseLock: (() => void) | null = null;
  let closed = false;
  role.set("follower");

  const locks = options.locks === undefined ? g.navigator?.locks ?? null : options.locks;

  /**
   * Starts a hub. With Web Locks it serves at once (the lock is exclusive).
   * Elected by heartbeat it is a candidate first: it announces itself and
   * serves no one for SETTLE_MS, giving way to a leader or to a candidate
   * with a smaller id that it hears in the meantime. Two tabs that claimed
   * at the same moment so settle on one before either opens a connection.
   */
  const becomeLeader = () => {
    if (closed || hub) return;
    hubChannel = new BroadcastChannel(name);
    const h = new RelayHub((m) => hubChannel!.postMessage(m), options.transport ?? new WebSocketTransport());
    hub = h;
    hubChannel.addEventListener("message", (ev) => {
      const m = ev.data as RelayMessage;
      if ((m.t === "hub" || m.t === "candidate") && m.hub !== h.id) {
        rivalAt = Date.now();
        if (locks) return;
        if (serving) {
          // Two leaders (announcements later than SETTLE_MS): the larger id steps down.
          // A candidate is answered at once, so that it gives way.
          if (m.t === "hub" && m.hub < h.id) return stepDown();
          if (m.t === "candidate") h.announce();
          return;
        }
        if (m.t === "hub" || m.hub < h.id) return stepDown();
        return;
      }
      if (serving) h.receive(m);
    });
    // Replaced by the server while another hub of ours is around: that hub's
    // connection replaced this one. Give way to it instead of telling the
    // tabs, which would stop them for good. Without a rival it was another
    // device, and the tabs are told.
    h.onReplaced = () => {
      if (locks || hub !== h || Date.now() - rivalAt > LEADER_TIMEOUT_MS) return false;
      stepDown();
      return true;
    };
    const serve = () => {
      settleTimer = null;
      if (hub !== h) return;
      serving = true;
      role.set("leader");
      h.announce();
      heartbeat = setInterval(() => h.announce(), HEARTBEAT_MS);
    };
    if (locks) return serve();
    hubChannel.postMessage({ t: "candidate", hub: h.id } satisfies RelayMessage);
    settleTimer = setTimeout(serve, SETTLE_MS);
  };

  const stepDown = () => {
    if (!hub) return;
    const h = hub;
    hub = null;
    serving = false;
    if (settleTimer) clearTimeout(settleTimer);
    settleTimer = null;
    if (heartbeat) clearInterval(heartbeat);
    heartbeat = null;
    h.stop();
    hubChannel?.close();
    hubChannel = null;
    if (!closed) role.set("follower");
  };

  if (locks) {
    void locks.request(name + ":leader", () => {
      becomeLeader();
      return new Promise<void>((resolve) => (releaseLock = resolve));
    });
  } else {
    // Heartbeat election: a leader announces itself every second; when none
    // is heard for a while, a tab claims leadership after a random delay and
    // stands as a candidate (see becomeLeader). A candidate counts as heard.
    let lastHeard = 0;
    clientChannel.addEventListener("message", (ev) => {
      const t = (ev.data as RelayMessage).t;
      if (t === "hub" || t === "candidate") lastHeard = Date.now();
    });
    const check = () => {
      if (hub || closed || Date.now() - lastHeard < LEADER_TIMEOUT_MS) return;
      setTimeout(() => {
        if (!hub && !closed && Date.now() - lastHeard >= LEADER_TIMEOUT_MS) becomeLeader();
      }, Math.random() * 400);
    };
    clientChannel.postMessage({ t: "hello" } satisfies RelayMessage);
    lastHeard = Date.now() - LEADER_TIMEOUT_MS + 600; // give an existing leader 600 ms to answer
    watchdog = setInterval(check, 500);
  }

  const onHide = () => {
    transport.closeAll();
    stepDown();
  };
  globalThis.addEventListener?.("pagehide", onHide);

  return {
    transport,
    role,
    close() {
      if (closed) return;
      closed = true;
      globalThis.removeEventListener?.("pagehide", onHide);
      if (watchdog) clearInterval(watchdog);
      transport.dispose();
      stepDown();
      releaseLock?.();
      clientChannel.close();
      role.set("solo");
    },
  };
}

/**
 * Runs the relay hub in a SharedWorker. The app's worker file is just:
 *
 *     import { serveSharedWorker } from "@talkwire/client";
 *     serveSharedWorker(self as unknown as SharedWorkerGlobalScope);
 */
export function serveSharedWorker(scope: { onconnect: ((ev: MessageEvent) => void) | null }, transport?: ImTransport): RelayHub {
  const ports = new Set<MessagePort>();
  const hub = new RelayHub((m) => {
    for (const p of ports) p.postMessage(m);
  }, transport);
  scope.onconnect = (ev: MessageEvent) => {
    const port = ev.ports[0];
    if (!port) return;
    ports.add(port);
    port.onmessage = (e: MessageEvent) => {
      const m = e.data as RelayMessage;
      hub.receive(m);
    };
    port.start();
    port.postMessage({ t: "hub", hub: hub.id } satisfies RelayMessage);
  };
  return hub;
}
