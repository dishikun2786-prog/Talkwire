/** Reading fields off frames: missing means zero (section 3.2), unknown fields are ignored. */

export type Frame = Record<string, unknown>;

export const str = (f: Frame, k: string): string => (typeof f[k] === "string" ? (f[k] as string) : "");
export const optStr = (f: Frame, k: string): string | undefined => (typeof f[k] === "string" && f[k] !== "" ? (f[k] as string) : undefined);
export const num = (f: Frame, k: string): number => (typeof f[k] === "number" && Number.isFinite(f[k]) ? (f[k] as number) : 0);
export const optNum = (f: Frame, k: string): number | undefined => (typeof f[k] === "number" && Number.isFinite(f[k]) ? (f[k] as number) : undefined);
export const bool = (f: Frame, k: string): boolean => f[k] === true;
export const arr = (f: Frame, k: string): unknown[] => (Array.isArray(f[k]) ? (f[k] as unknown[]) : []);
export const obj = (v: unknown): Frame | undefined => (v && typeof v === "object" && !Array.isArray(v) ? (v as Frame) : undefined);

/** A message as a `data` push or a `get` reply carries it. */
export interface WireMessage {
  topic: string;
  seq: number;
  from: string;
  ts: number;
  content: string;
  contentType: string;
  fileUrl?: string;
  fileName?: string;
  duration: number;
  extra?: unknown;
  senderKind: string;
  clientMsgId?: string;
  forwardFromMsgId?: number;
  forwardFromTopic?: string;
  /** Group name, on group `data` pushes. */
  name?: string;
}

export function parseWireMessage(f: Frame): WireMessage | null {
  const topic = str(f, "topic");
  const seq = num(f, "seq");
  if (!topic || seq <= 0) return null;
  const m: WireMessage = {
    topic,
    seq,
    from: str(f, "from"),
    ts: num(f, "ts"),
    content: str(f, "content"),
    contentType: str(f, "contentType"),
    duration: num(f, "duration"),
    senderKind: optStr(f, "senderKind") ?? "user",
  };
  const fileUrl = optStr(f, "fileUrl");
  if (fileUrl) m.fileUrl = fileUrl;
  const fileName = optStr(f, "fileName");
  if (fileName) m.fileName = fileName;
  if (f.extra !== undefined && f.extra !== null) m.extra = f.extra;
  const cid = optStr(f, "clientMsgId");
  if (cid) m.clientMsgId = cid;
  const ffm = optNum(f, "forwardFromMsgId");
  if (ffm) m.forwardFromMsgId = ffm;
  const fft = optStr(f, "forwardFromTopic");
  if (fft) m.forwardFromTopic = fft;
  const name = optStr(f, "name");
  if (name) m.name = name;
  return m;
}
