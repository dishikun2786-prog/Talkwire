// @vitest-environment jsdom
import { ChatEngine, ImClient, MemoryChatStore, type ChatMessage } from "@imkit/client";
import { uidOf } from "@imkit/protocol";
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { FakeServer } from "../../client/test/fake-server.js";
import { until } from "../../client/test/until.js";
import { ChatScreen, ConversationList, ImKitProvider, MessageBubble, MessageTypeRegistry, type ImUiHost, type MessageType } from "../src/index.js";

afterEach(cleanup);

const host: ImUiHost = { profiles: async (uids) => uids.map((uid) => ({ uid, name: uid === uidOf("bob") ? "Bob" : "Someone" })) };

const msg = (over: Partial<ChatMessage>): ChatMessage => ({
  conversation: "uid_peer0000001",
  seq: 1,
  localId: null,
  from: "uid_peer0000001",
  content: "",
  contentType: "text",
  duration: 0,
  senderKind: "user",
  ts: Date.now(),
  status: "received",
  ...over,
});

function bubble(message: ChatMessage, types?: MessageTypeRegistry) {
  const engine = new ChatEngine(new ImClient({ endpoints: ["ws://x"], tokenProvider: () => null, watchNetwork: false }), new MemoryChatStore(), { appUserId: "alice" });
  return render(
    <ImKitProvider engine={engine} host={host} types={types}>
      <MessageBubble message={message} />
    </ImKitProvider>,
  );
}

describe("message types", () => {
  /** C7: the bubble follows contentType, never the text in content; a voice bubble widens with the clip. */
  it("C7_BubblesFromContentType", () => {
    const text = bubble(msg({ content: "[Image]", contentType: "text" }));
    expect(text.container.querySelector("[data-type]")?.getAttribute("data-type")).toBe("text");
    expect(text.container.querySelector("img")).toBeNull();
    expect(text.getByText("[Image]")).toBeTruthy();
    cleanup();

    const image = bubble(msg({ content: "hello", contentType: "image/jpeg", fileUrl: "https://media.example/a.jpg", extra: { imageWidth: 400, imageHeight: 200 } }));
    expect(image.container.querySelector("[data-type]")?.getAttribute("data-type")).toBe("image");
    expect(image.container.querySelector("img")?.getAttribute("src")).toBe("https://media.example/a.jpg");
    expect(image.queryByText("hello"), "the fallback text is not shown").toBeNull();
    cleanup();

    const widths = [2, 30].map((duration) => {
      const v = bubble(msg({ content: "[Voice]", contentType: "audio/mp4", fileUrl: "https://media.example/v.m4a", duration }));
      const w = Number.parseInt((v.container.querySelector(".imkit-voice") as HTMLElement).style.width, 10);
      expect(v.queryByText("[Voice]")).toBeNull();
      cleanup();
      return w;
    });
    expect(widths[1]).toBeGreaterThan(widths[0]!);

    const unknown = bubble(msg({ content: "Update the app to see this", contentType: "application/vnd.example.poll+json" }));
    expect(unknown.container.querySelector("[data-type]")?.getAttribute("data-type")).toBe("unknown");
    expect(unknown.getByText("Update the app to see this")).toBeTruthy();
  });

  it("lets the app register its own type, ahead of the built-in ones", () => {
    const redPacket: MessageType = {
      name: "red_packet",
      matches: (m) => m.contentType === "application/vnd.imkit.red-packet+json",
      framed: false,
      render: (m) => <div className="red-packet">🧧 {String((m.extra as { amount?: number }).amount)}</div>,
      preview: () => "[Red packet]",
    };
    const types = new MessageTypeRegistry.Builder().register(redPacket).build();
    const r = bubble(msg({ content: "[Red packet]", contentType: "application/vnd.imkit.red-packet+json", extra: { amount: 8 } }), types);
    expect(r.container.querySelector(".red-packet")?.textContent).toBe("🧧 8");
    expect(types.preview(msg({ contentType: "application/vnd.imkit.red-packet+json" }), {} as never)).toBe("[Red packet]");
  });
});

describe("list and chat", () => {
  it("shows arriving messages and sends typed ones", async () => {
    const server = new FakeServer();
    const bob = uidOf("bob");
    server.onFrame = (conn, f) => {
      if (f.action !== "pub") return false;
      conn.push({ action: "pub", code: 200, id: f.id, topic: f.topic, seq: 2, ts: Date.now() });
      return true;
    };
    const client = new ImClient({ endpoints: ["ws://fake"], tokenProvider: () => "tok:alice", transport: server, watchNetwork: false });
    const engine = new ChatEngine(client, new MemoryChatStore(), { appUserId: "alice", autoSync: false });
    await engine.open();
    client.start();
    await client.awaitConnected(5_000);
    let opened: string | null = null;
    const ui = render(
      <ImKitProvider engine={engine} host={host} locale="zh">
        <ConversationList onOpen={(c) => (opened = c)} />
      </ImKitProvider>,
    );
    expect(ui.getByText("还没有聊天，从通讯录发起一个吧")).toBeTruthy();
    await act(async () => {
      server.live[0]!.push({ action: "data", topic: bob, from: bob, seq: 1, ts: Date.now(), content: "你好", contentType: "text" });
      await until("listed", () => engine.conversations.value.length === 1);
    });
    await until("Bob's name", () => !!screen.queryByText("Bob"));
    expect(screen.getByText("你好")).toBeTruthy();
    expect(screen.getByText("1"), "unread badge").toBeTruthy();
    fireEvent.click(screen.getByText("Bob"));
    expect(opened).toBe(bob);
    ui.unmount();

    const chat = render(
      <ImKitProvider engine={engine} host={host} locale="zh">
        <ChatScreen conversation={bob} />
      </ImKitProvider>,
    );
    await until("message shown", () => !!chat.queryByText("你好"));
    const input = chat.getByLabelText("发消息");
    fireEvent.change(input, { target: { value: "收到" } });
    fireEvent.keyDown(input, { key: "Enter" });
    await until("sent", () => server.sent("pub").some((f) => f.content === "收到"));
    await until("stored", () => engine.messages(bob).value.some((m) => m.content === "收到" && m.seq === 2));
    expect(chat.getByText("收到")).toBeTruthy();
    client.stop();
    engine.close();
  });
});
