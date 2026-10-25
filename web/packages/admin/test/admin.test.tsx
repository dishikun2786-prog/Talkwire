// @vitest-environment jsdom
import {
  CallDetail,
  CallRecords,
  ChatRecords,
  ClearTopicDialog,
  ImAdminApi,
  ImAdminError,
  ImAdminProvider,
  adminStringsFor,
  clearConfirmation,
  formatDuration,
  imAdminPages,
  matchImAdminPage,
  safeLink,
  type AdminRole,
  type CallRecord,
  type Person,
} from "@imkit/admin";
import { cleanup, fireEvent, render, waitFor, within } from "@testing-library/react";
import { StrictMode, type ReactNode } from "react";
import { afterEach, describe, expect, it } from "vitest";

afterEach(cleanup);

interface Req {
  method: string;
  path: string;
  query: Record<string, string>;
  body: Record<string, unknown> | undefined;
  headers: Record<string, string>;
}
type Answer = { status: number; body?: unknown } | "no answer";

const BASE = "/api/v1/admin/im";

/** A fetch that answers from a function and remembers every request. */
function fakeServer(route: (r: Req) => Answer) {
  const requests: Req[] = [];
  const fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(String(input), "http://admin.test");
    const req: Req = {
      method: init?.method ?? "GET",
      path: url.pathname.slice(BASE.length),
      query: Object.fromEntries(url.searchParams),
      body: typeof init?.body === "string" ? (JSON.parse(init.body) as Record<string, unknown>) : undefined,
      headers: (init?.headers ?? {}) as Record<string, string>,
    };
    requests.push(req);
    const a = route(req);
    if (a === "no answer") throw new TypeError("Failed to fetch");
    const text = a.body === undefined ? "" : typeof a.body === "string" ? a.body : JSON.stringify(a.body);
    return { ok: a.status >= 200 && a.status < 300, status: a.status, text: async () => text } as Response;
  }) as typeof globalThis.fetch;
  return { fetch, requests, api: new ImAdminApi({ fetch, headers: () => ({ authorization: "Bearer admin-token" }) }) };
}

function page(api: ImAdminApi, role: AdminRole, node: ReactNode) {
  return render(
    <StrictMode>
      <ImAdminProvider api={api} role={role} locale="zh">
        {node}
      </ImAdminProvider>
    </StrictMode>,
  );
}

const person = (userId: string, displayName: string | null): Person => ({ uid: `uid_${userId}`, userId, displayName });
const alice = person("alice", "Alice");
const bob = person("bob", "Bob");
const P2P = "p2p_alice_bob";

const topics = {
  uid: "uid_alice",
  topics: [
    { topic: P2P, isGroup: false, lastSeq: 5, messageCount: 5, lastMessageAt: 1_700_000_000_000, members: [alice, bob], clearVersion: 0, clearBeforeSeq: 0 },
    { topic: "grp_team", isGroup: true, name: "Team", lastSeq: 9, messageCount: 9, members: [alice, bob], clearVersion: 0, clearBeforeSeq: 0 },
  ],
};

const msg = (seq: number, extra: Record<string, unknown>) => ({ topic: P2P, seq, from: bob.uid, sender: bob, ts: 1_700_000_000_000 + seq, content: "", contentType: "text", ...extra });

function chatServer(clear: (r: Req) => Answer = () => ({ status: 404 })) {
  return fakeServer((r) => {
    if (r.path === "/users/alice/topics") return { status: 200, body: topics };
    if (r.path === `/topics/${P2P}/messages`) {
      if (r.query["beforeSeq"] === "3") return { status: 200, body: { hasMore: false, messages: [msg(2, { content: "older one" })] } };
      if (r.query["type"] === "image") return { status: 200, body: { hasMore: false, messages: [msg(4, { contentType: "image/png", fileUrl: "/media/a.png", fileName: "a.png" })] } };
      return {
        status: 200,
        body: {
          hasMore: true,
          messages: [
            msg(5, { content: "hello" }),
            msg(4, { contentType: "image/png", fileUrl: "javascript:alert(1)", fileName: "evil.png" }),
            msg(3, { contentType: "audio/mp4", fileUrl: "https://cdn.example/v.m4a", fileName: "voice.m4a", duration: 7 }),
          ],
        },
      };
    }
    if (r.path === "/topics/grp_team/messages") return { status: 200, body: { hasMore: false, messages: [] } };
    if (r.path === `/topics/${P2P}/clear`) return clear(r);
    return { status: 404, body: { error: "not found" } };
  });
}

async function openConversation(ui: ReturnType<typeof render>, name: RegExp) {
  fireEvent.change(ui.getByLabelText("用户 ID"), { target: { value: " alice " } });
  fireEvent.click(ui.getByRole("button", { name: "查询" }));
  fireEvent.click(await ui.findByRole("button", { name }));
}

describe("the API client", () => {
  it("sends the administrator's headers, drops empty filters, and reports refusals", async () => {
    const { api, requests } = fakeServer((r) =>
      r.path === "/calls" ? { status: 200, body: { calls: [], nextCursor: null } } : r.path.startsWith("/calls/") ? { status: 404, body: "404 page not found" } : { status: 409, body: { error: "this clear failed", auditId: "a-1" } },
    );
    await api.calls({ userId: "alice", status: undefined, limit: 5 });
    expect(requests[0]).toMatchObject({ method: "GET", path: "/calls", query: { userId: "alice", limit: "5" }, headers: { authorization: "Bearer admin-token" } });
    await expect(api.call("nope")).rejects.toMatchObject({ status: 404, message: "404 page not found" });
    const err = await api.clear(P2P, { confirm: clearConfirmation(P2P), reason: "x", idempotencyKey: "key-12345678" }).catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ImAdminError);
    expect(err).toMatchObject({ status: 409, message: "this clear failed", auditId: "a-1" });
    expect(requests.at(-1)).toMatchObject({ method: "POST", path: `/topics/${P2P}/clear`, headers: { "content-type": "application/json" } });
  });
});

describe("chat records", () => {
  it("finds a user's conversations, reads one with filters and older pages; an admin cannot clear", async () => {
    const { api, requests } = chatServer();
    const ui = page(api, "admin", <ChatRecords />);
    await openConversation(ui, /Bob/);
    expect(ui.getByRole("button", { name: /Team/ })).toBeTruthy();
    await ui.findByText("hello");
    // A javascript: link from a message is shown as text, never as a link.
    expect(ui.getByText("evil.png").closest("a")).toBeNull();
    expect(ui.getByRole("link", { name: "voice.m4a" }).getAttribute("href")).toBe("https://cdn.example/v.m4a");
    expect(ui.getByText(/0:07/)).toBeTruthy();
    expect(ui.queryByRole("button", { name: "清空聊天记录" })).toBeNull();

    fireEvent.click(ui.getByRole("button", { name: "加载更早" }));
    await ui.findByText("older one");
    expect(ui.queryByRole("button", { name: "加载更早" })).toBeNull();
    expect(requests.find((r) => r.query["beforeSeq"])?.query).toMatchObject({ beforeSeq: "3", limit: "50" });

    fireEvent.change(ui.getByLabelText("类型"), { target: { value: "image" } });
    fireEvent.click(ui.getByRole("button", { name: "筛选" }));
    await waitFor(() => expect(ui.queryByText("hello")).toBeNull());
    expect(ui.getByRole("link", { name: "a.png" })).toBeTruthy();
    expect(requests.at(-1)?.query).toMatchObject({ type: "image" });
  });

  it("clears with a reason and the typed confirmation; no answer keeps the key, a failure renews it", async () => {
    const answers: Answer[] = [
      "no answer",
      { status: 502, body: { error: "the IM server did not answer" } },
      { status: 200, body: { deletedMessages: 3, deletedMedia: 1, clearVersion: 1, clearBeforeSeq: 5, auditId: "a-2", replayed: false } },
    ];
    const { api, requests } = chatServer(() => answers.shift()!);
    const ui = page(api, "superadmin", <ChatRecords />);
    await openConversation(ui, /Team/);
    await ui.findByText("没有消息");
    expect(ui.queryByRole("button", { name: "清空聊天记录" })).toBeNull(); // groups are not cleared here

    fireEvent.click(ui.getByRole("button", { name: /Bob/ }));
    await ui.findByText("hello");
    const listings = () => requests.filter((r) => r.path === `/topics/${P2P}/messages`).length;
    fireEvent.click(ui.getByRole("button", { name: "清空聊天记录" }));
    const dialog = within(ui.getByRole("dialog", { name: "为双方清空这个会话？" }));
    const go = () => dialog.getByRole("button", { name: "清空聊天记录" }) as HTMLButtonElement;
    expect(go().disabled).toBe(true);
    fireEvent.change(dialog.getByLabelText("原因"), { target: { value: "  the user asked  " } });
    fireEvent.change(dialog.getByLabelText(`输入 ${clearConfirmation(P2P)} 确认`), { target: { value: P2P } });
    expect(go().disabled).toBe(true);
    fireEvent.change(dialog.getByLabelText(`输入 ${clearConfirmation(P2P)} 确认`), { target: { value: `clear ${P2P}` } });
    expect(go().disabled).toBe(true);
    fireEvent.change(dialog.getByLabelText(`输入 ${clearConfirmation(P2P)} 确认`), { target: { value: clearConfirmation(P2P) } });
    expect(go().disabled).toBe(false);

    fireEvent.click(go());
    await dialog.findByText("服务器没有响应。可以重试，不会重复清空。");
    fireEvent.click(go());
    await dialog.findByText("the IM server did not answer");
    const before = listings();
    fireEvent.click(go());
    await dialog.findByText("已删除 3 条消息、1 个文件。");

    const clears = requests.filter((r) => r.path === `/topics/${P2P}/clear`).map((r) => r.body!);
    expect(clears).toHaveLength(3);
    expect(clears[0]).toMatchObject({ confirm: clearConfirmation(P2P), reason: "the user asked", targetUserId: "alice" });
    expect(clears[1]!["idempotencyKey"]).toBe(clears[0]!["idempotencyKey"]);
    expect(clears[2]!["idempotencyKey"]).not.toBe(clears[1]!["idempotencyKey"]);
    await waitFor(() => expect(listings()).toBeGreaterThan(before));
    fireEvent.click(dialog.getByRole("button", { name: "关闭" }));
    expect(ui.queryByRole("dialog")).toBeNull();
  });

  it("offers clearing only to a superadministrator", () => {
    const { api } = chatServer();
    const ui = page(api, "admin", <ClearTopicDialog topic={P2P} onClose={() => {}} />);
    expect(ui.queryByRole("dialog")).toBeNull();
  });
});

const call = (id: string, extra: Partial<CallRecord>): CallRecord => ({
  callSessionId: id,
  callerUserId: "alice",
  calleeUserId: "bob",
  caller: alice,
  callee: bob,
  requestedType: "voice",
  callType: "voice",
  status: "ended",
  createdAt: "2026-10-01T10:00:00.000Z",
  answeredAt: null,
  upgradedAt: null,
  endedAt: "2026-10-01T10:00:30.000Z",
  endReason: "user_hangup",
  durationSec: 0,
  ...extra,
});

const callServer = () =>
  fakeServer((r) => {
    if (r.path === "/calls") {
      if (r.query["status"] === "ringing") return { status: 200, body: { calls: [], nextCursor: null } };
      if (r.query["cursor"] === "page-2") return { status: 200, body: { calls: [call("cs-1", { endReason: "no_answer", callee: person("carol", null) })], nextCursor: null } };
      return {
        status: 200,
        body: {
          calls: [
            call("cs-3", { status: "ringing", requestedType: "video", callType: "video", endedAt: null, endReason: null, durationSec: null }),
            call("cs-2", { requestedType: "video", answeredAt: "2026-10-01T10:00:05.000Z", endedAt: "2026-10-01T10:01:20.000Z", durationSec: 75 }),
          ],
          nextCursor: "page-2",
        },
      };
    }
    if (r.path === "/calls/cs-2") return { status: 200, body: call("cs-2", { requestedType: "video", answeredAt: "2026-10-01T10:00:05.000Z", durationSec: 75 }) };
    return { status: 404, body: { error: "no such call" } };
  });

describe("call records", () => {
  it("lists calls with who, what, how it ended and how long; filters, more, details", async () => {
    const { api, requests } = callServer();
    const opened: string[] = [];
    const ui = page(api, "admin", <CallRecords onOpen={(id) => opened.push(id)} />);
    await ui.findByText("振铃中");
    const row = (id: string) => within(ui.container.querySelector(`tr[data-call-id="${id}"]`) as HTMLElement);
    expect(row("cs-2").getByText("视频 → 语音")).toBeTruthy();
    expect(row("cs-2").getByText("已结束")).toBeTruthy();
    expect(row("cs-2").getByText("1:15")).toBeTruthy();
    expect(row("cs-3").getByText("视频")).toBeTruthy();
    expect(row("cs-3").getByText("—")).toBeTruthy();

    fireEvent.click(ui.getByRole("button", { name: "加载更多" }));
    await ui.findByText("未接听");
    expect(row("cs-1").getByText(/未知用户|carol/)).toBeTruthy();
    expect(ui.queryByRole("button", { name: "加载更多" })).toBeNull();

    fireEvent.click(row("cs-3").getByRole("button", { name: "详情" }));
    expect(opened).toEqual(["cs-3"]);

    fireEvent.change(ui.getByLabelText("状态"), { target: { value: "ringing" } });
    fireEvent.change(ui.getByLabelText("用户 ID"), { target: { value: "alice" } });
    fireEvent.click(ui.getByRole("button", { name: "筛选" }));
    await ui.findByText("没有通话");
    expect(requests.at(-1)?.query).toEqual({ userId: "alice", status: "ringing", limit: "50" });
  });

  it("shows one call's steps, and says when a call is not there", async () => {
    const { api } = callServer();
    let back = 0;
    const ui = page(api, "admin", <CallDetail callSessionId="cs-2" onBack={() => back++} />);
    await ui.findByRole("heading", { name: "Alice → Bob" });
    expect(ui.getByText("语音 (发起时为 视频)")).toBeTruthy();
    const steps = ui.getAllByRole("listitem").map((li) => li.textContent);
    expect(steps.some((t) => t?.endsWith("接听"))).toBe(true);
    expect(steps.some((t) => t?.endsWith("切换到视频"))).toBe(false);
    fireEvent.click(ui.getByRole("button", { name: "← 返回" }));
    expect(back).toBe(1);
    cleanup();
    const missing = page(api, "admin", <CallDetail callSessionId="nope" />);
    await missing.findByText("找不到这通通话");
  });
});

describe("pages and helpers", () => {
  it("matches paths to pages and renders them with the host's navigation", async () => {
    expect(matchImAdminPage("/im/calls/cs%2F1/")).toMatchObject({ page: { path: "im/calls/:callSessionId" }, params: { callSessionId: "cs/1" } });
    expect(matchImAdminPage("im/chats")?.page.path).toBe("im/chats");
    expect(matchImAdminPage("im/nope")).toBeNull();
    expect(matchImAdminPage("im/calls/%E0%A4%A")).toBeNull();
    const zh = adminStringsFor("zh-CN");
    expect(imAdminPages.map((p) => p.title?.(zh) ?? null)).toEqual(["聊天记录", "通话记录", null]);

    const { api } = callServer();
    const went: string[] = [];
    const found = matchImAdminPage("im/calls")!;
    const ui = page(api, "admin", found.page.render({ params: found.params, go: (to) => went.push(to) }));
    await ui.findByText("振铃中");
    fireEvent.click(within(ui.container.querySelector('tr[data-call-id="cs-3"]') as HTMLElement).getByRole("button", { name: "详情" }));
    expect(went).toEqual(["im/calls/cs-3"]);
  });

  it("opens only http(s) and same-site links; formats durations", () => {
    expect(safeLink("/media/x.png")).toBe("/media/x.png");
    expect(safeLink("https://cdn.example/x")).toBe("https://cdn.example/x");
    expect(safeLink("javascript:alert(1)")).toBeUndefined();
    expect(safeLink("data:text/html,hi")).toBeUndefined();
    expect(safeLink(undefined)).toBeUndefined();
    expect([formatDuration(0), formatDuration(75), formatDuration(3725)]).toEqual(["0:00", "1:15", "1:02:05"]);
  });
});
