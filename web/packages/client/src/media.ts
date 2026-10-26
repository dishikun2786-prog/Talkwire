/**
 * Media (5.7): files go to the host's upload endpoint and only their URL
 * travels over the IM connection. The defaults match the Node host SDK
 * (`POST /api/v1/chat/upload`, multipart field `file`) and the Android SDK,
 * so a message sent from the web looks the same on a phone:
 *
 * - images carry `extra.imageWidth` / `imageHeight`;
 * - videos carry `extra.thumbUrl` / `thumbWidth` / `thumbHeight` and `duration`;
 * - voice is `audio/*` with `duration` in seconds;
 * - `content` is a short fallback text for clients that do not know the
 *   type ("[Image]" …); Talkwire's own UI never shows it.
 */

import type { TokenProvider } from "./connection.js";
import type { ChatEngine } from "./engine.js";
import { messageKind, type ChatMessage, type ConversationId, type MessageKind } from "./model.js";
import { Signal, type ReadonlySignal } from "./util.js";

export interface UploadedMedia {
  /** Absolute URL to put in the message's `fileUrl`. */
  url: string;
  name: string;
  mime: string;
  size: number;
}

export class MediaUploadError extends Error {
  constructor(
    readonly status: number,
    message: string,
  ) {
    super(message);
    this.name = "MediaUploadError";
  }
}

export interface MediaUploaderOptions {
  uploadPath?: string;
  fetch?: typeof fetch;
}

export class MediaUploader {
  private readonly uploadPath: string;
  private readonly fetchFn: typeof fetch;

  constructor(
    private readonly hostBaseUrl: string,
    private readonly tokens: TokenProvider,
    options: MediaUploaderOptions = {},
  ) {
    this.uploadPath = options.uploadPath ?? "/api/v1/chat/upload";
    this.fetchFn = options.fetch ?? ((...a) => globalThis.fetch(...a));
  }

  async upload(file: Blob, fileName: string, mime?: string): Promise<UploadedMedia> {
    const token = await this.tokens(false);
    if (!token) throw new MediaUploadError(401, "not signed in");
    const body = new FormData();
    const typed = mime && file.type !== mime ? new Blob([file], { type: mime }) : file;
    body.append("file", typed, fileName);
    const resp = await this.fetchFn(new URL(this.uploadPath, this.hostBaseUrl), {
      method: "POST",
      headers: { Authorization: `Bearer ${token}` },
      body,
    });
    const text = await resp.text();
    let o: Record<string, unknown> = {};
    try {
      o = JSON.parse(text) as Record<string, unknown>;
    } catch {
      if (resp.ok) throw new MediaUploadError(resp.status, "upload reply is not JSON");
    }
    if (!resp.ok) throw new MediaUploadError(resp.status, typeof o.error === "string" ? o.error : "upload failed");
    if (typeof o.url !== "string" || !o.url) throw new MediaUploadError(resp.status, "upload reply without url");
    return {
      url: new URL(o.url, this.hostBaseUrl).toString(),
      name: typeof o.name === "string" ? o.name : fileName,
      mime: typeof o.mime === "string" ? o.mime : (mime ?? (file.type || "application/octet-stream")),
      size: typeof o.size === "number" ? o.size : file.size,
    };
  }

  /** Deletes an upload that was never sent; false when the host refuses (in use, too old, not mine). */
  async deleteUnsent(url: string): Promise<boolean> {
    const name = url.split("/").pop();
    const token = await this.tokens(false);
    if (!name || !token) return false;
    const resp = await this.fetchFn(new URL(`${this.uploadPath}/${encodeURIComponent(name)}`, this.hostBaseUrl), {
      method: "DELETE",
      headers: { Authorization: `Bearer ${token}` },
    });
    return resp.ok;
  }
}

export function defaultFallback(kind: MessageKind): string {
  switch (kind) {
    case "image":
      return "[Image]";
    case "video":
      return "[Video]";
    case "voice":
      return "[Voice]";
    default:
      return "[File]";
  }
}

export interface MediaSenderOptions {
  /** Larger files are refused before upload; the Node host SDK's default limit is 50 MB. */
  maxBytes?: number;
  fallbackText?: (kind: MessageKind) => string;
}

export type SendResult = { kind: "sent"; message: ChatMessage } | { kind: "tooLarge"; limitBytes: number } | { kind: "failed"; error: unknown };

/** Uploads a picked file or a voice clip and sends it as a message. */
export class MediaSender {
  readonly maxBytes: number;
  private readonly fallbackText: (kind: MessageKind) => string;

  constructor(
    private readonly engine: ChatEngine,
    private readonly uploader: MediaUploader,
    options: MediaSenderOptions = {},
  ) {
    this.maxBytes = options.maxBytes ?? 50 * 1024 * 1024;
    this.fallbackText = options.fallbackText ?? defaultFallback;
  }

  /** A file the user picked: images and videos get their dimensions and a thumbnail. */
  async sendFile(conversation: ConversationId, file: File): Promise<SendResult> {
    if (file.size > this.maxBytes) return { kind: "tooLarge", limitBytes: this.maxBytes };
    try {
      const mime = file.type || "application/octet-stream";
      if (mime.startsWith("image/")) {
        const dims = await imageSize(file).catch(() => null);
        const up = await this.uploader.upload(file, file.name, mime);
        const extra = dims ? { imageWidth: dims.width, imageHeight: dims.height } : undefined;
        return this.send(conversation, "image", up, 0, extra);
      }
      if (mime.startsWith("video/")) {
        const thumb = await videoThumbnail(file).catch(() => null);
        const up = await this.uploader.upload(file, file.name, mime);
        let extra: Record<string, unknown> | undefined;
        if (thumb) {
          const t = await this.uploader.upload(thumb.jpeg, file.name + "-thumb.jpg", "image/jpeg").catch(() => null);
          if (t) extra = { thumbUrl: t.url, thumbWidth: thumb.width, thumbHeight: thumb.height };
        }
        return this.send(conversation, "video", up, thumb?.durationSeconds ?? 0, extra);
      }
      const up = await this.uploader.upload(file, file.name, mime);
      // A picked .txt or .mp3 is a file, not a text or voice message.
      const type = messageKind(up.mime, up.url) === "file" ? up.mime : "application/octet-stream";
      return this.send(conversation, "file", { ...up, mime: type }, 0);
    } catch (error) {
      return { kind: "failed", error };
    }
  }

  async sendVoice(conversation: ConversationId, clip: VoiceClip): Promise<SendResult> {
    try {
      const up = await this.uploader.upload(clip.blob, "voice" + extensionOf(clip.mime), clip.mime);
      const mime = up.mime.startsWith("audio/") ? up.mime : clip.mime;
      return this.send(conversation, "voice", { ...up, mime }, Math.max(1, Math.round(clip.durationSeconds)));
    } catch (error) {
      return { kind: "failed", error };
    }
  }

  private async send(conversation: ConversationId, kind: MessageKind, up: UploadedMedia, duration: number, extra?: Record<string, unknown>): Promise<SendResult> {
    const message = await this.engine.send(conversation, {
      content: this.fallbackText(kind),
      contentType: up.mime,
      fileUrl: up.url,
      fileName: up.name,
      duration,
      ...(extra ? { extra } : {}),
    });
    return { kind: "sent", message };
  }
}

function extensionOf(mime: string): string {
  if (mime.includes("webm")) return ".webm";
  if (mime.includes("ogg")) return ".ogg";
  if (mime.includes("mp4") || mime.includes("aac")) return ".m4a";
  return ".audio";
}

async function imageSize(file: Blob): Promise<{ width: number; height: number }> {
  const bitmap = await createImageBitmap(file);
  const size = { width: bitmap.width, height: bitmap.height };
  bitmap.close();
  return size;
}

async function videoThumbnail(file: Blob): Promise<{ jpeg: Blob; width: number; height: number; durationSeconds: number }> {
  const url = URL.createObjectURL(file);
  try {
    const video = document.createElement("video");
    video.muted = true;
    video.preload = "auto";
    video.src = url;
    await new Promise<void>((resolve, reject) => {
      video.onloadeddata = () => resolve();
      video.onerror = () => reject(new Error("cannot read video"));
    });
    const canvas = document.createElement("canvas");
    const scale = Math.min(1, 480 / Math.max(video.videoWidth, video.videoHeight));
    canvas.width = Math.round(video.videoWidth * scale);
    canvas.height = Math.round(video.videoHeight * scale);
    canvas.getContext("2d")!.drawImage(video, 0, 0, canvas.width, canvas.height);
    const jpeg = await new Promise<Blob>((resolve, reject) => canvas.toBlob((b) => (b ? resolve(b) : reject(new Error("no frame"))), "image/jpeg", 0.8));
    return { jpeg, width: video.videoWidth, height: video.videoHeight, durationSeconds: Math.round(video.duration || 0) };
  } finally {
    URL.revokeObjectURL(url);
  }
}

// ------------------------------------------------------------------ voice

export interface VoiceClip {
  blob: Blob;
  mime: string;
  durationSeconds: number;
}

export type RecorderState = { kind: "idle" } | { kind: "recording"; startedAt: number } | { kind: "denied" } | { kind: "unsupported" };

/** The first container this browser records, preferring what Android plays back best. */
export function pickVoiceMime(isSupported: (mime: string) => boolean = (m) => typeof MediaRecorder !== "undefined" && MediaRecorder.isTypeSupported(m)): string | null {
  for (const m of ["audio/mp4", "audio/webm;codecs=opus", "audio/webm", "audio/ogg;codecs=opus"]) if (isSupported(m)) return m;
  return null;
}

/** Press-and-hold voice recording with MediaRecorder. */
export class VoiceRecorder {
  private readonly _state = new Signal<RecorderState>({ kind: "idle" });
  readonly state: ReadonlySignal<RecorderState> = this._state;
  private recorder: MediaRecorder | null = null;
  private stream: MediaStream | null = null;
  private chunks: Blob[] = [];

  constructor(
    readonly maxSeconds = 60,
    private readonly clock: () => number = Date.now,
  ) {}

  /** Starts recording; asks for the microphone the first time. */
  async start(): Promise<boolean> {
    if (this.recorder) return true;
    const mime = pickVoiceMime();
    if (!mime || !navigator.mediaDevices?.getUserMedia) {
      this._state.set({ kind: "unsupported" });
      return false;
    }
    try {
      this.stream = await navigator.mediaDevices.getUserMedia({ audio: true });
    } catch {
      this._state.set({ kind: "denied" });
      return false;
    }
    this.chunks = [];
    const recorder = new MediaRecorder(this.stream, { mimeType: mime });
    recorder.ondataavailable = (e) => {
      if (e.data.size > 0) this.chunks.push(e.data);
    };
    recorder.start(250);
    this.recorder = recorder;
    this._state.set({ kind: "recording", startedAt: this.clock() });
    return true;
  }

  /** Stops and returns the clip; null when nothing was recorded or it was shorter than `minSeconds`. */
  async stop(minSeconds = 1): Promise<VoiceClip | null> {
    const recorder = this.recorder;
    const s = this._state.value;
    if (!recorder || s.kind !== "recording") return null;
    const seconds = Math.min(this.maxSeconds, (this.clock() - s.startedAt) / 1000);
    await new Promise<void>((resolve) => {
      recorder.onstop = () => resolve();
      recorder.stop();
    });
    this.release();
    if (seconds < minSeconds || this.chunks.length === 0) return null;
    const mime = recorder.mimeType || "audio/webm";
    return { blob: new Blob(this.chunks, { type: mime.split(";")[0] }), mime: mime.split(";")[0]!, durationSeconds: seconds };
  }

  /** Stops and throws the recording away. */
  cancel(): void {
    if (this.recorder && this.recorder.state !== "inactive") this.recorder.stop();
    this.release();
    this.chunks = [];
  }

  private release(): void {
    this.stream?.getTracks().forEach((t) => t.stop());
    this.stream = null;
    this.recorder = null;
    this._state.set({ kind: "idle" });
  }
}
