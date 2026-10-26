---
title: "多端与推送"
description: "同一账号在多台设备上同时在线的规则与上限、自己消息回显到其他设备、在线状态，以及 FCM、APNs、Web Push 推送令牌的注册与投递对象。"
---

# 多端与推送

同一账号能否在多台设备上同时在线，取决于客户端登录时是否带 `device`：不带时与参考服务一样互相顶替，带了才多端同时在线（规范第 6.2 节）。离线推送只发给当时没有连接的设备注册的令牌（规范第 17 节）。

## 不带 `device`：互相顶替

不带 `device` 的登录是参考服务的全部行为：每个账号同一时刻只保留一个这样的连接，新连接登录成功后旧连接被关闭（规范第 6.2 节，F20）。关闭前服务端可能给旧连接发下面这帧，但不保证送达（F3）：

```json
{"action":"login","code":401,"reason":"logged in from another device"}
```

客户端在连接被意外关闭时**不应**自动无限重连抢回会话；收到上面这帧时**应当**停止自动重连并提示用户（规范第 6.2 节）。

顶替不产生下线推送：与该账号共享会话的在线用户反而再收到一次 `what:"on"`，`presRev` 递增（规范第 12.1 节，F51）。新连接自己的订阅不受旧连接清理的影响，例如黄金对话 [`core/supersede`](https://github.com/dishikun2786-prog/Talkwire/blob/main/contract/protocol/transcripts/core__supersede.json) 第 9 步，`a2` 顶替 `a1` 时对方 `b` 收到的是：

```json
{"action": "pres", "online": true, "presRev": "<rev>", "topic": "${uid:A}", "what": "on"}
```

## 带 `device`：多端同时在线

`device` 是客户端为本机选定并持久保存的名字，在 `login` 请求里与 `token` 一起发出（规范第 6.1、6.2 节）。只有同名设备的新登录才顶替旧连接，不同设备的连接同时在线。

| 规则 | 内容 |
| --- | --- |
| 名字格式 | 1–64 个字符，只含字母、数字和 `.`、`_`、`:`、`-`；不合法时 400 `invalid device`（`errCode` 为 `invalid_device`） |
| 名字稳定 | 同一台设备每次登录**必须**用同一个名字，不同设备**必须**用不同的名字 |
| 不带 `device` | 等于设备 `""`，所以不带它的客户端仍互相顶替 |
| 设备上限 | 一个账号最多同时在 `TALKWIRE_MAX_DEVICES_PER_USER` 台（默认 10）设备上在线 |
| 超出上限 | 新设备的登录得到 409 `too many devices`（`errCode` 为 `too_many_devices`）；已在线设备的再次登录照常顶替自己 |
| 上限的精度 | 在登录时检查，几台新设备同时登录时可能略微超出 |

客户端**应当**看到 `capabilities` 里有 `multi-device` 后才带 `device`；对不认识它的服务端带上也无害，服务端忽略未知字段，照旧互相顶替（规范第 6.2 节）。多端同时在线不分档位：legacy 档与 strict 档都支持，只在客户端带 `device` 时启用（规范第 0.3 节）。

### 多台设备在线时

多台设备同时在线时，各方面按下表处理（规范第 6.2 节）。

| 方面 | 规则 | 节 |
| --- | --- | --- |
| 在线状态 | 每次登录都推 `on`；只有最后一台设备离开才推 `off` | 12.1 |
| 自己发的消息 | 自己的其他在线设备收到同一条 `data`；单聊的 `topic` 写成对方 uid | 9.2 |
| 自己的已读、撤回、清空、建群、入群、退群、改群名 | 同步到自己的其他设备；“正在输入”不同步 | 9.5、10、11、12.3 |
| 离线补投 | 清空和群事件按设备补投、按设备回执：设备离开期间的变更，在它下次登录时补投 | 10.10、11.3 |
| 推送 | 只推给没有连接的设备注册的推送令牌 | 17 |
| 端到端加密 | 只属于发布身份密钥的那台设备 | 13.1 |
| 通话 | 被叫的所有设备都响铃；接听的设备拿到通话，其余设备收到 `answered_elsewhere` | 16 |

发起动作的设备不会收到自己动作的回显（规范第 6.2 节）。服务端按设备而不是按连接判断，所以同一设备已被顶替的旧连接里迟到的帧，也不会回显给这台设备的新连接。

## 自己发的消息回显到其他设备

`pub` 成功后，发送者自己的其他在线设备收到同一条 `data`；不在线的设备靠 `get` 补拉（规范第 9.2 节）。发出这条消息的设备只收到 `pub` 应答。

发给发送者自己设备的单聊帧里，`topic` 是对方 uid、`from` 是自己（规范第 9.2 节）。客户端按“`from` 等于自己时，会话对象取 `topic`”归档。

其他动作的回显同理（规范第 9.5、11.3、12.3 节）：

| 动作 | 自己的其他设备收到 |
| --- | --- |
| 撤回（`del`） | `recalled`，`from` 是自己 |
| 清空（`del_topic`） | `topic_cleared`，`from` 是自己 |
| 已读（`note` 的 `read`） | `info`；已读状态属于用户而不是设备 |
| 正在输入（`note` 的 `kp`） | 不发 |
| 建群、退群 | 建群时 `grp_sync`，退群时 `grp_disband`（规范第 10.10 节） |

### 离线补投按设备

多端时，离线群事件和清空记录按设备补投、按设备回执（规范第 10.10、11.3 节）。事件实时发给成员在线的每台设备；他最近 30 天出现过、此刻不在线的设备另有一条入队，只补投给还没拿到的设备。

回执只记本设备已拿到，最近 30 天出现过的设备都拿到后事件才删除（规范第 10.10 节）。从没出现过的新设备第一次登录时也会收到尚未删除的事件，这对客户端无害：`grp_sync` 只是刷新提示，已经不在其中的群的事件会被丢弃。

## 在线状态

服务端只给出“上线”和“下线”两种推送，客户端 SDK 据此维护在线、离线、未知三态（规范第 12 节，红线 C5）。

### 服务端的推送

用户上线或下线时，与他共享至少一个会话、且当前在线的用户收到 `pres` 推送（规范第 12.1 节）。下面两帧取自黄金对话 [`core/presence`](https://github.com/dishikun2786-prog/Talkwire/blob/main/contract/protocol/transcripts/core__presence.json) 第 7、13 步：

```json
{"action": "pres", "online": true, "presRev": "<rev>", "topic": "${uid:B}", "what": "on"}
```

```json
{"action": "pres", "presRev": "<rev>", "topic": "${uid:B}", "what": "off"}
```

下线时 `online` 省略（零值）。`presRev` 是每个用户单调递增的版本号，客户端**必须**丢弃 `presRev` 小于已知值的帧，以免乱序到达的旧状态覆盖新状态（规范第 12.1 节）。多端时推送发到对方的每台在线设备。

### 查询

`pres` 查询三选一：`{topic:"uid_…"}`、`{topics:["uid_…",…]}`、`{topic:"*"}`，`*` 表示所有与我共享会话的人（规范第 12.2 节）。应答带 `what=snapshot`，`states` 的顺序与请求一致（`*` 的顺序不保证），`lastSeenAt` 是最后下线时刻，未知时省略。

自己、非 uid 的项被丢弃，重复项去重，最多 500 个（规范第 12.2 节）。legacy 档可以查询任意 uid，包括陌生人；strict 档只返回与查询者共享会话的用户（缺陷 D3，红线 R22）。成功的快照应答在 legacy 档不带 `id`，按 `action=pres` 且 `what=snapshot` 关联（规范第 3.3 节）。

### 客户端的三态

“未知”不是“离线”：从没收到过、或收到得太久的状态显示为未知，不显示一个可能错误的结论（红线 C5）。Web 与 Android SDK 都把服务端的最后一次说法信任 180 秒（`ttlMs` 默认 180000），过期后回到未知，会话列表与聊天页读同一个来源（[`web/packages/client/src/presence.ts`](https://github.com/dishikun2786-prog/Talkwire/blob/main/web/packages/client/src/presence.ts)、[`PresenceTracker.kt`](https://github.com/dishikun2786-prog/Talkwire/blob/main/android/talkwire-core/src/main/kotlin/io/talkwire/core/sync/PresenceTracker.kt)）。

| 状态 | 条件 |
| --- | --- |
| 在线 | 最近一次收到的帧是 `on`（或快照里 `online: true`），且未过期 |
| 离线 | 最近一次收到的帧是 `off`（或快照里 `online: false`），且未过期 |
| 未知 | 从没收到过，或已过期 |

客户端 SDK 的用法见 [Web SDK](/client/web) 与 [Android SDK](/client/android)。

## 推送令牌

客户端用 `reg_token` 注册离线推送令牌、用 `unreg_token` 注销，两者都没有应答（规范第 17 节）：

```json
{"action":"reg_token","token":"<推送令牌>","provider":"fcm"}
{"action":"unreg_token","token":"<推送令牌>","provider":"fcm"}
```

这里的 `token` 放的是推送令牌，不是登录令牌（F27）。缺 `token` 时静默丢弃；客户端**应当**在每次登录后注册，退出登录前注销（规范第 17 节）。

### 服务商

`provider` 可选，取 `fcm`（缺省，参考服务只有它）、`apns` 或 `webpush`（规范第 17 节）。服务端配置了哪些服务商，strict 档的登录应答用 `capabilities` 里的 `push:<服务商>` 宣告；配置了 Web Push 时还带 VAPID 公钥 `vapidKey`（规范第 6.1 节）。

| 服务商 | 令牌 | 载荷 |
| --- | --- | --- |
| `fcm` | FCM 令牌 | 只含数据的消息，数据表与参考服务一直发的相同，现有 Android 客户端照常工作 |
| `apns` | APNs 令牌 | `aps` 里的提醒（标题、副标题和正文，或本地化键），另把数据作为顶层自定义键 |
| `webpush` | 浏览器 `PushSubscription` 的 JSON 字符串，服务端规范化后保存 | JSON 对象 `{"title", "body", "locKey", "data"}`（空字段省略），按 RFC 8291 加密，用服务端的 VAPID 密钥签名（RFC 8292） |

载荷一列出自 [`server/push/push.go`](https://github.com/dishikun2786-prog/Talkwire/blob/main/server/push/push.go) 的包文档。不合法的令牌被静默拒绝：没有应答，服务端只记日志，不记令牌本身（规范第 17 节）。服务商的配置见[环境变量](/server/env)。

### 推给谁

推送只发给没有连接的设备注册的令牌；一台设备都没连上时就是该用户的全部令牌，与参考服务相同（规范第 17 节）。有连接的设备不收推送，即使它此刻没有订阅该会话。

| 情形 | 令牌归属 |
| --- | --- |
| 不带 `device` 登录时注册的令牌 | 属于设备 `""` |
| 带 `device` 登录的设备 | 每个服务商只保留一个令牌，注册新的即删掉旧的 |

每次登录后重新注册，令牌就总挂在当前设备上（规范第 17 节）。端到端加密消息的推送只发给发布身份密钥的那台设备（规范第 13.1 节）；它的通知不带发送者也不带预览，APNs 与 Web Push 上用本地化键 `TALKWIRE_E2E_MESSAGE` 提示（`server/push/push.go`）。

“只推给没有连接的设备”有一个推论：群成员重连后、重新订阅群之前，他在线却收不到该群的实时消息，宿主推送通道也不会触发（规范第 7 节）。消息不会丢，按[同步与补拉](/protocol/sync)用 `get` 补回。
