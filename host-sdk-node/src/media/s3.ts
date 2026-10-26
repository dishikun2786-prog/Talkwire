// A BlobStore on any S3-compatible storage, through the minio client
// (an optional peer dependency: import from "@talkwire/host-node/s3").
//
// Keep the bucket private. Media is served through the host's own route
// (media.ts), never from a public bucket or a CDN: chat files are private,
// and a deleted message's file must stop being reachable.

import type { Client } from "minio";
import type { BlobStore } from "./types.js";

function missing(err: unknown): boolean {
  const code = (err as { code?: string })?.code;
  return code === "NoSuchKey" || code === "NotFound";
}

export class S3BlobStore implements BlobStore {
  constructor(
    private readonly client: Client,
    private readonly bucket: string,
    /** Prepended to every object name, e.g. "talkwire/". */
    private readonly prefix = "",
  ) {}

  async put(name: string, data: Buffer, contentType: string): Promise<void> {
    await this.client.putObject(this.bucket, this.prefix + name, data, data.length, { "Content-Type": contentType });
  }

  async open(name: string) {
    try {
      const info = await this.client.statObject(this.bucket, this.prefix + name);
      const stream = await this.client.getObject(this.bucket, this.prefix + name);
      return { size: info.size, stream };
    } catch (err) {
      if (missing(err)) return null;
      throw err;
    }
  }

  async delete(name: string): Promise<void> {
    try {
      await this.client.removeObject(this.bucket, this.prefix + name);
    } catch (err) {
      if (!missing(err)) throw err;
    }
  }
}
