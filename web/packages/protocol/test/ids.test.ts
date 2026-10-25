import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import { base64UrlNoPad, conversationOf, p2pTopic, sha256, uidOf } from "../src/index.js";

const vectorsPath = fileURLToPath(new URL("../../../../contract/host-api/uid-vectors.json", import.meta.url));
const hostVectors = JSON.parse(readFileSync(vectorsPath, "utf8")).vectors as { hostUserId: string; uid: string }[];

describe("sha256", () => {
  it("matches node:crypto across block boundaries", () => {
    for (const len of [0, 1, 55, 56, 63, 64, 65, 119, 120, 1000]) {
      const data = new Uint8Array(len).map((_, i) => (i * 31 + len) & 0xff);
      const expected = createHash("sha256").update(data).digest("hex");
      expect(Buffer.from(sha256(data)).toString("hex"), `length ${len}`).toBe(expected);
    }
  });

  it("encodes base64url without padding like node", () => {
    for (const len of [0, 1, 2, 3, 8, 32]) {
      const data = new Uint8Array(len).map((_, i) => (i * 97 + 250) & 0xff);
      expect(base64UrlNoPad(data)).toBe(Buffer.from(data).toString("base64url"));
    }
  });
});

describe("uid (section 5.1)", () => {
  it("reproduces the spec vectors", () => {
    expect(uidOf("alice")).toBe("uid_K9gGyX8OAK8");
    expect(uidOf("bob")).toBe("uid_gbY32PzSxto");
    expect(uidOf("00000000-0000-4000-8000-000000000001")).toBe("uid_EeWU9IGVjBA");
    expect(uidOf("用户一")).toBe("uid_7c5xGaxvxPw");
  });

  it("reproduces every host-api vector", () => {
    expect(hostVectors.length).toBeGreaterThan(5);
    for (const v of hostVectors) expect(uidOf(v.hostUserId), v.hostUserId).toBe(v.uid);
  });
});

describe("p2p topic (section 5.2)", () => {
  it("reproduces the spec vectors in either order", () => {
    const a = uidOf("alice"), b = uidOf("bob");
    expect(p2pTopic(a, b)).toBe("p2p_E5DsdYCpVC8");
    expect(p2pTopic(b, a)).toBe("p2p_E5DsdYCpVC8");
    const c = uidOf("00000000-0000-4000-8000-000000000001"), d = uidOf("用户一");
    expect(p2pTopic(c, d)).toBe("p2p_VVXxwJ61Kvw");
    expect(p2pTopic(d, c)).toBe("p2p_VVXxwJ61Kvw");
  });
});

describe("conversationOf (section 5.5)", () => {
  const me = uidOf("me"), peer = uidOf("peer"), other = uidOf("other");
  const canonical = p2pTopic(me, peer);

  it("rule 1: grp_ is a group", () => {
    expect(conversationOf("grp_abc123def456", peer, me)).toEqual({ kind: "group", topic: "grp_abc123def456" });
  });

  it("rule 2: p2p_ takes the peer from `from`, or from known peers in history", () => {
    expect(conversationOf(canonical, peer, me)).toEqual({ kind: "p2p", peer, topic: canonical });
    expect(conversationOf(canonical, undefined, me, [other, peer])).toEqual({ kind: "p2p", peer, topic: canonical });
    expect(conversationOf(canonical, undefined, me, [other])).toBeNull();
  });

  it("rule 3: my own uid means the peer is `from`", () => {
    expect(conversationOf(me, peer, me)).toEqual({ kind: "p2p", peer, topic: canonical });
  });

  it("rule 4: any other uid is the peer", () => {
    expect(conversationOf(peer, peer, me)).toEqual({ kind: "p2p", peer, topic: canonical });
  });

  it("multi-device: a frame from myself is filed under its topic", () => {
    // data echoed to my other devices: topic is the peer, from is me (section 9.1)
    expect(conversationOf(peer, me, me)).toEqual({ kind: "p2p", peer, topic: canonical });
    // recalled from my other device written as p2p_: resolved through known peers
    expect(conversationOf(canonical, me, me, [peer])).toEqual({ kind: "p2p", peer, topic: canonical });
  });
});
