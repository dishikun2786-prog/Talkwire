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
