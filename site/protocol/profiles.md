---
title: "legacy 与 strict"
description: "两档协议各保证什么、怎么选，strict 档相对 legacy 的三类改动，以及修正了哪些缺陷。"
---

# legacy 与 strict

Talkwire 服务端按两档之一回答客户端：legacy 与参考服务逐帧一致，strict 在此之上修正已知缺陷、新增字段和动作（规范第 0.3 节）。默认是 strict；迁移原版项目、要与旧客户端逐帧兼容时用 legacy。

参考服务指原版 IM 服务，它有若干已知缺陷，编号 D1–D9（规范第 18 节）。两档的区别全部来自是否保留这些缺陷，以及 strict 档新增的字段和动作。

## 各档保证什么

两档对客户端的承诺不同，适用的项目也不同。

| 档位 | 保证 | 适用 |
| --- | --- | --- |
| legacy | 对所有黄金对话逐帧一致，包括缺陷行为（规范第 0.3 节）；接受参考服务接受的全部令牌（ADR-0002 决定 3） | 迁移原版项目，现网旧客户端不升级就能连上 |
| strict | 修正第 18 节的缺陷（D6 除外），其余与 legacy 一致；新增的字段旧客户端忽略即可（规范第 0.3 节） | 新项目，以及旧客户端已全部换完的迁移项目 |

strict 档仍用同一套黄金对话检验：比较前去掉新增字段后须逐帧一致，有意偏离的 6 个场景改用另录的版本（规范第 19 节）。所以 strict 不是另一套协议，而是 legacy 加上新字段、新动作和缺陷修正。

## 怎么选

用环境变量 `TALKWIRE_PROTOCOL_PROFILE` 选档，取值 `legacy` 或 `strict`，不设时是 `strict`（规范第 0.3 节）。其他取值会让服务端启动失败（[`server/config.go`](https://github.com/dishikun2786-prog/Talkwire/blob/main/server/config.go)）。

迁移原版项目的顺序是“先换服务端（legacy）、再换客户端、最后切 strict”（[ADR-0002](https://github.com/dishikun2786-prog/Talkwire/blob/main/docs/adr/0002-protocol-profiles.md)）。换服务端时设 legacy，现网客户端看到的帧不变；旧客户端淘汰后再改成 strict。具体步骤见[从现有 IM 系统迁移](/migrate/)。

令牌校验随档位变化，另有两个开关（ADR-0002 决定 3）：

| 环境变量 | 作用 |
| --- | --- |
| `TALKWIRE_TOKEN_USER_ROLES` | strict 档接受的令牌 `role`，默认 `app_user`；令牌还必须是 `type=access`（红线 R18） |
| `TALKWIRE_ACCEPT_LEGACY_TOKENS` | 设为 `1` 时 strict 档也接受全部令牌，供迁移期间使用。默认关，开着时服务端启动打印警告 |

其余环境变量见[环境变量](/server/env)。

### 不分档位的功能

多端同时在线、可选模块、推送服务商两档都有，只在客户端用到新字段时启用（ADR-0002 决定 4）。例如不带 `device` 登录时，两档都与参考服务一样让同一账号的连接互相顶替（规范第 0.3、6.2 节）。

模块未安装时，模块动作在两档下都得到 501 `module not enabled: <模块名>`（规范第 4.1 节，[`server/modules.go`](https://github.com/dishikun2786-prog/Talkwire/blob/main/server/modules.go)）。宿主触发的 `custom_update` 推送也不分档位（规范附录 B）。

## strict 档的三类改动

strict 档相对 legacy 只做三类改动（规范第 0.3 节）。

### 新增字段

新增字段只加不改，旧客户端忽略即可。

| 字段 | 出现在 | 节 |
| --- | --- | --- |
| `id` | 成功的 `login` 应答、`pres` 快照应答 | 规范第 3.3 节 |
| `protocol`、`capabilities`、`vapidKey` | 成功的 `login` 应答；`vapidKey` 只在配置了 Web Push 时出现 | 规范第 6.1 节 |
| `errCode` | action 帧族的错误帧 | 规范第 4.3 节 |
| `readSeq` | `get` 应答，表示本人在该会话已读到的 seq | 规范第 9.3 节 |

`readSeq` 背后还有一处行为：strict 档把单聊的 `read` 存为发送者本人的已读位置，legacy 档不存（规范第 12.3 节）。

### 新增动作

新增动作在 `capabilities` 里宣告，旧客户端不会发出（规范第 0.3 节）。目前只有 `sync_summary`，宣告为能力 `sync-summary`（规范第 8.1 节）。

legacy 档对它像参考服务一样回 400 `unknown action: sync_summary`。用法见[同步与补拉](/protocol/sync)。

### 修正缺陷

strict 档修正了第 18 节中 D6 以外的全部缺陷；D6 两档相同，留给协议 v2（规范第 0.3、18 节）。修正方式与客户端应对见[已知缺陷](/protocol/defects)。

| 编号 | 参考服务的行为 | 修正的档位 |
| --- | --- | --- |
| D1 | `leave_grp` 不校验成员关系：非成员也得到 200，并让全群收到一条 `from` 为陌生人的 `grp_sync` | strict |
| D2 | `leave_grp` 对单聊 topic 生效，把自己移出单聊 | strict |
| D3 | `pres` 可以查询任意 uid，包括陌生人 | strict |
| D4 | 登录接受刷新令牌、管理员令牌等非访问令牌 | strict |
| D5 | 补投的 `grp_sync` 带入队时的旧群名 | strict |
| D6 | 单聊的 `recalled`、`info` 的 `topic` 照抄请求，可能是接收方自己的 uid | 不修正，两档相同 |
| D7 | 不需审核的扫码入群既不通知群成员，也不给入群者推 `grp_sync`、不订阅 | strict |
| D8 | `group_join_review` 先把申请标为已处理再做检查，检查失败时申请被白白消耗 | strict |
| D9 | 入群申请与审核结果的离线事件从不入队 | strict |

## 客户端怎样同时兼容两档

客户端**必须**在两档下都能正常工作：不依赖任何缺陷行为，对缺陷可能产生的帧做容错（规范第 0.3 节）。落到代码上是下面几条。

| 差异 | 客户端做法 | 出处 |
| --- | --- | --- |
| 成功 `login` 与 `pres` 快照是否带 `id` | 先按 `id` 匹配，匹配不到再按 `action` 关联 | 规范第 3.3 节 |
| 错误帧是否带 `errCode` | 有 `errCode` 时按它判断；没有时按 `code`，不解析 `reason` 文本 | 规范第 4.1、4.3 节 |
| 新动作与新字段 | 先看 `capabilities`：有 `sync-summary` 才发 `sync_summary`，有 `multi-device` 才带 `device` | 规范第 6.1、6.2、8.1 节 |
| `get` 应答是否带 `readSeq` | 带时推进本地已读位置；不带时（legacy）只靠本地记录 | 规范第 8 节第 5 步 |
| 缺陷行为 | 按第 18 节“客户端应对”一列处理 | 规范第 18 节 |

## 两档的一致性检验

legacy 档由对参考服务录制的 33 个黄金对话逐帧把关（ADR-0002 决定 5）。strict 档用同一套对话，比较前去掉新增字段（`id`、`protocol`、`capabilities`、`vapidKey`、`errCode`、`readSeq`），并对有意偏离的场景改用 `contract/protocol/transcripts-strict/` 里另录的版本（规范第 19 节）：

| 场景 | 对应缺陷 |
| --- | --- |
| `core/login-tokens` | D4 |
| `core/presence` | D3 |
| `core/leave-grp` | D1、D2 |
| `core/group-offline-events` | D5 |
| `module/qr-join` | D7 |
| `module/qr-join-review` | D8 |

D9 没有另录的对话，由服务端单测 `TestD9_JoinReviewQueuedForOfflineApplicant` 覆盖（[`docs/protocol-findings.md`](https://github.com/dishikun2786-prog/Talkwire/blob/main/docs/protocol-findings.md) F59）。检验命令见[协议概述](/protocol/#怎么检查)。
