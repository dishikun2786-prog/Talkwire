// @vitest-environment jsdom
import { ChatEngine, ImClient, MemoryChatStore } from "@imkit/client";
import { NoMediaEngine, type CallRinger, type CallTraceEntry } from "@imkit/call";
import { CallButtons, CallOverlay, CallProvider } from "@imkit/call/react";
import { uidOf } from "@imkit/protocol";
import { ImKitProvider, type ImUiHost } from "@imkit/react";
import { cleanup, fireEvent, render } from "@testing-library/react";
import { StrictMode, type ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { FakeServer, type Frame } from "../../client/test/fake-server.js";
import { until } from "../../client/test/until.js";

const bob = uidOf("bob");
const host: ImUiHost = { profiles: async (uids) => uids.map((uid) => ({ uid, name: uid === bob ? "Bob" : "Someone" })) };
const stops: (() => void)[] = [];
afterEach(() => {
  cleanup();
  for (const s of stops.splice(0)) s();
});

function answering(handlers: Record<string, (f: Frame) => Frame[]>): FakeServer {
  const server = new FakeServer();
  server.onFrame = (conn, f) => {
    const h = handlers[String(f.action)];
    if (!h) return false;
    for (const frame of h(f)) conn.push(frame.type ? frame : { action: f.action, id: f.id, ...frame });
    return true;
  };
  return server;
}

async function setup(server: FakeServer, user = "alice", ringer: CallRinger | null = null, opts: { tabChannel?: boolean; log?: (e: CallTraceEntry) => void } = {}) {
  const client = new ImClient({ endpoints: ["ws://fake"], tokenProvider: () => "tok:" + user, transport: server, watchNetwork: false });
  const engine = new ChatEngine(client, new MemoryChatStore(), { appUserId: user, autoSync: false });
  stops.push(() => {
    client.stop();
    engine.close();
  });
  await engine.open();
  client.start();
  await client.awaitConnected(5_000);
  const media = new NoMediaEngine();
  const wrap = (node: ReactNode) => (
    <StrictMode>
      <ImKitProvider engine={engine} host={host} locale="zh">
        <CallProvider media={media} ringer={ringer} {...(opts.tabChannel ? {} : { channel: null })} {...(opts.log ? { log: opts.log } : {})}>
          {node}
        </CallProvider>
      </ImKitProvider>
    </StrictMode>
  );
  return { wrap, media };
}

const creds = { channelId: "ch", rtcToken: "rtc", denoiseEnabled: true };

describe("call components", () => {
  it("calls from the header, shows the callee's name, and hangs up", async () => {
    const server = answering({
      call_invite: (f) => [{ code: 200, topic: f.topic, callSessionId: "cs-1" }, { type: "call_answered", callSessionId: "cs-1", ...creds }],
      call_hangup: (f) => [{ code: 200, callSessionId: f.callSessionId }],
    });
    const sounds: string[] = [];
    const ringer: CallRinger = { ring: () => sounds.push("ring"), ringback: () => sounds.push("ringback"), stop: () => sounds.push("stop") };
    const { wrap, media } = await setup(server, "alice", ringer);
    const ui = render(
      wrap(
        <>
          <CallButtons peer={bob} />
          <CallOverlay />
        </>,
      ),
    );
    fireEvent.click(ui.getByRole("button", { name: "语音通话" }));
    await until("in the call", () => /^\d+:\d\d$/.test(ui.container.querySelector("[data-call-status]")?.textContent ?? ""));
    expect(ui.getByRole("dialog", { name: "Bob" })).toBeTruthy();
    expect(media.log).toEqual(["join voice ch"]);
    expect(sounds, "the ringback stops once answered").toEqual(["ringback", "stop"]);
    fireEvent.click(ui.getByRole("button", { name: "静音" }));
    expect(ui.getByRole("button", { name: "取消静音" }).getAttribute("aria-pressed")).toBe("true");
    fireEvent.click(ui.getByRole("button", { name: "挂断" }));
    await until("ended", () => !!ui.queryByText("通话已结束"));
    expect(server.sent("call_hangup")).toHaveLength(1);
  });

  it("rings in the tab: answer a video call as voice, then a switch to video", async () => {
    const server = answering({
      call_accept: (f) => [{ code: 200, callSessionId: f.callSessionId, ...creds }],
      call_upgrade_accept: (f) => [{ code: 200, callSessionId: f.callSessionId }, { type: "call_upgraded", callSessionId: f.callSessionId, videoQuality: "hd" }],
    });
    const { wrap, media } = await setup(server, "bob");
    const ui = render(wrap(<CallOverlay />));
    server.live[0]!.push({ type: "call_ring", callSessionId: "cs-2", callType: "video", callerName: "Alice", ringTimeoutSeconds: 45 });
    await until("ringing", () => !!ui.queryByText("邀请你视频通话"));
    expect(ui.getByText("Alice")).toBeTruthy();
    fireEvent.click(ui.getByRole("button", { name: "以语音接听" }));
    await until("answered", () => media.log.length === 1);
    expect(server.sent("call_accept")[0]).toMatchObject({ callSessionId: "cs-2", acceptAsVoice: true });
    expect(ui.getByRole("button", { name: "切换到视频" })).toBeTruthy();

    server.live[0]!.push({ type: "call_upgrade_request", callSessionId: "cs-2", toType: "video" });
    await until("asked", () => !!ui.queryByText("对方请求切换到视频通话"));
    fireEvent.click(ui.getByRole("button", { name: "开启视频" }));
    await until("video", () => !!ui.container.querySelector(".imkit-call-videos"));
    expect(media.log).toContain("video true");

    server.live[0]!.push({ type: "call_ended", callSessionId: "cs-2", reason: "max_duration_reached" });
    await until("ended", () => !!ui.queryByText("已达通话时长上限"));
  });

  // Found in a browser: StrictMode built the manager twice, and the dropped one kept hearing every ring.
  it("listens once under StrictMode and closes its channel to the other tabs on unmount", async () => {
    const open = new Set<object>();
    class Channel {
      onmessage: unknown = null;
      constructor(readonly name: string) {
        open.add(this);
      }
      postMessage() {}
      close() {
        open.delete(this);
      }
    }
    vi.stubGlobal("BroadcastChannel", Channel);
    stops.push(() => vi.unstubAllGlobals());
    const server = answering({});
    const traced: CallTraceEntry[] = [];
    const { wrap } = await setup(server, "bob", null, { tabChannel: true, log: (e) => traced.push(e) });
    const ui = render(wrap(<CallOverlay />));
    await until("started", () => traced.some((e) => e.what === "start"));
    expect(open.size, "one channel to the other tabs").toBe(1);

    server.live[0]!.push({ type: "call_ring", callSessionId: "cs-4", callType: "voice", ringTimeoutSeconds: 45 });
    await until("ringing", () => !!ui.queryByText("邀请你语音通话"));
    await new Promise((r) => setTimeout(r, 30));
    expect(traced.filter((e) => e.what === "signal"), "one manager heard it").toHaveLength(1);
    ui.unmount();
    expect(open.size, "closed on unmount").toBe(0);
  });

  it("says why an invite was refused and names an unknown end reason plainly", async () => {
    const server = answering({ call_invite: () => [{ code: 409, reason: "offline" }] });
    const { wrap } = await setup(server);
    const refused: string[] = [];
    const ui = render(
      wrap(
        <>
          <CallButtons peer={bob} onRefused={(t) => refused.push(t)} />
          <CallOverlay />
        </>,
      ),
    );
    fireEvent.click(ui.getByRole("button", { name: "视频通话" }));
    await until("refused", () => refused.length === 1);
    expect(refused).toEqual(["对方不在线"]);
    expect(ui.container.querySelector(".imkit-call")).toBeNull();

    server.live[0]!.push({ type: "call_ring", callSessionId: "cs-3", callType: "voice", ringTimeoutSeconds: 45 });
    await until("ringing", () => !!ui.queryByText("邀请你语音通话"));
    server.live[0]!.push({ type: "call_ended", callSessionId: "cs-3", reason: "something_new" });
    await until("ended", () => !!ui.queryByText("通话已结束"));
  });
});
