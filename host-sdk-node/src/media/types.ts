// Where media files live. The SDK names every file itself (media.ts), so a
// store only stores bytes under a name; the content type follows from the
// name's extension.

import type { Readable } from "node:stream";

export interface StoredBlob {
  size: number;
  stream: Readable;
}

export interface BlobStore {
  put(name: string, data: Buffer, contentType: string): Promise<void>;
  /** null when there is no such blob. */
  open(name: string): Promise<StoredBlob | null>;
  /** Deleting a missing blob is not an error. */
  delete(name: string): Promise<void>;
}
