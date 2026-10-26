---
title: "扫码入群"
description: "群二维码令牌、凭令牌入群与入群审核，全部在 IM 服务端完成，宿主不需要实现任何接口。"
---

# 扫码入群

扫码入群模块（`groupjoin`）让不是好友的人凭群的二维码令牌入群，群可以要求群主或管理员先审核。整个流程在 IM 服务端完成，宿主不需要实现任何接口。线协议在规范第 14 节，见 [规范全文](/protocol/spec)。

## 装上与拿掉

模块在服务端和客户端各装一份，宿主一侧什么都不用加。

| 位置 | 装上 | 拿掉 |
| --- | --- | --- |
| 服务端 | `TALKWIRE_MODULES` 列出 `groupjoin`；不设这个变量时装宿主支持的全部模块 | 从列表里去掉；入群动作得到 501 `module not enabled: groupjoin` |
| Web | `@talkwire/group-join`，界面在 `@talkwire/group-join/react` | 不装 |
| Android | `talkwire-group-join` | 不装 |

- standalone 宿主（`TALKWIRE_HOST_MODE=standalone`）也支持这个模块。
- 模块有自己的迁移脚本 `server/migrations/groupjoin/`，装上时建令牌表与申请表；拿掉模块不删表。
- 解散群时，群的令牌和申请一并删除，旧二维码不再能用。

## 二维码令牌

令牌是群的一张通行证，二维码里放什么由应用决定。

- **谁能取**：`group_qr_token` 任何成员都能取群的当前令牌，重复调用得到同一个。带 `refreshQr: true` 只给群主和管理员：生成新令牌，旧的立即失效。非成员得到 401。
- **有效期**：运行配置 `qrTtlSeconds`，默认 604800 秒（7 天），宿主经 `ConfigSource.runtimeConfig` 下发。
- **二维码内容**：通常是应用自己的入群页链接，令牌放在链接里。套件不带二维码编码器，也不管扫码：Web 的 `GroupQrCard` 交给 `renderQr` 画，Android 由 App 画和扫。
- **示例的做法**：网页示例的链接是 `#/join/<令牌>`，同一个码浏览器和 App 都能打开；Android 示例 App 还能打开 `talkwiresample://join/<令牌>`。

## 入群页

入群页先预览、再加入，预览与加入都不需要好友关系。

`group_qr_resolve` 任何登录用户都能调，返回群的 `topic`、`name`、`memberCount`，以及 `alreadyMember`、`isFull`、`requireApproval`。令牌不存在、已过期或已被换掉时返回 404，Web 的 `resolve` 此时返回 `null`。

`group_join` 带上令牌和可选的留言 `joinMessage`（超过 60 个码点时服务端截断，界面也限制在 60 个）。服务端按群**当前**的设置重新判断，不信任之前预览的结果：

| 情况 | 应答 `joinStatus` | 其他效果 |
| --- | --- | --- |
| 已是成员 | `joined` | 无 |
| 群不需要审核 | `joined` | 直接入群；通知谁、订阅谁见下文“legacy 档的 D7” |
| 群需要审核 | `pending_approval` | 建一条申请，通知群主与管理员 |
| 已有待处理的申请 | `pending_approval` | 不重复通知 |

两端的入群页组件 `JoinGroupScreen` 都处理了整个过程：入群后订阅该群并刷新群列表；停在“申请已发送”时会跟着审核结果变成已入群或被拒绝。

## 入群申请与审核

需要审核的群，申请交给群主和管理员处理；群里的普通成员看不到申请。

- **通知**：在线的群主和管理员收到 `group_join_request`。不在线的，strict 档把这条事件排进离线队列、下次登录补投；legacy 档不排（缺陷 D9），客户端登录后应主动拉申请列表。离线推送的文案是运行配置 `pushText.joinRequest`。
- **列表**：`group_join_requests_list` 群主和管理员可调，每条带 `id`、`requesterUid`、`requesterName`、可选的 `message` 和 `createdAt`。`requesterName` 由宿主按查看者的视角解析，查不到时退回申请人的 uid（F46）。
- **审核**：`group_join_review` 带申请所属群的 `topic`（取自推送或列表）、`joinRequestId` 与 `approve`。申请人收到 `group_join_reviewed`；通过时还收到 `grp_sync` 并被订阅。
- **同意之后**：群里其他成员不会收到任何通知（F45），同意的那台设备也一样。所以同意后要调 `engine.noteGroupChanged(topic)`，本设备的成员列表随之刷新；两端的 `JoinRequestsPanel` 已经这样做。
- **被拒之后**：被拒的人可以用同一个令牌再申请，得到新的 `joinRequestId`（F56）。
- **审核开关**：`get_group_join_settings` 成员可调；`update_group_join_settings` 只有群主能改。界面组件是 `JoinApprovalSetting`。

## 群满了会怎样

群人数上限是运行配置 `maxMembers`，默认 500。服务端在三个地方检查它：

| 时机 | 判据 | 结果 |
| --- | --- | --- |
| 预览 | 成员数 ≥ `maxMembers` | `isFull: true`；两端的入群页显示“群已满”，不给加入按钮 |
| 加入 | 成员数 + 1 > `maxMembers` | 409 `群成员数量已达上限`；需要审核的群也在建申请之前拒绝 |
| 同意申请 | 同上 | 409；strict 档申请仍是待处理，legacy 档申请已被用掉（缺陷 D8） |

审核失败时按 strict 档的 `errCode` 区分，不解析 `reason` 的文字：`group_full` 是群满（申请还在，先腾出名额再同意）；其余的 409 是申请已被处理。两端的 `JoinRequestsPanel` 遇到后一种会重新拉列表，提示“该申请已被其他管理员处理”。legacy 档的错误帧没有 `errCode`，群满的 409 也按这一种显示，而那时申请确实已被用掉。

## legacy 档的 D7

legacy 档的服务端保留原版的缺陷 D7：不需要审核的扫码入群既不通知群成员，也不给入群者推 `grp_sync`，入群者也**没有被订阅**。所以客户端收到 `joined` 后**必须**自己订阅该群并刷新群列表。

strict 档已修正：发请求的连接被订阅；群里其余在线成员收到 `from` 为入群者的 `grp_sync`；入群者的其他设备收到同样的 `grp_sync` 并被订阅，没连上的设备入队。按 legacy 档的做法再订阅一次是无害的。

两端的 `JoinGroupScreen` 都会补做这一步。只用 `GroupJoinClient` 自己画界面时，`joined` 之后调 `afterJoined`：Web 是 `afterJoined(engine, topic)`，Android 是 `engine.afterJoined(topic)`。档位的区别见 [legacy 与 strict](/protocol/profiles)，缺陷全表见 [已知缺陷](/protocol/defects)。

## 客户端接入

两端都是一个 `GroupJoinClient` 加一组界面组件，群信息页上的部分经各自的扩展槽位接入。

| `GroupJoinClient` 方法 | 协议动作 | 谁能调 |
| --- | --- | --- |
| `qrToken` | `group_qr_token` | 成员；换新令牌只给群主和管理员 |
| `resolve` | `group_qr_resolve` | 任何登录用户 |
| `join` | `group_join` | 任何登录用户 |
| `requests` | `group_join_requests_list` | 群主、管理员 |
| `review` | `group_join_review` | 群主、管理员 |
| `requireApproval` | `get_group_join_settings` | 成员 |
| `setRequireApproval` | `update_group_join_settings` | 群主 |

`GroupJoinClient` 的 `events` 发出两种推送：有人申请加入我管理的群，我的申请有了结果。从离线队列补投来的这两种事件会自动回执：Web 由 `GroupJoinClient` 回执，Android 由 `ChatEngine` 回执。拒绝时 Web 抛 `RequestRefused`、Android 抛 `ImException.Refused`，`code` 为 401（不是成员或不是管理者）、404（令牌失效）或 409（群满或申请已被处理）。

::: code-group

```tsx [Web]
import { GroupJoinProvider, GroupQrCard, JoinGroupScreen, JoinRequestsPanel } from "@talkwire/group-join/react";
import { CallButtons, CallOverlay, CallProvider } from "@talkwire/call/react";

<TalkwireProvider engine={engine} host={host} locale="zh">
  <GroupJoinProvider>
    <CallProvider media={rtcAdapter}>
      {/* 群里：<GroupQrCard topic={topic} canRefresh={isManager} linkFor={(t) => `${appUrl}#/join/${t}`} renderQr={drawQr} /> */}
      {/* 打开入群链接：<JoinGroupScreen qrToken={token} onOpen={openChat} /> */}
      {/* 单聊页头：<CallButtons peer={uid} /> */}
      <CallOverlay />
    </CallProvider>
  </GroupJoinProvider>
</TalkwireProvider>
```

```kotlin [Android]
val calls = CallManager(client, engine.me, MyRtcEngine(), scope)   // 媒体走自己的 RTC；NoMediaEngine 只有信令
val groupJoin = GroupJoinClient(client)

class MyHost(…, private val calls: CallManager, private val groupJoin: GroupJoinClient) : ImUiHost {
    // 单聊页头的语音、视频按钮。按下时先要麦克风（视频再要摄像头）权限
    override val topBarExtras: (@Composable RowScope.(ConversationId) -> Unit) = { id ->
        if (!id.isGroup) CallButtons(calls, peer = id.id, onRefused = { reason -> toast(reason) })
    }
    // 群信息页：二维码人人可见，换码与审批只给群主和管理员，审核开关只有群主能改
    override val groupInfoExtras: (@Composable (String, GroupRole) -> Unit) = { topic, role ->
        Column {
            GroupQrCard(groupJoin, topic, canRefresh = role.canManage, linkFor = { token -> "https://app.example.com/join/$token" })
            JoinApprovalSetting(groupJoin, topic, editable = role == GroupRole.OWNER)
            if (role.canManage) JoinRequestsPanel(groupJoin, engine, topic)
        }
    }
}
```

:::

### Web：`@talkwire/group-join`

主入口是 `GroupJoinClient` 与 `afterJoined`，依赖 `@talkwire/client`；`/react` 里有 `GroupJoinProvider`、`GroupQrCard`、`JoinGroupScreen`、`JoinRequestsPanel`、`JoinApprovalSetting`。组件放在 `<TalkwireProvider>` 里，文案跟随它的 `locale`（英文、简体中文、越南文，可逐条替换）。

群信息页上的部分经 `ConversationInfo` 的 `groupExtras` 接入：

```tsx
groupExtras={(topic, role) => <GroupQrCard topic={topic} canRefresh={role !== "member"} linkFor={linkFor} />}
```

React 严格模式下效果会先卸载一次再挂上，`GroupJoinClient` 的 `dispose()` 之后可以再 `start()`，`GroupJoinProvider` 在效果里重新开始监听。

### Android：`talkwire-group-join`

Compose 模块，只依赖核心（经 `talkwire-ui`）。界面组件是 `GroupQrCard`、`JoinGroupScreen`、`JoinRequestsPanel`、`JoinApprovalSetting`，群信息页上的部分经 `ImUiHost.groupInfoExtras` 接入（见上例）。

- 拿到令牌后显示 `JoinGroupScreen(groupJoin, engine, token, onOpen = …)`；扫码与解析链接是 App 的事。
- `JoinRequestsPanel(groupJoin, engine, topic)` 要引擎，因为同意后要调 `engine.noteGroupChanged(topic)`。
- `groupJoinNotice(context, event, nameOf)` 把 `groupJoin.events` 里的推送变成一句可显示的话。

## 验证状态

服务端有黄金对话，两端有回放与界面测试，真机互通只做过一台手机。

- **服务端**：黄金对话 `module/qr-join`、`module/qr-join-review`；strict 档对这两个场景另录了修正 D7、D8 的版本（`contract/protocol/transcripts-strict/`）。
- **Web**：单测、组件测试与黄金对话回放；端到端用例 `web/e2e/group-join.spec.ts` 走一遍凭链接入群、再走一遍需要审核的入群。
- **Android**：单测、界面测试与 `GroupJoinReplayTest` 回放。[`android/DEVICE_CHECKS.md`](https://github.com/dishikun2786-prog/Talkwire/blob/main/android/DEVICE_CHECKS.md) 记了 2026-10-26 在一台小米（Android 15）上与网页示例互通扫码入群（含审批）通过，审核结果的提示文字是否弹出没有核实。
