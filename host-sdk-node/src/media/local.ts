// A BlobStore on the local disk: one file per blob in one directory.

import { createReadStream } from "node:fs";
import { mkdir, rename, stat, unlink, writeFile } from "node:fs/promises";
import path from "node:path";
import type { BlobStore } from "./types.js";

export class LocalDiskBlobStore implements BlobStore {
  constructor(private readonly dir: string) {}

  private file(name: string): string {
    // Names come from media.ts and are checked there; this is a second guard.
    if (name !== path.basename(name) || name.startsWith(".")) throw new Error(`bad blob name: ${name}`);
    return path.join(this.dir, name);
  }

  async put(name: string, data: Buffer): Promise<void> {
    const target = this.file(name);
    await mkdir(this.dir, { recursive: true });
    // Written beside and renamed, so a reader never sees half a file.
    const part = `${target}.part`;
    await writeFile(part, data);
    await rename(part, target);
  }

  async open(name: string) {
    const file = this.file(name);
    try {
      const info = await stat(file);
      if (!info.isFile()) return null;
      return { size: info.size, stream: createReadStream(file) };
    } catch (err) {
      if ((err as NodeJS.ErrnoException).code === "ENOENT") return null;
      throw err;
    }
  }

  async delete(name: string): Promise<void> {
    try {
      await unlink(this.file(name));
    } catch (err) {
      if ((err as NodeJS.ErrnoException).code !== "ENOENT") throw err;
    }
  }
}
