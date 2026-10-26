// @vitest-environment jsdom
import { ChatEngine, ImClient, MemoryChatStore, Signal, type ChatMessage } from "@talkwire/client";
import { uidOf } from "@talkwire/protocol";
import { act, cleanup, fireEvent, render, within } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { FakeServer } from "../../client/test/fake-server.js";
import { until } from "../../client/test/until.js";
import { ChatScreen, ContactList, ConversationList, EMOJI, TalkwireProvider, readersOf, snippet, type ImUiHost } from "../src/index.js";

afterEach(cleanup);

const alice = uidOf("alice");
const bob = uidOf("bob");
const carol = uidOf("carol");
const dave = uidOf("dave");
const group = "grp_listlistlist";
const names: Record<string, string> = { [bob]: "Bob", [carol]: "Carol", [dave]: "Dave" };

function hostWith(contacts = [{ uid: bob, name: "Bob" }, { uid: carol, name: "Carol" }]): ImUiHost {
  return { profiles: async (uids) => uids.map((uid) => ({ uid, name: names[uid] ?? "Someone" })), contacts: new Signal(contacts) };
}

async function setup() {
  const server = new FakeServer();
  let seq = 50;
  server.onFrame = (conn, f) => {
    if (f.action === "pub") {
      conn.push({ action: "pub", code: 200, id: f.id, topic: f.topic, seq: ++seq, ts: Date.now() });
      return true;
    }
    if (f.action === "get_members") {
      conn.push({ action: "get_members", code: 200, id: f.id, topic: f.topic, members: [alice, bob, carol, dave].map((uid, i) => ({ uid, role: i === 0 ? "owner" : "member", joinedAt: i })) });
      return true;
    }
    return false;
  };
  const client = new ImClient({ endpoints: ["ws://fake"], tokenProvider: () => "tok:alice", transport: server, watchNetwork: false });
  const engine = new ChatEngine(client, new MemoryChatStore(), { appUserId: "alice", autoSync: false });
  await engine.open();
  client.start();
  await client.awaitConnected(5_000);
  const push = (frame: Record<string, unknown>) => server.live[0]!.push(frame);
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

describe("conversation list", () => {
  it("pins, mutes and deletes from a row's menu", async () => {
    const { engine, push, close } = await setup();
    await act(async () => {
      push({ action: "data", topic: bob, from: bob, seq: 1, ts: Date.now() - 1000, content: "older", contentType: "text" });
      push({ action: "data", topic: carol, from: carol, seq: 1, ts: Date.now(), content: "newer", contentType: "text" });
      await until("listed", () => engine.conversations.value.length === 2);
    });
    const ui = render(
      <TalkwireProvider engine={engine} host={hostWith()} locale="en">
        <ConversationList onOpen={() => {}} />
      </TalkwireProvider>,
    );
    const order = () => [...ui.container.querySelectorAll("[data-conversation]")].map((e) => e.getAttribute("data-conversation"));
    await until("rows", () => order().length === 2);
    expect(order()).toEqual([carol, bob]);

    const row = (uid: string) => ui.container.querySelector(`[data-conversation="${uid}"]`)!;
    fireEvent.contextMenu(row(bob));
    fireEvent.click(ui.getByRole("menuitem", { name: "Pin" }));
    await until("pinned first", () => order()[0] === bob);
    expect(row(bob).className).toContain("talkwire-list-item-pinned");

    fireEvent.contextMenu(row(bob));
    expect(ui.getByRole("menuitem", { name: "Unpin" })).toBeTruthy();
    fireEvent.click(ui.getByRole("menuitem", { name: "Mute" }));
    await until("muted", () => !!within(row(bob) as HTMLElement).queryByRole("img", { name: "Mute" }));

    fireEvent.contextMenu(row(carol));
    fireEvent.click(ui.getByRole("menuitem", { name: "Delete chat" }));
    expect(ui.getByText("Delete this chat? Its messages are removed from this device only.")).toBeTruthy();
    fireEvent.click(within(ui.getByRole("dialog")).getByRole("button", { name: "Delete chat" }));
    await until("gone", () => order().length === 1);
    expect(engine.conversations.value.map((c) => c.id)).toEqual([bob]);
    ui.unmount();
    close();
  });
});

describe("search", () => {
  it("finds chats by name and stored text messages by their words, newest first", async () => {
    const { engine, push, close } = await setup();
    const now = Date.now();
    await act(async () => {
      await engine.ensureConversation(group, "Lunch club");
      push({ action: "data", topic: bob, from: bob, seq: 1, ts: now - 3000, content: "Lunch at noon?", contentType: "text" });
      push({ action: "data", topic: bob, from: bob, seq: 2, ts: now - 2500, content: "lunch photo", contentType: "image/png", fileUrl: "https://media.example/l.png" });
      push({ action: "data", topic: carol, from: carol, seq: 1, ts: now - 2000, content: "see you at lunch", contentType: "text" });
      push({ action: "data", topic: group, from: dave, seq: 1, ts: now - 1000, content: "the LUNCH is ready", contentType: "text" });
      push({ action: "data", topic: carol, from: carol, seq: 2, ts: now, content: "unrelated", contentType: "text" });
      await until("stored", () => engine.messages(carol).value.length === 2 && engine.messages(group).value.length === 1);
    });
    const opened: string[] = [];
    const ui = render(
      <TalkwireProvider engine={engine} host={hostWith()} locale="en">
        <ConversationList onOpen={(c) => opened.push(c)} />
      </TalkwireProvider>,
    );
    await until("rows", () => ui.container.querySelectorAll("[data-conversation]").length === 3);
    const box = ui.getByRole("searchbox", { name: "Search chats and messages" });
    fireEvent.change(box, { target: { value: "  lunch " } });
    const hits = () => [...ui.container.querySelectorAll("[data-hit]")].map((e) => e.getAttribute("data-hit"));
    await until("hits", () => hits().length === 3);
    expect(hits(), "text only, newest first").toEqual([`${group}:1`, `${carol}:1`, `${bob}:1`]);
    // The group matches by its name too, above the messages.
    const list = ui.getByRole("list", { name: "Search" });
    expect([...list.children].map((e) => e.textContent?.slice(0, 8))).toEqual(["Chats", expect.stringContaining("Lunch"), "Messages", expect.anything(), expect.anything(), expect.anything()]);
    expect(list.querySelector(`[data-conversation="${group}"]`)).toBeTruthy();
    const hit = (key: string) => ui.container.querySelector(`[data-hit="${key}"]`) as HTMLElement;
    expect(hit(`${bob}:1`).querySelector("mark")!.textContent, "the match keeps its case").toBe("Lunch");
    await until("sender named", () => hit(`${group}:1`).textContent!.includes("Dave: the LUNCH is ready"));
    fireEvent.click(hit(`${carol}:1`));
    expect(opened).toEqual([carol]);

    fireEvent.change(box, { target: { value: "zzz" } });
    await until("none", () => !!ui.queryByText("No results"));
    fireEvent.change(box, { target: { value: "" } });
    await until("back to the list", () => ui.container.querySelectorAll("[data-conversation]").length === 3);
    expect(ui.getByRole("list", { name: "Chats" })).toBeTruthy();
    ui.unmount();
    close();
  });

  it("cuts a long lead before the match, and can be turned off", async () => {
    expect(snippet("a\nb c", "B")).toEqual({ before: "a ", match: "b", after: " c" });
    const s = snippet("x".repeat(40) + "needle tail", "needle", 5);
    expect(s).toEqual({ before: "…xxxxx", match: "needle", after: " tail" });
    expect(snippet("abc", "zz").match).toBe("");

    const { engine, close } = await setup();
    const ui = render(
      <TalkwireProvider engine={engine} host={hostWith()} locale="en">
        <ConversationList onOpen={() => {}} searchable={false} />
      </TalkwireProvider>,
    );
    expect(ui.queryByRole("searchbox")).toBeNull();
    ui.unmount();
    close();
  });
});

describe("composer", () => {
  it("puts an emoji in at the caret", async () => {
    const { server, engine, close } = await setup();
    await engine.ensureConversation(bob);
    const ui = render(
      <TalkwireProvider engine={engine} host={hostWith()} locale="en">
        <ChatScreen conversation={bob} />
      </TalkwireProvider>,
    );
    const input = ui.getByLabelText("Message") as HTMLTextAreaElement;
    fireEvent.change(input, { target: { value: "ab" } });
    input.setSelectionRange(1, 1);
    fireEvent.click(ui.getByRole("button", { name: "Emoji" }));
    const panel = ui.getByRole("listbox", { name: "Emoji" });
    expect(within(panel).getAllByRole("option").length).toBe(EMOJI.length);
    fireEvent.click(within(panel).getByRole("option", { name: "👍" }));
    expect(input.value).toBe("a👍b");
    fireEvent.keyDown(input, { key: "Enter" });
    await until("sent", () => server.sent("pub").some((f) => f.content === "a👍b"));
    ui.unmount();
    close();
  });
});

describe("contacts", () => {
  it("searches the host's contacts and opens the chat with one", async () => {
    const { engine, close } = await setup();
    let opened: string | null = null;
    const ui = render(
      <TalkwireProvider engine={engine} host={hostWith()} locale="zh">
        <ContactList onOpen={(c) => (opened = c)} />
      </TalkwireProvider>,
    );
    expect(ui.getByText("Bob")).toBeTruthy();
    fireEvent.change(ui.getByRole("searchbox", { name: "搜索" }), { target: { value: "car" } });
    expect(ui.queryByText("Bob")).toBeNull();
    fireEvent.click(ui.getByText("Carol"));
    await until("opened", () => opened === carol);
    expect(engine.conversations.value.some((c) => c.id === carol)).toBe(true);
    fireEvent.change(ui.getByRole("searchbox", { name: "搜索" }), { target: { value: "nobody" } });
    expect(ui.getByText("暂无联系人")).toBeTruthy();
    ui.unmount();
    close();
  });
});

describe("read by", () => {
  it("counts the members who read each own message, not the sender", () => {
    const m = { seq: 5, from: alice } as ChatMessage;
    const cursors = new Map([[bob, 5], [carol, 4], [alice, 9]]);
    expect(readersOf(m, cursors, [alice, bob, carol, dave])).toEqual([bob]);
    expect(readersOf({ ...m, seq: null }, cursors, [alice, bob])).toEqual([]);
    expect(readersOf(m, new Map(), [alice, bob])).toEqual([]);
  });

  it("shows who read an own message in a group and lists them", async () => {
    const { engine, push, close } = await setup();
    await engine.ensureConversation(group, "team");
    const ui = render(
      <TalkwireProvider engine={engine} host={hostWith()} locale="en">
        <ChatScreen conversation={group} />
      </TalkwireProvider>,
    );
    const input = ui.getByLabelText("Message");
    fireEvent.change(input, { target: { value: "who reads this" } });
    fireEvent.keyDown(input, { key: "Enter" });
    await until("stored", () => engine.messages(group).value.some((x) => x.content === "who reads this" && x.seq !== null));
    const seq = engine.messages(group).value.find((x) => x.content === "who reads this")!.seq!;
    await act(async () => {
      push({ action: "info", topic: group, from: bob, what: "read", seq });
      push({ action: "info", topic: group, from: carol, what: "read", seq });
      await until("cursors", () => engine.readCursors(group).value.size === 2);
    });
    await until("receipt", () => !!ui.queryByText("Read by 2"));
    fireEvent.click(ui.getByText("Read by 2"));
    const dialog = ui.getByRole("dialog", { name: "Read by 2" });
    await until("names", () => !!within(dialog).queryByText("Carol"));
    expect(within(dialog).getByText("Bob")).toBeTruthy();
    expect(within(dialog).getByText("3 members · 1 haven't read it")).toBeTruthy();
    ui.unmount();
    close();
  });
});

describe("new messages line", () => {
  it("marks what was unread when the chat opened, not what arrives while it is open", async () => {
    const { engine, push, close } = await setup();
    await act(async () => {
      push({ action: "data", topic: bob, from: bob, seq: 1, ts: Date.now(), content: "before", contentType: "text" });
      await until("stored", () => engine.messages(bob).value.length === 1);
    });
    await engine.markRead(bob);
    const quiet = render(
      <TalkwireProvider engine={engine} host={hostWith()} locale="en">
        <ChatScreen conversation={bob} />
      </TalkwireProvider>,
    );
    await act(async () => {
      push({ action: "data", topic: bob, from: bob, seq: 2, ts: Date.now(), content: "while looking", contentType: "text" });
      await until("stored", () => engine.messages(bob).value.length === 2);
    });
    await until("shown", () => !!quiet.queryByText("while looking"));
    expect(quiet.queryByText("New messages"), "nothing was unread at opening").toBeNull();
    quiet.unmount();

    await act(async () => {
      push({ action: "data", topic: bob, from: bob, seq: 3, ts: Date.now(), content: "unread one", contentType: "text" });
      await until("unread", () => engine.conversations.value.find((c) => c.id === bob)?.unread === 1);
    });
    const busy = render(
      <TalkwireProvider engine={engine} host={hostWith()} locale="en">
        <ChatScreen conversation={bob} />
      </TalkwireProvider>,
    );
    await until("line", () => !!busy.queryByText("New messages"));
    await act(async () => {
      push({ action: "data", topic: bob, from: bob, seq: 4, ts: Date.now(), content: "later", contentType: "text" });
      await until("stored", () => engine.messages(bob).value.length === 4);
    });
    await until("shown", () => !!busy.queryByText("later"));
    // The line stays above the message that was unread at opening.
    const rows = [...busy.container.querySelectorAll(".talkwire-messages > *")].map((e) => e.textContent);
    expect(rows.indexOf("New messages")).toBe(rows.findIndex((r) => r?.includes("unread one")) - 1);
    busy.unmount();
    close();
  });
});
