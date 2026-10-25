import { createHash } from "node:crypto";

/**
 * The wire uid of a host user id: "uid_" followed by the first 8 bytes of
 * SHA-256(hostUserId) in base64url without padding, 11 characters. The IM
 * server computes the same (server/host/uids.go); contract/host-api/
 * uid-vectors.json pins both. Changing it orphans every stored uid.
 */
export function imUid(hostUserId: string): string {
  const digest = createHash("sha256").update(hostUserId, "utf8").digest();
  return "uid_" + digest.subarray(0, 8).toString("base64url");
}

const UID = /^uid_[A-Za-z0-9_-]{11}$/;

export function isUid(s: string): boolean {
  return UID.test(s);
}
