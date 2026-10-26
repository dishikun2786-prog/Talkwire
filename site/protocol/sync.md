---
title: "同步与补拉"
description: "客户端每次登录后如何用 get 与 sync_summary 补齐消息，如何记账撤回与清空留下的 seq，以及已读位置和清空版本怎样校正。"
---

# 同步与补拉

服务端只把推送发给已订阅、在线的连接，所以客户端每次登录成功后都要自己把离线期间漏掉的补回来（规范第 7、8 节）。补拉以 seq 为准：从本地连续段的末尾往后拉，撤回和清空留下的 seq 单独记账。

## 登录后的同步顺序

客户端**必须**按下面的顺序重建状态，每次登录成功后都做，不只是首次（规范第 8 节）。

1. 登录。
2. 发 `get_groups`，得到所在的全部群。本地有、列表里没有的群标记为已退出：可能是离线时被踢或群被解散，而补投事件尚未到达。
3. 对每个群发 `sub`，或直接发第 4 步的 `get`，它会顺带订阅。
4. 对每个会话（群与本地已知的单聊）发 `get`，`afterSeq` 取本地该会话的最大 seq，循环直到返回的消息数小于 `limit`。服务端宣告了 `sync-summary` 时，先发 `sync_summary`，只对有变化的会话发 `get`。补拉时按下文“补拉时的 seq 空缺”记账。
5. 用 `get` 应答里的 `clearVersion`、`clearBeforeSeq` 校正本地清空状态；应答带 `readSeq` 时推进本地已读位置。
6. 处理登录后补投的帧（`topic_cleared`、离线群事件、`e2e_deliver`），按要求回执。
7. 需要在线状态时发 `pres`。

单聊不需要预先 `sub` 就能收到对方的新消息，因为对方 `pub` 时服务端会订阅在线的接收方；但第 4 步的补拉仍然必须做（规范第 8 节）。群成员重连后在重新订阅之前收不到该群的实时消息，此时他在线，宿主推送通道也不会触发，漏掉的消息只能靠 `get` 补回（规范第 7 节）。

## `get` 拉取历史

`get` 按 seq 返回一个会话的消息，同时订阅该会话（规范第 7、9.3 节）。请求字段是 `topic`（必须）、`limit`、`beforeSeq`、`afterSeq`。

| 参数组合 | 返回 |
| --- | --- |
| 都不给 | 最新的 `limit` 条 |
| `beforeSeq` | seq 小于它的最新 `limit` 条 |
| `afterSeq` | seq 大于它的最早 `limit` 条；同时给了 `beforeSeq` 时以 `afterSeq` 为准 |

`limit` 在 1–200 之外（含不给）时用默认页长 50，宿主可调（规范第 9.3 节）。消息总是按 seq 升序排列；没有“还有更多”的标志，返回条数小于 `limit` 即到头。

成功应答的字段（规范第 9.3 节）：

| 字段 | 说明 |
| --- | --- |
| `topic` | 请求里的写法 |
| `messages` | 消息数组，每条含 `topic`（规范 topic）、`seq`、`from`、`content`、`contentType`、`ts`，以及可选的 `fileUrl`、`fileName`、`duration`、`extra`、`senderKind`、`forwardFromMsgId`、`forwardFromTopic`。没有消息时省略 |
| `clearVersion`、`clearBeforeSeq` | 会话的清空状态，从未清空时省略 |
| `readCursors` | 只对群、且宿主开启了已读回执时出现：`[{uid, seq}]`，表示该成员已读到 seq（含） |
| `readSeq` | 仅 strict 档：请求者本人在该会话已读到的 seq（含）；没有记录时省略 |

单聊时，`get` 应答之后服务端还会发一帧对方的在线状态（`pres` 推送形态）（规范第 9.3 节）。客户端排序与展示**应当**以 `get` 返回的 `ts` 为准，`data` 推送的 `ts` 是广播时刻，可能差几毫秒（规范第 9.2 节）。

### 分页

补拉用 `afterSeq` 向后翻页：每页之后把 `afterSeq` 换成这一页的最大 seq，直到返回条数小于 `limit`（规范第 8 节第 4 步）。加载更早的历史用 `beforeSeq` 向前翻页。

下面取自黄金对话 [`core/history-paging`](https://github.com/dishikun2786-prog/Talkwire/blob/main/contract/protocol/transcripts/core__history-paging.json) 第 11 步：群里有 seq 1–5 五条消息，带 `afterSeq: 3` 拉取。测试台发送时给请求加上 `id`（`s` 加步骤号）。

```json
{"action": "get", "topic": "${grp}", "afterSeq": 3, "limit": 10}
```

```json
{"action": "get", "code": 200, "id": "s11", "messages": [{"content": "m4", "contentType": "text/plain", "from": "${uid:A}", "senderKind": "user", "seq": 4, "topic": "${grp}", "ts": "<time>"}, {"content": "m5", "contentType": "text/plain", "from": "${uid:A}", "senderKind": "user", "seq": 5, "topic": "${grp}", "ts": "<time>"}], "topic": "${grp}"}
```

返回 2 条，少于 `limit` 的 10 条，说明已到头。同一对话的第 12 步用 `limit: 100000` 拉取，超出 1–200，退回默认页长（规范第 9.3 节，F18）。

## `sync_summary` 会话变更摘要

`sync_summary` 用一次往返告诉客户端哪些会话有变化，客户端不必对每个会话各发一次 `get`（规范第 8.1 节）。它只在 strict 档、且登录应答的 `capabilities` 含 `sync-summary` 时可用；legacy 档回 400 `unknown action: sync_summary`。

请求 `{"id":"s1","action":"sync_summary"}`，没有参数。成功应答（规范第 8.1 节）：

```json
{"id":"s1","action":"sync_summary","code":200,"topics":[
  {"topic":"grp_…","lastSeq":42,"isGroup":true,"clearVersion":1,"clearBeforeSeq":12,"readSeq":40,"readsAt":1793000000000},
  {"topic":"p2p_…","lastSeq":7,"isGroup":false,"peer":"uid_…","readSeq":7}]}
```

`topics` 列出调用者所在的全部群与单聊，按 `topic` 排序，一个都没有时省略。

| 字段 | 说明 |
| --- | --- |
| `topic` | 规范 topic：群是 `grp_…`，单聊是 `p2p_…` |
| `lastSeq` | 该会话分配过的最大 seq，含已撤回的；没有消息时为 0 |
| `isGroup` | 是否为群 |
| `clearVersion`、`clearBeforeSeq` | 清空状态，从未清空时省略 |
| `peer` | 单聊的对方 uid；群没有 |
| `readSeq` | 调用者本人的已读位置，与 `get` 应答的 `readSeq` 同义；没有时省略 |
| `readsAt` | 只对群、且宿主开启了已读回执时出现：其他成员最近一次推进已读位置的时刻（Unix 毫秒） |

服务端在读取摘要之前把本连接订阅到所有这些会话，所以客户端不再 `get` 的会话也能收到实时帧（规范第 8.1 节）。读取之后提交的消息以推送送达、之前提交的计入 `lastSeq`，两者都覆盖到的消息会被拉到两次，客户端按 seq 去重。

### 哪些会话要 `get`

对本地每个会话（未退出的群与本地已知的单聊），客户端**应当**这样判断（规范第 8.1 节）：

| 情况 | 做法 |
| --- | --- |
| 摘要里没有这个群，但它出现在 `get_groups` 里 | 照常 `get` |
| 摘要里没有这个单聊 | 服务端还没有这个会话，不 `get` |
| `lastSeq` 大于本地 seq 连续段的末尾 | 有新消息或空缺，`get` |
| `clearVersion` 大于本地记录 | `get`，由应答校正清空状态 |
| `readsAt` 与本进程上次见到的不同 | `get`，由应答带回全部已读游标 |
| 其余 | 不 `get`；`readSeq` 大于本地已读位置时直接推进 |

`readsAt` 可以只记在内存里，进程重启后把出现 `readsAt` 的群各拉一次（规范第 8.1 节）。客户端**不应**仅凭摘要创建本地没有的单聊，否则用户在本地删除过的会话每次重连都会回来。

## 补拉时的 seq 空缺

已撤回的消息不返回、seq 也不复用，所以补拉时客户端**必须**把服务端没返回的 seq 记为已撤回，否则会一直从同一个空缺重拉（规范第 8.2 节）。客户端从没收到、又在离线时被撤回的消息会在本地留下这样的空缺；如果把它当成“还没拉到”，每次重连都从这里重拉，空缺之后超过一次补拉上限的部分永远补不到。

记账规则（规范第 8.2 节）：

- 一页 `get` 返回后，`(下界, 本页最大 seq]` 里服务端没有返回的 seq 一律记为已撤回：本地有这条消息就按撤回处理，没有就只记下这个 seq。带 `afterSeq` 时下界是 `afterSeq`；不带时是本页最小 seq 减 1，因为更早的历史只是没加载，不是空缺。
- 返回条数小于 `limit`（已到头）且从 `sync_summary` 知道 `lastSeq` 时，`(本页最大 seq, lastSeq]` 同样记为已撤回；空页时从下界算起。
- 低于清空水位的 seq 不需要记。

记下的 seq 与已有的消息一起构成连续段，下次补拉从段尾开始（规范第 8.2 节，红线 C3）。按这些规则，本地连续段到 seq 3、带 `afterSeq: 3` 拉回 seq 5、6 且条数小于 `limit` 时，seq 4 记为已撤回，下次从 6 之后拉。

在线时收到的撤回是 `recalled` 推送，`topic`、`seq`、`from` 指明撤回了哪条（规范第 9.5 节）。撤回不进入离线队列：离线成员下次 `get` 时自然看不到这条消息，客户端补拉时**应当**删除本地有、而服务端在相同 seq 区间里没有返回的消息。

## 已读位置

已读用 `note` 上报，`what` 为 `read`、`seq` 为已读到的序号；`note` 没有应答（规范第 12.3 节）。会话里其他在线成员收到 `info` 推送：

```json
{"action":"info","topic":"…","from":"uid_…","what":"read","seq":5}
```

服务端是否保存已读，取决于会话类型和档位（规范第 9.3、12.3 节）：

| 会话 | 保存与返回 |
| --- | --- |
| 群 | 宿主开启已读回执时，存为该成员的已读游标，`get` 用 `readCursors` 返回所有成员的游标；strict 档另用 `readSeq` 返回本人那一项 |
| 单聊，legacy 档 | 不存储，与参考服务相同 |
| 单聊，strict 档 | 存为发送者本人的已读位置，只前进不后退，超过最后一条消息的 seq 时按最后一条存；只通过本人的 `get` 应答的 `readSeq` 返回，对方拿不到 |

strict 档单聊的已读存储不受已读回执开关影响：开关管的是群成员互相看到已读，这里只是同一用户的不同设备共享自己的已读位置（规范第 12.3 节）。`seq` 缺省或不大于 0 的 `read` 不存储。

客户端收到 `readSeq` 时，把本地该会话的已读位置推进到它；本地已经更大时不动，已读位置只前进不后退（规范第 8 节第 5 步）。少了这一步，新设备或换账号后拉回的历史会全部算作未读。多端在线时 `read` 也发给自己的其他设备，“正在输入”（`kp`）不发（规范第 12.3 节）。

## 清空版本

清空用 `del_topic`，对所有成员生效，消息和媒体从服务端物理删除，不可恢复（规范第 11.2 节）。单聊任一方可以清空，群只有群主或管理员可以。

```json
{"id":…,"action":"del_topic","code":200,"topic":…,
 "clearVersion":1,"clearBeforeSeq":12,"deletedMessages":12,"deletedMedia":0}
```

`clearVersion` 是会话的清空版本，客户端只接受比本地记录大的版本；`clearBeforeSeq` 是清空水位，seq 不大于它的消息都要删除（规范第 8、11.3 节）。在线的其他成员立即收到 `topic_cleared`：

```json
{"action":"topic_cleared","topic":"…","from":"uid_…","clearVersion":1,"clearBeforeSeq":12}
```

客户端收到后**必须**删除本地该会话 seq ≤ `clearBeforeSeq` 的全部消息并记录 `clearVersion`；`clearVersion` 不大于本地记录时忽略（规范第 11.3 节）。单聊里这帧的 `topic` 是清空者的 uid。

### 离线时的清空

不在线的成员下次登录时收到同样的帧，多一个 `id`（离线记录的编号，数字字符串），处理完**应当**用 `ack_topic_cleared` 回执；回执没有应答，不回执的记录保留 30 天（规范第 11.3 节）。下面取自黄金对话 [`core/clear-offline`](https://github.com/dishikun2786-prog/Talkwire/blob/main/contract/protocol/transcripts/core__clear-offline.json) 第 11、12 步：

```json
{"action": "topic_cleared", "clearBeforeSeq": 1, "clearVersion": 1, "from": "${uid:A}", "id": "1", "topic": "${uid:A}"}
```

```json
{"action": "ack_topic_cleared", "id": "1"}
```

这里的 `id` 不是请求关联用的 id（规范第 11.3 节，F9）。即使漏掉补投，第 5 步也会兜底：`get` 应答的 `clearVersion` 大于本地记录时，删除本地 seq ≤ `clearBeforeSeq` 的消息（规范第 8 节）。

## 离线群事件

建群、邀请、被踢、群解散等事件发生时，对不在线的相关成员各存一条，下次登录时补投，补投帧比实时帧多一个 `eventId`（规范第 10.10 节）。处理完**应当**回执，回执没有应答，不回执的事件每次登录都重放：

```json
{"action":"ack_group_event","eventId":1}
```

改角色、改群名不入队，离线成员要靠下次 `get_groups` 得知（规范第 10.6、10.10 节）。事件保留 30 天；入队的种类与两档差异见规范第 10.10 节和[已知缺陷](/protocol/defects)的 D5、D9，多端时按设备补投见[多端与推送](/protocol/devices-push)。
