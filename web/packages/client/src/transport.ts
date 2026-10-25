/**
 * The byte pipe under the client. The default is the platform's WebSocket
 * (browsers, and Node 22+ which has it built in); tests and the multi-tab
 * relay plug in their own.
 */

export interface ImSocketListener {
  onOpen(): void;
  onText(text: string): void;
  /** The socket closed, cleanly or not. Called at most once per socket together with `onFailure`. */
  onClosed(code: number, reason: string): void;
  onFailure(error: unknown): void;
}

export interface ImSocket {
  /** Queues a text frame; false when the socket is not open. */
  send(text: string): boolean;
  /** Closes the socket; no listener calls follow for this socket. */
  close(code?: number, reason?: string): void;
}

export interface ImTransport {
  open(url: string, listener: ImSocketListener): ImSocket;
}

type WebSocketCtor = new (url: string) => WebSocket;

export class WebSocketTransport implements ImTransport {
  constructor(private readonly ctor: WebSocketCtor | undefined = (globalThis as { WebSocket?: WebSocketCtor }).WebSocket) {}

  open(url: string, listener: ImSocketListener): ImSocket {
    if (!this.ctor) throw new Error("no WebSocket in this environment");
    const ws = new this.ctor(url);
    let done = false;
    const finish = (fn: () => void) => {
      if (done) return;
      done = true;
      fn();
    };
    ws.onopen = () => {
      if (!done) listener.onOpen();
    };
    ws.onmessage = (ev: MessageEvent) => {
      if (!done && typeof ev.data === "string") listener.onText(ev.data);
    };
    ws.onerror = () => finish(() => listener.onFailure(new Error("websocket error")));
    ws.onclose = (ev: CloseEvent) => finish(() => listener.onClosed(ev.code, ev.reason));
    return {
      send(text) {
        if (done || ws.readyState !== 1) return false;
        try {
          ws.send(text);
          return true;
        } catch {
          return false;
        }
      },
      close(code = 1000, reason = "") {
        done = true;
        try {
          ws.close(code, reason);
        } catch {
          // already closing
        }
      },
    };
  }
}
