import { describe, expect, it } from "vitest";
import { MediaUploadError, MediaUploader, notifyDecision, pickVoiceMime, type ChatMessage, type ConversationSummary } from "../src/index.js";

const msg = (over: Partial<ChatMessage> = {}): ChatMessage => ({
  conversation: "uid_peer0000001",
  seq: 3,
  localId: null,
  from: "uid_peer0000001",
  content: "hi",
  contentType: "text",
  duration: 0,
  senderKind: "user",
  ts: 1,
  status: "received",
  ...over,
});
const conv = (over: Partial<ConversationSummary> = {}): ConversationSummary => ({
  id: "uid_peer0000001",
  isGroup: false,
  unread: 1,
  lastReadSeq: 0,
  peerReadSeq: 0,
  pinned: false,
  muted: false,
  left: false,
  updatedAt: 1,
  ...over,
});

describe("notification decision", () => {
  const me = "uid_me000000001";
  it("follows page visibility and the open conversation", () => {
    expect(notifyDecision(msg(), conv(), { pageVisible: false, openConversation: null, me })).toBe("system");
    expect(notifyDecision(msg(), conv(), { pageVisible: true, openConversation: null, me })).toBe("banner");
    expect(notifyDecision(msg(), conv(), { pageVisible: true, openConversation: "uid_peer0000001", me })).toBe("none");
    expect(notifyDecision(msg(), conv(), { pageVisible: false, openConversation: "uid_peer0000001", me }), "hidden page: the open chat is not seen").toBe("system");
  });

  it("stays quiet for muted chats, my own and system messages", () => {
    const ctx = { pageVisible: false, openConversation: null, me };
    expect(notifyDecision(msg(), conv({ muted: true }), ctx)).toBe("none");
    expect(notifyDecision(msg({ from: me, status: "sent" }), conv(), ctx)).toBe("none");
    expect(notifyDecision(msg({ contentType: "system" }), conv(), ctx)).toBe("none");
  });
});

describe("media upload", () => {
  it("posts multipart with the bearer token and resolves the URL", async () => {
    const calls: { url: string; init: RequestInit }[] = [];
    const uploader = new MediaUploader("https://host.example/app/", () => "tok", {
      fetch: async (url, init) => {
        calls.push({ url: String(url), init: init! });
        return new Response(JSON.stringify({ url: "/media/abc.png", name: "abc.png", mime: "image/png", size: 3 }), { status: 200 });
      },
    });
    const up = await uploader.upload(new Blob([new Uint8Array([1, 2, 3])], { type: "image/png" }), "a.png");
    expect(up).toEqual({ url: "https://host.example/media/abc.png", name: "abc.png", mime: "image/png", size: 3 });
    expect(calls[0]!.url).toBe("https://host.example/api/v1/chat/upload");
    expect((calls[0]!.init.headers as Record<string, string>).Authorization).toBe("Bearer tok");
    expect((calls[0]!.init.body as FormData).get("file")).toBeInstanceOf(Blob);
  });

  it("reports the host's error", async () => {
    const uploader = new MediaUploader("https://host.example", () => "tok", {
      fetch: async () => new Response(JSON.stringify({ error: "file too large" }), { status: 413 }),
    });
    await expect(uploader.upload(new Blob(["x"]), "x.bin")).rejects.toEqual(new MediaUploadError(413, "file too large"));
  });

  it("prefers a voice container Android plays", () => {
    expect(pickVoiceMime((m) => m === "audio/webm;codecs=opus" || m === "audio/mp4")).toBe("audio/mp4");
    expect(pickVoiceMime((m) => m.startsWith("audio/webm"))).toBe("audio/webm;codecs=opus");
    expect(pickVoiceMime(() => false)).toBeNull();
  });
});
