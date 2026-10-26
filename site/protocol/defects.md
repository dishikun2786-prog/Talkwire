---
title: "已知缺陷"
description: "参考服务里被记为缺陷的九种行为，各自出什么问题、哪一档修正、客户端该怎么应对，供迁移旧客户端时对照。"
---

# 已知缺陷

规范第 18 节把参考服务的九种行为记为缺陷 D1–D9：legacy 档保留它们，strict 档修正 D6 以外的全部，D6 两档相同、留给协议 v2。按每条的“客户端应对”处理，客户端就能在两档下都正常工作（规范第 18 节）。

参考服务指原版 IM 服务，现网旧客户端是对着它写的。下文每条写明旧客户端可能依赖了什么，以及切到 strict 档后它会看到什么变化。

## 迁移时怎么用这一页

迁移的顺序是“先换服务端（legacy）、再换客户端、最后切 strict”（[ADR-0002](https://github.com/dishikun2786-prog/Talkwire/blob/main/docs/adr/0002-protocol-profiles.md)）。legacy 档保留全部缺陷，所以换服务端这一步旧客户端看到的帧不变。

切 strict 之前，逐条核对旧客户端是否依赖下表“参考服务的行为”一列；依赖了的，先按“客户端应对”改新客户端，等旧客户端淘汰后再切（规范第 0.3 节）。完整步骤见[从现有 IM 系统迁移](/migrate/)。

| 编号 | 参考服务的行为 | strict 档 | 客户端应对 |
| --- | --- | --- | --- |
| D1 | 非成员 `leave_grp` 也得到 200，并让全群收到 `from` 为陌生人的 `grp_sync` | 401，不推送 | 收到 `grp_sync` 只当作刷新提示，不信任其中的 `from` |
| D2 | `leave_grp` 对单聊生效，把自己移出单聊 | 400 | 禁止对单聊发 `leave_grp`；忽略 `topic` 为 `p2p_…` 的 `grp_sync` |
| D3 | `pres` 可查询任意 uid | 只返回共享会话的用户 | 不依赖查询陌生人 |
| D4 | 登录接受非访问令牌 | 只接受 `type=access` | 只使用访问令牌 |
| D5 | 补投的 `grp_sync` 带入队时的旧群名 | 带当前群名 | 以 `get_groups` 的群名为准 |
| D6 | 单聊 `recalled`、`info` 的 `topic` 照抄请求，可能是接收方自己的 uid | 不修正 | 按规范第 5.5 节映射 |
| D7 | 不需审核的扫码入群不通知任何人，也不订阅入群者 | 订阅并通知 | 收到 `joined` 后自己 `sub` 并刷新群列表 |
| D8 | `group_join_review` 先写入再检查，检查失败时申请被消耗 | 先检查后写入 | 用申请所属群的 `topic` 审核；群满先腾名额 |
| D9 | 入群申请与审核结果的离线事件从不入队 | 入队并补投 | 登录后主动拉 `group_join_requests_list` |

## D1 `leave_grp` 不校验成员关系

参考服务对任意群的 `leave_grp` 都不检查调用者是不是成员：非成员也得到 200，群里所有在线成员收到一条 `from` 为该非成员的 `grp_sync`（规范第 18 节，F35）。

strict 档对非成员返回 401 `not a member of this conversation`（`errCode` 为 `not_member`），不推送任何帧、不改变成员关系（规范第 10.5 节）。下帧取自 [`transcripts-strict/core__leave-grp.json`](https://github.com/dishikun2786-prog/Talkwire/blob/main/contract/protocol/transcripts-strict/core__leave-grp.json) 第 11 步：

```json
{"action": "leave_grp", "code": 401, "errCode": "not_member", "id": "s11", "reason": "not a member of this conversation"}
```

客户端应对：收到 `grp_sync` 只当作“去刷新群信息”的提示，不信任其中的 `from`（规范第 18 节）。群 topic 由非密码学随机数生成，不能当作访问凭据；在参考服务上，知道群 topic 的陌生人可以借 D1 向全群推 `grp_sync`（F41）。

## D2 `leave_grp` 对单聊生效

参考服务不检查 topic 类型：对 `p2p_…` 发 `leave_grp` 会把自己从单聊成员中移除，之后 `get` 该 topic 返回 401，对方收到 `topic` 为 `p2p_…` 的 `grp_sync`（F36）。下次按对方 uid 发消息时，服务端重新校验好友并恢复成员关系。

strict 档对不是 `grp_` 的 topic 返回 400 `only group topics`（`errCode` 为 `not_a_group`），不推送、不改成员关系（规范第 10.5 节）。同一份 strict 对话第 19 步：

```json
{"action": "leave_grp", "code": 400, "errCode": "not_a_group", "id": "s19", "reason": "only group topics"}
```

客户端应对：**禁止**对单聊发 `leave_grp`；收到 `topic` 为 `p2p_…` 的 `grp_sync` 时忽略（规范第 10.5、18 节）。

## D3 `pres` 可查询任意 uid

参考服务的 `pres` 查询不检查关系，任何登录用户都能查任意 uid 是否在线和最后在线时间，陌生人也行（F30）。黄金对话 [`core/presence`](https://github.com/dishikun2986-prog/Talkwire/blob/main/contract/protocol/transcripts/core__presence.json) 第 9 步里，与 A 没有共享会话的 C 仍出现在 A 的查询结果里。

strict 档只返回与查询者共享会话的用户，其他 uid 从结果中剔除（规范第 12.2 节，红线 R22）。客户端应对：不依赖查询陌生人的在线状态；需要展示在线状态的对象，都应是已有单聊或同群的人。

## D4 登录接受非访问令牌

参考服务接受任何带 `sub` 的有效 HS256 令牌：刷新令牌、代理令牌、超级管理员令牌、不带 `type` 和 `role` 的令牌都能登录（规范第 6.1 节，F2）。过期、乱码、缺 `sub` 的令牌两档都拒绝。

strict 档只接受 `type=access`、`role` 在 `TALKWIRE_TOKEN_USER_ROLES`（默认 `app_user`）里的令牌，其余一律 401 `invalid token`，`errCode` 为 `invalid_token`（规范第 6.1 节，红线 R18，ADR-0002 决定 3）。迁移期还有旧客户端用别的令牌时，可以临时设 `TALKWIRE_ACCEPT_LEGACY_TOKENS=1` 让 strict 档也接受全部令牌，服务端启动时会打印警告。

客户端应对：只使用访问令牌；宿主**应当**只把访问令牌交给客户端，这样两档行为相同（规范第 6.1 节）。令牌的签发见[签发 IM 令牌](/host/token)。

## D5 补投的 `grp_sync` 带旧群名

离线成员下次登录时补投的 `grp_sync`，在参考服务里带的是事件入队时的群名，群改过名就是旧名（规范第 10.10 节，F24）。

strict 档补投的帧凡带 `name` 的都用补投时的群名；群已不存在或群名读不到时，用入队时的群名（规范第 10.10 节）。客户端应对：群名以 `get_groups` 的结果为准，不拿补投帧里的 `name` 覆盖它（规范第 18 节）。

## D6 单聊 `recalled`、`info` 的 `topic` 照抄请求

单聊的 `recalled`、`info` 推送里，`topic` 原样照抄发送方请求里的字符串：发送方按对方 uid 寻址时，接收方看到的是**自己的** uid；按 `p2p_…` 寻址时看到 `p2p_…`（规范第 5.5 节，F29）。这与 `data` 推送总是写发送方 uid 不同。

两档都保持不变，因为改动会破坏已有客户端；修正留给协议 v2（规范第 18 节）。黄金对话 [`core/recall-p2p`](https://github.com/dishikun2986-prog/Talkwire/blob/main/contract/protocol/transcripts/core__recall-p2p.json) 第 7 步，A 按 B 的 uid 撤回，B 收到的 `topic` 是 B 自己：

```json
{"action": "recalled", "from": "${uid:A}", "seq": 1, "topic": "${uid:B}"}
```

同一对话第 11 步，A 按规范 topic 撤回，B 收到的 `topic` 是 `p2p_…`：

```json
{"action": "recalled", "from": "${uid:A}", "seq": 2, "topic": "${p2p:A,B}"}
```

客户端应对：按规范第 5.5 节的映射规则找会话（规范第 18 节）：

1. `topic` 以 `grp_` 开头：群会话。
2. `topic` 以 `p2p_` 开头：单聊，对方是 `from`；没有 `from` 时（只在历史消息里出现）按规范第 5.2 节从本地已知的好友 uid 反查。
3. `topic` 等于自己的 uid：单聊，对方是 `from`。
4. 其他 uid：单聊，对方就是这个 uid。

## D7 不需审核的扫码入群没有通知

在参考服务上，群不需要审核时 `group_join` 只回 `joinStatus: "joined"`：不通知任何人，入群者不收 `grp_sync`，也没有被订阅（规范第 14.2 节，F45）。入群者因此收不到之后的群消息，直到自己 `sub`。

strict 档订阅发请求的连接；群里其余在线成员收到 `from` 为入群者的 `grp_sync`；入群者的其他设备收到同样的 `grp_sync` 并被订阅，没连上的设备入队（规范第 14.2 节）。需要审核的群在审核通过时只通知申请人，这一点两档相同（F45）。

客户端应对：收到 `joined` 后**必须**自己 `sub` 该群并刷新群列表（规范第 14.2 节）。在 strict 档这样做无害。

## D8 入群审核先写入再检查

参考服务的 `group_join_review` 先把申请标为已处理，再检查它是否属于请求里的 `topic`、群是否存在、人数是否已满（规范第 14.3 节，F57）。后几项失败时返回 400、404 或 409，但申请已被消耗：从待处理列表消失，真正的群管理员再审核只得到 409，申请人没入群也没收到通知。

strict 档先做完全部检查再写入，状态与成员行在同一个事务里写；任一检查失败时申请保持待处理（规范第 14.3 节）。对另一个群的申请，两档的应答相同：已处理的 409，待处理的 400。

客户端应对：审核时用申请所属群的 `topic`（取自 `group_join_request` 帧或待处理列表）；群满时先腾出名额再通过；在 legacy 档遇到这类失败，只能让申请人重新申请（规范第 14.3、18 节）。被拒的申请人可以用同一个二维码令牌再次申请，得到新的申请 id（F56）。

## D9 入群申请与审核结果不进离线队列

参考服务的离线事件存储只接受 `grp_sync`、`grp_disband` 两种，入群申请（`group_join_request`）和审核结果（`group_join_reviewed`）的入队总是失败、只记日志（规范第 10.10 节，F59）。不在线的管理员收不到申请，不在线的申请人收不到审核结果。

strict 档把这两种事件入队，下次登录时补投，审核结果带 `joinStatus`；补投帧同样带 `eventId`，按 `ack_group_event` 回执（规范第 10.10 节）。客户端应对：登录后主动拉 `group_join_requests_list`，不依赖补投（规范第 18 节）。

## 两档都已修正的其他问题

除 D1–D9 外，[`docs/protocol-findings.md`](https://github.com/dishikun2986-prog/Talkwire/blob/main/docs/protocol-findings.md) 还记录了几项参考服务同样存在、Talkwire 服务端在两档下都已修正的问题。它们不改变帧的格式，但旧客户端如果曾为它们写过绕行代码，可以在迁移后去掉。

| 编号 | 参考服务的行为 | Talkwire 服务端 |
| --- | --- | --- |
| F53 | 撤回视频时缩略图的引用被清掉，却不请求宿主删除，成为回收不到的文件 | 两档都一并回收缩略图（规范第 9.5 节） |
| F60 | 被顶替的连接里迟到的 `sub`、`leave`、`pub` 会抢走或删掉新连接的订阅，新连接自以为已订阅却收不到推送 | 已修：每次登录分配递增的代号，订阅索引只许代号更新的连接覆盖，退订只删自己 |
| F61 | 同一新用户在两条连接上几乎同时首次登录，其中一次得到 500 | 已修，撞上唯一约束时重试一次 |
| F63 | 登录过程中断开的连接可能被注册为在线，该用户一直显示在线 | 已修，注册时跳过已关闭的连接 |
| F64 | 补投的 `e2e_deliver` 可能不按发送顺序到达 | 已修，按发送顺序补投 |
| F65 | 积压的 E2EE 信封塞不进发送队列时被丢掉且找不回 | 已修，塞不下的放回队列，下次登录再补投 |

各项的复现与对应测试见 `docs/protocol-findings.md` 同编号一行。
