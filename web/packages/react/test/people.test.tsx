// @vitest-environment jsdom
import { ChatEngine, ImClient, MemoryChatStore, Signal, type GroupMember, type GroupRole } from "@imkit/client";
import { uidOf } from "@imkit/protocol";
import { act, cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { FakeServer } from "../../client/test/fake-server.js";
import { until } from "../../client/test/until.js";
import { ChatScreen, ConversationInfo, ImKitProvider, PickPeople, defaultGroupName, memberPermissions, sortMembers, type ImProfile, type ImUiHost } from "../src/index.js";

afterEach(cleanup);

const alice = uidOf("alice");
const bob = uidOf("bob");
const carol = uidOf("carol");
const dave = uidOf("dave");
const names: Record<string, string> = { [alice]: "Alice", [bob]: "Bob", [carol]: "Carol", [dave]: "Dave" };
const group = "grp_peoplepeople";

function hostWith(contacts: ImProfile[] = []): ImUiHost {
  return {
    profiles: async (uids) => uids.map((uid) => ({ uid, name: names[uid] ?? "Someone" })),
    contacts: new Signal(contacts),
  };
}

/** A server whose group has `roster`; it records every request and answers `refuse` actions with that code. */
async function setup(roster: { uid: string; role: GroupRole }[], refuse: Record<string, number> = {}) {
  const server = new FakeServer();
  let members: GroupMember[] = roster.map((m, i) => ({ ...m, joinedAt: i + 1 }));
  server.onFrame = (conn, f) => {
    const code = refuse[String(f.action)];
    if (code) {
      conn.push({ action: f.action, code, id: f.id, reason: "no" });
      return true;
    }
    if (f.action === "get_members") {
      conn.push({ action: "get_members", code: 200, id: f.id, topic: f.topic, members: members.map((m) => ({ uid: m.uid, role: m.role, joinedAt: m.joinedAt })) });
      return true;
    }
    if (f.action === "get_groups") {
      conn.push({ action: "get_groups", code: 200, id: f.id, groups: members.some((m) => m.uid === alice) ? [{ topic: group, name: "team", owner: alice, members: members.length }] : [] });
      return true;
    }
    if (f.action === "set_member_role") {
      members = members.map((m) => (m.uid === f.user ? { ...m, role: f.memberRole as GroupRole } : m));
    }
    if (f.action === "leave_grp") members = members.filter((m) => m.uid !== alice);
    if (f.action === "create_grp") {
      conn.push({ action: "create_grp", code: 200, id: f.id, topic: "grp_newnewnewnew" });
      return true;
    }
    return false;
  };
  const client = new ImClient({ endpoints: ["ws://fake"], tokenProvider: () => "tok:alice", transport: server, watchNetwork: false });
  const engine = new ChatEngine(client, new MemoryChatStore(), { appUserId: "alice", autoSync: false });
  await engine.open();
  await engine.ensureConversation(group, "team");
  client.start();
  await client.awaitConnected(5_000);
  return {
    server,
    engine,
    close() {
      client.stop();
      engine.close();
    },
  };
}

describe("member permissions", () => {
  it("mirrors the server's checks", () => {
    const m = (uid: string, role: GroupRole): GroupMember => ({ uid, role, joinedAt: 0 });
    expect(memberPermissions("owner", alice, m(bob, "member"))).toEqual({ setRole: true, kick: true, transfer: true });
    expect(memberPermissions("owner", alice, m(bob, "admin"))).toEqual({ setRole: true, kick: true, transfer: true });
    expect(memberPermissions("admin", alice, m(bob, "member"))).toEqual({ setRole: false, kick: true, transfer: false });
    expect(memberPermissions("admin", alice, m(bob, "admin")), "admins do not remove admins").toEqual({ setRole: false, kick: false, transfer: false });
    expect(memberPermissions("member", alice, m(bob, "member"))).toEqual({ setRole: false, kick: false, transfer: false });
    expect(memberPermissions("owner", alice, m(alice, "owner")), "not on oneself").toEqual({ setRole: false, kick: false, transfer: false });
    expect(memberPermissions("admin", alice, m(bob, "owner")), "never on the owner").toEqual({ setRole: false, kick: false, transfer: false });
  });

  it("sorts owners, then admins, then members by joining time", () => {
    const sorted = sortMembers([
      { uid: "c", role: "member", joinedAt: 3 },
      { uid: "a", role: "admin", joinedAt: 5 },
      { uid: "m", role: "member", joinedAt: 1 },
      { uid: "o", role: "owner", joinedAt: 9 },
    ]);
    expect(sorted.map((m) => m.uid)).toEqual(["o", "a", "m", "c"]);
  });

  it("names a new group after its members, cut to fit", () => {
    expect(defaultGroupName(["Bob", "Carol"])).toBe("Bob, Carol");
    const long = defaultGroupName(["Alexandria", "Bartholomew", "Christopher", "Dominique"]);
    expect(long.length).toBeLessThanOrEqual(30);
    expect(long.endsWith("…")).toBe(true);
  });
});

describe("conversation info", () => {
  it("gives the owner the member actions and sends what is picked", async () => {
    const { server, engine, close } = await setup([
      { uid: alice, role: "owner" },
      { uid: bob, role: "member" },
      { uid: carol, role: "admin" },
    ]);
    const ui = render(
      <ImKitProvider engine={engine} host={hostWith()} locale="en">
        <ConversationInfo conversation={group} groupExtras={(topic, role) => <div>extras:{topic}:{role}</div>} />
      </ImKitProvider>,
    );
    await until("roster", () => !!ui.queryByText("Members (3)"));
    await until("names", () => !!ui.queryByText("Bob"));
    // Owner first, then the admin, then members.
    expect([...ui.container.querySelectorAll("[data-member]")].map((e) => e.getAttribute("data-member"))).toEqual([alice, carol, bob]);
    expect(ui.getByText("You")).toBeTruthy();
    expect(ui.getByText("Owner")).toBeTruthy();
    expect(ui.getByText("Admin")).toBeTruthy();
    expect(ui.getByText(`extras:${group}:owner`)).toBeTruthy();
    expect(ui.getByText("Rename group")).toBeTruthy();
    expect(ui.getByText("Disband group")).toBeTruthy();
    expect(ui.queryByText("Leave group")).toBeNull();
    expect(ui.getByText("Clear chat history")).toBeTruthy();
    // My own row is not a button.
    expect(ui.container.querySelector(`[data-member="${alice}"]`)?.tagName).toBe("DIV");

    fireEvent.click(ui.container.querySelector(`[data-member="${bob}"]`)!);
    expect(ui.getByRole("menuitem", { name: "Make admin" })).toBeTruthy();
    expect(ui.getByRole("menuitem", { name: "Make owner" })).toBeTruthy();
    fireEvent.click(ui.getByRole("menuitem", { name: "Make admin" }));
    await until("sent", () => server.sent("set_member_role").length === 1);
    expect(server.sent("set_member_role")[0]).toMatchObject({ topic: group, user: bob, memberRole: "admin" });
    await until("reloaded", () => ui.getAllByText("Admin").length === 2);

    fireEvent.click(ui.container.querySelector(`[data-member="${carol}"]`)!);
    fireEvent.click(ui.getByRole("menuitem", { name: "Remove from group" }));
    expect(ui.getByText("Remove Carol from the group?")).toBeTruthy();
    fireEvent.click(within(ui.getByRole("dialog")).getByRole("button", { name: "Remove from group" }));
    await until("kick", () => server.sent("kick").length === 1);
    expect(server.sent("kick")[0]).toMatchObject({ topic: group, user: carol });
    ui.unmount();
    close();
  });

  it("keeps a member to what members may do", async () => {
    const { server, engine, close } = await setup([
      { uid: bob, role: "owner" },
      { uid: alice, role: "member" },
      { uid: carol, role: "member" },
    ]);
    let left = false;
    const ui = render(
      <ImKitProvider engine={engine} host={hostWith()} locale="zh">
        <ConversationInfo conversation={group} onLeft={() => (left = true)} />
      </ImKitProvider>,
    );
    await until("roster", () => !!ui.queryByText("群成员（3）"));
    // Members cannot open each other: no row is a button.
    expect(ui.container.querySelectorAll("button[data-member]").length).toBe(0);
    expect(ui.queryByText("修改群名")).toBeNull();
    expect(ui.queryByText("清空聊天记录"), "only managers clear a group for everyone").toBeNull();
    expect(ui.queryByText("解散群聊")).toBeNull();
    fireEvent.click(ui.getByText("退出群聊"));
    fireEvent.click(within(ui.getByRole("dialog")).getByRole("button", { name: "退出群聊" }));
    await until("left", () => left);
    expect(server.sent("leave_grp")[0]).toMatchObject({ topic: group });
    ui.unmount();
    close();
  });

  it("stops at the admin limit, before and after asking the server", async () => {
    const { server, engine, close } = await setup(
      [
        { uid: alice, role: "owner" },
        { uid: bob, role: "admin" },
        { uid: carol, role: "member" },
        { uid: dave, role: "member" },
      ],
      { set_member_role: 409 },
    );
    const ui = render(
      <ImKitProvider engine={engine} host={hostWith()} locale="en">
        <ConversationInfo conversation={group} maxAdmins={1} />
      </ImKitProvider>,
    );
    await until("roster", () => !!ui.container.querySelector(`[data-member="${carol}"]`));
    fireEvent.click(ui.container.querySelector(`[data-member="${carol}"]`)!);
    fireEvent.click(ui.getByRole("menuitem", { name: "Make admin" }));
    expect(ui.getByRole("alert").textContent).toBe("This group already has as many admins as it can have");
    expect(server.sent("set_member_role").length, "not asked: the limit is known").toBe(0);
    ui.unmount();

    // A limit the client did not know: the server's 409 says the same.
    const again = render(
      <ImKitProvider engine={engine} host={hostWith()} locale="en">
        <ConversationInfo conversation={group} maxAdmins={5} />
      </ImKitProvider>,
    );
    await until("roster", () => !!again.container.querySelector(`[data-member="${dave}"]`));
    fireEvent.click(again.container.querySelector(`[data-member="${dave}"]`)!);
    fireEvent.click(again.getByRole("menuitem", { name: "Make admin" }));
    await until("refused", () => !!again.queryByRole("alert"));
    expect(again.getByRole("alert").textContent).toBe("This group already has as many admins as it can have");
    again.unmount();
    close();
  });

  it("shows pin and mute for a direct chat, and clear", async () => {
    const { engine, close } = await setup([{ uid: alice, role: "owner" }]);
    await engine.ensureConversation(bob);
    const ui = render(
      <ImKitProvider engine={engine} host={hostWith()} locale="en">
        <ConversationInfo conversation={bob} />
      </ImKitProvider>,
    );
    await until("name", () => !!ui.queryByText("Bob"));
    expect(ui.getByText("Chat info")).toBeTruthy();
    expect(ui.queryByText(/Members/)).toBeNull();
    const pin = ui.getByRole("switch", { name: "Pin chat" }) as HTMLInputElement;
    expect(pin.checked).toBe(false);
    await act(async () => {
      fireEvent.click(pin);
      await until("pinned", () => engine.conversations.value.find((c) => c.id === bob)?.pinned === true);
    });
    expect((ui.getByRole("switch", { name: "Pin chat" }) as HTMLInputElement).checked).toBe(true);
    expect(ui.getByText("Clear chat history")).toBeTruthy();
    ui.unmount();
    close();
  });

  it("puts an info button in the chat header only when asked", async () => {
    const { engine, close } = await setup([{ uid: alice, role: "owner" }]);
    let opened = 0;
    const ui = render(
      <ImKitProvider engine={engine} host={hostWith()} locale="en">
        <ChatScreen conversation={group} onOpenInfo={() => opened++} />
      </ImKitProvider>,
    );
    fireEvent.click(ui.getByRole("button", { name: "Group info" }));
    expect(opened).toBe(1);
    ui.unmount();
    const plain = render(
      <ImKitProvider engine={engine} host={hostWith()} locale="en">
        <ChatScreen conversation={group} />
      </ImKitProvider>,
    );
    expect(plain.queryByRole("button", { name: "Group info" })).toBeNull();
    plain.unmount();
    close();
  });
});

describe("picking people", () => {
  const contacts: ImProfile[] = [
    { uid: bob, name: "Bob" },
    { uid: carol, name: "Carol" },
    { uid: dave, name: "Dave" },
  ];

  it("adds the picked contacts to a group, not the ones already in it", async () => {
    const { server, engine, close } = await setup([
      { uid: alice, role: "owner" },
      { uid: bob, role: "member" },
    ]);
    let done: string | null = null;
    const ui = render(
      <ImKitProvider engine={engine} host={hostWith(contacts)} locale="en">
        <PickPeople topic={group} onDone={(c) => (done = c)} />
      </ImKitProvider>,
    );
    await until("existing", () => (ui.container.querySelector(`[data-contact="${bob}"] input`) as HTMLInputElement | null)?.disabled === true);
    fireEvent.click(ui.container.querySelector(`[data-contact="${carol}"] input`)!);
    fireEvent.click(ui.container.querySelector(`[data-contact="${dave}"] input`)!);
    fireEvent.click(ui.getByRole("button", { name: "Add (2)" }));
    await until("done", () => done === group);
    expect(server.sent("invite").map((f) => f.user).sort()).toEqual([carol, dave].sort());
    ui.unmount();
    close();
  });

  it("filters by name and starts a new group named after its members", async () => {
    const { server, engine, close } = await setup([{ uid: alice, role: "owner" }]);
    let done: string | null = null;
    const ui = render(
      <ImKitProvider engine={engine} host={hostWith(contacts)} locale="zh">
        <PickPeople onDone={(c) => (done = c)} />
      </ImKitProvider>,
    );
    fireEvent.change(ui.getByRole("searchbox", { name: "搜索" }), { target: { value: "ca" } });
    expect(ui.queryByText("Bob")).toBeNull();
    fireEvent.click(ui.container.querySelector(`[data-contact="${carol}"] input`)!);
    fireEvent.change(ui.getByRole("searchbox", { name: "搜索" }), { target: { value: "" } });
    fireEvent.click(ui.container.querySelector(`[data-contact="${bob}"] input`)!);
    fireEvent.click(ui.getByRole("button", { name: "创建（2）" }));
    await until("created", () => done === "grp_newnewnewnew");
    const f = server.sent("create_grp")[0]!;
    expect(f.name).toBe("Bob, Carol");
    expect([...(f.members as string[])].sort()).toEqual([bob, carol].sort());
    expect(screen.queryByRole("alert")).toBeNull();
    ui.unmount();
    close();
  });
});
