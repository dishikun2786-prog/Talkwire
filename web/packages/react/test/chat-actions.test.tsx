// @vitest-environment jsdom
import { ChatEngine, ImClient, MemoryChatStore } from "@talkwire/client";
import { p2pTopic, uidOf } from "@talkwire/protocol";
import { act, cleanup, fireEvent, render, within } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { FakeServer } from "../../client/test/fake-server.js";
import { until } from "../../client/test/until.js";
import { ChatScreen, ConversationList, TalkwireProvider, replyExtra, replyRefOf, type ImUiHost } from "../src/index.js";

afterEach(cleanup);

const alice = uidOf("alice");
const bob = uidOf("bob");
const carol = uidOf("carol");
const group = "grp_actionsaction";
const names: Record<string, string> = { [bob]: "Bob", [carol]: "Carol" };
const host: ImUiHost = { profiles: async (uids) => uids.map((uid) => ({ uid, name: names[uid] ?? "Someone" })) };

/** A server that stores what alice publishes and answers forwards; `failTopics` refuse a forward. */
async function setup(failTopics: string[] = []) {
  const server = new FakeServer();
  let seq = 100;
  server.onFrame = (conn, f) => {
    if (f.action === "pub") {
      conn.push({ action: "pub", code: 200, id: f.id, topic: f.topic, seq: ++seq, ts: Date.now() });
      return true;
    }
    if (f.action === "forward_broadcast") {
      const targets = f.targetTopics as string[];
      conn.push({ type: "forward_broadcast_result", reqId: f.reqId, results: targets.map((t) => (failTopics.includes(t) ? { topic: t, ok: false, reason: "no" } : { topic: t, ok: true, seq: ++seq })) });
      return true;
    }
    if (f.action === "forward_bundle") {
      conn.push({ type: "forward_bundle_result", reqId: f.reqId, ok: true, insertedSeqs: (f.sourceMsgIds as number[]).map(() => ++seq) });
      return true;
    }
    return false;
  };
  const client = new ImClient({ endpoints: ["ws://fake"], tokenProvider: () => "tok:alice", transport: server, watchNetwork: false });
  const engine = new ChatEngine(client, new MemoryChatStore(), { appUserId: "alice", autoSync: false });
  await engine.open();
  await engine.ensureConversation(group, "team");
  await engine.ensureConversation(carol, "Carol");
  client.start();
  await client.awaitConnected(5_000);
  const push = async (frame: Record<string, unknown>) =>
    act(async () => {
      server.live[0]!.push({ action: "data", topic: bob, from: bob, ts: Date.now(), contentType: "text", ...frame });
      await until("stored", () => engine.messages(bob).value.some((m) => m.seq === frame.seq));
    });
  return {
    server,
    engine,
    push,
    close() {
      client.stop();
      engine.close();
    },
  };
}

const bubble = (ui: ReturnType<typeof render>, text: string) => ui.getByText(text).closest(".talkwire-bubble, .talkwire-card")!;

describe("reply", () => {
  it("quotes a message, sends the quote with the reply and shows it", async () => {
    const { server, engine, push, close } = await setup();
    await push({ seq: 1, content: "lunch at noon?" });
    const ui = render(
      <TalkwireProvider engine={engine} host={host} locale="en">
        <ChatScreen conversation={bob} />
      </TalkwireProvider>,
    );
    await until("shown", () => !!ui.queryByText("lunch at noon?"));
    fireEvent.contextMenu(bubble(ui, "lunch at noon?"));
    expect(ui.queryByRole("menuitem", { name: "Unsend" }), "not on someone else's message").toBeNull();
    fireEvent.click(ui.getByRole("menuitem", { name: "Reply" }));
    await until("reply bar", () => !!ui.queryByText("Reply to Bob"));

    const input = ui.getByLabelText("Message");
    fireEvent.change(input, { target: { value: "sure" } });
    fireEvent.keyDown(input, { key: "Enter" });
    await until("sent", () => server.sent("pub").some((f) => f.content === "sure"));
    expect(server.sent("pub").find((f) => f.content === "sure")!.extra).toEqual({ replyTo: { seq: 1, from: bob, text: "lunch at noon?" } });
    expect(ui.queryByText("Reply to Bob"), "the bar goes once sent").toBeNull();
    await until("stored", () => engine.messages(bob).value.some((m) => m.content === "sure" && m.seq !== null));
    const mine = bubble(ui, "sure");
    expect(within(mine as HTMLElement).getByText("Bob")).toBeTruthy();
    expect(within(mine as HTMLElement).getByText("lunch at noon?")).toBeTruthy();
    ui.unmount();
    close();
  });

  it("names the user in a quote of their own message, and cuts long quotes", async () => {
    const { engine, push, close } = await setup();
    await push({ seq: 2, content: "agreed", extra: { replyTo: { seq: 1, from: alice, text: "let's ship" } } });
    const ui = render(
      <TalkwireProvider engine={engine} host={host} locale="zh">
        <ChatScreen conversation={bob} />
      </TalkwireProvider>,
    );
    await until("shown", () => !!ui.queryByText("agreed"));
    const b = bubble(ui, "agreed") as HTMLElement;
    expect(within(b).getByText("我")).toBeTruthy();
    expect(within(b).getByText("let's ship")).toBeTruthy();
    ui.unmount();
    close();

    const long = "字".repeat(200);
    const extra = replyExtra({ seq: 9, from: bob, text: long });
    expect(Array.from(extra.replyTo.text).length).toBe(120);
    expect(replyRefOf({ extra: { replyTo: { seq: "x" } } } as never), "a quote without a seq is ignored").toBeNull();
  });
});

describe("forward", () => {
  it("sends one message to the chats picked, by their topics", async () => {
    const { server, engine, push, close } = await setup([p2pTopic(alice, carol)]);
    await push({ seq: 3, content: "pass it on" });
    const ui = render(
      <TalkwireProvider engine={engine} host={host} locale="en">
        <ChatScreen conversation={bob} />
      </TalkwireProvider>,
    );
    await until("shown", () => !!ui.queryByText("pass it on"));
    fireEvent.contextMenu(bubble(ui, "pass it on"));
    fireEvent.click(ui.getByRole("menuitem", { name: "Forward" }));
    const dialog = ui.getByRole("dialog", { name: "Forward to" });
    expect(dialog.querySelector(`[data-target="${bob}"]`), "not into the chat it came from").toBeNull();
    fireEvent.click(dialog.querySelector(`[data-target="${group}"] input`)!);
    fireEvent.click(dialog.querySelector(`[data-target="${carol}"] input`)!);
    fireEvent.click(within(dialog).getByRole("button", { name: "Send (2)" }));
    await until("sent", () => server.sent("forward_broadcast").length === 1);
    const f = server.sent("forward_broadcast")[0]!;
    expect(f.sourceTopic).toBe(p2pTopic(alice, bob));
    expect(f.sourceMsgId).toBe(3);
    expect([...(f.targetTopics as string[])].sort()).toEqual([group, p2pTopic(alice, carol)].sort());
    await until("told", () => ui.queryByRole("alert")?.textContent === "Couldn't forward to 1 chats");
    ui.unmount();
    close();
  });

  it("selects several messages and bundles them into one chat, in order", async () => {
    const { server, engine, push, close } = await setup();
    await push({ seq: 4, content: "first" });
    await push({ seq: 5, content: "second" });
    await push({ seq: 6, content: "third" });
    const ui = render(
      <TalkwireProvider engine={engine} host={host} locale="en">
        <ChatScreen conversation={bob} />
      </TalkwireProvider>,
    );
    await until("shown", () => !!ui.queryByText("third"));
    fireEvent.contextMenu(bubble(ui, "third"));
    fireEvent.click(ui.getByRole("menuitem", { name: "Select" }));
    expect(ui.getByText("1 selected")).toBeTruthy();
    expect(ui.queryByLabelText("Message"), "no composer while selecting").toBeNull();
    fireEvent.click(bubble(ui, "first"));
    fireEvent.click(bubble(ui, "second"));
    fireEvent.click(bubble(ui, "second"));
    expect(ui.getByText("2 selected")).toBeTruthy();
    // A right click while selecting opens no menu.
    fireEvent.contextMenu(bubble(ui, "first"));
    expect(ui.queryByRole("menu")).toBeNull();

    fireEvent.click(ui.getByRole("button", { name: "Forward" }));
    const dialog = ui.getByRole("dialog", { name: "Forward to" });
    fireEvent.click(dialog.querySelector(`[data-target="${group}"]`)!);
    await until("sent", () => server.sent("forward_bundle").length === 1);
    expect(server.sent("forward_bundle")[0]).toMatchObject({ sourceTopic: p2pTopic(alice, bob), sourceMsgIds: [4, 6], targetTopic: group });
    await until("told", () => ui.queryByRole("alert")?.textContent === "Forwarded");
    expect(ui.getByLabelText("Message"), "back to the composer").toBeTruthy();
    ui.unmount();
    close();
  });
});

describe("menu", () => {
  it("offers save on media, and unsend only on own sent messages", async () => {
    const { engine, push, close } = await setup();
    await push({ seq: 7, content: "[Image]", contentType: "image/png", fileUrl: "https://media.example/p.png" });
    const ui = render(
      <TalkwireProvider engine={engine} host={host} locale="en">
        <ChatScreen conversation={bob} />
      </TalkwireProvider>,
    );
    await until("image", () => !!ui.container.querySelector("img.talkwire-media"));
    fireEvent.contextMenu(ui.container.querySelector("[data-seq='7'] .talkwire-card")!);
    expect(ui.getAllByRole("menuitem").map((b) => b.textContent)).toEqual(["Save", "Reply", "Forward", "Select"]);
    fireEvent.keyDown(window, { key: "Escape" });

    const input = ui.getByLabelText("Message");
    fireEvent.change(input, { target: { value: "mine" } });
    fireEvent.keyDown(input, { key: "Enter" });
    await until("stored", () => engine.messages(bob).value.some((m) => m.content === "mine" && m.seq !== null));
    fireEvent.contextMenu(bubble(ui, "mine"));
    expect(ui.getAllByRole("menuitem").map((b) => b.textContent)).toEqual(["Copy", "Reply", "Forward", "Select", "Unsend"]);
    ui.unmount();
    close();
  });
});

describe("recall", () => {
  it("leaves a note saying who unsent a message, here and in the list", async () => {
    const { server, engine, push, close } = await setup();
    server.onFrame = ((prev) => (conn, f) => {
      if (f.action === "del") {
        conn.push({ action: "del", code: 200, id: f.id, topic: f.topic, seq: f.seq });
        return true;
      }
      return prev(conn, f);
    })(server.onFrame);
    await push({ seq: 1, content: "oops, wrong chat" });
    const ui = render(
      <TalkwireProvider engine={engine} host={host} locale="en">
        <ConversationList onOpen={() => {}} />
        <ChatScreen conversation={bob} />
      </TalkwireProvider>,
    );
    await until("shown", () => ui.queryAllByText("oops, wrong chat").length > 0);
    await act(async () => {
      server.live[0]!.push({ action: "recalled", topic: alice, from: bob, seq: 1 });
      await until("tombstone", () => engine.messages(bob).value.some((m) => m.seq === 1 && m.content === ""));
    });
    await until("note", () => !!ui.queryByText("Bob unsent a message"));
    expect(ui.queryAllByText("oops, wrong chat")).toEqual([]);
    expect(within(ui.container.querySelector(`[data-conversation="${bob}"]`) as HTMLElement).getByText("Message unsent")).toBeTruthy();

    // My own: sent, then unsent from the menu.
    const input = ui.getByLabelText("Message");
    fireEvent.change(input, { target: { value: "never mind" } });
    fireEvent.keyDown(input, { key: "Enter" });
    await until("stored", () => engine.messages(bob).value.some((m) => m.content === "never mind" && m.seq !== null));
    fireEvent.contextMenu(within(ui.container.querySelector(".talkwire-messages") as HTMLElement).getByText("never mind").closest(".talkwire-bubble")!);
    fireEvent.click(ui.getByRole("menuitem", { name: "Unsend" }));
    await until("own note", () => !!ui.queryByText("You unsent a message"));
    expect(server.sent("del").length).toBe(1);
    expect(engine.conversations.value.find((c) => c.id === bob)?.unread).toBe(0);
    ui.unmount();
    close();
  });
});
