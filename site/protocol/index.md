---
title: "协议概述"
description: "Talkwire 线协议 v1 的传输、帧、登录、topic 与 seq，以及规范、JSON Schema 和黄金对话放在哪里、怎么检查。"
---

# 协议概述

Talkwire 的客户端与服务端之间只有一条 WebSocket 连接，上面每帧传一个 JSON 对象。协议 v1 的权威定义是[规范全文](/protocol/spec)；本页把读规范之前需要的骨架讲清楚，每条都注明出自规范哪一节。

规范的目标读者是要独立实现客户端或服务端的团队：只读规范、JSON Schema 和黄金对话，就应能写出与参考服务逐帧兼容的实现（规范开头）。参考服务指原版 IM 服务，规范依据它的源码和对它录制的黄金对话写成。

## 传输

客户端用 `GET /ws/im`（或等价的 `GET /ws`）升级为 WebSocket，之后只收发文本帧（规范第 2 节）。

| 项目 | 规定 |
| --- | --- |
| 帧 | 文本帧，UTF-8，每帧恰好一个 JSON 对象 |
| 单帧上限 | 256 KB（262144 字节）。超过时服务端直接断开连接，不发错误帧 |
| 心跳 | 服务端约每 27 秒发一次 ping，60 秒内收不到任何帧（含 pong）就断开。客户端**必须**回 pong |
| 写超时 | 服务端单帧写入超过 10 秒即断开 |
| 发送缓冲 | 服务端每连接缓冲 256 帧，满了就断开连接，迫使客户端重连后重新同步 |
| 连接上限 | 服务端连接数达到上限（默认 10000）时，升级请求返回 HTTP 503 |
| 未登录连接 | 没有登录时限，只要回 pong 就一直保持 |

客户端**必须**把断线当作正常事件：重连、登录，再按[同步与补拉](/protocol/sync)重新同步（规范第 2、8 节）。

## 帧

帧分两族：绝大多数请求、应答和推送用 `action` 判别，转发结果、群 AI 设置、E2EE 密钥相关应答和通话信令推送用 `type` 判别（规范第 3.1 节）。一帧只会有其中一个判别字段，客户端解析时**必须**先看 `type`，没有再看 `action`。

字段为零值（`0`、`false`、`""`、空数组、`null`）时服务端通常省略它，客户端**必须**把缺省当作零值（规范第 3.2 节）。双方**必须**忽略不认识的字段，协议只以新增字段的方式演进。时间戳一律是 Unix 毫秒整数。

字段、错误码和全部动作的清单见[帧格式与错误码](/protocol/frames)。

## 请求与应答的关联

客户端在请求里放 `id`（同一连接内唯一的字符串），服务端的应答原样带回 `id`，`action` 与请求相同（规范第 3.3 节）。有三类例外：

| 情形 | 应答的样子 | 怎么关联 |
| --- | --- | --- |
| 成功的 `login` | legacy 档不带 `id` | 按 `action=login` 关联；一条连接同时只应有一个在飞的 `login` |
| `pres` 查询成功 | legacy 档不带 `id`，带 `what=snapshot` | 按 `action=pres` 且 `what=snapshot` 关联 |
| type 帧族的请求 | 用 `reqId` 关联 | 请求里放 `reqId`，应答带回同一个 `reqId` |

strict 档的成功 `login` 和 `pres` 快照也带 `id`（规范第 3.3 节，红线 R19）。客户端可以先按 `id` 匹配，匹配不到再按 `action`，这样两档都成立。

推送是服务端主动发的帧，不带 `id`（规范第 3.4 节）。`note`、`ack_group_event`、`ack_topic_cleared`、`reg_token`、`unreg_token` 没有应答，客户端**不应**等待（规范附录 A）。

## 登录握手

连接建立后先发 `login`，未登录时发其他请求只会得到 401 `not authenticated`（规范第 4.2 节）。`token` 是宿主用与服务端共享的密钥签发的 HS256 JWT，服务端只要求 `sub`（即宿主侧的 `appUserId`）存在且签名、有效期合法（规范第 6.1 节）；令牌怎么签发见[签发 IM 令牌](/host/token)。

```json
{"id": "l1", "action": "login", "token": "<JWT>"}
```

| 结果 | 应答 |
| --- | --- |
| 成功 | `code` 200，带本账号的 `uid` |
| 缺 `token` | 401 `token required` |
| 令牌无效或过期 | 401 `invalid token` |
| 连接已登录 | 400 `already logged in` |
| `device` 不合法 | 400 `invalid device` |
| 已在上限台数的设备上在线，本次是一台新设备 | 409 `too many devices` |

可选的 `device` 字段给连接所在的设备命名，用于多端同时在线，见[多端与推送](/protocol/devices-push)（规范第 6.2 节）。strict 档只接受 `type=access` 且 `role` 为普通用户的令牌，legacy 档接受任何带 `sub` 的有效令牌（规范第 6.1 节，缺陷 D4）。

登录成功后，服务端异步补投离线期间积压的 `topic_cleared`、离线群事件和 `e2e_deliver`，顺序不保证，可能早于也可能晚于客户端下一个请求的应答（规范第 6.3 节）。客户端接着按[同步与补拉](/protocol/sync)重建状态。

### 黄金对话里的一次登录

下面的帧取自黄金对话 [`core/login`](https://github.com/dishikun2786-prog/Talkwire/blob/main/contract/protocol/transcripts/core__login.json) 第 2、3 步，录制自参考服务（legacy 档的行为）。测试台发出的请求是 `{"id":"s2","action":"login","token":…}`：`id` 为 `s` 加步骤号，令牌按场景现签（[`runner.go`](https://github.com/dishikun2786-prog/Talkwire/blob/main/tools/conformance/harness/runner.go)）。

成功的应答不带 `id`：

```json
{"action": "login", "code": 200, "uid": "${uid:A}"}
```

同一连接再登录一次被拒，失败应答带 `id`：

```json
{"action": "login", "code": 400, "id": "s3", "reason": "already logged in"}
```

strict 档的成功应答多出 `id`、`protocol` 和 `capabilities`，下例取自 [`transcripts-strict/core__leave-grp.json`](https://github.com/dishikun2786-prog/Talkwire/blob/main/contract/protocol/transcripts-strict/core__leave-grp.json) 第 8 步：

```json
{"action": "login", "capabilities": ["errcode", "reply-id", "multi-device", "module:groupjoin", "module:e2ee", "module:groupai", "module:call"], "code": 200, "id": "s8", "protocol": 1, "uid": "${uid:A}"}
```

`${uid:A}` 是占位符：测试台在比较前把用户 id、单聊 topic、群 topic、时间戳和 `presRev` 换成占位符（规范第 19 节）。`capabilities` 的内容随服务端版本和安装的模块变化，客户端**必须**忽略不认识的能力（规范第 6.1 节）。

## topic 与 uid

topic 即会话，分单聊、群和保留 topic 三种（规范第 1、5 节）。

| 名字 | 形式 | 出处 |
| --- | --- | --- |
| uid | `"uid_" + base64url_nopad(SHA-256(UTF-8(appUserId))[0:8])`，固定 15 个字符，客户端可以本地算出 | 规范第 5.1 节 |
| 单聊 topic | 两个 uid 按字节升序排列为 `a`、`b`，`"p2p_" + base64url_nopad(SHA-256(a + ":" + b)[0:8])` | 规范第 5.2 节 |
| 群 topic | `"grp_"` 加 12 个字母或数字，服务端建群时生成；客户端**必须**当作不透明字符串 | 规范第 5.3 节 |
| 保留 topic | `me`（可以 `sub`，没有其他作用）、`fnd`（不支持）、`*`（只用于 `pres` 查询） | 规范第 5.4 节 |

规范第 5.1、5.2 节给出测试向量，例如 `alice` 的 uid 是 `uid_K9gGyX8OAK8`，`alice` 与 `bob` 的单聊 topic 是 `p2p_E5DsdYCpVC8`。

同一个单聊在线上有三种写法：发请求时推荐写对方的 uid，`data` 推送里是发送方的 uid，`get` 应答里每条消息是规范 topic `p2p_…`（规范第 5.5 节）。客户端**必须**三种都认，映射规则见规范第 5.5 节，相关缺陷见[已知缺陷](/protocol/defects)的 D6。

## seq

seq 是消息在 topic 内的序号，从 1 开始，严格递增，不复用（规范第 1 节）。已撤回的消息不再由 `get` 返回，但它的 seq 仍被占用（规范第 9.3 节）。

客户端靠 seq 判断缺了什么：补拉从本地连续段的末尾开始，撤回与清空留下的 seq 要单独记账（规范第 8.2 节），见[同步与补拉](/protocol/sync)。

## 订阅

服务端只把推送发给已订阅对应 topic 的连接（规范第 7 节）。订阅属于连接，断线即消失，登录不会恢复订阅。

发 `sub`、`get`、`pub` 都会订阅该 topic；strict 档的 `sync_summary` 订阅自己所在的全部会话；在线时被拉进群、收到对方单聊消息也会被订阅（规范第 7 节）。群成员重连后在重新订阅之前收不到该群的实时消息，消息已入库，用 `get` 补回即可。

## 能力

strict 档的成功 `login` 应答带 `protocol`（整数 `1`）、`capabilities`（字符串数组），配置了 Web Push 时还带 `vapidKey`（规范第 6.1 节）。客户端据 `capabilities` 决定能用哪些新功能。

| 能力 | 含义 |
| --- | --- |
| `errcode` | 错误帧带 `errCode` |
| `reply-id` | 登录与在线快照应答带 `id` |
| `multi-device` | 支持多端同时在线（规范第 6.2 节） |
| `sync-summary` | 支持 `sync_summary`（规范第 8.1 节） |
| `module:<名>` | 已安装的模块：`call`、`groupjoin`、`groupai`、`e2ee` |
| `push:<服务商>` | 已配置的推送服务商：`fcm`、`apns`、`webpush` |

legacy 档的登录应答没有这些字段。客户端没看到某项能力时，就当服务端不支持它。

## 两档协议

Talkwire 服务端按 legacy 或 strict 一档回答，用环境变量 `TALKWIRE_PROTOCOL_PROFILE` 选择，默认 strict（规范第 0.3 节）。legacy 对所有黄金对话逐帧一致，包括缺陷行为；strict 修正第 18 节列出的缺陷（D6 除外），并新增字段和动作。

客户端**必须**在两档下都能工作：不依赖任何缺陷行为，对缺陷可能产生的帧做容错（规范第 0.3 节）。两档的区别见 [legacy 与 strict](/protocol/profiles)。

## 规范、Schema 与黄金对话

协议的事实分别放在四类文件里，相互校验。

| 位置 | 内容 |
| --- | --- |
| [`contract/protocol/spec/protocol-v1.md`](https://github.com/dishikun2786-prog/Talkwire/blob/main/contract/protocol/spec/protocol-v1.md) | 规范正文（中文）。每个动作的小节标题带出处标记：【录】黄金对话有实测帧并核对过源码，【码】只读源码，【测】Talkwire 新增、由 `server/` 的自动化测试验证（规范第 0.2 节） |
| [`contract/protocol/schema/client-frames.schema.json`](https://github.com/dishikun2786-prog/Talkwire/blob/main/contract/protocol/schema/client-frames.schema.json) | 客户端发出的帧，51 个 `action`。允许未声明字段，因为服务端必须忽略它们 |
| [`contract/protocol/schema/server-frames.schema.json`](https://github.com/dishikun2786-prog/Talkwire/blob/main/contract/protocol/schema/server-frames.schema.json) | 服务端发出的帧，56 个 `action` 名和 15 个 `type` 名。**禁止**未声明字段，保证服务端不发规范里没写的字段；客户端运行时不得用它校验收到的帧 |
| [`contract/protocol/transcripts/`](https://github.com/dishikun2786-prog/Talkwire/tree/main/contract/protocol/transcripts) | 对参考服务录制的 33 个黄金对话（核心组 26 个、模块组 7 个），场景定义在 `contract/protocol/scenarios/` |
| [`contract/protocol/transcripts-strict/`](https://github.com/dishikun2786-prog/Talkwire/tree/main/contract/protocol/transcripts-strict) | strict 档有意偏离 legacy 的 6 个场景另录的版本，帧里保留 strict 档的新增字段 |
| [`docs/protocol-findings.md`](https://github.com/dishikun2786-prog/Talkwire/blob/main/docs/protocol-findings.md) | 协议事实的逐条出处，规范里以 F 编号引用 |

两份 Schema 由 [`tools/schema/gen.py`](https://github.com/dishikun2786-prog/Talkwire/blob/main/tools/schema/gen.py) 生成，改协议时只改生成器，不手改 Schema（[`tools/schema/README.md`](https://github.com/dishikun2786-prog/Talkwire/blob/main/tools/schema/README.md)）。

### 怎么检查

一致性测试台 `tools/conformance` 用黄金对话驱动被测服务端，逐步比较每条连接收到的帧（规范第 19 节）。比较规则：

- 按连接比较收到的帧的多重集合，不比较帧的到达顺序。
- 用户 id、单聊 topic、群 topic、时间戳、`presRev` 先替换成占位符再比较。
- 标为 `optional` 的帧不参与比较，用来记录参考服务自身的竞态。
- 帧内数组按原顺序比较；`get_members` 的 `members` 例外，比较前按 uid 占位符排序。

| 检查 | 命令 |
| --- | --- |
| legacy 档跑黄金对话 | `conformance check -bin <服务端可执行文件>` |
| strict 档跑黄金对话 | `conformance check -additions strict -overlay contract/protocol/transcripts-strict -env TALKWIRE_PROTOCOL_PROFILE=strict` |
| 每帧过 Schema | `node tools/schema/check.mjs` |
| Schema 与生成器一致 | `python -I tools/schema/gen.py --check` |
| 每个帧名都有黄金用例 | `conformance coverage` |

`-additions strict` 在比较前去掉 strict 档新增的字段（`id`、`protocol`、`capabilities`、`vapidKey`、`errCode`、`readSeq`），去掉之后仍须逐帧一致（规范第 19 节）。用法和参数见 [`tools/conformance/README.md`](https://github.com/dishikun2786-prog/Talkwire/blob/main/tools/conformance/README.md)。

参考服务没有的能力没有黄金对话：多端同时在线由 `server/multidevice_test.go` 等用例覆盖，`sync_summary` 由 `server/sync_summary_test.go` 覆盖（规范第 19 节）。CI 通过 [`tools/ci.sh`](https://github.com/dishikun2786-prog/Talkwire/blob/main/tools/ci.sh) 的 `deps` 任务检查 Schema，`conformance` 任务在两档下跑黄金对话并统计覆盖。
