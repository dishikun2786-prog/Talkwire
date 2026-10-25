// The add token proves where a contact request came from: a search or a QR
// resolve issues one, bound to both users, and adding a contact presents it.
// Without it, anyone who learned a user's host id elsewhere could send them
// a request without knowing their handle. Stateless: an HMAC over both ids
// and the expiry, so nothing is stored and nothing needs sweeping.

import { createHmac, timingSafeEqual } from "node:crypto";

export const ADD_TOKEN_TTL_MS = 120_000;

/** "require" refuses a request without a valid token; "observe" accepts it and reports; "off" ignores tokens. */
export type AddTokenMode = "require" | "observe" | "off";

export type AddTokenCheck = "absent" | "valid" | "invalid";

function signature(secret: string, searcher: string, target: string, expiresAtMs: number): string {
  return createHmac("sha256", secret).update(`contact-add:${searcher}:${target}:${expiresAtMs}`).digest("hex").slice(0, 32);
}

export function issueAddToken(secret: string, searcher: string, target: string, now = Date.now()): string {
  const expiresAtMs = now + ADD_TOKEN_TTL_MS;
  return `${expiresAtMs}.${signature(secret, searcher, target, expiresAtMs)}`;
}

export function checkAddToken(secret: string, token: unknown, searcher: string, target: string, now = Date.now()): AddTokenCheck {
  if (typeof token !== "string" || token.length === 0) return "absent";
  const dot = token.indexOf(".");
  if (dot <= 0) return "invalid";
  const expiresAtMs = Number(token.slice(0, dot));
  if (!Number.isSafeInteger(expiresAtMs) || expiresAtMs < now) return "invalid";
  const provided = Buffer.from(token.slice(dot + 1));
  const expected = Buffer.from(signature(secret, searcher, target, expiresAtMs));
  // timingSafeEqual throws on different lengths instead of answering false.
  return provided.length === expected.length && timingSafeEqual(provided, expected) ? "valid" : "invalid";
}

// A contact QR code carries the user's uid signed by the host, not the bare
// uid: uids are not secret (every group member sees every other member's),
// and a bare uid that resolves to a profile and an add token would let a
// group member add any other member without ever having been shown their
// code. Permanent like the user's handle; it names the uid only.

const CONTACT_QR = /^c1\.(uid_[A-Za-z0-9_-]{1,40})\.([0-9a-f]{32})$/;

function qrSignature(secret: string, uid: string): string {
  return createHmac("sha256", secret).update(`contact-qr:${uid}`).digest("hex").slice(0, 32);
}

/** The payload of a user's contact QR code. */
export function issueContactQr(secret: string, uid: string): string {
  return `c1.${uid}.${qrSignature(secret, uid)}`;
}

/** The uid a contact QR payload names, or null when it is not one this secret signed. */
export function checkContactQr(secret: string, qr: unknown): string | null {
  if (typeof qr !== "string") return null;
  const m = CONTACT_QR.exec(qr);
  if (!m) return null;
  const provided = Buffer.from(m[2]!);
  const expected = Buffer.from(qrSignature(secret, m[1]!));
  return provided.length === expected.length && timingSafeEqual(provided, expected) ? m[1]! : null;
}
