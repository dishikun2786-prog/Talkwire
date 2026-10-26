// Chat media: upload, serve, delete. The SDK names every file, so a name says
// who uploaded it and when, and the rest of it cannot be guessed:
//
//   m-<owner>-<uploaded, ms, base 36>-<32 random hex digits>.<ext>
//
// <owner> is the first 8 hex digits of SHA-256(host user id). It is not a
// secret; it only lets a user delete their own unsent uploads.
//
// Every file is served with Cache-Control "private, no-store": chat media is
// private, so it must not sit in a shared cache or a CDN, where it would stay
// reachable after its message was recalled or cleared. Serve it from this
// route only, with the blob store kept private.

import { createHash, randomBytes } from "node:crypto";
import path from "node:path";
import multipart from "@fastify/multipart";
import type { FastifyInstance, FastifyRequest } from "fastify";
import type { ImClient } from "../im-client.js";
import type { BlobStore } from "./types.js";

/** Extensions accepted by default, with the type each is served as. */
export const DEFAULT_MEDIA_TYPES: Readonly<Record<string, string>> = {
  ".jpg": "image/jpeg",
  ".jpeg": "image/jpeg",
  ".png": "image/png",
  ".gif": "image/gif",
  ".webp": "image/webp",
  ".bmp": "image/bmp",
  ".mp4": "video/mp4",
  ".webm": "video/webm",
  ".mov": "video/quicktime",
  ".m4a": "audio/mp4",
  ".mp3": "audio/mpeg",
  ".aac": "audio/aac",
  ".pdf": "application/pdf",
  ".doc": "application/msword",
  ".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
  ".txt": "text/plain; charset=utf-8",
  ".zip": "application/zip",
};

const INLINE = /^(image|video|audio)\//;
const NAME = /^m-[0-9a-f]{8}-[0-9a-z]{1,11}-[0-9a-f]{32}(\.[a-z0-9]{1,8})?$/;

export function ownerTag(userId: string): string {
  return createHash("sha256").update(userId, "utf8").digest("hex").slice(0, 8);
}

export interface MediaOptions {
  store: BlobStore;
  /** The signed-in user's host id, or null when the request is not signed in. */
  authenticate(request: FastifyRequest): Promise<string | null>;
  /** Used to make sure no message still uses a file before deleting it. Without it, uploads cannot be deleted. */
  im?: ImClient;
  /** Accepted extensions and their content types; default DEFAULT_MEDIA_TYPES. */
  types?: Readonly<Record<string, string>>;
  /** Largest upload in bytes; default 50 MB. */
  maxBytes?: number;
  /** Where files are served; default /media. Message URLs are this plus "/" plus the name. */
  servePath?: string;
  /** Where upload and delete live; default /api/v1/chat/upload. */
  uploadPath?: string;
  /** Whether reading a file needs a signed-in user; default false (the name is unguessable). */
  serveRequiresAuth?: boolean;
  /** How long after upload a user may still delete their own file; default 30 minutes. */
  deleteWindowMs?: number;
  /** Clock, for tests. */
  now?(): number;
}

/** The blob name behind a media URL issued by these routes, or null for any other URL. */
export function mediaName(url: string, servePath = "/media"): string | null {
  let pathname = url;
  try {
    pathname = new URL(url, "http://local").pathname;
  } catch {
    return null;
  }
  const prefix = servePath.replace(/\/$/, "") + "/";
  if (!pathname.startsWith(prefix)) return null;
  const name = pathname.slice(prefix.length);
  return NAME.test(name) ? name : null;
}

/**
 * The host plugin's deleteMedia: deletes the blobs behind URLs these routes
 * issued and ignores every other URL. The IM server calls it only for files
 * no remaining message uses.
 */
export function mediaDeleter(store: BlobStore, servePath = "/media"): (urls: string[]) => Promise<number> {
  return async (urls) => {
    let deleted = 0;
    for (const url of urls) {
      const name = mediaName(url, servePath);
      if (!name) continue;
      await store.delete(name);
      deleted++;
    }
    return deleted;
  };
}

export async function talkwireMediaRoutes(app: FastifyInstance, options: MediaOptions): Promise<void> {
  const types = options.types ?? DEFAULT_MEDIA_TYPES;
  const maxBytes = options.maxBytes ?? 50 * 1024 * 1024;
  const servePath = (options.servePath ?? "/media").replace(/\/$/, "");
  const uploadPath = (options.uploadPath ?? "/api/v1/chat/upload").replace(/\/$/, "");
  const deleteWindow = options.deleteWindowMs ?? 30 * 60 * 1000;
  const now = options.now ?? Date.now;
  const { store } = options;

  await app.register(multipart, { limits: { fileSize: maxBytes, files: 1, fields: 10 } });

  app.post(uploadPath, async (request, reply) => {
    const userId = await options.authenticate(request);
    if (!userId) return reply.status(401).send({ error: "unauthorized" });
    if (!request.isMultipart()) return reply.status(400).send({ error: "expected multipart/form-data" });
    const file = await request.file();
    if (!file) return reply.status(400).send({ error: "no file" });
    const ext = path.extname(file.filename).toLowerCase();
    const contentType = Object.hasOwn(types, ext) ? types[ext] : undefined;
    if (!contentType) {
      file.file.resume();
      return reply.status(400).send({ error: `file type not accepted: ${ext || "(none)"}` });
    }
    let data: Buffer;
    try {
      data = await file.toBuffer();
    } catch (err) {
      if ((err as { code?: string }).code === "FST_REQ_FILE_TOO_LARGE") {
        return reply.status(413).send({ error: `file larger than ${maxBytes} bytes` });
      }
      throw err;
    }
    if (data.length === 0) return reply.status(400).send({ error: "empty file" });
    const name = `m-${ownerTag(userId)}-${now().toString(36)}-${randomBytes(16).toString("hex")}${ext}`;
    await store.put(name, data, contentType);
    return { url: `${servePath}/${name}`, name: file.filename, mime: contentType, size: data.length };
  });

  app.get(`${servePath}/:name`, async (request, reply) => {
    if (options.serveRequiresAuth && !(await options.authenticate(request))) {
      return reply.status(401).send({ error: "unauthorized" });
    }
    const { name } = request.params as { name: string };
    const ext = path.extname(name).toLowerCase();
    const contentType = Object.hasOwn(types, ext) ? types[ext] : undefined;
    if (!NAME.test(name) || !contentType) return reply.status(404).send({ error: "not found" });
    const blob = await store.open(name);
    if (!blob) return reply.status(404).send({ error: "not found" });
    reply
      .header("Content-Type", contentType)
      .header("Content-Length", blob.size)
      .header("Cache-Control", "private, no-store")
      .header("X-Content-Type-Options", "nosniff")
      .header("Content-Disposition", INLINE.test(contentType) ? "inline" : "attachment");
    return reply.send(blob.stream);
  });

  // A user abandons an upload they never sent (the upload and the message
  // are two requests; a crash or a cancel in between leaves the file).
  app.delete(`${uploadPath}/:name`, async (request, reply) => {
    const userId = await options.authenticate(request);
    if (!userId) return reply.status(401).send({ error: "unauthorized" });
    const { name } = request.params as { name: string };
    const notYours = () => reply.status(404).send({ error: "no such upload of yours" });
    if (!NAME.test(name) || !name.startsWith(`m-${ownerTag(userId)}-`)) return notYours();
    const uploadedAt = parseInt(name.split("-")[2] ?? "", 36);
    if (!Number.isFinite(uploadedAt) || now() - uploadedAt > deleteWindow) return notYours();
    if (!options.im) return reply.status(501).send({ error: "deleting uploads needs the IM client" });
    let refs: number;
    try {
      refs = await options.im.mediaRefCount(`${servePath}/${name}`);
    } catch (err) {
      request.log.warn({ err }, "talkwire: media-ref-count failed");
      return reply.status(503).send({ error: "cannot check whether a message uses the file; try again" });
    }
    if (refs > 0) return reply.status(409).send({ error: "a message uses this file" });
    await store.delete(name);
    return { deleted: true };
  });
}
