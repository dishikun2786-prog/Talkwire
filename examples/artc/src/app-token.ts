// Channel tokens for Alibaba Cloud ARTC 3.0 (DingRTC), the "AppToken" format,
// ported from the reference deployment, where it was checked byte for byte
// against Alibaba Cloud's own Node.js demo
// (github.com/aliyun/AliRTCSample, Server/nodejs). The golden vectors in
// test/app-token.test.ts are that demo's output.
//
//   token = "000" + base64(deflate(
//     int32(len(sig)) + sig + string(appId) + int32(issued) + int32(salt) + int32(expires)
//     + bytes(service) + bytes(options)
//   ))
//   sig     = HMAC-SHA256(key = signKey, data = everything after it, padded)
//   signKey = HMAC-SHA256(key = int32(salt), data = HMAC-SHA256(key = int32(issued), data = appKey))
//
// All int32 are big-endian; a string is an int32 byte length and its UTF-8
// bytes; deflate is zlib, not gzip. The app key never leaves the host.

import crypto from "node:crypto";
import zlib from "node:zlib";

const VERSION = "000";

/** Tokens live at most 24 hours (a limit of the format). */
export const MAX_TOKEN_SECONDS = 24 * 60 * 60;

/** Grows from 256 by doubling, like the demo's ByteBuffer. */
function capacity(n: number): number {
  let c = 256;
  while (c < n) c *= 2;
  return c;
}

class Writer {
  private chunks: Buffer[] = [];
  private length = 0;

  int32(v: number): this {
    const b = Buffer.alloc(4);
    b.writeInt32BE(v, 0);
    return this.bytes(b);
  }

  bool(v: boolean): this {
    return this.bytes(Buffer.from([v ? 1 : 0]));
  }

  string(v: string): this {
    const b = Buffer.from(v, "utf8");
    return this.int32(b.length).bytes(b);
  }

  bytes(b: Buffer): this {
    this.chunks.push(b);
    this.length += b.length;
    return this;
  }

  /**
   * The demo's ByteBuffer.getvalue(shrink = false): unless shrunk, the buffer
   * is padded with zeros to its capacity, and the padding is signed and
   * compressed too. The two outer buffers are not shrunk, the inner ones are.
   */
  done(shrink = false): Buffer {
    const written = Buffer.concat(this.chunks, this.length);
    if (shrink) return written;
    const padded = Buffer.alloc(capacity(this.length));
    written.copy(padded);
    return padded;
  }
}

/** signKey = HMAC(key = salt, data = HMAC(key = issued, data = appKey)). */
function generateSignKey(appKey: string, issued: number, salt: number): Buffer {
  const ts = Buffer.alloc(4);
  ts.writeUInt32BE(issued, 0);
  const inner = crypto.createHmac("sha256", ts).update(appKey, "utf8").digest();
  const s = Buffer.alloc(4);
  s.writeUInt32BE(salt, 0);
  return crypto.createHmac("sha256", s).update(inner).digest();
}

export interface AppTokenInput {
  appId: string;
  appKey: string;
  channelId: string;
  userId: string;
  /** Unix seconds. */
  expiresAt: number;
  /** Fixed only in tests; default now. */
  issuedAt?: number;
  /** Fixed only in tests; default random. */
  salt?: number;
}

/** A token for userId in channelId with every privilege (publish and subscribe audio and video). */
export function buildAppToken(i: AppTokenInput): string {
  const issued = i.issuedAt ?? Math.floor(Date.now() / 1000);
  const salt = i.salt ?? crypto.randomInt(0, issued);
  // Service: channel, user, no privilege restriction.
  const service = new Writer().string(i.channelId).string(i.userId).bool(false).done(true);
  // Options: the demo's default engineOptions is {}, not null, so "has options" is true with zero entries.
  const options = new Writer().bool(true).int32(0).done(true);
  const body = new Writer().string(i.appId).int32(issued).int32(salt).int32(i.expiresAt).bytes(service).bytes(options).done();
  const signature = crypto.createHmac("sha256", generateSignKey(i.appKey, issued, salt)).update(body).digest();
  const framed = new Writer().int32(signature.length).bytes(signature).bytes(body).done();
  return VERSION + zlib.deflateSync(framed).toString("base64");
}

/** What a token says, for tests and debugging; the signature is not checked. */
export function readAppToken(token: string): { appId: string; issuedAt: number; salt: number; expiresAt: number; channelId: string; userId: string } {
  if (!token.startsWith(VERSION)) throw new Error("not a version 000 AppToken");
  const buf = zlib.inflateSync(Buffer.from(token.slice(VERSION.length), "base64"));
  let at = 0;
  const int32 = () => {
    const v = buf.readInt32BE(at);
    at += 4;
    return v;
  };
  const string = () => {
    const n = int32();
    const v = buf.subarray(at, at + n).toString("utf8");
    at += n;
    return v;
  };
  const signatureLength = int32();
  at += signatureLength; // skipped; `at += int32()` would add to the old offset
  const appId = string();
  const issuedAt = int32();
  const salt = int32();
  const expiresAt = int32();
  const channelId = string();
  const userId = string();
  return { appId, issuedAt, salt, expiresAt, channelId, userId };
}
