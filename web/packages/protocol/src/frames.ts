import type { ClientFrame } from "./generated/client-frames.js";
import type { ServerAction, ServerFrame, ServerType } from "./generated/server-frames.js";

/** The largest frame either side may send (section 2): 256 KiB of UTF-8. */
export const MAX_FRAME_BYTES = 256 * 1024;

/** The error code of a frame, or 0 when it is not an error (section 4). */
export function errorCode(frame: { code?: unknown }): number {
  return typeof frame.code === "number" && frame.code >= 400 ? frame.code : 0;
}

/**
 * A parsed server frame. A frame is either a `type` frame or an `action`
 * frame and the client must look at `type` first (section 3.1); a frame
 * with neither is malformed and dropped.
 */
export type ParsedFrame =
  | { kind: "type"; type: string; frame: ServerFrame & { type: ServerType } }
  | { kind: "action"; action: string; frame: ServerFrame & { action: ServerAction | string } };

/** Parses one text frame. Returns null for anything that is not a JSON object with `type` or `action`. */
export function parseServerFrame(text: string): ParsedFrame | null {
  let value: unknown;
  try {
    value = JSON.parse(text);
  } catch {
    return null;
  }
  if (!value || typeof value !== "object" || Array.isArray(value)) return null;
  const obj = value as Record<string, unknown>;
  if (typeof obj.type === "string" && obj.type) {
    return { kind: "type", type: obj.type, frame: obj as unknown as ServerFrame & { type: ServerType } };
  }
  if (typeof obj.action === "string" && obj.action) {
    return { kind: "action", action: obj.action, frame: obj as unknown as ServerFrame & { action: string } };
  }
  return null;
}

const encoder = new TextEncoder();

/** Serializes a client frame, refusing one larger than the frame limit. */
export function encodeClientFrame(frame: ClientFrame): string {
  const text = JSON.stringify(frame);
  if (encoder.encode(text).length > MAX_FRAME_BYTES) throw new RangeError("frame exceeds " + MAX_FRAME_BYTES + " bytes");
  return text;
}
