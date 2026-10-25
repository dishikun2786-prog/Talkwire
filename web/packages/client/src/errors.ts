import { num, optStr, type Frame } from "./wire.js";

export type ImErrorKind =
  /** The request was not sent: the client is not logged in. */
  | "not_connected"
  /** The connection dropped before the reply arrived; the server may or may not have acted. */
  | "connection_lost"
  /** No reply in time on a live connection; the server may or may not have acted. */
  | "timeout";

export class ImError extends Error {
  constructor(readonly kind: ImErrorKind) {
    super(kind.replace("_", " "));
    this.name = "ImError";
  }
}

/** A server's answer to an action-family request, success or error (section 4). */
export interface Reply {
  code: number;
  /** strict servers: the stable error code (section 4.3). */
  errCode?: string;
  /** For people only; never parse it (section 4.1). */
  reason?: string;
  frame: Frame;
}

export function replyOf(f: Frame): Reply {
  const r: Reply = { code: num(f, "code"), frame: f };
  const errCode = optStr(f, "errCode");
  if (errCode) r.errCode = errCode;
  const reason = optStr(f, "reason");
  if (reason) r.reason = reason;
  return r;
}

export const isOk = (r: Reply): boolean => r.code >= 200 && r.code < 300;
