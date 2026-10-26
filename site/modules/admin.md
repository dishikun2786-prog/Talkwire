---
title: "后台页面"
description: "可嵌进宿主管理后台的聊天记录、通话记录与通话详情页面，以及它们背后的宿主接口、清空保护与审计。"
---

# 后台页面

`@talkwire/admin` 是一组 React 页面，嵌进宿主自己的管理后台：聊天记录（查一个用户的会话与消息，为双方清空单聊）、通话记录与通话详情。它们对接 Node 宿主 SDK 的 `talkwireAdminRoutes`；前端包只依赖 React（对等依赖 `>=18.2 <20`），不另建一个后台应用。

## 宿主一侧：`talkwireAdminRoutes`

宿主注册 `talkwireAdminRoutes`，管理员的请求经它转到 IM 服务端的内部接口。通话记录还要用 `recordingCalls` 包住自己的通话处理器：

```ts
import { talkwireAdminRoutes, recordingCalls } from "@talkwire/host-node";

const { calls: callRecords } = prismaStores(prisma); // 模型里有 TalkwireCallRecord 时才有
await app.register(talkwireHostPlugin, { /* 同上 */ calls: recordingCalls(myCallHandlers, callRecords!) });
await app.register(talkwireAdminRoutes, { authenticate: adminAuth, im, audit, profiles, calls: callRecords });
```

| 选项 | 作用 |
| --- | --- |
| `authenticate` | 返回当前管理员 `{ adminId, role: "admin" \| "superadmin" }`，不是管理员时返回 `null`（答 401） |
| `im` | `ImClient`，调 IM 服务端的内部端点 |
| `audit` | `AuditStore`，记清空的审计 |
| `profiles` | `ProfileStore`，把 uid 换成管理员看得懂的名字 |
| `calls` | `CallRecordStore`，可选；不给时没有通话记录的路由（404） |
| `basePath` | 路由的前缀，默认 `/api/v1/admin/im` |

管理员的令牌与用户的令牌要分开签（不同的密钥或不同的 `type`）：IM 服务端只认普通用户的访问令牌（红线 R18）。

路由（相对 `basePath`）：

| 路由 | 作用 | 谁能用 |
| --- | --- | --- |
| `GET users/:userId/topics` | 一个用户的会话，带成员的名字 | 管理员 |
| `GET topics/:topic` | 一个会话 | 管理员 |
| `GET topics/:topic/messages` | 消息，可按 `beforeSeq`、`limit`（1–200）、`type`、`keyword`、`fromMs`、`toMs` 筛选 | 管理员 |
| `POST topics/:topic/clear` | 为双方清空单聊，见下文 | 超级管理员 |
| `GET calls` | 通话记录，可按 `userId`、`status`、`callType`、`endReason`、时间筛选，游标分页（`limit` 1–100，默认 50） | 管理员（给了 `calls` 时） |
| `GET calls/:callSessionId` | 一通电话的详情 | 管理员（给了 `calls` 时） |

## 前端：`@talkwire/admin`

页面放在 `ImAdminProvider` 里，用一个 `ImAdminApi` 访问上面的路由：

```tsx
import { AdminStyles, CallRecords, ChatRecords, ImAdminApi, ImAdminProvider, imAdminPages } from "@talkwire/admin";

const api = new ImAdminApi({ headers: () => ({ authorization: `Bearer ${adminToken}` }) }); // 默认 /api/v1/admin/im
<ImAdminProvider api={api} role={me.role} locale="zh">
  <AdminStyles />
  <ChatRecords />
</ImAdminProvider>
```

- **路由表**：页面也给成路由表 `imAdminPages`，路径相对宿主挂载的位置，配合任何路由库：`im/chats`（聊天记录）、`im/calls`（通话记录）、`im/calls/:callSessionId`（通话详情，从通话记录进入，不进菜单）。没有路由库时用 `matchImAdminPage(path)`。
- **角色**：`role` 只决定显示什么，每个请求服务端都会再查。
- **文案**：英文、简体中文、越南文，`locale` 默认英文。
- **链接**：消息里的文件链接来自用户，只有 http(s) 和同站路径会做成链接，`javascript:`、`data:` 只显示文字。

用法示例见 `web/packages/admin/src/pages.tsx` 开头的注释和网页示例的 `web/sample/src/Admin.tsx`。

## 清空的保护

清空会永久删除一个单聊里的全部消息与媒体，所以要过几道关。

| 保护 | 做法 | 不满足时 |
| --- | --- | --- |
| 只给超级管理员 | `role` 必须是 `superadmin` | 403 |
| 只对单聊 | IM 服务端只清两个人的单聊，群不行 | 409 |
| 理由 | `reason` 必填，1–500 个字符，记进审计 | 400 |
| 逐字确认 | `confirm` 必须等于 `CLEAR <会话>`（`clearConfirmation(topic)`） | 400 |
| 幂等键 | `idempotencyKey` 必填，8–128 个字符 | 400 |

同一个幂等键再来一次只回放第一次的结果，不会清两次：

- 第一次成功了：返回同样的结果，带 `replayed: true`。
- 第一次还在进行：409。
- 第一次失败了：409，要换一个新键重试。
- 键用在了别的会话或别的管理员的请求上：409。

前端的清空对话框 `ClearTopicDialog` 只对超级管理员显示，理由填了、确认语逐字对上才能提交。它按这个约定用键：没收到应答时重试沿用同一个键，服务端明确报了失败才换新键（“还在进行”的 409 除外）。

清空生效后，IM 服务端给双方各推一个 `topic_cleared`，正在看这个会话的人屏幕随之清空；不在线的设备下次登录时补投（红线 R7）。

### 审计

每次清空写一条审计，只记元数据，不记消息内容。

| 字段 | 内容 |
| --- | --- |
| `adminId` | 谁清的 |
| `action` | `im.clear_topic` |
| `topic`、`targetUserId` | 清的哪个会话；管理员当时在看哪个用户（可选） |
| `reason` | 理由 |
| `idempotencyKey` | 幂等键 |
| `status` | 先写 `pending`，结束后改为 `success` 或 `failed` |
| `details` | 成功时是删除的消息数、媒体数与清空水位（`deletedMessages`、`deletedMedia`、`clearVersion`、`clearBeforeSeq`）；失败时是 IM 服务端的状态码 |

审计存在宿主实现的 `AuditStore` 里；Node 宿主 SDK 有内存版和 Prisma 版（模型 `TalkwireAdminAudit`）。清空要二次确认并记审计是红线 R13，宿主用例 `R13_AdminClearNeedsConfirmAndAudit` 覆盖。

## 通话记录

通话记录是宿主自己的数据：`recordingCalls` 包住宿主的通话处理器，把 `create` 接受的每通电话写进 `CallRecordStore`。被拒绝的邀请不是通话，不记录。

| 字段 | 内容 |
| --- | --- |
| `callSessionId` | 通话号 |
| `callerUserId`、`calleeUserId` | 双方的宿主用户 id；后台页面显示成名字 |
| `requestedType`、`callType` | 主叫要的类型、最终的类型（以语音接听的视频邀请是语音，升级过的是视频） |
| `status` | `ringing`、`active`、`ended` |
| `createdAt`、`answeredAt`、`upgradedAt`、`endedAt` | 各步的时间 |
| `endReason` | 结束原因，即 IM 服务端交给 `settle` 的原因 |
| `durationSec` | 接听到结束的秒数；结束前为 `null`，没接听为 0 |

- 只存元数据，不存频道号和 RTC 凭证，更没有通话内容；计费金额是宿主的业务，不在这里。
- 每一步都只往前走：IM 服务端重试同一请求、或者结束两次，都不会改坏记录。
- 记录写失败不影响通话：错误交给 `onError`（默认 `console.error`）。`settle` 里计费抛错时，结束照样记下。
- 存储有内存版和 Prisma 版（模型 `TalkwireCallRecord`）；`prismaStores(prisma)` 只在 schema 里有这个模型时才给出 `calls`。

通话本身见 [音视频通话](/modules/call)。

## 验证状态

宿主一侧的路由与包装器有单测，前端有组件测试，整条链路有一个端到端用例。

- **宿主**：后台路由（含通话记录接口）与 `recordingCalls` 的测试，内存版与 Prisma 版存储跑同一套（含幂等键、通话记录各步只进不退与游标分页）。
- **端到端**：`web/e2e/admin.spec.ts` 让一通电话进入通话记录、查看详情、读聊天记录、为双方清空，双方屏幕随之清空。
