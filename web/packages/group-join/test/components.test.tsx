// @vitest-environment jsdom
import { ChatEngine, ImClient, MemoryChatStore } from "@talkwire/client";
import { GroupJoinProvider, GroupQrCard, JoinApprovalSetting, JoinGroupScreen, JoinRequestsPanel } from "@talkwire/group-join/react";
import { TalkwireProvider, type ImUiHost } from "@talkwire/react";
import { cleanup, fireEvent, render } from "@testing-library/react";
import { StrictMode, type ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { FakeServer, type Frame } from "../../client/test/fake-server.js";
import { until } from "../../client/test/until.js";

const host: ImUiHost = { profiles: async () => [] };
const group = "grp_scan00000001";
const stops: (() => void)[] = [];
afterEach(() => {
  cleanup();
  for (const s of stops.splice(0)) s();
  vi.restoreAllMocks();
});

async function setup(server: FakeServer) {
  const client = new ImClient({ endpoints: ["ws://fake"], tokenProvider: () => "tok:alice", transport: server, watchNetwork: false });
  const engine = new ChatEngine(client, new MemoryChatStore(), { appUserId: "alice", autoSync: false });
  stops.push(() => {
    client.stop();
    engine.close();
  });
  await engine.open();
  client.start();
  await client.awaitConnected(5_000);
  const wrap = (node: ReactNode) => (
    <TalkwireProvider engine={engine} host={host} locale="zh">
      <GroupJoinProvider>{node}</GroupJoinProvider>
    </TalkwireProvider>
  );
  return { engine, wrap };
}

function answering(handlers: Record<string, (f: Frame) => Frame>): FakeServer {
  const server = new FakeServer();
  server.onFrame = (conn, f) => {
    const h = handlers[String(f.action)];
    if (!h) return false;
    conn.push({ action: f.action, id: f.id, ...h(f) });
    return true;
  };
  return server;
}

describe("scan-to-join components", () => {
  it("joins an open group, then subscribes, lists it and offers to open it", async () => {
    const server = answering({
      group_qr_resolve: () => ({ code: 200, topic: group, name: "徒步群", memberCount: 3 }),
      group_join: () => ({ code: 200, topic: group, joinStatus: "joined" }),
      get_groups: () => ({ code: 200, groups: [{ topic: group, name: "徒步群", owner: "uid_x", members: 4 }] }),
    });
    const { engine, wrap } = await setup(server);
    const opened: string[] = [];
    const ui = render(wrap(<JoinGroupScreen qrToken="qr-1" onOpen={(t) => opened.push(t)} />));
    await until("preview", () => !!ui.queryByText("徒步群"));
    expect(ui.getByText("3 人")).toBeTruthy();
    expect(ui.queryByLabelText("给管理员的留言（可选）"), "no message without approval").toBeNull();
    fireEvent.click(ui.getByText("加入群聊"));
    await until("joined", () => !!ui.queryByText("进入群聊"));
    expect(ui.getByText("4 人"), "the preview's count was taken before I joined").toBeTruthy();
    expect(server.sent("group_join").map((f) => f.joinMessage)).toEqual([undefined]);
    expect(server.sent("sub").map((f) => f.topic)).toEqual([group]);
    expect(engine.conversation(group)?.title).toBe("徒步群");
    fireEvent.click(ui.getByText("进入群聊"));
    expect(opened).toEqual([group]);
  });

  it("asks to join with a message when approval is needed, and says so", async () => {
    const server = answering({
      group_qr_resolve: () => ({ code: 200, topic: group, name: "徒步群", memberCount: 3, requireApproval: true }),
      group_join: () => ({ code: 200, topic: group, joinStatus: "pending_approval" }),
    });
    const { wrap } = await setup(server);
    const ui = render(wrap(<JoinGroupScreen qrToken="qr-1" />));
    await until("preview", () => !!ui.queryByText("申请加入"));
    expect(ui.getByText("需要群管理员同意才能加入")).toBeTruthy();
    const long = "我".repeat(80);
    fireEvent.change(ui.getByLabelText("给管理员的留言（可选）"), { target: { value: long } });
    fireEvent.click(ui.getByText("申请加入"));
    await until("pending", () => !!ui.queryByText("申请已发送，管理员处理后会通知你"));
    expect(server.sent("group_join")[0]!.joinMessage).toBe("我".repeat(60)); // the server's limit, in code points
    expect(server.sent("sub"), "not a member yet").toHaveLength(0);
  });

  // Found on a phone: after the admin decided, the page still said the request was sent.
  it("follows the admin's decision on a pending request", async () => {
    const server = answering({
      group_qr_resolve: () => ({ code: 200, topic: group, name: "徒步群", memberCount: 3, requireApproval: true }),
      group_join: () => ({ code: 200, topic: group, joinStatus: "pending_approval" }),
      get_groups: () => ({ code: 200, groups: [{ topic: group, name: "徒步群", owner: "uid_x", members: 4 }] }),
    });
    const { engine, wrap } = await setup(server);
    const opened: string[] = [];
    const ui = render(<StrictMode>{wrap(<JoinGroupScreen qrToken="qr-1" onOpen={(t) => opened.push(t)} />)}</StrictMode>);
    await until("preview", () => !!ui.queryByText("申请加入"));
    fireEvent.click(ui.getByText("申请加入"));
    await until("pending", () => !!ui.queryByText("申请已发送，管理员处理后会通知你"));

    // A decision on another group changes nothing.
    server.live[0]!.push({ action: "group_join_reviewed", topic: "grp_other000000001", name: "别的群", joinStatus: "approved" });
    await new Promise((r) => setTimeout(r, 50));
    expect(ui.getByText("申请已发送，管理员处理后会通知你")).toBeTruthy();

    server.live[0]!.push({ action: "group_join_reviewed", topic: group, name: "徒步群", joinStatus: "approved" });
    await until("approved", () => !!ui.queryByText("你的入群申请已通过，欢迎加入「徒步群」"));
    expect(ui.getByText("4 人")).toBeTruthy();
    await until("subscribed", () => server.sent("sub").some((f) => f.topic === group));
    expect(engine.conversation(group)).toBeTruthy();
    fireEvent.click(ui.getByText("进入群聊"));
    expect(opened).toEqual([group]);
  });

  it("says when a pending request was declined", async () => {
    const server = answering({
      group_qr_resolve: () => ({ code: 200, topic: group, name: "徒步群", memberCount: 3, requireApproval: true }),
      group_join: () => ({ code: 200, topic: group, joinStatus: "pending_approval" }),
    });
    const { wrap } = await setup(server);
    const ui = render(wrap(<JoinGroupScreen qrToken="qr-1" />));
    await until("preview", () => !!ui.queryByText("申请加入"));
    fireEvent.click(ui.getByText("申请加入"));
    await until("pending", () => !!ui.queryByText("申请已发送，管理员处理后会通知你"));
    server.live[0]!.push({ action: "group_join_reviewed", topic: group, name: "徒步群", joinStatus: "rejected" });
    await until("declined", () => !!ui.queryByText("你申请加入「徒步群」被拒绝"));
    expect(ui.queryByText("进入群聊")).toBeNull();
    expect(server.sent("sub")).toHaveLength(0);
  });

  it("shows an expired code and a full group", async () => {
    const expired = answering({ group_qr_resolve: () => ({ code: 404, reason: "二维码不存在或已过期" }) });
    const one = await setup(expired);
    const ui = render(one.wrap(<JoinGroupScreen qrToken="old" />));
    await until("invalid", () => !!ui.queryByText("二维码不存在或已过期"));
    cleanup();

    const full = answering({ group_qr_resolve: () => ({ code: 200, topic: group, name: "满员群", memberCount: 500, isFull: true }) });
    const two = await setup(full);
    const ui2 = render(two.wrap(<JoinGroupScreen qrToken="qr-1" />));
    await until("full", () => !!ui2.queryByText("群成员数量已达上限"));
    expect(ui2.queryByText("加入群聊"), "no join button").toBeNull();
  });

  it("lists requests, reviews them, and keeps one a full group could not take (strict)", async () => {
    let pending: Frame[] = [
      { id: 1, requesterUid: "uid_e", requesterName: "Eve", message: "我是同事", createdAt: 1 },
      { id: 2, requesterUid: "uid_f", requesterName: "Fay", createdAt: 2 },
    ];
    const server = answering({
      group_join_requests_list: () => ({ code: 200, topic: group, ...(pending.length ? { joinRequests: pending } : {}) }),
      group_join_review: (f) => {
        if (f.joinRequestId === 2) return { code: 409, reason: "群成员数量已达上限", errCode: "group_full" };
        pending = pending.filter((r) => r.id !== f.joinRequestId);
        return { code: 200, topic: group, joinStatus: f.approve ? "approved" : "rejected" };
      },
    });
    const { engine, wrap } = await setup(server);
    const changed: string[] = [];
    stops.push(engine.events.on((e) => e.kind === "groupChanged" && changed.push(e.conversation)));
    const ui = render(wrap(<JoinRequestsPanel topic={group} />));
    await until("listed", () => !!ui.queryByText("Eve"));
    expect(ui.getByText("我是同事")).toBeTruthy();
    const row = (id: number) => ui.container.querySelector(`[data-join-request="${id}"]`) as HTMLElement;
    fireEvent.click(row(1).querySelector("button.talkwire-send")!);
    await until("approved", () => !ui.queryByText("Eve"));
    // The server tells only the applicant (F45): the panel tells this tab's engine, so the member list reloads.
    expect(changed).toEqual([group]);
    fireEvent.click(row(2).querySelector("button.talkwire-send")!);
    await until("refused", () => !!ui.queryByText("群成员数量已达上限"));
    expect(ui.getByText("Fay"), "still pending on a strict server").toBeTruthy();

    // A new request for this group reloads the list.
    pending = [...pending, { id: 3, requesterUid: "uid_g", requesterName: "Gus", createdAt: 3 }];
    server.live[0]!.push({ action: "group_join_request", topic: group, name: "徒步群", from: "uid_g" });
    await until("reloaded", () => !!ui.queryByText("Gus"));
  });

  it("still hears pushes under React StrictMode, which mounts effects twice", async () => {
    let pending: Frame[] = [];
    const server = answering({ group_join_requests_list: () => ({ code: 200, topic: group, ...(pending.length ? { joinRequests: pending } : {}) }) });
    const { wrap } = await setup(server);
    const ui = render(<StrictMode>{wrap(<JoinRequestsPanel topic={group} />)}</StrictMode>);
    await until("empty list", () => !!ui.queryByText("暂无待处理的申请"));
    pending = [{ id: 1, requesterUid: "uid_g", requesterName: "Gus", createdAt: 1 }];
    server.live[0]!.push({ action: "group_join_request", topic: group, name: "徒步群", from: "uid_g" });
    await until("the push reloads the list", () => !!ui.queryByText("Gus"));
  });

  it("drops a request another admin handled", async () => {
    let listed = 0;
    const server = answering({
      group_join_requests_list: () => (listed++ === 0 ? { code: 200, topic: group, joinRequests: [{ id: 1, requesterUid: "uid_e", requesterName: "Eve", createdAt: 1 }] } : { code: 200, topic: group }),
      group_join_review: () => ({ code: 409, reason: "该申请已被处理", errCode: "join_request_handled" }),
    });
    const { wrap } = await setup(server);
    const ui = render(wrap(<JoinRequestsPanel topic={group} />));
    await until("listed", () => !!ui.queryByText("Eve"));
    fireEvent.click(ui.getByText("拒绝"));
    await until("dropped", () => !ui.queryByText("Eve"));
    expect(ui.getByText("该申请已被其他管理员处理")).toBeTruthy();
    expect(ui.getByText("暂无待处理的申请")).toBeTruthy();
  });

  it("shows the share link and lets a manager reset the code", async () => {
    let n = 0;
    const server = answering({ group_qr_token: (f) => ({ code: 200, topic: f.topic, qrToken: f.refreshQr ? `qr-${++n + 1}` : "qr-1", expiresAt: Date.UTC(2030, 0, 1) }) });
    const { wrap } = await setup(server);
    vi.spyOn(window, "confirm").mockReturnValue(true);
    const ui = render(wrap(<GroupQrCard topic={group} canRefresh linkFor={(t) => `https://chat.example/join?t=${t}`} renderQr={(v) => <img alt="qr" data-value={v} />} />));
    await until("link", () => !!ui.queryByText("https://chat.example/join?t=qr-1"));
    expect(ui.getByAltText("qr").getAttribute("data-value")).toBe("https://chat.example/join?t=qr-1");
    fireEvent.click(ui.getByText("重置二维码"));
    await until("reset", () => !!ui.queryByText("https://chat.example/join?t=qr-2"));
    expect(server.sent("group_qr_token").map((f) => f.refreshQr ?? false)).toEqual([false, true]);
  });

  it("switches approval on for the owner only", async () => {
    const server = answering({
      get_group_join_settings: (f) => ({ code: 200, topic: f.topic }),
      update_group_join_settings: (f) => ({ code: 200, topic: f.topic, requireApproval: f.requireApproval }),
    });
    const { wrap } = await setup(server);
    const ui = render(wrap(<JoinApprovalSetting topic={group} editable />));
    const box = () => ui.getByLabelText("通过二维码入群需管理员同意") as HTMLInputElement;
    await until("loaded", () => !box().disabled);
    expect(box().checked).toBe(false);
    fireEvent.click(box());
    await until("on", () => box().checked);
    expect(server.sent("update_group_join_settings")[0]).toMatchObject({ topic: group, requireApproval: true });
    cleanup();
    const ro = render(wrap(<JoinApprovalSetting topic={group} />));
    await until("loaded again", () => server.sent("get_group_join_settings").length === 2);
    expect((ro.getByLabelText("通过二维码入群需管理员同意") as HTMLInputElement).disabled).toBe(true);
  });
});
