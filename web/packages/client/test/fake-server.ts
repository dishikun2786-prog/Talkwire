/**
 * An in-process server for unit tests: a transport whose sockets are answered
 * by a handler. It logs in any token "tok:<appUserId>" and leaves the rest to
 * the test, so each test states the exact server behaviour it relies on.
 */

import { uidOf } from "@imkit/protocol";
import type { ImSocket, ImSocketListener, ImTransport } from "../src/index.js";

export type Frame = Record<string, unknown>;

export class FakeConn {
  closed = false;
  /** Replaced by a newer login of the same user (see `FakeServer.replaceOlderLogins`). */
  replaced = false;
  uid: string | undefined;
  readonly received: Frame[] = [];
  readonly socket: ImSocket;

  constructor(
    private readonly server: FakeServer,
    private readonly listener: ImSocketListener,
  ) {
    this.socket = {
      send: (text) => {
        if (this.closed) return false;
        const f = JSON.parse(text) as Frame;
        this.received.push(f);
        setTimeout(() => {
          if (!this.closed) this.server.handle(this, f);
        });
        return true;
      },
      close: () => {
        this.closed = true;
      },
    };
  }

  push(frame: Frame): void {
    if (this.closed) return;
    const text = JSON.stringify(frame);
    setTimeout(() => {
      if (!this.closed) this.listener.onText(text);
    });
  }

  /** The network dies under the client. */
  drop(): void {
    if (this.closed) return;
    this.closed = true;
    this.listener.onClosed(1006, "dropped");
  }

  open(): void {
    setTimeout(() => {
      if (!this.closed) this.listener.onOpen();
    });
  }

  /** What the server tells a connection a newer login replaced, before closing it (section 6.2). */
  replace(): void {
    if (this.closed) return;
    this.replaced = true;
    const text = JSON.stringify({ action: "login", code: 401, reason: "logged in from another device" });
    setTimeout(() => {
      if (this.closed) return;
      this.listener.onText(text);
      this.closed = true;
      this.listener.onClosed(1000, "replaced");
    });
  }
}

export class FakeServer implements ImTransport {
  readonly conns: FakeConn[] = [];
  /** Answers every frame but `login`; return true when handled. */
  onFrame: (conn: FakeConn, frame: Frame) => boolean = () => false;
  /** What the login reply advertises. */
  capabilities: string[] = ["errcode", "reply-id"];
  /** One connection per user, as a legacy server (or one device): a new login replaces the older connection. */
  replaceOlderLogins = false;

  open(_url: string, listener: ImSocketListener): ImSocket {
    const c = new FakeConn(this, listener);
    this.conns.push(c);
    c.open();
    return c.socket;
  }

  get live(): FakeConn[] {
    return this.conns.filter((c) => !c.closed);
  }

  /** Every frame received with this action, over all connections. */
  sent(action: string): Frame[] {
    return this.conns.flatMap((c) => c.received.filter((f) => f.action === action));
  }

  handle(conn: FakeConn, f: Frame): void {
    if (f.action === "login" && !conn.uid) {
      const token = String(f.token ?? "");
      if (!token.startsWith("tok:")) return conn.push({ action: "login", code: 401, id: f.id, reason: "invalid token" });
      conn.uid = uidOf(token.slice(4));
      if (this.replaceOlderLogins) for (const other of this.live) if (other !== conn && other.uid === conn.uid) other.replace();
      return conn.push({ action: "login", code: 200, uid: conn.uid, protocol: 1, capabilities: this.capabilities, id: f.id });
    }
    if (this.onFrame(conn, f)) return;
    if (f.action === "note" || f.action === "ack_topic_cleared" || f.action === "ack_group_event") return;
    conn.push({ action: f.action, code: 200, id: f.id, topic: f.topic });
  }
}
