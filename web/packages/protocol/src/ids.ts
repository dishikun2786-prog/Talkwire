/**
 * Addresses on the wire (protocol section 5): a user's uid, the canonical
 * single-chat topic, and how a frame's topic maps to a conversation.
 *
 * SHA-256 is implemented here rather than taken from WebCrypto because
 * `crypto.subtle.digest` is asynchronous, and mapping an incoming frame to a
 * conversation must be synchronous.
 */

const K = new Uint32Array([
  0x428a2f98, 0x71374491, 0xb5c0fbcf, 0xe9b5dba5, 0x3956c25b, 0x59f111f1, 0x923f82a4, 0xab1c5ed5,
  0xd807aa98, 0x12835b01, 0x243185be, 0x550c7dc3, 0x72be5d74, 0x80deb1fe, 0x9bdc06a7, 0xc19bf174,
  0xe49b69c1, 0xefbe4786, 0x0fc19dc6, 0x240ca1cc, 0x2de92c6f, 0x4a7484aa, 0x5cb0a9dc, 0x76f988da,
  0x983e5152, 0xa831c66d, 0xb00327c8, 0xbf597fc7, 0xc6e00bf3, 0xd5a79147, 0x06ca6351, 0x14292967,
  0x27b70a85, 0x2e1b2138, 0x4d2c6dfc, 0x53380d13, 0x650a7354, 0x766a0abb, 0x81c2c92e, 0x92722c85,
  0xa2bfe8a1, 0xa81a664b, 0xc24b8b70, 0xc76c51a3, 0xd192e819, 0xd6990624, 0xf40e3585, 0x106aa070,
  0x19a4c116, 0x1e376c08, 0x2748774c, 0x34b0bcb5, 0x391c0cb3, 0x4ed8aa4a, 0x5b9cca4f, 0x682e6ff3,
  0x748f82ee, 0x78a5636f, 0x84c87814, 0x8cc70208, 0x90befffa, 0xa4506ceb, 0xbef9a3f7, 0xc67178f2,
]);

/** SHA-256 of `data`. */
export function sha256(data: Uint8Array): Uint8Array {
  const bitLen = data.length * 8;
  const padded = new Uint8Array((((data.length + 9 + 63) >> 6) << 6));
  padded.set(data);
  padded[data.length] = 0x80;
  const view = new DataView(padded.buffer);
  view.setUint32(padded.length - 8, Math.floor(bitLen / 0x100000000));
  view.setUint32(padded.length - 4, bitLen >>> 0);

  const h = new Uint32Array([
    0x6a09e667, 0xbb67ae85, 0x3c6ef372, 0xa54ff53a, 0x510e527f, 0x9b05688c, 0x1f83d9ab, 0x5be0cd19,
  ]);
  const w = new Uint32Array(64);
  for (let off = 0; off < padded.length; off += 64) {
    for (let i = 0; i < 16; i++) w[i] = view.getUint32(off + i * 4);
    for (let i = 16; i < 64; i++) {
      const a = w[i - 15]!, b = w[i - 2]!;
      const s0 = ((a >>> 7) | (a << 25)) ^ ((a >>> 18) | (a << 14)) ^ (a >>> 3);
      const s1 = ((b >>> 17) | (b << 15)) ^ ((b >>> 19) | (b << 13)) ^ (b >>> 10);
      w[i] = (w[i - 16]! + s0 + w[i - 7]! + s1) | 0;
    }
    let a = h[0]!, b = h[1]!, c = h[2]!, d = h[3]!, e = h[4]!, f = h[5]!, g = h[6]!, hh = h[7]!;
    for (let i = 0; i < 64; i++) {
      const S1 = ((e >>> 6) | (e << 26)) ^ ((e >>> 11) | (e << 21)) ^ ((e >>> 25) | (e << 7));
      const ch = (e & f) ^ (~e & g);
      const t1 = (hh + S1 + ch + K[i]! + w[i]!) | 0;
      const S0 = ((a >>> 2) | (a << 30)) ^ ((a >>> 13) | (a << 19)) ^ ((a >>> 22) | (a << 10));
      const maj = (a & b) ^ (a & c) ^ (b & c);
      const t2 = (S0 + maj) | 0;
      hh = g; g = f; f = e; e = (d + t1) | 0; d = c; c = b; b = a; a = (t1 + t2) | 0;
    }
    h[0] = (h[0]! + a) | 0; h[1] = (h[1]! + b) | 0; h[2] = (h[2]! + c) | 0; h[3] = (h[3]! + d) | 0;
    h[4] = (h[4]! + e) | 0; h[5] = (h[5]! + f) | 0; h[6] = (h[6]! + g) | 0; h[7] = (h[7]! + hh) | 0;
  }
  const out = new Uint8Array(32);
  const outView = new DataView(out.buffer);
  for (let i = 0; i < 8; i++) outView.setUint32(i * 4, h[i]!);
  return out;
}

const B64URL = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_";

/** base64url without padding. */
export function base64UrlNoPad(bytes: Uint8Array): string {
  let out = "";
  let i = 0;
  for (; i + 3 <= bytes.length; i += 3) {
    const n = (bytes[i]! << 16) | (bytes[i + 1]! << 8) | bytes[i + 2]!;
    out += B64URL[(n >> 18) & 63]! + B64URL[(n >> 12) & 63]! + B64URL[(n >> 6) & 63]! + B64URL[n & 63]!;
  }
  const rest = bytes.length - i;
  if (rest === 1) {
    const n = bytes[i]! << 16;
    out += B64URL[(n >> 18) & 63]! + B64URL[(n >> 12) & 63]!;
  } else if (rest === 2) {
    const n = (bytes[i]! << 16) | (bytes[i + 1]! << 8);
    out += B64URL[(n >> 18) & 63]! + B64URL[(n >> 12) & 63]! + B64URL[(n >> 6) & 63]!;
  }
  return out;
}

const utf8 = new TextEncoder();

function shortHash(text: string): string {
  return base64UrlNoPad(sha256(utf8.encode(text)).subarray(0, 8));
}

/** The uid of a host user id (section 5.1). The client can compute any user's uid, never the reverse. */
export function uidOf(appUserId: string): string {
  return "uid_" + shortHash(appUserId);
}

/** The canonical single-chat topic of two uids, independent of order (section 5.2). */
export function p2pTopic(uidA: string, uidB: string): string {
  const [a, b] = byteOrder(uidA, uidB) <= 0 ? [uidA, uidB] : [uidB, uidA];
  return "p2p_" + shortHash(a + ":" + b);
}

/** Byte-wise (code unit) comparison: uppercase before lowercase, as the spec requires. */
function byteOrder(a: string, b: string): number {
  return a < b ? -1 : a > b ? 1 : 0;
}

const UID = /^uid_[A-Za-z0-9_-]{11}$/;

/** A user address on the wire: a uid, or a legacy `usr…` id the server still accepts. */
export function isUserAddress(s: string): boolean {
  return UID.test(s) || s.startsWith("usr");
}

export function isGroupTopic(topic: string): boolean {
  return topic.startsWith("grp_");
}

export function isP2pTopic(topic: string): boolean {
  return topic.startsWith("p2p_");
}

/** Where a frame belongs: a group, or a single chat with a peer. */
export type ConversationRef =
  | { kind: "group"; topic: string }
  | { kind: "p2p"; peer: string; topic: string };

/**
 * Maps a frame's `topic` (and `from`, when it has one) to the conversation it
 * belongs to, following the four rules of section 5.5. `knownPeers` is used
 * only for a `p2p_` topic without `from` (history): the peer whose canonical
 * topic matches. Returns null when the frame cannot be placed, which is only
 * a `p2p_` topic from an unknown peer.
 */
export function conversationOf(
  topic: string,
  from: string | undefined,
  selfUid: string,
  knownPeers: Iterable<string> = [],
): ConversationRef | null {
  if (isGroupTopic(topic)) return { kind: "group", topic };
  if (isP2pTopic(topic)) {
    let peer = from && from !== selfUid ? from : undefined;
    if (!peer) {
      for (const candidate of knownPeers) {
        if (p2pTopic(selfUid, candidate) === topic) {
          peer = candidate;
          break;
        }
      }
    }
    return peer ? { kind: "p2p", peer, topic } : null;
  }
  if (topic === selfUid) {
    if (!from || from === selfUid) return null;
    return { kind: "p2p", peer: from, topic: p2pTopic(selfUid, from) };
  }
  return { kind: "p2p", peer: topic, topic: p2pTopic(selfUid, topic) };
}
