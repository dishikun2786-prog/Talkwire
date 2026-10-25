# 协议事实记录

这些是录制黄金对话时观察到的原版服务行为，是协议规范 v1 草稿（任务 0.6）的输入。F1–F57 来自 Phase 0 的录制；F58 起是 Phase 1 改造服务端时发现的，参考服务同样存在。每条注明出处：录制 = 来自 `contract/protocol/transcripts/` 中的实测帧；代码 = 读源码得到。

| # | 事实 | 出处 | 规范里的处理 |
| --- | --- | --- | --- |
| F1 | 成功的 `login` 应答和 `pres` 快照应答不带请求 `id`；失败应答带 | 录制 core/login、core/p2p | 写明现状；ImKit 补 `id`（只加不改），客户端只对这两种按 `action` 关联 |
| F2 | 刷新令牌、代理令牌、超级管理员令牌、不带 `type` 和 `role` 的令牌都能登录；过期、乱码、缺 `sub` 的被拒（401 `invalid token`） | 录制 core/login-tokens | R18：只接受 `type=access` |
| F3 | 同一用户的新连接顶替旧连接；给旧连接的 401 通知只是有时送达（先入队再立即关闭） | 录制 core/login + 代码 `hub.go` Run | ImKit 先冲刷通知再关闭；多端在线后只在同一设备时顶替 |
| F4 | 单聊用对方 uid 寻址；实时 `data` 推送里 `topic` 是发送方 uid（从接收方看的地址），而 `get` 返回的消息里 `topic` 是规范化的 `p2p_…` | 录制 core/p2p | 规范写明两种表示，客户端负责映射 |
| F5 | 订阅单聊对方、或对单聊 `get` 时，服务端会附带一帧对方的在线状态 | 录制 core/p2p、core/clear-offline | 写入同步算法一节 |
| F6 | 好友关系只在单聊成员关系不存在时向宿主校验；宿主撤销好友后，必须调 `contact-sync` (`removed`) 才会真正禁止 | 录制 core/host-contact-sync | R20 |
| F7 | `contact-sync` 会给双方推 `contact_sync`（`what` = `accepted`/`removed`，`from` = 对方 uid），并返回 `{topic, uidA, uidB}` | 录制 core/host-contact-sync | 写入宿主契约 |
| F8 | 清空（`del_topic`）返回 `clearVersion`、`clearBeforeSeq`、`deletedMessages`；离线成员登录后收到 `topic_cleared`，其 `id` 是待清空记录的行 id | 录制 core/clear-offline | R7、R21 |
| F9 | `ack_topic_cleared` 用请求的 `id` 字段携带待清空记录的行 id，没有应答 | 代码 `hub.go:461` + 录制 | 规范单列；ImKit 可另加明确字段（只加不改） |
| F10 | 被踢出的成员收到的是 `grp_disband`，不是专门的“被移出”事件 | 录制 core/group-admin | 写明现状；可考虑新增 `what` 区分（只加不改） |
| F11 | 设置角色、转让群主时，操作者自己也会收到 `grp_sync` | 录制 core/group-admin | 写入群组一节 |
| F12 | 把成员设为 `owner` 返回 400 `invalid member role`；管理员踢群主返回 401；普通成员踢人被拒 | 录制 core/group-admin | R5 |
| F13 | 加入群的成员必须先登录过至少一次（服务端要从 `im_users` 反查宿主用户 id） | 代码 `handleCreateGrp` + 录制 | 写入群组一节；宿主可调 `/api/v1/im/user/register` 预注册 |
| F14 | 畸形 JSON 返回 `{"action":"error","code":400}`（不带 `id`），连接继续可用 | 录制 core/errors | 写入错误一节 |
| F15 | 未登录时的请求返回 401 `not authenticated`，带 `id` | 录制 core/errors | 写入错误一节 |
| F16 | 没有群时 `get_groups` 的应答省略 `groups` 字段（不是空数组） | 录制 core/errors | 规范写明“缺省即空” |
| F17 | 第 21 条 10 秒内的发布返回 429 | 录制 core/rate-limit | R9、R10 |
| F18 | 历史分页 `limit` 超出 1–200 时退回配置的默认页长（50） | 录制 core/history-paging | R6 |
| F19 | 重复 `clientMsgId` 返回原 `seq` 和原时间戳，且不再广播 | 录制 core/group-messaging | R1 |
| F20 | 每个账号同一时刻只允许一个连接；E2EE 身份按用户存、设备号固定为 1；离线队列按用户确认 | 代码 `hub.go:23`、`store.go` | ADR-0001 决定 3、Phase 1 任务 1.12 |
| F21 | 转发结果用 `type`（`forward_bundle_result` / `forward_broadcast_result`）加 `reqId` 关联，不带 `id`；所有拒绝也走这两种帧 | 录制 core/forward + 代码 `handleForwardBroadcast` | 写入转发一节；客户端按 `reqId` 关联 |
| F22 | `forward_broadcast` 的目标必须是规范 topic 名：传对方 uid 返回 `topic_not_found`（`pub` 却接受对方 uid），传 `p2p_…` 成功 | 录制 core/forward | 写明现状；ImKit 可让转发也接受对方 uid（只加不改） |
| F23 | `forward_broadcast` 按 `reqId` 幂等：同一 `reqId` 再发，即使目标列表不同，也原样返回第一次的结果、不再转发 | 录制 core/forward + 代码 `claimForwardBroadcast` | R1；规范写明 `reqId` 必须每次请求唯一 |
| F24 | 只有建群/邀请、踢人、解散、入群审核会给离线成员入队；改角色、改群名不入队。补投的 `grp_sync` 带 `eventId`，群名是入队时的旧名；成员已不在群里的 `grp_sync` 补投前被静默丢弃；未回执的事件每次登录都重放 | 录制 core/group-offline-events + 代码 `deliverPendingGroupEvents` | R8；ImKit 补投时取当前群名 |
| F25 | `ack_group_event` 用 `eventId` 字段，无应答 | 录制 core/group-offline-events | 写入同步算法一节 |
| F26 | 解散群时操作者自己也收到 `grp_disband`；改群名只给其他在线成员推带新群名的 `grp_sync`（不含操作者），离线成员不入队，所以离线成员要靠下次拉群列表才知道新名 | 录制 core/group-offline-events + 代码 `handleUpdateGrp` | 写入群组一节；R8 |
| F27 | `reg_token` / `unreg_token` 复用 `token` 字段放推送令牌，没有任何应答，缺参数也不报错 | 录制 core/push-token | 规范写明“无应答”；ImKit 可另加明确字段（只加不改） |
| F28 | 消息正文上限按字符计 20000，超出返回 400 `message too long`；群名超过 30 字符返回 400，`reason` 是中文 `群名称过长`；单帧超过 256 KB 时服务端直接断开连接，不发错误帧 | 录制 core/limits + 代码 `client.go:46` | R9、R10；规范把 `reason` 定为不透明文本，客户端只看 `code` |
| F29 | 单聊的 `recalled` 和 `info`（输入中/已读）推送里，`topic` 原样照抄发送方请求里的字符串：发送方按对方 uid 寻址时，接收方看到的是**自己的** uid；按 `p2p_…` 寻址时看到 `p2p_…`。`data` 推送则总是发送方 uid | 录制 core/recall-p2p、core/notes + 代码 `handleDel`、`handleNote` | 规范写明现状，客户端按“`topic` 等于自己的 uid 时，会话对象取 `from`”映射；ImKit v1 保持逐帧一致，修正放到能力协商后 |
| F30 | `pres` 查询不检查关系：任何登录用户都能查任意 uid 是否在线和最后在线时间（陌生人也行）；上限 500 个 uid；自己和非 uid 项被丢弃，重复项去重 | 录制 core/presence + 代码 `handlePresQuery` | 新红线 R22：只返回与查询者共享 topic 的用户 |
| F31 | 在线状态的推送只发给与该用户共享至少一个 topic 的在线用户；`pres` 快照应答不带 `id`，用 `what=snapshot` 与推送区分；离线后快照带 `lastSeenAt`（毫秒） | 录制 core/presence | 写入在线状态一节 |
| F32 | `note` 没有应答；缺 `topic` 或 `what` 时静默丢弃；非成员收到 `action=note` 的 401。开启已读回执时，群的已读游标会存下来，`get` 群消息时用 `readCursors` 返回（含读者自己的游标） | 录制 core/notes + 代码 `handleNote`、`handleGet` | 写入输入中与已读一节 |
| F33 | 撤回是软删：被撤回的消息从 `get` 里消失，但 seq 不复用；非发送者撤回返回 404 `message not found or not yours` | 录制 core/recall-p2p | R3 |
| F34 | `data` 推送的 `ts` 取广播时刻，`pub` 应答和历史消息的 `ts` 取入库时刻，两者可能相差几毫秒 | 代码 `announceMessage`、`deliverMessage` | 规范写明以历史消息的 `ts` 为准 |
| F35 | `leave_grp` 不校验调用者是否是成员：非成员对任意群发 `leave_grp` 得到 200，群里所有在线成员收到一条 `from` 为该非成员的 `grp_sync` | 录制 core/leave-grp + 代码 `handleLeaveGrp` | 已知缺陷 D1：规范要求 401；ImKit 严格模式修正 |
| F36 | `leave_grp` 不校验 topic 类型：对 `p2p_…` 发送会把自己从单聊成员中移除（之后 `get` 该 topic 返回 401），对方收到 `topic` 为 `p2p_…` 的 `grp_sync`；下次按对方 uid 发消息时重新校验好友并恢复成员关系 | 录制 core/leave-grp | 已知缺陷 D2：规范要求 400 |
| F37 | 群主在还有其他成员时 `leave_grp` 返回 409；群主是最后一名成员时退出即解散；普通成员退出后，其余在线成员收到 `grp_sync`（`from` 为退出者），退出者自己不收 | 录制 core/leave-grp | 写入群组一节 |
| F38 | 未知 `action` 返回 400 `unknown action: <名>`，`action` 原样回显、带 `id`；`action` 为空的帧被静默丢弃；未登录的连接没有超时，只要回 pong 就能一直挂着 | 代码 `ProcessMessage`、`handleMessage`、`ReadPump` | 写入错误一节；未登录超时列入 ImKit 加固 |
| F39 | 订阅属于连接，登录时不会恢复。重连后，群成员在重新 `sub` 之前收不到该群的 `data` 推送；此时他在线，离线推送也不会发。单聊例外：对方 `pub` 时服务端会顺带订阅在线的接收方。漏掉的消息用 `get` + `afterSeq` 补回 | 录制 core/resubscribe + 代码 `Hub.Run`、`resolveP2PTopic` | 写入同步算法：登录后必须先拉群列表并逐个 `sub`，再按各会话本地最大 seq 补拉 |
| F40 | `get_members` 的成员结构预留了 `name`，但参考服务从不填写；成员按入群时间升序，同一事务里一起入群的成员时间相同，彼此顺序由数据库决定 | 代码 `GetGroupMembers` + 录制 core/leave-grp | 规范写明；一致性比较对这类数组按 uid 排序后再比 |
| F41 | 群 topic（12 位）和通话会话 id（32 位）用 `math/rand` 生成，不是密码学安全随机数。结合 F35，知道群 topic 的陌生人就能向全群推 `grp_sync` | 代码 `RandString`、`CreateGroup` | ImKit 改用 `crypto/rand`；群 topic 不应被当作访问凭据 |
| F42 | E2EE 每个账号只有一套密钥，`deviceId` 恒为 1；`e2e_fetch_bundle`、`e2e_send`、`e2e_clear`、`e2e_terminate` 每次都向宿主查一次好友关系，不缓存；取密钥包会消耗并删除一个一次性预密钥，用完后照常返回其余字段 | 录制 module/e2ee-p2p | 写入第 13.1 节；多设备 E2EE 不在 v1 范围 |
| F43 | 单聊 `e2e_send` 的 `topic` 只是客户端自定的标签，服务端不校验、原样转发；放行条件是已有单聊、同在加密群或好友 | 录制 module/e2ee-p2p + 代码 `e2ePeerAllowed` | 写入第 13.2 节 |
| F44 | 群加密开启后不能关闭；重复开启返回成功并再次广播；之后普通 `pub` 返回 409，同群成员可以互取密钥包；踢人、退群会广播 `member_removed` / `member_left` 提示换密钥 | 录制 module/e2ee-group | 写入第 13.3 节 |
| F45 | 不需审核的扫码入群只回 `joined`：不通知任何人，入群者不收 `grp_sync`、也不被订阅。需审核时审核通过只通知申请人（`group_join_reviewed` + `grp_sync`），其余成员不收通知 | 录制 module/qr-join + 代码 `handleGroupJoin` | 已知缺陷 D7 |
| F46 | `group_join_request` 推送带申请人 uid（`from`），与源码注释“不带申请人信息”不符；入群申请列表里宿主查不到申请人资料时，`requesterName` 退回申请人 uid；刷新二维码后旧令牌立即失效 | 录制 module/qr-join | 写入第 14 节 |
| F47 | 非成员、未登录或出错时 `get_group_ai_settings` 不报错，返回与真实默认设置相同的默认值（每日 50 次、冷却 5 秒） | 录制 module/group-ai | 写入第 15.1 节 |
| F48 | 群 AI 回复的 `contentType` 是 `text`（不是 `text/plain`），`from` 为 `assistant_xiaotaozi`；冷却期内的提问静默忽略；宿主拒绝时服务端把原因换成写死的中文文案，作为 AI 消息存入历史；向宿主提问时请求体带完整问题、知识库 id 和群主的宿主 id | 录制 module/group-ai | 写入第 15.2 节；ImKit 把触发词与兜底文案做成宿主配置 |
| F49 | 通话成功发起时被叫方先收到 `call_ring`、主叫方后收到应答；`call_invite` 不校验好友，由宿主的建会话接口决定；非参与者操作一通电话返回 404；任一方都能请求升级视频；振铃超时双方都收到 `no_answer` | 录制 module/call、module/call-timeout | 写入第 16 节 |
| F50 | 宿主拒绝发起的通话也会生成会话 id 并传给宿主的建会话接口，但这个 id 不会出现在任何应答里 | 录制 module/call | 一致性工具把未捕获的 `callSessionId` 归一为 `<call-id>` |
| F51 | 同一用户的新连接顶替旧连接时，共享会话的在线好友收不到下线，反而再收到一次 `what:"on"`（`presRev` 递增）；新连接自己的群订阅不受旧连接清理影响，群消息和单聊消息都送到新连接 | 录制 core/supersede | R3；写入第 12.1 节 |
| F52 | 踢人的四道守卫：踢自己 400、普通成员踢人 401、踢群主 401、管理员踢管理员 401（只有群主能踢管理员）；踢非成员 400。重复邀请已在群里的人直接返回 200，不改角色，也不查好友关系 | 录制 core/group-guards + 代码 `handleKick`、`handleInvite` | R5；写入第 10.3、10.4 节 |
| F53 | 撤回后文件不再被任何消息引用时，服务端向宿主发 `delete-media`；仍被其他消息引用则不发。群的清空只限群主或管理员，普通成员 401。清空的 `deletedMessages` 把撤回留下的墓碑也算进去，`deletedMedia` 是清空时仍被引用的不同文件数，其中无人引用的随后请求宿主删除。撤回视频时缩略图引用被清掉但不请求删除（读代码） | 录制 core/clear-media + 代码 `SoftDeleteMessage`、`ClearTopicMessages`、`gcMediaBlob` | R6、R21；写入第 9.5、11.2 节；缩略图由计划 1.9 修复 |
| F54 | `leave` 不校验成员关系和 topic 是否存在，重复退订、退订不存在的群都返回 200；退订后仍是成员，`get` 照常可用。单聊的订阅记在规范的 `p2p_…` topic 下：按对方 uid 发 `leave` 返回 200 但不取消任何东西，对方下线照样推过来；按 `p2p_…` 退订后不再收到对方的上下线，直到对方再发来一条消息把本连接重新订阅 | 录制 core/leave | 写入第 9.4 节 |
| F55 | `invite` 没有接受步骤，应答前对方已是成员。在线的被邀请者被订阅并收到 `grp_sync`，之后群消息直接送到；离线的在下次登录收到带 `eventId` 的 `grp_sync`；群里其余成员收不到任何通知。非成员邀请 401 `only group members can invite`，邀请非好友 401 `not friends`，对单聊邀请 400 | 录制 core/invite | R8；写入第 10.3 节 |
| F56 | 入群审核拒绝时申请人收到 `joinStatus:"rejected"` 的 `group_join_reviewed`，不加入任何人；被拒后可以用同一个令牌再次申请，得到新的申请 id。普通成员审核 401 | 录制 module/qr-join-review | 写入第 14.3 节 |
| F57 | `group_join_review` 先把申请标为已处理，之后才检查申请是否属于请求里的 `topic`。另一个群的管理员拿别的群的申请 id 来审核，得到 400 `请求与群聊不匹配`，但申请已被消耗：从待处理列表消失，真正的群主再审核得到 409，申请人没入群也没收到通知。通过时“群不存在”“人数已满”两项检查同样在写入之后（读代码） | 录制 module/qr-join-review + 代码 `handleGroupJoinReview` | 已知缺陷 D8；写入第 14.3、18 节 |
| F58 | 发起通话时给 `call.ringTimer` 赋值不持 `call.mu`（`hub.go` 的 `handleCallInvite`），而取消呼叫、断线结束通话会在别的 goroutine 里调用持锁读它的 `resolve()`。`resolve()` 赶在赋值前执行时停不掉计时器，振铃超时照常触发，因状态已是结束而空转。不影响线上帧，但是数据竞争；`call_test.go` 同款写法在 `go test -race` 下失败 | `server/` 首次在竞态检测下跑原有单测 + 代码 | 已修（计划 1.11）：在锁内启动计时器，状态已不是振铃时不启动；`TestCallStateStartRingTimer_OnlyWhileRinging`、`TestCallStateStartRingTimer_RaceWithResolve` |
| F59 | 入群申请（`group_join_request`）和审核结果（`group_join_reviewed`）的离线事件从不入队：存储层只接受 `grp_sync`、`grp_disband` 两种事件，入队调用总是返回错误、只记日志，不在线的管理员和申请人从来收不到 | 代码 `EnqueuePendingGroupEvent`、`notifyGroupManagersOfJoinRequest`、`notifyRequesterOfReview` | 已知缺陷 D9；strict 档入队并补投（计划 1.10，`TestD9_JoinReviewQueuedForOfflineApplicant`），legacy 档保持原样 |
| F60 | 被顶替的连接在被顶替前已读入、之后才处理的 `sub`、`leave`、`pub`，会抢走新连接在话题索引里的位置，或把它删掉：新连接自以为已订阅，却再也收不到该会话的推送，直到重新登录 | 竞态测试 + 代码 `subscribeTopic`、`unsubscribeTopic` | 已修（计划 1.11）：每次登录分配递增的代号，索引只许代号更新的连接覆盖，已关闭的连接不入索引，退订只删自己；`TestR3_LateFrameFromReplacedConnection`、`TestR3_TopicIndexRules` |
| F61 | 同一个新用户在两条连接上几乎同时首次登录时，后一个插入撞上 `im_users_im_user_id_key` 唯一约束（`ON CONFLICT` 只仲裁 `app_user_id`），这次登录得到 500 `server error` | 竞态测试 + 代码 `EnsureUser` | 已修（计划 1.11）：撞上该约束时重试一次；`TestEnsureUserConcurrentFirstLogin` |
