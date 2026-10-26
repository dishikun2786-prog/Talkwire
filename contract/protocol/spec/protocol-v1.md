# Talkwire 客户端协议 v1（草稿）

状态：草稿，2026-10-24；2026-10-25 补入 Talkwire 服务端 Phase 1 的结果：strict 档新增的字段与错误码（第 0.3、3.3、4、6.1 节）、模块未安装时的 501（第 4.1 节）、推送服务商（第 17 节）、多端同时在线（第 6.2 节及各节的“多端”段）、D9（第 18 节）；同日补入 strict 档 `get` 应答的 `readSeq`（第 8、9.3、12.3 节）。依据参考服务（原版 `server/im-go`，提交 `33e23250`）的源码和 `contract/protocol/transcripts/` 中录制的黄金对话写成。协议事实的逐条出处见 `docs/protocol-findings.md`（下文以 F 编号引用）。

本文的目标读者是要独立实现客户端或服务端的团队。只读本文、JSON Schema（`contract/protocol/schema/`）和黄金对话，就应能写出与参考服务逐帧兼容的实现。

## 0. 读法

### 0.1 措辞

- **必须 / 禁止**：不遵守就不兼容。
- **应当 / 不应当**：有正当理由时可以不遵守，但要写明理由。
- **可以**：可选。

### 0.2 出处标记

每个动作的小节标题后有一个标记，说明这一节的可信度：

| 标记 | 含义 |
| --- | --- |
| 【录】 | 黄金对话里有实测帧，并核对过源码 |
| 【码】 | 只读源码得到，还没有录制 |
| 【测】 | Talkwire 服务端新增、参考服务没有的行为：没有黄金对话，由 `server/` 的自动化测试验证 |

模块组（第 13–16 节）已录制，只有第 16.4 节末尾注明的两种情况尚未录制。

### 0.3 两种一致性档位

参考服务有若干已知缺陷（第 18 节）。服务端实现声明自己属于哪一档：

- **legacy**：对所有黄金对话逐帧一致，包括缺陷行为。Talkwire 迁移原版项目时用这一档。
- **strict**：修正第 18 节列出的缺陷，其余与 legacy 一致。新项目默认用这一档。

Talkwire 服务端用环境变量 `TALKWIRE_PROTOCOL_PROFILE=legacy|strict` 选择档位，默认 strict；迁移原版项目、要与原版客户端逐帧兼容时设 legacy。strict 档相对 legacy 只做三类改动：

- **新增字段**，旧客户端忽略即可：成功的 `login` 和 `pres` 快照应答带 `id`（第 3.3 节）；`login` 应答带 `protocol`、`capabilities`、`vapidKey`（第 6.1 节）；错误帧带 `errCode`（第 4.3 节）；`get` 应答带本人的已读位置 `readSeq`（第 9.3 节）。
- **新增动作**，在 `capabilities` 里宣告，旧客户端不会发出：`sync_summary`（第 8.1 节）。legacy 档对它们像参考服务一样回 400 `unknown action`。
- **修正第 18 节的缺陷**。Talkwire 服务端修正了 D6 以外的全部各项；D6 两档相同，留给协议 v2。

多端同时在线（第 6.2 节）不分档位：客户端在 `login` 里带 `device` 才启用，不带时两档都与参考服务一致。

客户端**必须**在两种档位下都能正常工作。具体做法是：不依赖任何缺陷行为；对缺陷行为可能产生的帧做容错处理（第 18 节逐条写明）。

## 1. 术语

| 术语 | 含义 |
| --- | --- |
| 宿主 | 接入 Talkwire 的业务系统，负责账号、好友关系、资料、推送通道、媒体存储 |
| `appUserId` | 宿主侧的用户 id，任意字符串，Talkwire 不解释 |
| uid | Talkwire 侧的用户 id，由 `appUserId` 推导（第 5.1 节） |
| topic | 会话。三种：单聊、群、保留 topic（第 5 节） |
| 规范 topic | 服务端存储会话用的名字：单聊是 `p2p_…`，群是 `grp_…` |
| seq | 消息在 topic 内的序号，从 1 开始，严格递增，不复用 |
| 连接 | 一条 WebSocket 连接 |
| 订阅 | 连接与 topic 的关联。服务端只把推送发给已订阅该 topic 的连接（第 7 节） |

## 2. 传输

| 项目 | 规定 | 出处 |
| --- | --- | --- |
| 端点 | `GET /ws` 或 `GET /ws/im` 升级为 WebSocket，两者等价 | 码 |
| 帧 | 文本帧，UTF-8，每帧恰好一个 JSON 对象 | 录 |
| 单帧上限 | 256 KB（262144 字节）。超过时服务端直接断开连接，不发错误帧 | 录 F28 |
| 心跳 | 服务端约每 27 秒发一次 ping，60 秒内收不到任何帧（含 pong）就断开。客户端**必须**回 pong（WebSocket 库通常自动处理） | 码 |
| 写超时 | 服务端单帧写入超过 10 秒即断开 | 码 |
| 发送缓冲 | 服务端每连接缓冲 256 帧，满了就断开连接，迫使客户端重连后重新同步 | 码 |
| 连接上限 | 服务端连接数达到上限（默认 10000）时，升级请求返回 HTTP 503 | 码 |
| Origin | 不校验 | 码 |
| 未登录连接 | 没有登录时限，只要回 pong 就一直保持 | 码 F38 |

客户端**必须**把连接断开视为正常事件：重连后按第 8 节重新同步。

## 3. 帧结构

### 3.1 两族帧

| 族 | 判别字段 | 用于 |
| --- | --- | --- |
| action 帧 | `action` | 绝大多数请求、应答和推送 |
| type 帧 | `type` | 转发结果、群 AI 设置、E2EE 密钥相关应答、通话信令推送 |

一帧只会有其中一个判别字段。客户端解析时**必须**先看 `type`，没有再看 `action`。

### 3.2 字段的出现规则

- 字段值为零值（`0`、`false`、`""`、空数组、`null`）时，服务端通常**省略**该字段。客户端**必须**把“缺省”当作零值，例如 `get_groups` 在没有群时不带 `groups` 字段（F16）。
- 例外：type 帧里声明为必有的字段（如 `ok`、`found`、`results`）即使为零值也会出现。JSON Schema 里有标注。
- 双方**必须**忽略不认识的字段。协议只会以“新增字段”的方式演进（ADR-0001 决定 4）。
- 时间戳一律是 Unix 毫秒整数。

### 3.3 请求与应答的关联

客户端在请求里放 `id`（字符串，同一连接内唯一）。服务端的应答把 `id` 原样带回，`action` 与请求相同。

有三类例外，客户端**必须**特殊处理：

| 情形 | 应答的样子 | 怎么关联 |
| --- | --- | --- |
| 成功的 `login` | 不带 `id` | 按 `action=login` 关联；一条连接同时只应有一个在飞的 `login` |
| `pres` 查询成功 | 不带 `id`，带 `what=snapshot` | 按 `action=pres` 且 `what=snapshot` 关联 |
| type 帧族的请求 | 用 `reqId` 关联 | 客户端在请求里放 `reqId`，应答带回同一个 `reqId` |

出处：F1、F21、F31。失败的 `login` 和失败的 `pres` 会带 `id`。

strict 档成功的 `login` 和 `pres` 快照也带 `id`（红线 R19）。按上表关联在两档下都成立；客户端也**可以**先按 `id` 匹配，匹配不到再按 `action`。

有些请求没有任何应答（第 9 节各动作里写明“无应答”）。客户端**不应**为它们等待应答。

### 3.4 推送

推送是服务端主动发出的帧，不带 `id`。同一个 `action` 名不会既用作请求的应答又用作推送（例如撤回的应答是 `del`，推送给他人的是 `recalled`），只有第 3.3 节的 `pres` 例外：`pres` 推送没有 `what=snapshot`。

## 4. 错误

### 4.1 错误帧

action 帧族的错误应答形如：

```json
{"id": "r7", "action": "pub", "code": 400, "reason": "message too long"}
```

| `code` | 含义 |
| --- | --- |
| 200 | 成功 |
| 201 | 已创建（只用于 `create_grp`） |
| 400 | 请求不合法 |
| 401 | 未登录，或没有权限（不是成员、不是好友、角色不够） |
| 404 | 对象不存在 |
| 409 | 状态冲突（如群主不能直接退群、群已加密） |
| 429 | 超过频率限制 |
| 501 | 动作属于服务端没有安装的模块，`reason` 为 `module not enabled: <模块名>`（Talkwire 服务端；参考服务没有模块开关） |
| 500 | 服务端内部错误，可以重试 |

`reason` 是给人看的说明，**不透明**：有中文也有英文，措辞随版本变化（F28）。客户端**必须**只根据 `code`（以及第 9 节里明确列出的少数 `reason` 值）做逻辑判断，不得解析其他 `reason` 文本。strict 档的错误帧另带 `errCode`（第 4.3 节），客户端**应当**优先按它判断。

type 帧族的错误不用 `code`，而是 `ok: false` 加 `reason`。这里的 `reason` 是固定的小写英文枚举（如 `not_member`），可以用于判断。

### 4.2 协议层错误

| 情况 | 服务端行为 | 出处 |
| --- | --- | --- |
| 帧不是合法 JSON | 回 `{"action":"error","code":400,"reason":"invalid JSON: …"}`，不带 `id`，连接继续可用 | 录 F14 |
| `action` 为空 | 静默丢弃 | 码 F38 |
| 未知 `action` | 回 400，`reason` 为 `unknown action: <名>`，带 `id` | 码 F38 |
| 未登录就发其他请求 | 回 401 `not authenticated`，带 `id`；“无应答”类动作直接丢弃 | 录 F15 |

### 4.3 `errCode`（strict 档）

strict 档的 action 帧族错误帧多一个 `errCode`：小写下划线形式的稳定代码，不随 `reason` 的措辞变化。

```json
{"id": "r7", "action": "pub", "code": 400, "reason": "message too long", "errCode": "message_too_long"}
```

服务端按以下顺序得出 `errCode`：`reason` 在下表对应的固定文案里时取表中的值；`reason` 以 `not friends: `、`unknown action: `、`invalid JSON: `、`module not enabled: ` 开头时分别取 `not_friends`、`unknown_action`、`invalid_json`、`module_not_enabled`；`reason` 本身已是小写下划线形式（E2EE 的原因、宿主给的通话原因）时原样使用；否则按 `code` 取通用值：400 `bad_request`、401 `unauthorized`、404 `not_found`、409 `conflict`、429 `rate_limited`、501 `module_not_enabled`，其余 `internal_error`。客户端**必须**容忍不认识的 `errCode`，退回按 `code` 处理。

| `not_authenticated` | 未登录 |
| `token_required` | 登录缺令牌 |
| `invalid_token` | 令牌无效、过期，或 strict 档下不是访问令牌 |
| `already_logged_in` | 连接已登录 |
| `invalid_device` | 登录的 `device` 不合法（第 6.2 节） |
| `too_many_devices` | 账号在线设备已达上限，新设备不能登录（第 6.2 节） |
| `bad_request` | 请求不合法（通用） |
| `missing_field` | 缺必填字段 |
| `message_too_long` | 消息超长 |
| `group_name_too_long` | 群名超长 |
| `rate_limited` | 超过频率限制 |
| `unsupported_topic` | 不能对 `me`、`fnd` 做这个操作 |
| `not_a_group` | 只能对群做的操作用在了单聊上 |
| `invalid_member_role` | 角色不合法 |
| `invalid_call_type` | 通话类型不是 `voice` 或 `video` |
| `cannot_target_self` | 不能对自己做（踢自己、改自己角色、呼叫自己） |
| `target_not_member` | 对象不是群成员 |
| `not_friends` | 不是好友 |
| `relationship_check_failed` | 向宿主核对关系失败，可以重试 |
| `not_authorized` | 没有权限（通用） |
| `not_member` | 不是该会话的成员 |
| `not_group_manager` | 需要群主或管理员 |
| `not_group_owner` | 需要群主 |
| `cannot_remove_owner` | 不能踢群主 |
| `owner_cannot_leave` | 群主要先转让或解散才能退群 |
| `admin_limit_reached` | 管理员人数已达上限 |
| `group_not_found` | 群不存在 |
| `group_full` | 群人数已达上限 |
| `message_not_found` | 要撤回的消息不存在、已撤回或不是自己发的 |
| `qr_token_invalid` | 二维码令牌不存在或已过期 |
| `join_request_handled` | 入群申请已被处理 |
| `join_request_mismatch` | 入群申请不属于请求里的群 |
| `call_not_found` | 通话不存在、已结束，或本设备不在这通电话里 |
| `call_ended` | 通话已结束 |
| `call_already_handled` | 通话已被接听、拒接或结束 |
| `call_not_active` | 通话尚未接通 |
| `already_video` | 已经是视频通话 |
| `offline` | 对方不在线 |
| `unknown_action` | 未知动作 |
| `invalid_json` | 帧不是合法 JSON |
| `module_not_enabled` | 动作所属的模块没有安装 |

`reason` 文案到 `errCode` 的权威映射是服务端 `server/profile.go` 的 `errCodes` 表；单测 `TestEveryErrorReasonHasACode` 保证服务端发出的每个固定 `reason` 都在表里。type 帧族的错误本来就用小写英文的 `reason`，没有 `errCode`。

## 5. 身份与地址

### 5.1 uid

```
uid = "uid_" + base64url_nopad( SHA-256( UTF-8(appUserId) )[0:8] )
```

结果固定 15 个字符。测试向量（由独立实现算出，`tools/conformance/harness/ids_test.go` 校验）：

| `appUserId` | uid |
| --- | --- |
| `alice` | `uid_K9gGyX8OAK8` |
| `bob` | `uid_gbY32PzSxto` |
| `00000000-0000-4000-8000-000000000001` | `uid_EeWU9IGVjBA` |
| `用户一` | `uid_7c5xGaxvxPw` |

以 `usr` 开头的字符串也被服务端当作用户 uid（早期系统遗留的格式）。新实现**不应**再生成这种 uid。

客户端可以本地算出任何用户的 uid，但不能反推 `appUserId`。

### 5.2 单聊 topic

```
(a, b) = 两个 uid 按字节升序排列（ASCII 序，大写字母排在小写之前）
topic  = "p2p_" + base64url_nopad( SHA-256( a + ":" + b )[0:8] )
```

测试向量：`alice` 与 `bob` 的单聊 topic 是 `p2p_E5DsdYCpVC8`（参数顺序无关）；`00000000-0000-4000-8000-000000000001` 与 `用户一` 是 `p2p_VVXxwJ61Kvw`。

### 5.3 群 topic

`"grp_"` 加 12 个字母或数字，由服务端在建群时生成。客户端**必须**把它当作不透明字符串。

### 5.4 保留 topic

| topic | 说明 |
| --- | --- |
| `me` | 可以 `sub`，没有其他作用；不能 `pub` |
| `fnd` | 不支持，`sub` 返回 400；不能 `pub` |
| `*` | 只用于 `pres` 查询，表示“所有与我共享会话的人” |

### 5.5 单聊的三种写法

同一个单聊会话在线上会以三种字符串出现，客户端**必须**都认（F4、F29）：

| 出现位置 | 值 |
| --- | --- |
| 客户端发请求时的 `topic` | 推荐写**对方的 uid**；也可以写规范 topic |
| `data` 推送的 `topic` | 永远是**发送方的 uid** |
| `get` 应答里每条消息的 `topic` | 永远是规范 topic `p2p_…` |
| `recalled`、`info` 推送的 `topic` | 照抄发送方请求里写的值：可能是**接收方自己的 uid**，也可能是 `p2p_…` |
| `topic_cleared` 推送的 `topic` | 对方的 uid |

客户端映射规则：

1. `topic` 以 `grp_` 开头：群会话。
2. `topic` 以 `p2p_` 开头：单聊。对方是 `from`；如果没有 `from`（只在历史消息里出现），用第 5.2 节从本地已知的好友 uid 反查。
3. `topic` 等于自己的 uid：单聊，对方是 `from`。
4. 其他 uid：单聊，对方就是这个 uid。

只有发给已存在单聊的请求会被接受：对方 uid 写成 topic 时，服务端会在第一次使用时向宿主校验好友关系并建立单聊（F6）；写成 `p2p_…` 时只校验调用者是否已是该单聊的成员。

## 6. 登录

### 6.1 请求【录】

```json
{"id": "l1", "action": "login", "token": "<JWT>"}
```

`token` 是宿主签发的 HS256 JWT，与服务端共享密钥。服务端只要求 `sub`（即 `appUserId`）存在且签名、有效期合法。

`device`（可选，Talkwire 服务端【测】）：本连接所在设备的名字，见第 6.2 节。

| 结果 | 应答 |
| --- | --- |
| 成功 | `{"action":"login","code":200,"uid":"uid_…"}`，**不带 `id`** |
| 缺 `token` | 401 `token required` |
| 令牌无效或过期 | 401 `invalid token` |
| 连接已登录 | 400 `already logged in` |
| `device` 不合法（第 6.2 节） | 400 `invalid device` |
| 已在上限台数的设备上在线，本次是一台新设备（第 6.2 节） | 409 `too many devices` |

legacy 档接受任何带 `sub` 的有效 HS256 令牌，包括刷新令牌、管理员令牌、没有 `type` 的令牌（F2，红线 R18）。strict 档只接受 `type=access` 且 `role` 为普通用户的令牌，其余一律 401 `invalid token`。宿主**应当**只把访问令牌交给客户端，这样两档行为相同。

strict 档的成功应答另带 `id`、`protocol`（协议版本，整数 `1`）、`capabilities`（字符串数组）、`vapidKey`（配置了 Web Push 时的 VAPID 公钥）：

```json
{"id": "l1", "action": "login", "code": 200, "uid": "uid_…", "protocol": 1,
 "capabilities": ["errcode", "reply-id", "multi-device", "module:e2ee", "push:fcm"]}
```

`capabilities` 的取值：`errcode`（错误帧带 `errCode`）、`reply-id`（登录与在线快照应答带 `id`）、`multi-device`（支持第 6.2 节的多端在线）、`sync-summary`（支持第 8.1 节的 `sync_summary`）、`module:<名>`（已安装的模块：`call`、`groupjoin`、`groupai`、`e2ee`）、`push:<服务商>`（已配置的推送服务商：`fcm`、`apns`、`webpush`）。客户端**必须**忽略不认识的能力。

### 6.2 同一账号的多个连接

**不带 `device` 的登录**是参考服务的全部行为（F20）：每个账号同一时刻只保留一个这样的连接，新连接登录成功后，旧连接被关闭。关闭前服务端**可能**给旧连接发一帧 `{"action":"login","code":401,"reason":"logged in from another device"}`，但不保证送达（F3）。

客户端在连接被意外关闭时**不应**自动无限重连抢回会话；收到上述 401 时**应当**停止自动重连并提示用户。

**带 `device` 的登录**【测】（Talkwire 服务端，计划任务 1.12）：`device` 是客户端为本机选定并持久保存的名字，1–64 个字符，只含字母、数字和 `. _ : -`。同一台设备每次登录都**必须**用同一个名字，不同设备**必须**用不同的名字。只有同名设备的新登录才顶替旧连接，不同设备的连接同时在线。不带 `device` 等于设备 `""`，所以不带它的客户端仍像参考服务那样互相顶替。一个账号最多同时在 `TALKWIRE_MAX_DEVICES_PER_USER` 台（默认 10）设备上在线，超出时新设备的登录得到 409 `too many devices`；这个上限在登录时检查，几台新设备同时登录时可能略微超出。

多台设备同时在线时：

| 方面 | 规则 | 节 |
| --- | --- | --- |
| 在线状态 | 每次登录都推 `on`；只有最后一台设备离开才推 `off` | 12.1 |
| 自己发的消息 | 自己的其他在线设备收到同一条 `data`；单聊的 `topic` 写成对方 uid | 9.2 |
| 自己的已读、撤回、清空、建群、入群、退群、改群名 | 同步到自己的其他设备；“正在输入”不同步 | 9.5、10、11、12.3 |
| 离线补投 | 清空和群事件按设备补投、按设备回执：设备离开期间的变更，在它下次登录时补投 | 10.10、11.3 |
| 推送 | 只推给没有连接的设备注册的推送令牌 | 17 |
| 端到端加密 | 只属于发布身份密钥的那台设备 | 13.1 |
| 通话 | 被叫的所有设备都响铃；接听的设备拿到通话，其余设备收到 `answered_elsewhere` | 16 |

发起动作的设备不会收到自己动作的回显。服务端按设备而不是按连接判断，所以同一设备已被顶替的旧连接里迟到的帧，也不会回显给这台设备的新连接。

客户端**应当**看到 `capabilities` 里有 `multi-device` 后才带 `device`。对不认识 `device` 的服务端带上它也无害：服务端忽略未知字段，照旧互相顶替。

### 6.3 登录后服务端主动补投的帧

登录成功后，服务端异步补投离线期间积压的三类帧，顺序不保证：

1. `topic_cleared`（第 11.3 节）
2. 离线群事件（第 10.10 节）
3. `e2e_deliver`（第 13.4 节）

这些帧可能早于、也可能晚于客户端登录后发出的第一个请求的应答。多端时只补投本设备还没拿到的（第 10.10、11.3、13.1 节）。

## 7. 订阅

服务端只把推送发给**已订阅**对应 topic 的连接（F39）。订阅属于连接，断线即消失，**登录不会恢复订阅**。

连接在下列情况下被订阅：

| 情况 | 订阅的 topic |
| --- | --- |
| 发 `sub` | 该 topic |
| 发 `get`、`pub` | 该 topic |
| 发 `sync_summary`（strict 档，第 8.1 节） | 自己所在的全部群与单聊 |
| 发 `create_grp` | 新群 |
| 自己在线时被拉进群（建群、邀请、入群审核通过） | 该群 |
| 对方给自己发单聊消息（自己在线） | 该单聊 |
| 收到补投的 `grp_sync` | 该群 |

`leave` 取消订阅；被踢、退群、群解散时也会取消。

推论：群成员重连后，在重新 `sub` 之前收不到该群的实时消息；他此时在线，宿主推送通道也不会被触发。消息不会丢——它们已入库，用 `get` 补回即可。

## 8. 同步算法（客户端）

客户端**必须**按下面的顺序重建状态。每次登录成功后都要做，不只是首次。

1. 登录。
2. 发 `get_groups`，得到所在的全部群。对本地有但列表里没有的群：本地标记为已退出（可能是离线时被踢或群被解散，而补投事件尚未到达）。
3. 对每个群发 `sub`（或直接发第 4 步的 `get`，它会顺带订阅）。
4. 对每个会话（群与本地已知的单聊）发 `get`，`afterSeq` = 本地该会话的最大 seq，循环直到返回的消息数小于 `limit`。服务端宣告了 `sync-summary` 时，先发 `sync_summary`，只对有变化的会话发 `get`（第 8.1 节）。补拉时按第 8.2 节处理 seq 的空缺。
5. 用 `get` 应答里的 `clearVersion`、`clearBeforeSeq` 校正本地：若服务端的 `clearVersion` 大于本地记录，删除本地 seq ≤ `clearBeforeSeq` 的消息（第 11.3 节）。应答带 `readSeq` 时（strict 档），把本地该会话的已读位置推进到它；本地已经更大时不动，已读位置只前进不后退。没有这一步，新设备或换账号后拉回的历史会全部算作未读。
6. 处理补投帧（第 6.3 节），按要求回执。
7. 需要在线状态时发 `pres`（第 12 节）。

单聊不需要预先 `sub` 才能收到对方的新消息（对方 `pub` 时服务端会订阅在线的接收方），但第 4 步的补拉仍然必须做。

### 8.1 `sync_summary` 会话变更摘要【测】

只在 strict 档、登录应答的 `capabilities` 含 `sync-summary` 时可用（Talkwire 服务端）。它让重连的客户端用一次往返知道哪些会话有变化，不必对每个会话各发一次 `get`。

请求：`{"id":"s1","action":"sync_summary"}`，无参数。

成功应答：

```json
{"id":"s1","action":"sync_summary","code":200,"topics":[
  {"topic":"grp_…","lastSeq":42,"isGroup":true,"clearVersion":1,"clearBeforeSeq":12,"readSeq":40,"readsAt":1793000000000},
  {"topic":"p2p_…","lastSeq":7,"isGroup":false,"peer":"uid_…","readSeq":7}]}
```

`topics` 列出调用者所在的全部群与单聊，按 `topic` 排序，一个都没有时省略。每项：

| 字段 | 说明 |
| --- | --- |
| `topic` | 规范 topic：群是 `grp_…`，单聊是 `p2p_…` |
| `lastSeq` | 该会话分配过的最大 seq，含已撤回的；没有消息时为 0 |
| `isGroup` | 是否为群 |
| `clearVersion`、`clearBeforeSeq` | 清空状态（第 11.3 节），从未清空时省略 |
| `peer` | 单聊的对方 uid；群没有 |
| `readSeq` | 调用者本人的已读位置，与 `get` 应答的 `readSeq` 同义（第 9.3 节）：单聊总有记录可查，群只在宿主开启已读回执时有；没有时省略 |
| `readsAt` | 只对群、且宿主开启了已读回执时出现：其他成员最近一次推进已读位置的时刻（Unix 毫秒）。调用者自己的推进只改变 `readSeq` |

服务端在读取摘要**之前**把本连接订阅到所有这些会话（第 7 节），所以客户端不再 `get` 的会话也能收到实时帧。在读取之后提交的消息以推送送达，之前提交的计入 `lastSeq`；两者都覆盖到的消息会被拉到两次，客户端按 seq 去重。

客户端对本地每个会话（未退出的群与本地已知的单聊）**应当**这样判断要不要 `get`：

- 摘要里没有它：群出现在 `get_groups` 里却不在摘要里时照常 `get`；单聊说明服务端还没有这个会话，不需要 `get`。
- `lastSeq` 大于本地 seq 连续段的末尾（补拉的起点，红线 C3；已撤回的 seq 与清空水位以下都算在段内）：有新消息或空缺，`get`。
- `clearVersion` 大于本地记录：`get`，由应答校正清空状态。
- `readsAt` 与本进程上次见到的不同：`get`，由应答带回全部已读游标。客户端可以只在内存里记它，进程重启后把出现 `readsAt` 的群各拉一次。
- 其余情况不 `get`；`readSeq` 大于本地已读位置时直接推进本地的已读位置。

客户端**不应**仅凭摘要创建本地没有的单聊：用户在本地删除过的会话会因此每次重连都回来。

错误：401 未登录；500。legacy 档回 400 `unknown action: sync_summary`。

### 8.2 补拉时的 seq 空缺

已撤回的消息不返回，但它的 seq 不复用（第 9.3 节）。客户端从没收到过、又在离线时被撤回的消息，在本地留下一个空缺；客户端如果把空缺当成“还没拉到”，每次重连都会从这里重拉，空缺之后超过一次补拉上限的部分永远补不到。所以补拉时客户端**必须**这样记账：

- 一页 `get` 返回后，`(下界, 本页最大 seq]` 里服务端没有返回的 seq 一律记为已撤回：本地有这条消息就按撤回处理（第 9.5 节），没有就只记下这个 seq。下界在带 `afterSeq` 时是 `afterSeq`，不带时是本页最小 seq 减 1（更早的历史只是没加载，不是空缺）。
- 返回条数小于 `limit`（已到头）且从第 8.1 节知道 `lastSeq` 时，`(本页最大 seq, lastSeq]` 同样记为已撤回；空页时从下界算起。
- 低于清空水位的 seq 不需要记。

记下的 seq 与已有的消息一起构成连续段，下次补拉从段尾开始。

## 9. 消息

### 9.1 `pub` 发送【录】

请求字段：

| 字段 | 必须 | 说明 |
| --- | --- | --- |
| `topic` | 是 | 对方 uid、`p2p_…` 或 `grp_…` |
| `content` | 否 | 文本；按 Unicode 码点计不超过 20000（宿主可调小） |
| `contentType` | 否 | MIME 风格的类型，服务端不解释，原样存储与转发 |
| `fileUrl`、`fileName` | 否 | 媒体地址；媒体本身不经过本协议 |
| `duration` | 否 | 音视频时长（秒） |
| `extra` | 否 | 任意 JSON 值，原样存储与转发；`null` 视为没有 |
| `clientMsgId` | 应当 | 客户端生成的唯一 id，用于去重（F19） |

成功应答：`{"id":…, "action":"pub","code":200,"topic":<请求里的 topic>,"seq":n,"ts":毫秒}`，若请求带 `extra` 则原样带回。

其他成员收到 `data` 推送（第 9.2 节）。发送者自己的连接不会收到。

去重：同一 `topic` 下 `clientMsgId` 已存在时，应答返回原来的 `seq` 和 `ts`，不再推送（F19）。客户端在不确定上次是否发送成功时（例如应答丢失），**应当**用同一个 `clientMsgId` 重发。不带 `clientMsgId` 时不去重。

| 错误 | 条件 |
| --- | --- |
| 429 | 该账号 10 秒内已发送 20 条（宿主可调）。计数在其他校验之前进行，所以失败的 `pub` 也计数（F17） |
| 400 `topic required` | 缺 `topic` |
| 400 `cannot publish to me/fnd` | 发往保留 topic |
| 400 `message too long` | 正文超长（F28） |
| 401 | 不是成员；单聊时不是好友 |
| 409 `group_encrypted` | 群已开启端到端加密，不接受明文（第 13 节） |
| 500 | 存储失败 |

### 9.2 `data` 推送【录】

```json
{"action":"data","topic":"…","from":"uid_…","seq":3,"ts":…,"content":"…","contentType":"…",
 "senderKind":"user","clientMsgId":"…","name":"群名"}
```

- `topic`：群是 `grp_…`；单聊是**发送方 uid**（第 5.5 节）。
- `senderKind`：`user`，或 `ai_assistant`（群 AI 的回复，`from` 为保留值 `assistant_xiaotaozi`）。
- `name`：只在群消息里出现，是当前群名。
- `forwardFromMsgId`、`forwardFromTopic`：转发来的消息才有（第 9.6 节）。
- `ts`：广播时刻，可能与入库时刻差几毫秒；客户端排序与展示**应当**以 `get` 返回的 `ts` 为准（F34）。
- 多端【测】：发送者自己的其他在线设备也收到这一帧（不在线的设备靠 `get` 补拉）。发给发送者自己设备的单聊帧，`topic` 是**对方** uid、`from` 是自己；客户端按“`from` 等于自己时，会话对象取 `topic`”归档。

### 9.3 `get` 拉取历史【录】

请求字段：`topic`（必须）、`limit`、`beforeSeq`、`afterSeq`。

| 参数组合 | 返回 |
| --- | --- |
| 都不给 | 最新的 `limit` 条 |
| `beforeSeq` | seq 小于它的最新 `limit` 条 |
| `afterSeq` | seq 大于它的最早 `limit` 条；同时给了 `beforeSeq` 时以 `afterSeq` 为准 |

`limit` 在 1–200 之外（含不给）时用默认页长 50（宿主可调，F18）。消息总是按 seq **升序**排列。没有“还有更多”的标志：返回条数小于 `limit` 即到头。已撤回的消息不返回，但它的 seq 不复用（F33）。

成功应答字段：

| 字段 | 说明 |
| --- | --- |
| `topic` | 请求里的写法 |
| `messages` | 消息数组，每条含 `topic`（规范 topic）、`seq`、`from`、`content`、`contentType`、`ts`，以及可选的 `fileUrl`、`fileName`、`duration`、`extra`、`senderKind`、`forwardFromMsgId`、`forwardFromTopic`。没有消息时省略 |
| `clearVersion`、`clearBeforeSeq` | 会话的清空状态（第 11.3 节），从未清空时省略 |
| `readCursors` | 只对群、且宿主开启了已读回执时出现：`[{uid, seq}]`，表示该成员已读到 seq（含） |
| `readSeq` | 仅 strict 档【测】：请求者本人在该会话已读到的 seq（含）。单聊总有记录可查（第 12.3 节）；群只在开启了已读回执时有，值与 `readCursors` 里本人那一项相同。没有记录时省略，legacy 档总是省略 |

单聊时，应答之后服务端还会发一帧对方的在线状态（`pres` 推送形态，F5）。

错误：400 `topic required`；401 不是成员 / 不是好友；500。

### 9.4 `sub` 与 `leave`【录】

`sub`：请求 `{topic}`。成功应答 `{action:"sub",code:200,topic}`。单聊时应答之后服务端发一帧对方在线状态。错误：400 `topic required`、400 `fnd not supported`、401。

`leave`：请求 `{topic}`，只取消本连接的订阅，不影响成员关系。成功应答 `{action:"leave",code:200,topic}`。未登录或缺 `topic` 时**无应答**（参考服务的行为，客户端不要等）。

- 不校验成员关系，也不校验 topic 是否存在：对未订阅的群、不存在的群都返回 200（F54）。
- 退订后不再收到该群的 `data`，但仍是成员，`get` 照常可用，所以可以随时按序号补拉。
- 单聊：服务端把订阅记在规范的 `p2p_…` topic 下。按对方 uid 发 `leave` 也返回 200，但**什么都不取消**，对方的在线状态照样推过来；要退订必须用 `p2p_…` topic。退订后对方再发来消息时，本连接会被重新订阅，在线状态随之恢复（F54）。

### 9.5 `del` 撤回【录】

请求：`{topic, seq}`。只能撤回自己发的消息，没有时间限制。

成功应答 `{action:"del",code:200,topic,seq}`。会话里其他在线成员收到：

```json
{"action":"recalled","topic":"…","seq":2,"from":"uid_…"}
```

单聊时这里的 `topic` 是撤回者请求里写的值（第 5.5 节）。多端【测】：撤回者自己的其他设备也收到 `recalled`，`from` 是自己。

撤回清空这条消息的正文和文件引用（`fileUrl`、`fileName`、`extra`），seq 仍占用。被撤回消息的文件如果不再被任何消息引用，服务端请求宿主删除（`POST /api/v1/internal/im/delete-media`）；仍被其他消息引用则保留（F53）。参考服务撤回视频时只回收主文件，缩略图的引用随 `extra` 一起清掉却不请求删除，成为再也回收不到的文件（读代码所得）。Talkwire 服务端在两档下都一并回收缩略图（计划任务 1.9）。

| 错误 | 条件 |
| --- | --- |
| 400 `topic and seq required` | 缺字段或 `seq` ≤ 0 |
| 404 `message not found or not yours` | 消息不存在、已撤回或不是自己发的 |
| 401 | 不是成员 |

撤回不进入离线队列。不在线的成员下次 `get` 时自然看不到这条消息；客户端在补拉时**应当**删除本地有、而服务端在相同 seq 区间里没有返回的消息，并把本地没有的那些 seq 也记为已撤回（第 8.2 节）。

### 9.6 转发【录】

两种转发都用 type 帧应答，按 `reqId` 关联（F21）。

**`forward_bundle`**：把一个会话里的多条消息按顺序复制进另一个会话。

请求：`{reqId, sourceTopic, sourceMsgIds:[seq…], targetTopic}`，`sourceMsgIds` 最多 50 个（宿主可调）。调用者必须同时是两个会话的成员。

应答：`{"type":"forward_bundle_result","reqId":…,"ok":true,"insertedSeqs":[…]}`；失败时 `ok:false` 加 `reason`：`not_authenticated`、`bad_request`、`too_many_messages`、`not_member`、`source_not_found`、`forward_chain_too_deep`、`internal_error`。失败时一条都不写入。目标会话的其他在线成员收到普通 `data` 推送，带 `forwardFrom*`。

**`forward_broadcast`**：把一条消息分别转发到多个会话，逐个独立成败。

请求：`{reqId, sourceTopic, sourceMsgId, targetTopics:[…]}`，最多 20 个目标（宿主可调）。目标**必须**写规范 topic；写对方 uid 会得到 `topic_not_found`（F22）。

应答：`{"type":"forward_broadcast_result","reqId":…,"results":[{topic, ok, seq?, reason?}]}`。整体失败（未登录、参数错、目标过多、不是源会话成员、源消息不存在、转发链过深）时，每个目标都带同一个 `reason`。

`reqId` 幂等：同一个 `reqId` 再发一次，即使目标不同，也原样返回第一次的结果，不再转发（F23）。客户端**必须**为每次转发生成新的 `reqId`，只在重试同一次转发时复用。

转发链：转发的副本再被转发时，链深度加一；超过上限返回 `forward_chain_too_deep`。

## 10. 群

### 10.1 角色

`owner`（恰好一个）、`admin`、`member`。管理权限（下文写作“管理员”）指 owner 或 admin。

| 操作 | owner | admin | member |
| --- | --- | --- | --- |
| 邀请 | 可 | 可 | 可 |
| 踢人 | 可 | 可（不能踢 owner 和其他 admin） | 否 |
| 改群名 | 可 | 可 | 否 |
| 清空记录 | 可 | 可 | 否 |
| 设置管理员 | 可 | 否 | 否 |
| 转让群主、解散 | 可 | 否 | 否 |

### 10.2 `create_grp`【录】

请求：`{name, members:[uid…]}`。`name` 去掉首尾空白后不能为空，不超过 30 个码点（宿主可调）。`members` 会去重并剔除自己；连同自己不超过 500 人（宿主可调）。每个成员都**必须**是调用者的好友，并且**必须**至少登录过一次（F13）。宿主可以用该用户自己的访问令牌调用 `POST /api/v1/im/user/register` 预先登记。

成功应答 **201**：`{action:"create_grp",code:201,topic:"grp_…",name}`。在线成员被订阅并收到 `{action:"grp_sync",topic,name,from}`；离线成员的通知进入离线队列（第 10.10 节）。

错误：400 `group name required`、400（名称过长）、400（人数超限）、401 `not friends: <uid>` 等、500。

### 10.3 `invite`【录】

请求：`{topic, user}`，只能用于群。任何成员都可以邀请；被邀请者必须是邀请者的好友。

成功应答 `{action:"invite",code:200,topic,user}`。对方已是成员时同样返回 200，不再通知，也不改变其角色：重复邀请一位管理员不会把他降成普通成员（F52）。这个判断在好友校验之前，所以已在群里、但已解除好友的人也返回 200。

邀请没有“对方接受”这一步，应答之前对方已经是成员（F55）：

- 被邀请者在线：被订阅并收到 `grp_sync`（带群名，`from` 为邀请者），之后的群消息直接送到，不需要再 `sub`。
- 被邀请者离线：`grp_sync` 进入离线队列，下次登录时收到，带 `eventId`，按第 10.10 节回执。
- 群里其余成员（含群主）**不**收到任何通知，要靠 `get_members` 才知道新人。

错误：400（缺字段）、400 `only group topics can be invited into`、404 `group not found`、401 `only group members can invite`、401 `not friends`、409（人数已满）、500。

### 10.4 `kick`【录】

请求：`{topic, user}`。成功应答 `{action:"kick",code:200,topic,user}`。

- 被踢者在线时收到 `{action:"grp_disband",topic,from}`（**不是**专门的“被移出”事件，F10），离线时进入离线队列。客户端收到 `grp_disband` 时一律当作“我已不在这个群”处理。
- 剩余在线成员（**含**操作者自己）收到 `grp_sync`（不带群名）。

错误：400 `cannot kick yourself`、400 `target is not a group member`、401（权限不够、踢 owner、admin 踢 admin）、500。

### 10.5 `leave_grp`【录】

请求：`{topic}`。

| 情况 | 结果 |
| --- | --- |
| 普通成员或 admin | 200；其余在线成员收到 `grp_sync`，`from` 为退出者；退出者自己不收 |
| owner，群里还有别人 | 409 |
| owner，自己是最后一人 | 200，群被解散 |

legacy 档的缺陷：不校验调用者是否是成员，也不校验 topic 是否是群（D1、D2，第 18 节）。strict 档对不是 `grp_` 的 topic 返回 400 `only group topics`（`errCode` 为 `not_a_group`），对非成员返回 401 `not a member of this conversation`（`not_member`），两种情况都不推送任何帧、不改变成员关系。客户端**禁止**对单聊发 `leave_grp`。

### 10.6 `update_grp` 改群名【录】

请求：`{topic, name}`，需管理员。成功应答 `{action:"update_grp",code:200,topic,name}`。其他在线成员收到带新群名的 `grp_sync`；操作者不收；离线成员**不**入队（F26），他们要靠下次 `get_groups` 得知新名。

### 10.7 `set_member_role`【录】

请求：`{topic, user, memberRole}`，`memberRole` 只能是 `admin` 或 `member`，只有 owner 能调用，不能改自己。admin 最多 10 人（宿主可调），超出返回 409 `admin limit reached`。

成功应答 `{action:"set_member_role",code:200,topic,user,memberRole}`（这四个字段总是出现）。目标已是该角色时也返回 200，但不写库、不广播。角色确有变化时，群里所有在线成员（含操作者）收到 `grp_sync`。不入离线队列。

### 10.8 `transfer_owner`、`disband_grp`【录】

`transfer_owner`：请求 `{topic, user}`，只有 owner 能调用，目标必须是成员。原 owner 变为普通成员。成功应答 `{action:"transfer_owner",code:200,topic,user}`；所有在线成员（含操作者）收到 `grp_sync`。

`disband_grp`：请求 `{topic}`，只有 owner 能调用。所有在线成员（含操作者）先收到 `grp_disband`，然后操作者收到 `{action:"disband_grp",code:200,topic}`；离线成员进入离线队列。

### 10.9 `get_groups`、`get_members`【录】

`get_groups`：无参数。应答 `{action:"get_groups",code:200,groups:[{topic,name,owner,members,encrypted?}]}`，没有群时省略 `groups`。`members` 是人数。

`get_members`：请求 `{topic}`，必须是成员。应答 `{…,topic,members:[{uid,role,joinedAt}]}`。按入群时间升序；同一时刻入群的成员之间顺序不保证。成员结构里预留了 `name` 字段，但参考服务从不填写，客户端要自己向宿主查询展示名。

成员信息里**禁止**出现 `appUserId` 等能定位宿主账号的字段：群成员之间不能借此互相识别或加好友。

### 10.10 离线群事件【录】

以下事件发生时，对当时不在线的相关成员各存一条事件，下次登录时补投（F24）：

| 触发 | 补投的帧 |
| --- | --- |
| 建群、邀请、扫码入群（仅 strict 档，入群者没连上的设备）、入群审核通过 | `grp_sync`（legacy 档带入队时的群名，strict 档带当前群名，D5） |
| 被踢、群解散 | `grp_disband` |
| 有人申请入群（发给管理员），仅 strict 档 | `group_join_request` |
| 自己的入群申请被处理，仅 strict 档 | `group_join_reviewed`，带 `joinStatus` |

改角色、改群名不入队。参考服务也想把后两种入队，但它的存储只接受 `grp_sync`、`grp_disband`，入队总是失败，不在线的管理员和申请人从来收不到（D9，F59）；strict 档修正。

补投帧比实时帧多一个 `eventId`（整数）。客户端处理完**应当**回执：

```json
{"action":"ack_group_event","eventId":1}
```

回执无应答。不回执的事件每次登录都会重放。补投的 `grp_sync` 如果对应的群已经不再包含该成员，服务端会在补投前静默丢弃它。

strict 档补投的帧凡带 `name` 的都用补投时的群名；群已不存在或群名读不到时，用入队时的群名（D5）。

事件保留 30 天。

多端【测】：事件实时发给成员在线的每台设备；他最近 30 天出现过、此刻不在线的设备另有一条入队，只补投给还没拿到的设备。回执只记本设备已拿到，最近 30 天出现过的设备都拿到后事件才删除。从没出现过的新设备第一次登录时也会收到尚未删除的事件，这对客户端无害：`grp_sync` 只是刷新提示，已经不在其中的群的事件会被丢弃。操作者自己的其他设备也会收到：例如自己建群时其他设备收到 `grp_sync`，自己退群时其他设备收到 `grp_disband`。

## 11. 会话清理

### 11.1 撤回

见第 9.5 节。

### 11.2 `del_topic` 清空【录】

请求：`{topic}`。单聊任一方可以清空；群只有群主或管理员可以，普通成员返回 401。**清空对所有成员生效，消息和媒体从服务端物理删除，不可恢复**（ADR-0001 决定 5）。

成功应答：

```json
{"id":…,"action":"del_topic","code":200,"topic":…,
 "clearVersion":1,"clearBeforeSeq":12,"deletedMessages":12,"deletedMedia":0}
```

`deletedMessages` 是删除的行数，已撤回消息留下的墓碑也算在内。`deletedMedia` 是清空时会话里仍被消息引用的不同文件数（主文件与缩略图都算，已撤回的消息不再引用文件）；服务端对其中不再被任何会话引用的文件请求宿主删除（F53）。

### 11.3 `topic_cleared` 推送与回执【录】

其他成员在线时立即收到：

```json
{"action":"topic_cleared","topic":"…","from":"uid_…","clearVersion":1,"clearBeforeSeq":12}
```

单聊里 `topic` 是对方（清空者）的 uid。客户端收到后**必须**删除本地该会话 seq ≤ `clearBeforeSeq` 的全部消息，并记录 `clearVersion`；`clearVersion` 不大于本地记录时忽略。

不在线的成员在下次登录时收到同样的帧，但多一个 `id`，值是离线记录的编号（数字字符串）。客户端处理完**应当**回执：

```json
{"action":"ack_topic_cleared","id":"<收到的 id>"}
```

注意这里的 `id` 不是请求关联用的 id（F9）。回执无应答。离线记录保留 30 天。

多端【测】：与第 10.10 节相同，按设备补投、按设备回执。清空者自己的其他设备也收到 `topic_cleared`，`from` 是自己。同一会话再次清空时离线记录换成新的水位，所有还没拿到新水位的设备都会再收到。

## 12. 在线状态与输入中

### 12.1 在线状态推送【录】

用户上线或下线时，与他共享至少一个会话、且当前在线的用户收到：

```json
{"action":"pres","topic":"uid_…","what":"on","online":true,"presRev":…}
```

下线时 `what:"off"`，`online` 省略。`presRev` 是每个用户单调递增的版本号，客户端**必须**丢弃 `presRev` 小于已知值的帧，以免乱序到达的旧状态覆盖新状态。

同一账号的新连接顶替旧连接时，不产生下线推送；共享会话的在线用户反而会再收到一次 `what:"on"`，`presRev` 递增（F51）。新连接自己的订阅不受旧连接清理的影响。

多端【测】：每台设备登录都推一次 `on`，只有最后一台设备离开才推 `off`，其中一台离开不产生推送。推送发到对方的每台在线设备。

### 12.2 `pres` 查询【录】

请求三选一：`{topic:"uid_…"}`、`{topics:["uid_…",…]}`、`{topic:"*"}`。

应答（**不带 `id`**）：

```json
{"action":"pres","code":200,"what":"snapshot",
 "states":[{"uid":"uid_…","online":false,"presRev":…,"lastSeenAt":…}]}
```

- 自己、非 uid 的项被丢弃，重复项去重，最多 500 个。
- `states` 的顺序与请求里的顺序一致；`*` 的顺序不保证。
- `lastSeenAt` 是最后下线时刻，未知时省略。

缺参数返回 400 `topic or topics required`（带 `id`）。

legacy 档的缺陷：可以查询任意 uid，包括陌生人（D3，红线 R22）。strict 档只返回与查询者共享会话的用户，其他 uid 从结果中剔除。

### 12.3 `note` 输入中与已读【录】

请求：`{topic, what, seq?}`。`what` 为 `kp`（正在输入）或 `read`（已读到 `seq`）。**无应答**；缺 `topic` 或 `what` 时静默丢弃。

会话里其他在线成员收到：

```json
{"action":"info","topic":"…","from":"uid_…","what":"read","seq":5}
```

单聊里 `topic` 照抄发送方请求里的写法（第 5.5 节）。多端【测】：`read` 也发给发送者自己的其他设备（已读状态属于用户而不是设备），`kp` 不发。

宿主开启已读回执时，群里的 `read` 会被存为该成员的已读游标，并在 `get` 时通过 `readCursors` 返回（第 9.3 节）。

单聊：legacy 档不存储已读，与参考服务相同。strict 档【测】把单聊的 `read` 存为**发送者本人**的已读位置，只前进不后退，超过会话最后一条消息的 seq 时按最后一条存；它只通过本人的 `get` 应答的 `readSeq` 返回，对方拿不到（对方仍只从实时的 `info` 得知）。这一项不受已读回执开关影响：开关管的是群成员互相看到已读，这里只是同一个用户的不同设备共享自己的已读位置。`seq` 缺省或不大于 0 的 `read` 不存储。

非成员发送时收到 `action:"note"` 的 401（F32）。

## 13. 端到端加密（模块组）【录】

服务端只转发密文：不解密，也不把密文写进消息历史，与 `pub`/`get` 完全分开。宿主可以整体关闭本模块（见第 13.5 节）。

黄金对话：`module/e2ee-p2p`、`module/e2ee-group`。

### 13.1 密钥包

| 请求 | 字段 | 应答（type 帧） |
| --- | --- | --- |
| `e2e_publish_bundle` | `reqId`、`e2eBundle`（见下） | `e2e_publish_bundle_result`：`{reqId, ok, reason?}` |
| `e2e_fetch_bundle` | `reqId`、`user`（对方 uid） | `e2e_bundle`：`{reqId, user, found, …}` |
| `e2e_otpk_count` | `reqId` | `e2e_otpk_count_result`：`{reqId, count}` |

`e2eBundle` 的字段：`registrationId`、`identityKey`、`signedPreKeyId`、`signedPreKey`、`signedPreKeySignature`、`kyberPreKeyId`、`kyberPreKey`、`kyberPreKeySignature`，以及可选的 `oneTimePreKeys:[{keyId, preKey}]`。二进制一律 base64。服务端只检查 `identityKey`、`signedPreKey`、`signedPreKeySignature`、`kyberPreKey`、`kyberPreKeySignature` 非空，缺任何一个返回 `ok:false, reason:"bad_request"`；不校验密钥本身。

再次发布会覆盖之前的身份密钥。每个账号只有一套密钥，`e2e_bundle` 里的 `deviceId` 恒为 1（F42）。

多端【测】：发布身份密钥的那台设备是本账号的加密设备。`e2e_deliver`、它的补投、`e2e_terminated`、`e2e_cleared` 和加密消息的推送通知都只给这台设备，其他设备（例如没有密钥的网页端）收不到；另一台设备重新发布即接管。v1 不支持多设备加密。从不带 `device` 的登录发布的身份（包括 Talkwire 1.12 之前发布的）属于设备 `""`：客户端开始带 `device` 登录后**必须**重新发布一次密钥包，否则加密消息一直等着设备 `""`。

`e2e_fetch_bundle` 只在满足以下任一条件时返回密钥：

1. 双方已有单聊；
2. 双方同在某个已加密的群；
3. 宿主确认双方是好友（每次请求都向宿主查询，不缓存）。

否则返回 `found:false`。“对方从未发布”同样是 `found:false`，两者刻意不区分，以免借此探测关系。

`found:true` 时应答带全部公钥字段，另带一个一次性预密钥 `oneTimePreKeyId`、`oneTimePreKey`，并把它从服务端删除；预密钥用完时这两个字段省略，其余照常返回。`e2e_otpk_count` 只能查自己还剩几个，客户端**应当**在不足时补充发布。

### 13.2 `e2e_send` 转发密文

请求：`{topic, user?, e2eKind, e2eMsgType?, content}`。

- `e2eKind`：`msg`（加密消息，`content` 必须非空）或 `sender_key`（群发送密钥分发）。
- `e2eMsgType`：libsignal 的消息类型，服务端原样转发。
- 单聊：必须给 `user`。放行条件同第 13.1 节，不满足时返回 401 `not friends`。`topic` 只是客户端自定的会话标签，服务端不校验、原样转发（F43）；参考客户端用规范单聊 topic。
- 群：`topic` 必须是已加密的群，调用者必须是成员；密文发给除自己外的全部成员。

成功应答 `{action:"e2e_send",code:200,topic}`。接收方在线时立即收到：

```json
{"action":"e2e_deliver","topic":"…","from":"uid_…","e2eKind":"msg","e2eMsgType":3,"content":"<base64>","ts":…}
```

接收方不在线时密文暂存（默认 14 天），在他下次登录时补投；补投后即从服务端删除，不会重复投递。

| 错误 | 条件 |
| --- | --- |
| 400 `bad request` | 缺 `topic` 或 `e2eKind`；`msg` 类型没有 `content` |
| 400 `user required` | 单聊没给 `user` |
| 401 `not friends` | 单聊不满足放行条件 |
| 409 `group_not_encrypted` | 群尚未开启加密 |
| 409 `e2ee_disabled` | 宿主关闭了本模块 |

### 13.3 群加密

`e2e_set_group_encrypted`：`{reqId, topic}`，只有 owner 能调用。应答是 type 帧 `e2e_set_group_encrypted_result`：`{reqId, ok, reason?}`，`reason` 可能是 `not_owner`、`topic_not_found`、`bad_request`、`e2ee_disabled`、`group_e2ee_disabled`。

成功后群里所有在线成员（含 owner）收到：

```json
{"action":"e2e_set_group_encrypted","topic":"grp_…","from":"uid_…","e2eGroupEncrypted":true,"reason":"owner_enabled"}
```

加密**不能关闭**。重复开启同样返回成功，并再次广播（F44）。开启后：

- 普通 `pub` 返回 409 `group_encrypted`；
- `get_groups` 里该群带 `encrypted:true`；
- 同群成员之间可以互取密钥包，无需是好友。

成员变化时剩余成员需要换新的发送密钥，服务端用同一个推送提示：

| 触发 | 谁收到 | `reason` |
| --- | --- | --- |
| 踢人 | 剩余全部在线成员，含操作者 | `member_removed` |
| 主动退群 | 剩余在线成员 | `member_left` |

客户端收到任何 `e2e_set_group_encrypted` 推送时，**必须**重新生成并分发自己的发送密钥。

### 13.4 清空与结束

| 请求 | 条件 | 推给谁 | 应答 |
| --- | --- | --- | --- |
| `e2e_clear` `{topic, user}` | 放行条件同第 13.1 节 | 对方：`{action:"e2e_cleared", topic, from}` | `{action:"e2e_clear",code:200,topic}` |
| `e2e_terminate` `{topic, user}` | 同上 | 对方：`{action:"e2e_terminated", topic, from}` | `{action:"e2e_terminate",code:200,topic}` |
| `e2e_group_clear` `{topic}` | 管理员 | 其他在线成员：`{action:"e2e_group_cleared", topic, from}` | `{action:"e2e_group_clear",code:200,topic}` |

`e2e_clear` 只清消息、保留加密会话；`e2e_terminate` 结束加密会话。这三种通知只发给当时在线的人，不进离线队列；客户端**应当**在对方重新上线后自行重发或在界面上提示。不满足条件时：前两者返回 401 `not authorized`，后者返回 401（权限不够）；缺字段返回 400 `bad request`。

### 13.5 宿主开关

宿主通过 `e2ee-config`（`{enabled, groupEnabled, envelopeTtlHours}`）控制本模块；取不到时按全部开启、密文保留 336 小时处理。`enabled:false` 时发布与发送被拒绝、取密钥包返回 `found:false`，但 `e2e_terminate` 照常可用，已排队的密文照常补投。`groupEnabled:false` 只禁止新开启群加密，已加密的群不受影响。

## 14. 扫码入群（模块组）【录】

黄金对话：`module/qr-join`。扫码入群**不需要**好友关系。

### 14.1 二维码令牌

`group_qr_token` `{topic, refreshQr?}`：任何成员可以取群的当前令牌，应答 `{topic, qrToken, expiresAt}`；重复调用返回同一个令牌。`refreshQr:true` 需管理员，生成新令牌，旧令牌立即失效。令牌默认 7 天过期（宿主可调）。非成员返回 401。

`group_qr_resolve` `{qrToken}`：任何登录用户可调用，用来在加入前预览。应答 `{topic, name, memberCount, alreadyMember?, isFull?, requireApproval?}`，后三项为假时省略。令牌不存在、已过期或已被替换时返回 404。

### 14.2 加入

`group_join` `{qrToken, joinMessage?}`：

| 情况 | 应答 `joinStatus` | 其他效果 |
| --- | --- | --- |
| 已是成员 | `joined` | 无 |
| 群不需要审核 | `joined` | 直接入群。legacy 档**不通知任何人**，入群者也**没有被订阅**（D7，F45），所以客户端收到 `joined` 后**必须**自己 `sub` 该群并刷新群列表。strict 档：发请求的连接被订阅；群里其余在线成员收到 `grp_sync`（`from` 为入群者）；入群者的其他设备收到同样的 `grp_sync` 并被订阅，没连上的设备入队（第 10.10 节）。按 legacy 档的做法自己再 `sub` 一次无害 |
| 群需要审核 | `pending_approval` | 创建申请；在线的管理员收到 `group_join_request`，离线的进入离线队列 |
| 已有待处理的申请 | `pending_approval` | 不重复通知 |

`joinMessage` 超过 60 个码点时截断。人数已满返回 409。

管理员收到的推送：

```json
{"action":"group_join_request","topic":"grp_…","name":"群名","from":"<申请人 uid>"}
```

### 14.3 审核

`group_join_requests_list` `{topic}`：管理员可调用，应答 `joinRequests:[{id, requesterUid, requesterName, message?, createdAt}]`，没有待处理申请时省略该字段。`requesterName` 由宿主按查看者视角解析；查不到时退回申请人 uid（F46）。

`group_join_review` `{topic, joinRequestId, approve}`：管理员可调用，应答 `joinStatus`（`approved` 或 `rejected`）。申请人收到：

```json
{"action":"group_join_reviewed","topic":"grp_…","name":"群名","joinStatus":"approved"}
```

通过时申请人还收到 `grp_sync`（`from` 为审核人）并被订阅。群里其他成员不会收到任何通知（F45）。

拒绝时申请人收到 `joinStatus:"rejected"` 的 `group_join_reviewed`，不加入任何人。被拒的人可以用同一个二维码令牌再次申请，得到新的 `joinRequestId`（F56）。

错误：401 `只有群主或管理员可以执行此操作`；申请已被处理时 409 `该申请已被处理`；`joinRequestId` 不属于 `topic` 时 400 `请求与群聊不匹配`；通过时群不存在 404 `群聊不存在`，人数已满 409 `群成员数量已达上限`。legacy 档的这些检查（除权限外）都在申请状态**已经写入之后**：检查失败时申请从待处理列表里消失，真正的群管理员再审核只得到 409，申请人既没入群也没收到通知（已知缺陷 D8，F57）。strict 档先做完全部检查再写入，状态与成员行在同一个事务里写；任一检查失败，申请保持待处理。对另一个群的申请，两档的应答相同：已处理的 409，待处理的 400。客户端发审核时必须用申请所属群的 `topic`（取自 `group_join_request` 帧或待处理列表）。

### 14.4 入群设置

`get_group_join_settings` `{topic}`：成员可调用，应答带 `requireApproval`（为假时省略）。`update_group_join_settings` `{topic, requireApproval}`：只有 owner，应答回显新值。

## 15. 群 AI（模块组）【录】

黄金对话：`module/group-ai`。

### 15.1 设置

| 请求 | 权限 | 应答（type 帧） |
| --- | --- | --- |
| `get_group_ai_settings` `{reqId, topic}` | 成员 | `group_ai_settings`：`{reqId, topic, aiQaEnabled, aiKbIds, aiDocIds, aiDailyQuota, aiCooldownSec}` |
| `update_group_ai_settings` `{reqId, topic, aiQaEnabled, aiKbIds?, aiDocIds?}` | owner | `update_group_ai_settings_result`：`{reqId, ok, reason?}` |

`group_ai_settings` 的字段总是全部出现。非成员、未登录或出错时，`get_group_ai_settings` 不报错，而是返回默认值（`aiQaEnabled:false`、空数组、每日 50 次、冷却 5 秒），与真实的默认设置无法区分（F47）。`update_group_ai_settings` 的 `reason`：`not_owner`、`topic_not_found`、`bad_request`、`internal_error`。

### 15.2 触发与回复

群消息正文里任意位置出现 `@小桃子`、且后面紧跟空白、中英文标点、`@` 或正文结尾时触发（`@小桃子的粉丝群` 不会触发）。这个触发词是参考服务写死的，Talkwire 会把它做成宿主配置。

触发后：

1. 发送者照常收到 `pub` 应答，其他成员照常收到 `data`。
2. 群里所有在线成员（含发送者）收到 `{"type":"ai_typing","topic":"grp_…"}`。
3. 服务端向宿主提问，拿到回答后以普通消息写入该群，所有在线成员（含发送者）收到 `data` 推送，其中 `from` 为 `assistant_xiaotaozi`、`senderKind` 为 `ai_assistant`、`contentType` 为 `text`（不是 `text/plain`）。这条消息也出现在 `get` 的历史里。

同一个群在冷却时间（默认 5 秒）内的后续提问被静默忽略：用户消息照常发出，但没有 `ai_typing`，也没有回复。

宿主拒绝回答时，服务端按原因发一条固定文案的 AI 消息，例如 `quota_exceeded` 对应“本群今日的小桃子问答次数已用完，请明天再试”。这些文案是参考服务写死的中文（F48）。每日次数等计费规则由宿主执行。

## 16. 音视频通话信令（模块组）【录】

黄金对话：`module/call`、`module/call-timeout`。请求都是 action 帧、按 `id` 应答；推送都是 type 帧，没有 `id`/`reqId`。媒体走宿主提供的 RTC 服务，不经过本协议。

### 16.1 发起

`call_invite` `{topic:<对方 uid>, callType:"voice"|"video", clientCallId?}`。成功时被叫方**先**收到 `call_ring`，主叫方**后**收到应答 `{action:"call_invite",code:200,topic,callSessionId}`（F49）：

```json
{"type":"call_ring","callSessionId":"…","callType":"voice","callerName":"Alice","callerAvatar":"https://…","ringTimeoutSeconds":45}
```

`callerName`、`callerAvatar` 由宿主按被叫方视角解析，查不到时省略。

| 错误 | 条件 |
| --- | --- |
| 400 `topic (callee uid) required` | 缺对方 uid |
| 400 `cannot call yourself` | 呼叫自己 |
| 400 `callType must be voice or video` | 类型不合法 |
| 409 `offline` | 对方不在线（在询问宿主之前判断） |
| 409 `busy` | 对方或自己已在通话中 |
| 401 `not_friends` | 宿主拒绝，原因是不是好友 |
| 409 `<宿主给的原因>` | 宿主以其他原因拒绝，`reason` 原样透传 |

通话是否允许发起由宿主决定，服务端本身不校验好友关系。

多端【测】：被叫的每台在线设备都收到 `call_ring`，被叫只要有一台设备在线就不算 `offline`。主叫的其他设备不收任何通话帧。

### 16.2 应答与挂断

| 请求 | 谁可以发 | 成功应答的额外字段 | 对方收到 |
| --- | --- | --- | --- |
| `call_accept` `{callSessionId, acceptAsVoice?}` | 被叫，振铃中 | `channelId`、`rtcToken`、`appId`、`videoQuality`、`denoiseEnabled` | `call_answered`（同样五个字段） |
| `call_reject` `{callSessionId}` | 被叫，振铃中 | `callSessionId` | `call_ended`，`reason:"rejected"` |
| `call_cancel` `{callSessionId}` | 主叫，振铃中 | `callSessionId` | `call_ended`，`reason:"cancelled"` |
| `call_hangup` `{callSessionId}` | 任一方，已接通 | `callSessionId` | `call_ended`，`reason:"user_hangup"` |

`rtcToken` 只属于收到它的一方：被叫在 `call_accept` 应答里拿到自己的令牌，主叫在 `call_answered` 里拿到自己的令牌，两者不同。客户端**禁止**把自己的令牌发给对方。

`acceptAsVoice:true` 用于以语音方式接听视频邀请，此后按语音通话处理。

振铃超过 `ringTimeoutSeconds` 无人应答时，双方都收到 `call_ended`（`reason:"no_answer"`）。

会话不存在、已结束或调用者不是这通电话的参与者时返回 404 `通话不存在或已结束`；状态不对（例如重复接听）返回 409。

多端【测】：哪台设备发起、哪台设备接听，通话就在哪台设备上。接听成功后，被叫的其他设备收到 `call_ended`、`reason:"answered_elsewhere"`；振铃时拒接，其他设备收到 `reason:"rejected"`。`call_cancel`、`call_hangup`、`call_upgrade`、`call_upgrade_accept`、`call_upgrade_reject` 只接受通话所在的设备（振铃期间被叫的任一设备都可以接听或拒接），其他设备得到与陌生人相同的 404 `通话不存在或已结束`。对方的 `call_answered`、`call_ended` 和升级推送也只发到通话所在的设备；被叫振铃期间的 `call_ended` 发到他的所有设备。断线宽限期只看通话所在的设备，同一账号的其他设备上下线不影响通话。

### 16.3 升级为视频

任一方可以在通话中发 `call_upgrade` `{callSessionId, toType:"video"}`，对方收到 `{"type":"call_upgrade_request","callSessionId":…,"toType":"video"}`。

| 对方的回应 | 结果 |
| --- | --- |
| `call_upgrade_accept` | 双方都收到 `{"type":"call_upgraded","callSessionId":…,"videoQuality":…}` |
| `call_upgrade_reject` | 发起方收到 `{"type":"call_upgrade_declined","callSessionId":…}` |

已经是视频通话时返回 409；尚未接通返回 409；对方不在线返回 409。

### 16.4 `call_ended` 的原因

`user_hangup`、`no_answer`、`rejected`、`cancelled`、`disconnected`（一方断线超过宽限期）、`failed`（宿主接听失败）、`answered_elsewhere`（多端：被叫的另一台设备已接听）【测】，以及宿主主动结束时的 `credit_exhausted`、`max_duration_reached`。客户端**必须**容忍未知原因。

> 断线宽限期（`disconnected`）与宿主主动结束两种情况尚未录制。

## 17. 推送令牌【录】

```json
{"action":"reg_token","token":"<推送令牌>","provider":"fcm"}
{"action":"unreg_token","token":"<推送令牌>","provider":"fcm"}
```

注意 `token` 字段在这里放的是推送令牌，不是登录令牌（F27）。两者都**无应答**，缺 `token` 时静默丢弃。客户端**应当**在每次登录后注册，退出登录前注销。

`provider`（可选，Talkwire 服务端）：推送服务商，`fcm`（缺省，参考服务只有它）、`apns` 或 `webpush`。Web Push 的令牌是浏览器 `PushSubscription` 的 JSON 字符串，服务端规范化后保存。不合法的令牌被静默拒绝：没有应答，服务端只记日志，不记令牌本身。各服务商的推送载荷见 `server/push/push.go` 的包文档。

推送对象【测】：只推给没有连接的设备注册的令牌。一台设备都没连上时就是该用户的全部令牌，与参考服务相同。不带 `device` 登录时注册的令牌属于设备 `""`。带 `device` 登录的设备每个服务商只保留一个令牌，注册新的即删掉旧的。每次登录后重新注册，令牌就总挂在当前设备上。

## 18. 已知缺陷

参考服务的下列行为是缺陷。legacy 档保留它们，strict 档按“修正后”列实现。客户端按“客户端应对”列处理，就能在两档下都正常工作。

Talkwire 服务端的 strict 档修正了 D6 以外的全部各项：D3、D4 在任务 1.8，D9 在任务 1.10，D1、D2、D5、D7、D8 在任务 1.13。D6 两档相同。

| 编号 | 缺陷 | strict 档修正后 | 客户端应对 | 出处 |
| --- | --- | --- | --- | --- |
| D1 | `leave_grp` 不校验成员关系：非成员也得到 200，并让所有在线成员收到一条 `from` 为陌生人的 `grp_sync` | 401 `not a member of this conversation`，不推送 | 收到 `grp_sync` 只当作“去刷新群信息”的提示，不信任其中的 `from` | F35 |
| D2 | `leave_grp` 对单聊 topic 生效，会把自己移出单聊 | 400 `only group topics` | 禁止对单聊发 `leave_grp`；收到 `topic` 为 `p2p_…` 的 `grp_sync` 时忽略 | F36 |
| D3 | `pres` 可查询任意 uid | 只返回共享会话的用户 | 不依赖查询陌生人 | F30，红线 R22 |
| D4 | 登录接受非访问令牌 | 只接受 `type=access` | 只使用访问令牌 | F2，红线 R18 |
| D5 | 补投的 `grp_sync` 带入队时的旧群名 | 补投的帧带当前群名；群已不存在时用入队时的群名 | 以 `get_groups` 的群名为准 | F24 |
| D6 | 单聊的 `recalled`、`info` 的 `topic` 照抄请求，可能是接收方自己的 uid | 保持不变（改动会破坏已有客户端），留给协议 v2 | 按第 5.5 节映射 | F29 |
| D7 | 不需审核的扫码入群既不通知群成员，也不给入群者推 `grp_sync`、不订阅 | 订阅发请求的连接；入群者的其他设备收到 `grp_sync` 并被订阅（没连上的入队）；其余在线成员收到 `from` 为入群者的 `grp_sync`（第 14.2 节） | 收到 `joinStatus:"joined"` 后自己 `sub` 该群并刷新群列表 | F45 |
| D8 | `group_join_review` 先把申请标为已处理，再检查它是否属于 `topic`、群是否存在、人数是否已满。后几项失败时返回 400/404/409，但申请已被消耗：从待处理列表消失，再审核得到 409，申请人没入群也没收到通知 | 所有检查放在写入之前，写入状态和加成员在同一事务里完成；任一检查失败时申请保持待处理 | 审核时用申请所属群的 `topic`；群满时先腾出名额再通过；遇到这类失败只能让申请人重新申请 | F57 |
| D9 | 入群申请与审核结果的离线事件从不入队（存储只接受 `grp_sync`、`grp_disband` 两种），不在线的管理员和申请人收不到 | 入队，下次登录补投（第 10.10 节） | 登录后主动拉 `group_join_requests_list`，不依赖补投 | F59 |

## 19. 一致性测试

`tools/conformance` 用黄金对话检验服务端实现：

```
conformance check -bin <服务端可执行文件>
```

比较规则：

- 每一步按连接比较收到的帧的**多重集合**，不比较帧的到达顺序。
- 用户 id、单聊 topic、群 topic、时间戳、`presRev` 先替换成占位符再比较。
- 标为可选（`optional`）的帧不参与比较，用于记录参考服务自身的竞态。
- 帧内数组按原顺序比较，例外是 `get_members` 的 `members`：同一时刻入群的成员顺序不保证，所以比较前先按 uid 占位符排序。`pres` 查询 `*` 的结果顺序同样不保证，场景里只让它返回一个元素。

帧的结构另由 JSON Schema 校验（`contract/protocol/schema/`，用法见 `tools/schema/README.md`）。服务端发出的每一帧都必须通过 `server-frames.schema.json`，该 Schema 禁止未声明字段。

strict 档用同一套黄金对话检验：

```
conformance check -additions strict -overlay contract/protocol/transcripts-strict -env TALKWIRE_PROTOCOL_PROFILE=strict
```

`-additions strict` 在比较前去掉 strict 档新增的字段（`id`、`protocol`、`capabilities`、`vapidKey`、`errCode`、`readSeq`）；有意偏离 legacy 的六个场景改用 `transcripts-strict/` 里另录的版本：`core/login-tokens`（D4）、`core/presence`（D3）、`core/leave-grp`（D1、D2）、`core/group-offline-events`（D5）、`module/qr-join`（D7）、`module/qr-join-review`（D8）。Schema 校验同时覆盖 `transcripts-strict/`，那里的帧保留了 strict 档的新增字段。

多端同时在线没有黄金对话：参考服务没有这项能力。它由 `server/multidevice_test.go` 等用例覆盖（进程内真实服务端加真实 WebSocket）。`sync_summary`（第 8.1 节）同样没有黄金对话，由 `server/sync_summary_test.go` 覆盖。

## 附录 A. 动作一览

| 动作 | 方向 | 帧族 | 应答 | 节 |
| --- | --- | --- | --- | --- |
| `login` | 请求 | action | 成功无 `id`（strict 档带） | 6 |
| `sub`、`leave` | 请求 | action | 有（`leave` 失败时无） | 9.4 |
| `pub` | 请求 | action | 有 | 9.1 |
| `get` | 请求 | action | 有 | 9.3 |
| `sync_summary` | 请求 | action | 有（strict 档） | 8.1 |
| `del` | 请求 | action | 有 | 9.5 |
| `del_topic` | 请求 | action | 有 | 11.2 |
| `note` | 请求 | action | 无 | 12.3 |
| `pres` | 请求 | action | 成功无 `id`（strict 档带） | 12.2 |
| `create_grp`、`invite`、`kick`、`leave_grp`、`update_grp`、`set_member_role`、`transfer_owner`、`disband_grp`、`get_groups`、`get_members` | 请求 | action | 有 | 10 |
| `forward_bundle`、`forward_broadcast` | 请求 | action 请求、type 应答 | 按 `reqId` | 9.6 |
| `ack_topic_cleared`、`ack_group_event` | 请求 | action | 无 | 11.3、10.10 |
| `reg_token`、`unreg_token` | 请求 | action | 无 | 17 |
| `data`、`recalled`、`info`、`pres`、`grp_sync`、`grp_disband`、`topic_cleared`、`contact_sync`、`custom_update`、`red_packet_update` | 推送 | action | — | 9、10、11、12、附录 B |
| E2EE、扫码入群、群 AI、通话 | 两向 | 见各节 | 见各节 | 13–16 |

## 附录 B. 宿主触发的推送

宿主通过服务端的内部 HTTP 接口触发以下推送（接口契约另见宿主适配器文档）：

| 推送 | 字段 | 说明 |
| --- | --- | --- |
| `contact_sync` | `what`（`accepted` 或 `removed`）、`from`（对方 uid） | 好友关系变化，双方都收到。客户端收到后**应当**刷新好友列表；`removed` 之后再向对方发单聊会被拒绝（F6、F7） |
| `call_ended` | `reason` 为 `credit_exhausted` 或 `max_duration_reached` | 宿主侧结束通话 |
| `custom_update` 【测】 | `topic`（按会话推送时才有）、`what`（宿主定的卡片类型）、`extra`（宿主载荷） | 宿主自定义消息卡片的状态更新，见下文 |
| `red_packet_update` 【码】 | `topic`、`extra` | 参考部署的红包进度推送，等同 `what` 为红包的 `custom_update`。新宿主不应当再用 |

`custom_update` 不分档位，由宿主调用 `custom-broadcast` 接口触发，服务端不解读 `extra`：

- 按会话推送时，订阅了该会话的连接都收到，帧里带 `topic`；按用户推送时，该用户的每个连接都收到，帧里没有 `topic`。
- `what` 符合 `^[a-z][a-z0-9_.-]{0,63}$`，含义由宿主约定，例如 `red_packet`。客户端**应当**按 `what` 分派，**必须**忽略不认识的 `what`。
- 不进离线队列，也不重发。不在线的连接收不到；客户端打开会话或卡片时**应当**向宿主拉取当前状态，不能只靠这个推送。
