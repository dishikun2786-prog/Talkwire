import { uidOf } from "@imkit/protocol";
import { ChatEngine, ImClient, MemoryChatStore, RequestRefused } from "@imkit/client";
import { GroupJoinClient, afterJoined, type GroupJoinEvent } from "@imkit/group-join";
import { afterEach, describe, expect, it } from "vitest";
import { FakeServer, type Frame } from "../../client/test/fake-server.js";
import { until } from "../../client/test/until.js";

const stops: (() => void)[] = [];
afterEach(() => {
  for (const s of stops.splice(0)) s();
});

async function connected(server: FakeServer, appUserId = "alice") {
  const client = new ImClient({ endpoints: ["ws://fake"], tokenProvider: () => "tok:" + appUserId, transport: server, requestTimeoutMs: 2_000, watchNetwork: false });
  const api = new GroupJoinClient(client);
  stops.push(() => {
    api.dispose();
    client.stop();
  });
  client.start();
  await client.awaitConnected(5_000);
  return { client, api };
}

const group = "grp_scan00000001";

describe("group join client", () => {
  it("sends exactly the fields of section 14 and reads the replies", async () => {
    const server = new FakeServer();
    server.onFrame = (conn, f) => {
      const reply = (extra: Frame) => conn.push({ action: f.action, code: 200, id: f.id, ...extra });
      switch (f.action) {
        case "group_qr_token":
          reply({ topic: f.topic, qrToken: f.refreshQr ? "qr-2" : "qr-1", expiresAt: 1_700_000_000_000 });
          return true;
        case "group_qr_resolve":
          if (f.qrToken === "gone") conn.push({ action: f.action, code: 404, id: f.id, reason: "二维码不存在或已过期" });
          else reply({ topic: group, name: "scan", memberCount: 3, requireApproval: true });
          return true;
        case "group_join":
          if (f.qrToken === "full") conn.push({ action: f.action, code: 409, id: f.id, reason: "群成员数量已达上限" });
          else reply({ topic: group, joinStatus: f.joinMessage ? "pending_approval" : "joined" });
          return true;
        case "group_join_requests_list":
          reply({ topic: f.topic, joinRequests: [{ id: 7, requesterUid: "uid_e", requesterName: "Eve", message: "hi", createdAt: 5 }, { id: 8, requesterUid: "uid_f", requesterName: "", createdAt: 6 }, { bogus: true }] });
          return true;
        case "group_join_review":
          reply({ topic: f.topic, joinStatus: f.approve ? "approved" : "rejected" });
          return true;
        case "get_group_join_settings":
          reply({ topic: f.topic }); // false is omitted
          return true;
        case "update_group_join_settings":
          reply({ topic: f.topic, requireApproval: f.requireApproval });
          return true;
      }
      return false;
    };
    const { api } = await connected(server);

    expect(await api.qrToken(group)).toEqual({ topic: group, qrToken: "qr-1", expiresAt: 1_700_000_000_000 });
    expect((await api.qrToken(group, { refresh: true })).qrToken).toBe("qr-2");
    expect(await api.resolve("qr-2")).toEqual({ topic: group, name: "scan", memberCount: 3, alreadyMember: false, isFull: false, requireApproval: true });
    expect(await api.resolve("gone")).toBeNull();
    expect(await api.join("qr-2")).toEqual({ topic: group, status: "joined" });
    expect(await api.join("qr-2", "let me in")).toEqual({ topic: group, status: "pending_approval" });
    await expect(api.join("full")).rejects.toSatisfy((e) => e instanceof RequestRefused && e.reply.code === 409);
    expect(await api.requests(group)).toEqual([
      { id: 7, requesterUid: "uid_e", requesterName: "Eve", message: "hi", createdAt: 5 },
      { id: 8, requesterUid: "uid_f", requesterName: "uid_f", createdAt: 6 }, // F46: no name, the uid
    ]);
    expect(await api.review(group, 7, true)).toBe("approved");
    expect(await api.review(group, 8, false)).toBe("rejected");
    expect(await api.requireApproval(group)).toBe(false);
    expect(await api.setRequireApproval(group, true)).toBe(true);

    const strip = (f: Frame) => {
      const { id: _id, ...rest } = f;
      return rest;
    };
    expect(server.sent("group_qr_token").map(strip)).toEqual([
      { action: "group_qr_token", topic: group },
      { action: "group_qr_token", topic: group, refreshQr: true },
    ]);
    expect(server.sent("group_join").map(strip)).toEqual([
      { action: "group_join", qrToken: "qr-2" },
      { action: "group_join", qrToken: "qr-2", joinMessage: "let me in" },
      { action: "group_join", qrToken: "full" },
    ]);
    expect(server.sent("group_join_review").map(strip)).toEqual([
      { action: "group_join_review", topic: group, joinRequestId: 7, approve: true },
      { action: "group_join_review", topic: group, joinRequestId: 8, approve: false },
    ]);
  });

  it("turns the pushes into events and acknowledges the queued ones", async () => {
    const server = new FakeServer();
    const { api } = await connected(server);
    const seen: GroupJoinEvent[] = [];
    api.events.on((e) => seen.push(e));
    const eve = uidOf("eve");
    const conn = server.live[0]!;
    conn.push({ action: "group_join_request", topic: group, name: "scan", from: eve });
    conn.push({ action: "group_join_reviewed", topic: group, name: "scan", joinStatus: "approved", eventId: 41 });
    conn.push({ action: "group_join_reviewed", topic: group, name: "scan", joinStatus: "rejected" });
    await until("three events", () => seen.length === 3);
    expect(seen).toEqual([
      { kind: "request", topic: group, name: "scan", from: eve },
      { kind: "reviewed", topic: group, name: "scan", status: "approved" },
      { kind: "reviewed", topic: group, name: "scan", status: "rejected" },
    ]);
    await until("acked", () => server.sent("ack_group_event").length === 1);
    expect(server.sent("ack_group_event")[0]!.eventId).toBe(41);
  });

  it("after joining, subscribes and lists the group (D7)", async () => {
    const server = new FakeServer();
    server.onFrame = (conn, f) => {
      if (f.action !== "get_groups") return false;
      conn.push({ action: "get_groups", code: 200, id: f.id, groups: [{ topic: group, name: "scan", owner: "uid_x", members: 3 }] });
      return true;
    };
    const client = new ImClient({ endpoints: ["ws://fake"], tokenProvider: () => "tok:alice", transport: server, watchNetwork: false });
    const engine = new ChatEngine(client, new MemoryChatStore(), { appUserId: "alice", autoSync: false });
    stops.push(() => {
      client.stop();
      engine.close();
    });
    await engine.open();
    client.start();
    await client.awaitConnected(5_000);
    await afterJoined(engine, group, "scan");
    expect(server.sent("sub").map((f) => f.topic)).toEqual([group]);
    expect(server.sent("get_groups")).toHaveLength(1);
    expect(engine.conversation(group)?.title).toBe("scan");
  });
});
