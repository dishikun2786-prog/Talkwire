# 红线对照表

> 由 `contract/redlines/redlines.json` 生成，请勿手改。改完 JSON 后在 `tools/conformance` 下运行 `go run ./cmd/redlines write`；`go run ./cmd/redlines check` 校验它与场景、与本文件是否一致。

ImKit 红线对照表（计划任务 0.8）。每条红线对应到已录的黄金对话步骤和计划中的自动化用例。matrix.md 由本文件生成，校验与生成命令见 tools/conformance/README.md。

## 概况

- 红线 32 条：服务端与宿主 22 条（R），客户端 10 条（C）。
- 已有黄金用例的红线 17 条，共引用 23 个场景的 118 处步骤。
- R 类红线在参照服务上的现状：符合 13、部分符合 1、违反 3、未观测 5。
- 计划用例 77 个。按平台：服务端 25、宿主 8、Android 20、Web 12、iOS 12。
- 按测试层：单测 26、并发压力 7、SQL 扫描 1、契约测试 9、strict 档一致性 3、回放 14、回放 + 故障注入 10、UI 测试 5、真机 2。
- 已写用例（已写/计划）：服务端 25/25、宿主 8/8、Android 18/20、Web 12/12。

图例：

- 参照服务：小桃子原版服务（源提交 33e23250）在黄金对话里的表现。“违反”的红线在 legacy 档按原样录、在 strict 档由计划用例修正；“未观测”是黄金对话录不到，只能靠计划用例。
- 已录用例：已经录成黄金对话的场景与步号，ImKit 服务端用一致性测试台逐帧比对，三端 SDK 用回放服务器重放。
- 计划用例：自动化用例，标明计划任务号与平台，用例名带红线编号；并发压力用例重复 100 轮并开竞态检测，单次通过不算数。服务端、宿主、Android 与 Web 已经写好的标“已写”：`redlines check` 扫描 `server/` 下的 Go 测试函数、`host-sdk-node/` 的 node:test 用例、`android/` 各模块 `src/test` 下的 Kotlin 测试函数和 `web/` 下的 Vitest 与 Playwright 用例（`*.test.ts(x)`、`*.spec.ts` 里的 `it(…)`、`test(…)`）得出；按红线命名（服务端 `TestR<n>_…`，宿主 `R<n>_…`，Android 与 Web `R<n>_…` 或 `C<n>_…`）却不在本表里的测试会被报出来。iOS 还没有代码，不扫描。

## 总表

| 编号 | 红线 | 平台 | 参照服务 | 已录用例 | 计划用例 |
| --- | --- | --- | --- | --- | --- |
| [R1](#r1) | clientMsgId 幂等，重复提交返回原 seq、不再广播 | 服务端 | 符合 | `core/group-messaging` 8、9 | `TestR1_ConcurrentDuplicatePublish`：服务端 1.11 已写 |
| [R2](#r2) | 应答回填 id（含新增的登录与在线快照） | 服务端 | 部分符合 | `core/errors` 2、3、5、6、7、9<br>`core/group-messaging` 6、8<br>`core/login` 3 | — |
| [R3](#r3) | 被顶替的连接不清新连接的订阅、不广播下线；不在持锁时断开连接 | 服务端 | 符合 | `core/supersede` 9、10、11、12、13<br>`core/login` 5 | `TestR3_SupersedeStorm`：服务端 1.11 已写<br>`TestR3_LateFrameFromReplacedConnection`：服务端 1.11 已写<br>`TestR3_TopicIndexRules`：服务端 1.11 已写 |
| [R4](#r4) | 在线状态串行化、版本号单调、批量查询 | 服务端 | 符合 | `core/presence` 8、9、10、12、13、14、16<br>`core/p2p` 7、14、15 | `TestR4_PresenceRevMonotonic`：服务端 1.11 已写 |
| [R5](#r5) | 群成员写入不降级、不能改群主角色、踢人 4 道守卫 | 服务端 | 符合 | `core/group-guards` 12、13、14、15、16、17、18、19<br>`core/group-admin` 9、10、11 | `TestR5_MemberWritesNeverDowngrade`：服务端 1.10 已写 |
| [R6](#r6) | 撤回是软删，所有读消息的查询都过滤；历史条数只在一处归一化 | 服务端 | 符合 | `core/recall` 9、10、11、12<br>`core/recall-p2p` 7、8、9、10、11<br>`core/history-paging` 9、10、11、12<br>`core/clear-media` 11、12 | `TestR6_MessageReadsFilterRecalled`：服务端 1.10 已写<br>`TestR6_HistoryLimitNormalizedOnce`：服务端 1.10 已写<br>`TestR6_RecallReclaimsVideoThumbnail`：服务端 1.9 已写 |
| [R7](#r7) | 清空：水位 + 离线补投 + ack + 30 天清理，对离线成员生效 | 服务端、Android、Web、iOS | 符合 | `core/clear-offline` 8、9、11、12、15 | `TestR7_PendingClearsExpireAfter30Days`：服务端 1.10 已写<br>`R7_ClearOfflineReplay`：Android 3.10 已写、Web 5.8 已写、iOS 6.6 |
| [R8](#r8) | 群事件离线队列同理 | 服务端 | 符合 | `core/group-offline-events` 6、7、8、9、10、12、13、16<br>`core/invite` 19、24 | `TestR8_GroupEventsExpireAfter30Days`：服务端 1.10 已写 |
| [R9](#r9) | 可配项默认值等于原写死值（如消息上限 20000） | 服务端 | 符合 | `core/limits` 4、5、6<br>`core/rate-limit` 23、24<br>`core/history-paging` 12 | `TestR9_DefaultsEqualOriginalValues`：服务端 1.10 已写 |
| [R10](#r10) | 限流、连接上限、帧上限、队列满即断开（E2EE 走降级）、重复登录拒绝 | 服务端 | 符合 | `core/rate-limit` 24<br>`core/limits` 7<br>`core/login` 3 | `TestR10_ConnectionCap`：服务端 1.11 已写<br>`TestR10_SendQueueFullDisconnects`：服务端 1.11 已写<br>`TestR10_E2EEQueueFullFallsBackToOffline`：服务端 7.1 已写 |
| [R11](#r11) | 心跳间隔 0.45 × 超时 | 服务端 | 未观测 | — | `TestR11_PingIntervalIs045OfTimeout`：服务端 1.10 已写 |
| [R12](#r12) | 推送只发数据载荷；加密消息不带预览；备注按方向 | 服务端、宿主 | 未观测 | — | `TestR12_PushPayloadIsDataOnly`：服务端 1.4 已写<br>`TestR12_EncryptedMessageHasNoPreview`：服务端 1.4 已写<br>`R12_ProfileAliasByDirection`：宿主 2.2 已写 |
| [R13](#r13) | 内部接口鉴权；后台清空二次确认加审计 | 服务端、宿主 | 未观测 | — | `TestR13_InternalEndpointsRejectBadTokens`：服务端 2.1 已写<br>`R13_HostEndpointsRejectBadTokens`：宿主 2.1 已写<br>`R13_AdminClearNeedsConfirmAndAudit`：宿主 2.5 已写 |
| [R14](#r14) | 群成员不能互看资料；加好友需一次性令牌 | 宿主 | 未观测 | — | `R14_GroupMembersCannotViewProfiles`：宿主 2.3 已写<br>`R14_AddContactRequiresOneTimeToken`：宿主 2.3 已写 |
| [R15](#r15) | 媒体带 Content-Length；删前查引用；私密媒体不进 CDN | 宿主 | 未观测 | — | `R15_MediaResponsesCarryContentLength`：宿主 2.4 已写<br>`R15_DeleteChecksReferences`：宿主 2.4 已写<br>`R15_PrivateMediaBypassesCDN`：宿主 2.4 已写 |
| [R16](#r16) | 通话状态用比较后替换；断线宽限；计时器显式停；用宿主用户 id | 服务端 | 符合 | `module/call` 11、13、14、19、21、25、27<br>`module/call-timeout` 6、7 | `TestR16_CallStateCASUnderRace`：服务端 1.11 已写<br>`TestR16_DisconnectGraceEndsCall`：服务端 7.2 已写<br>`TestR16_TimersStoppedOnEveryPath`：服务端 7.2 已写 |
| [R17](#r17) | E2EE 只存转不透明信封；补投；本机身份初始化互斥 | 服务端、Android、iOS | 符合 | `module/e2ee-p2p` 16、20、22、25<br>`module/e2ee-group` 17、18 | `R17_IdentityInitIsMutuallyExclusive`：Android 7.1 已写、iOS 7.1 |
| [R18](#r18) | 用户令牌只接受 type=access，刷新、代理、管理员令牌一律拒绝 | 服务端 | 违反 | `core/login-tokens` 2、4、6、8、10、12、14 | `TestR18_OnlyAccessTokensLogIn`：服务端 1.8 已写 |
| [R19](#r19) | 登录与在线快照应答带 id；客户端只对旧服务端按 action 关联这两种 | 服务端、Android、Web、iOS | 违反 | `core/login` 2、6<br>`core/p2p` 11<br>`core/presence` 10 | `TestR19_LoginAndSnapshotRepliesCarryId`：服务端 1.7 已写<br>`R19_CorrelateLegacyRepliesByAction`：Android 3.4 已写、Web 5.2 已写、iOS 6.1 |
| [R20](#r20) | 好友关系在单聊首次建立时校验；解除好友经 contact_sync 收回 | 服务端 | 符合 | `core/host-contact-sync` 5、7、8、11、12、13<br>`core/p2p` 12、13 | — |
| [R21](#r21) | 清空是全员物理删除并回收媒体 | 服务端 | 符合 | `core/clear-offline` 8、9<br>`core/clear-media` 13、14、15 | — |
| [R22](#r22) | 在线状态查询只返回与查询者共享会话的用户（参考服务可查任意用户） | 服务端 | 违反 | `core/presence` 9、14 | `TestR22_PresenceOnlyForSharedTopics`：服务端 1.8 已写 |
| [C1](#c1) | 重连抖动、建连前清身份、连接串行化、登录确认前不算已连接、世代号 | Android、Web、iOS | 不适用 | — | `C1_ReconnectAfterInjectedDisconnect`：Android 3.10 已写、Web 5.8 已写、iOS 6.6<br>`C1_IgnoresDuplicatedFrames`：Android 3.10 已写、Web 5.8 已写 |
| [C2](#c2) | 回执超时但连接仍在时保持“发送中”、不自动重发；媒体按 URL 对账 | Android、Web、iOS | 不适用 | — | `C2_LostPublishReplyStaysSending`：Android 3.10 已写、Web 5.8 已写、iOS 6.6<br>`C2_MediaReconciledByUrl`：Android 3.7 已写、Web 5.8 已写、iOS 6.6 |
| [C3](#c3) | 离线补拉取本地连续段末尾并检测空洞 | Android、Web、iOS | 不适用 | — | `C3_ResumeFromContiguousEnd`：Android 3.10 已写、Web 5.8 已写、iOS 6.6 |
| [C4](#c4) | 缓存归属：换账号清空全部缓存表 | Android、Web、iOS | 不适用 | — | `C4_SwitchingAccountClearsAllCaches`：Android 3.5 已写、Web 5.4 已写、iOS 6.3 |
| [C5](#c5) | 在线三态加过期时间，只有一个真相源 | Android、Web、iOS | 不适用 | — | `C5_PresenceTriStateWithExpiry`：Android 3.7 已写、Web 5.2 已写、iOS 6.1 |
| [C6](#c6) | 通知抑制条件、渠道静音由 App 播声、来电通知时序与取消 | Android | 不适用 | — | `C6_NotificationDecision`：Android 3.8 已写<br>`C6_DeviceSoundAndCallTiming`：Android 4.5 |
| [C7](#c7) | 媒体缓存；语音气泡随时长变宽；占位符用类型字段 | Android、Web、iOS | 不适用 | — | `C7_MediaCacheHitsSecondPlay`：Android 3.9 已写<br>`C7_BubblesFromContentType`：Android 4.3 已写、Web 5.9 已写、iOS 6.7 |
| [C8](#c8) | 按住说话单手势；弹层语言跟随 | Android | 不适用 | — | `C8_HoldToTalkSingleGesture`：Android 4.1 已写<br>`C8_SheetsFollowAppLocale`：Android 4.2 已写<br>`C8_DeviceHoldToTalk`：Android 4.5 |
| [C9](#c9) | 本人群角色从名册取、群同步推送触发刷新、失败回落普通成员 | Android、Web、iOS | 不适用 | — | `C9_RoleFromRoster`：Android 3.10 已写、Web 5.8 已写、iOS 6.6<br>`C9_UnknownRosterMeansMember`：Android 3.10 已写、Web 5.8 已写 |
| [C10](#c10) | E2EE 失败原因分类 | Android、iOS | 不适用 | — | `C10_E2EEFailureReasons`：Android 7.1 已写、iOS 7.1 |

## 逐条明细

<a id="r1"></a>

### R1 clientMsgId 幂等，重复提交返回原 seq、不再广播

- 平台：服务端
- 参照服务：符合
- 已录用例：
  - `core/group-messaging` 第 8、9 步（pub、pub）：同一 clientMsgId 第二次提交返回原 seq 与时间戳，B 只收到一条 data
- 计划用例：
  - `TestR1_ConcurrentDuplicatePublish`（服务端，1.11 并发压力用例，并发压力，已写）：同一 clientMsgId 并发提交，只落一条消息、只广播一次

<a id="r2"></a>

### R2 应答回填 id（含新增的登录与在线快照）

- 平台：服务端
- 参照服务：部分符合
- 说明：普通应答（含错误应答、未知 action）都带回 id；登录与在线快照的成功应答不带，见 R19。无法解析的帧没有可回填的 id。
- 已录用例：
  - `core/errors` 第 2、3、5、6、7、9 步（sub、get_groups、no_such_action、sub、create_grp、get_groups）：未登录、未知 action、非成员、参数错误的错误应答和成功应答都带回请求 id
  - `core/group-messaging` 第 6、8 步（sub、pub）：订阅与发布的成功应答带回请求 id
  - `core/login` 第 3 步（login）：登录失败的应答带回 id（成功应答不带，见 R19）

<a id="r3"></a>

### R3 被顶替的连接不清新连接的订阅、不广播下线；不在持锁时断开连接

- 平台：服务端
- 参照服务：符合
- 说明：被顶替时旁观者会再收到一次上线推送（core/supersede 第 9 步），不是下线。
- 已录用例：
  - `core/supersede` 第 9、10、11、12、13 步（login、sub、pub、pub、pres）：顶替时旁观的好友没收到下线；新连接的群订阅在旧连接清理后仍在，群消息和单聊消息都送到新连接；在线查询仍为在线
  - `core/login` 第 5 步（login）：同一用户从新连接登录，旧连接被关闭
- 计划用例：
  - `TestR3_SupersedeStorm`（服务端，1.11 并发压力用例，并发压力，已写）：同一用户反复建连互相顶替：新连接订阅不丢、旁观者不收到下线、不出现持锁断开导致的死锁
  - `TestR3_LateFrameFromReplacedConnection`（服务端，1.11 并发压力用例，并发压力，已写）：被顶替连接已读入的 sub、leave、pub 在顶替之后才处理，既不抢走、也不删掉新连接在话题索引里的位置（F60）
  - `TestR3_TopicIndexRules`（服务端，1.11 并发压力用例，单测，已写）：话题索引只许登录代号更新的连接覆盖，旧代号既不覆盖也不删除新连接的位置，已关闭的连接不入索引（F60）

<a id="r4"></a>

### R4 在线状态串行化、版本号单调、批量查询

- 平台：服务端
- 参照服务：符合
- 说明：黄金对话把 presRev 归一化成 <rev>，看不出是否单调；单调性只能靠并发压力用例。
- 已录用例：
  - `core/presence` 第 8、9、10、12、13、14、16 步（pres、pres、pres、close、close、pres、login）：按单个 uid、按列表、按 * 批量查询；上下线只推给共享会话的人
  - `core/p2p` 第 7、14、15 步（sub、close、pres）：订阅单聊时补发对端在线状态；对端下线后推送并可查询
- 计划用例：
  - `TestR4_PresenceRevMonotonic`（服务端，1.11 并发压力用例，并发压力，已写）：多连接并发上下线，同一用户的 presRev 严格递增，最终状态与真实连接一致

<a id="r5"></a>

### R5 群成员写入不降级、不能改群主角色、踢人 4 道守卫

- 平台：服务端
- 参照服务：符合
- 已录用例：
  - `core/group-guards` 第 12、13、14、15、16、17、18、19 步（kick、kick、kick、kick、kick、invite、get_members、kick）：不能踢自己、普通成员不能踢、谁都不能踢群主、只有群主能踢管理员、踢非成员被拒；重复邀请管理员后角色不变
  - `core/group-admin` 第 9、10、11 步（set_member_role、kick、kick）：不能把群主角色指派给别人；管理员不能踢群主；普通成员不能踢人
- 计划用例：
  - `TestR5_MemberWritesNeverDowngrade`（服务端，1.10 工程化，单测，已写）：存储层写成员行遇到已有行不改角色；改角色的语句碰不到群主行

<a id="r6"></a>

### R6 撤回是软删，所有读消息的查询都过滤；历史条数只在一处归一化

- 平台：服务端
- 参照服务：符合
- 说明：参考服务撤回视频时只回收主文件、不回收缩略图，由 1.9 修复。
- 已录用例：
  - `core/recall` 第 9、10、11、12 步（del、del、del、get）：只有发送者能撤回、不能重复撤回；撤回后历史里没有这条
  - `core/recall-p2p` 第 7、8、9、10、11 步（del、del、get、pub、del）：单聊撤回；被撤回的 seq 仍占用，下一条不复用
  - `core/history-paging` 第 9、10、11、12 步（get、get、get、get）：向前、向后分页；超范围的 limit 回落到配置的页大小
  - `core/clear-media` 第 11、12 步（del、del）：撤回后文件不再被引用才回收，仍被其他消息引用则不回收
- 计划用例：
  - `TestR6_MessageReadsFilterRecalled`（服务端，1.10 工程化，SQL 扫描，已写）：扫描存储层所有读 im_messages 的查询，必须过滤已撤回的行，例外要写明原因
  - `TestR6_HistoryLimitNormalizedOnce`（服务端，1.10 工程化，单测，已写）：历史条数只在一个函数里归一化，存储层不再另行截断
  - `TestR6_RecallReclaimsVideoThumbnail`（服务端，1.9 已知缺口修复，单测，已写）：撤回视频时主文件和缩略图都回收

<a id="r7"></a>

### R7 清空：水位 + 离线补投 + ack + 30 天清理，对离线成员生效

- 平台：服务端、Android、Web、iOS
- 参照服务：符合
- 说明：30 天清理是计时行为，黄金对话看不到。
- 已录用例：
  - `core/clear-offline` 第 8、9、11、12、15 步（del_topic、get、login、ack_topic_cleared、login）：清空返回新水位；离线成员下次登录收到 topic_cleared，ack 后再登录不再收到
- 计划用例：
  - `TestR7_PendingClearsExpireAfter30Days`（服务端，1.10 工程化，单测，已写）：注入时钟，未 ack 的清空墓碑 30 天后清理
  - `R7_ClearOfflineReplay`（Android，3.10 Android 回放用例，回放，用 `core/clear-offline`，已写）：离线一方上线收到 topic_cleared 后清本地缓存并回 ack，再次登录不重复处理
  - `R7_ClearOfflineReplay`（Web，5.8 Web 回放用例，回放，用 `core/clear-offline`，已写）：同上
  - `R7_ClearOfflineReplay`（iOS，6.6 iOS 回放用例，回放，用 `core/clear-offline`）：同上

<a id="r8"></a>

### R8 群事件离线队列同理

- 平台：服务端
- 参照服务：符合
- 已录用例：
  - `core/group-offline-events` 第 6、7、8、9、10、12、13、16 步（create_grp、create_grp、set_member_role、update_grp、disband_grp、login、ack_group_event、login）：离线期间的建群、解散入队，改角色和改名不入队；已失效的事件丢弃；ack 过的不再补投
  - `core/invite` 第 19、24 步（invite、login）：邀请离线成员时入队，下次登录收到带 eventId 的 grp_sync
- 计划用例：
  - `TestR8_GroupEventsExpireAfter30Days`（服务端，1.10 工程化，单测，已写）：注入时钟，未 ack 的群事件 30 天后清理

<a id="r9"></a>

### R9 可配项默认值等于原写死值（如消息上限 20000）

- 平台：服务端
- 参照服务：符合
- 已录用例：
  - `core/limits` 第 4、5、6 步（pub、pub、create_grp）：消息 20000 字通过、20001 字拒绝；群名超过 30 字拒绝
  - `core/rate-limit` 第 23、24 步（pub、pub）：10 秒内第 21 条发布返回 429
  - `core/history-paging` 第 12 步（get）：超范围的 limit 回落到默认页大小
- 计划用例：
  - `TestR9_DefaultsEqualOriginalValues`（服务端，1.10 工程化，单测，已写）：每个可配项的默认值逐项等于源基线的写死值

<a id="r10"></a>

### R10 限流、连接上限、帧上限、队列满即断开（E2EE 走降级）、重复登录拒绝

- 平台：服务端
- 参照服务：符合
- 说明：连接上限与发送队列满两项只在压力下出现，黄金对话里没有。
- 已录用例：
  - `core/rate-limit` 第 24 步（pub）：超过发送限流返回 429
  - `core/limits` 第 7 步（raw）：超过 256 KB 的帧，服务端关闭连接
  - `core/login` 第 3 步（login）：同一连接第二次登录被拒绝
- 计划用例：
  - `TestR10_ConnectionCap`（服务端，1.11 并发压力用例，并发压力，已写）：连接数达到上限后新连接被拒绝，已有连接不受影响
  - `TestR10_SendQueueFullDisconnects`（服务端，1.11 并发压力用例，并发压力，已写）：慢消费者的发送队列满时断开该连接，不阻塞其他人
  - `TestR10_E2EEQueueFullFallsBackToOffline`（服务端，7.1 E2EE 模块，单测，已写）：E2EE 信封遇到发送队列满时转入离线队列，不断开连接

<a id="r11"></a>

### R11 心跳间隔 0.45 × 超时

- 平台：服务端
- 参照服务：未观测
- 没有黄金用例：心跳是计时行为，黄金对话按同步点比对帧集合，看不到间隔。
- 计划用例：
  - `TestR11_PingIntervalIs045OfTimeout`（服务端，1.10 工程化，单测，已写）：ping 间隔等于 0.45 × 读超时，配置改动时同步变化

<a id="r12"></a>

### R12 推送只发数据载荷；加密消息不带预览；备注按方向

- 平台：服务端、宿主
- 参照服务：未观测
- 没有黄金用例：参考服务没有推送凭据，录不到推送载荷；core/push-token 只录了推送令牌的登记与注销。
- 计划用例：
  - `TestR12_PushPayloadIsDataOnly`（服务端，1.4 推送接口，单测，已写）：假推送通道收到的 FCM、APNs、Web Push 载荷都只有数据字段
  - `TestR12_EncryptedMessageHasNoPreview`（服务端，1.4 推送接口，单测，已写）：加密消息的推送不带正文预览和发信人
  - `R12_ProfileAliasByDirection`（宿主，2.2 Node 插件，契约测试，已写）：profile 端点按 viewer→sender 方向取备注，双账号互设备注互不影响

<a id="r13"></a>

### R13 内部接口鉴权；后台清空二次确认加审计

- 平台：服务端、宿主
- 参照服务：未观测
- 没有黄金用例：测试台只会带合法的内部令牌调用，拒绝路径要靠契约测试。
- 计划用例：
  - `TestR13_InternalEndpointsRejectBadTokens`（服务端，2.1 宿主契约与契约测试，契约测试，已写）：IM 的内部接口拒绝缺失、过期、角色不对的内部令牌
  - `R13_HostEndpointsRejectBadTokens`（宿主，2.1 宿主契约与契约测试，契约测试，已写）：宿主的内部接口同样拒绝不合法的内部令牌
  - `R13_AdminClearNeedsConfirmAndAudit`（宿主，2.5 后台，契约测试，已写）：后台清空需超级管理员二次确认；审计只记元数据，不含正文与媒体地址

<a id="r14"></a>

### R14 群成员不能互看资料；加好友需一次性令牌

- 平台：宿主
- 参照服务：未观测
- 说明：IM 侧的 get_members 本来就不下发昵称（core/group-guards 第 18 步），资料只能经宿主取。
- 计划用例：
  - `R14_GroupMembersCannotViewProfiles`（宿主，2.3 App 接口，契约测试，已写）：只知道群里看到的 uid 时，只能查到名字和头像（显示聊天要用），查不到宿主 id、handle、加好友令牌；裸 uid 不能当二维码解析，要本人出示的签名二维码
  - `R14_AddContactRequiresOneTimeToken`（宿主，2.3 App 接口，契约测试，已写）：加好友必须带搜索或扫码得到的令牌，绑定双方、两分钟内有效；签给别人或过期的令牌无效。令牌不记状态，两分钟内可对同一对用户再用（第二次请求遇到已有的请求，409）

<a id="r15"></a>

### R15 媒体带 Content-Length；删前查引用；私密媒体不进 CDN

- 平台：宿主
- 参照服务：未观测
- 说明：IM 侧在请求宿主删除前先数引用，录在 core/clear-media 第 11、12 步（归 R6、R21）。
- 计划用例：
  - `R15_MediaResponsesCarryContentLength`（宿主，2.4 媒体，契约测试，已写）：媒体下载响应都带 Content-Length
  - `R15_DeleteChecksReferences`（宿主，2.4 媒体，契约测试，已写）：删除媒体前查引用计数，仍被引用则不删
  - `R15_PrivateMediaBypassesCDN`（宿主，2.4 媒体，契约测试，已写）：私密媒体的地址不指向 CDN

<a id="r16"></a>

### R16 通话状态用比较后替换；断线宽限；计时器显式停；用宿主用户 id

- 平台：服务端
- 参照服务：符合
- 说明：断线宽限期结束通话、宿主主动结束通话两条路径参考服务还没录。
- 已录用例：
  - `module/call` 第 11、13、14、19、21、25、27 步（call_invite、call_accept、call_accept、call_upgrade、call_hangup、call_reject、call_accept）：已接听再接 409、已是视频再升级 409、已结束再挂 404、局外人拒接 404、不存在的会话 404；calls/create 回调里是宿主用户 id
  - `module/call-timeout` 第 6、7 步（wait、call_accept）：振铃计时器到点结束通话并通知双方，超时后接听返回 404
- 计划用例：
  - `TestR16_CallStateCASUnderRace`（服务端，1.11 并发压力用例，并发压力，已写）：接听、挂断、断线同时发生，状态只推进一次、宿主只结算一次
  - `TestR16_DisconnectGraceEndsCall`（服务端，7.2 通话服务端，单测，已写）：注入时钟，断线超过 10 秒宽限期结束通话，宽限内重连则保留
  - `TestR16_TimersStoppedOnEveryPath`（服务端，7.2 通话服务端，单测，已写）：每条非超时的结束路径都停掉振铃与宽限计时器

<a id="r17"></a>

### R17 E2EE 只存转不透明信封；补投；本机身份初始化互斥

- 平台：服务端、Android、iOS
- 参照服务：符合
- 说明：v1 的 Web 不做 E2EE。
- 已录用例：
  - `module/e2ee-p2p` 第 16、20、22、25 步（e2e_send、e2e_send、login、login）：密文原样转发；对方离线时入队、上线补投，投递后删除不再重发
  - `module/e2ee-group` 第 17、18 步（e2e_send、e2e_send）：群内 sender key 与消息信封原样扇出给其他成员
- 计划用例：
  - `R17_IdentityInitIsMutuallyExclusive`（Android，7.1 E2EE 模块，单测，已写）：并发初始化本机身份只生成一套密钥
  - `R17_IdentityInitIsMutuallyExclusive`（iOS，7.1 E2EE 模块，单测）：同上

<a id="r18"></a>

### R18 用户令牌只接受 type=access，刷新、代理、管理员令牌一律拒绝

- 平台：服务端
- 参照服务：违反
- 说明：参考服务接受刷新、设备代理、超级管理员令牌，也接受不带 type 的令牌（core/login-tokens 第 2、4、6、14 步）。
- 已录用例：
  - `core/login-tokens` 第 2、4、6、8、10、12、14 步（login、login、login、login、login、login、login）：记录参考服务的现状：哪些令牌能登录，过期、非 JWT、缺 sub 的被拒
- 计划用例：
  - `TestR18_OnlyAccessTokensLogIn`（服务端，1.8 安全修复，strict 档一致性，已写）：strict 档下除 type=access 外的令牌一律 401；迁移期兼容开关默认关闭

<a id="r19"></a>

### R19 登录与在线快照应答带 id；客户端只对旧服务端按 action 关联这两种

- 平台：服务端、Android、Web、iOS
- 参照服务：违反
- 已录用例：
  - `core/login` 第 2、6 步（login、pres）：参考服务的登录成功应答与在线快照应答不带 id
  - `core/p2p` 第 11 步（pres）：按 * 查询的快照应答不带 id
  - `core/presence` 第 10 步（pres）：按 * 查询的快照应答不带 id
- 计划用例：
  - `TestR19_LoginAndSnapshotRepliesCarryId`（服务端，1.7 只加不改的协议改进，strict 档一致性，已写）：登录与在线快照应答带回请求 id（只加不改）
  - `R19_CorrelateLegacyRepliesByAction`（Android，3.4 Android 请求关联，回放，用 `core/login`，已写）：对旧服务端只对登录与在线快照按 action 关联，其余严格按 id
  - `R19_CorrelateLegacyRepliesByAction`（Web，5.2 Web 客户端，回放，用 `core/login`，已写）：同上
  - `R19_CorrelateLegacyRepliesByAction`（iOS，6.1 iOS 核心，回放，用 `core/login`）：同上

<a id="r20"></a>

### R20 好友关系在单聊首次建立时校验；解除好友经 contact_sync 收回

- 平台：服务端
- 参照服务：符合
- 已录用例：
  - `core/host-contact-sync` 第 5、7、8、11、12、13 步（sub、host、sub、pub、host、pub）：宿主推 contact_sync 后单聊可用；宿主单方面解除好友不影响已建立的单聊，推 removed 后才收回
  - `core/p2p` 第 12、13 步（sub、pub）：非好友订阅、发布单聊被拒

<a id="r21"></a>

### R21 清空是全员物理删除并回收媒体

- 平台：服务端
- 参照服务：符合
- 说明：清空返回的 deletedMessages 把已撤回的墓碑行也算在内（core/clear-media 第 14 步为 3）。
- 已录用例：
  - `core/clear-offline` 第 8、9 步（del_topic、get）：单聊清空后拉历史为空并带水位
  - `core/clear-media` 第 13、14、15 步（del_topic、del_topic、get）：群的清空只限群主或管理员；清空回收剩下的文件；另一成员的历史也为空

<a id="r22"></a>

### R22 在线状态查询只返回与查询者共享会话的用户（参考服务可查任意用户）

- 平台：服务端
- 参照服务：违反
- 说明：参考服务按 uid 查询时会返回陌生人的在线状态和最后在线时间（core/presence 第 9、14 步）。
- 已录用例：
  - `core/presence` 第 9、14 步（pres、pres）：记录现状：陌生人 C 的在线状态与 lastSeenAt 也被返回
- 计划用例：
  - `TestR22_PresenceOnlyForSharedTopics`（服务端，1.8 安全修复，strict 档一致性，已写）：strict 档下不共享会话的 uid 从结果里去掉

<a id="c1"></a>

### C1 重连抖动、建连前清身份、连接串行化、登录确认前不算已连接、世代号

- 平台：Android、Web、iOS
- 参照服务：不适用
- 计划用例：
  - `C1_ReconnectAfterInjectedDisconnect`（Android，3.10 Android 回放用例，回放 + 故障注入，用 `core/p2p`，已写）：回放在第 9 步注入断线：重连带抖动、建连前清空身份、先登录再发请求、旧连接的迟到应答被世代号丢弃
  - `C1_ReconnectAfterInjectedDisconnect`（Web，5.8 Web 回放用例，回放 + 故障注入，用 `core/p2p`，已写）：同上
  - `C1_ReconnectAfterInjectedDisconnect`（iOS，6.6 iOS 回放用例，回放 + 故障注入，用 `core/p2p`）：同上
  - `C1_IgnoresDuplicatedFrames`（Android，3.10 Android 回放用例，回放 + 故障注入，用 `core/p2p`，已写）：每一步的帧都发两遍、应答排在最后：重复的应答、登录应答与推送都被忽略，连接不中断，请求不重发
  - `C1_IgnoresDuplicatedFrames`（Web，5.8 Web 回放用例，回放 + 故障注入，用 `core/p2p`，已写）：同上

<a id="c2"></a>

### C2 回执超时但连接仍在时保持“发送中”、不自动重发；媒体按 URL 对账

- 平台：Android、Web、iOS
- 参照服务：不适用
- 说明：core/clear-media 里发送方收不到自己消息的回显，媒体按 URL 对账在回放里验证不了：Android 改为单测（进程内服务端丢掉回执后从历史拉回），Web、iOS 到对应阶段同样处理。
- 计划用例：
  - `C2_LostPublishReplyStaysSending`（Android，3.10 Android 回放用例，回放 + 故障注入，用 `core/group-messaging`，已写）：回放丢弃第 8 步的帧：消息停在发送中，不自动重发（重发会被回放判为脚本外请求）
  - `C2_MediaReconciledByUrl`（Android，3.7 Android 拆仓储，单测，已写）：媒体消息的回执丢失后，从历史拉回的同一条按 fileUrl 与“发送中”的那条对账：不重发、不重复
  - `C2_LostPublishReplyStaysSending`（Web，5.8 Web 回放用例，回放 + 故障注入，用 `core/group-messaging`，已写）：同上
  - `C2_MediaReconciledByUrl`（Web，5.8 Web 回放用例，回放，用 `core/clear-media`，已写）：同上
  - `C2_LostPublishReplyStaysSending`（iOS，6.6 iOS 回放用例，回放 + 故障注入，用 `core/group-messaging`）：同上
  - `C2_MediaReconciledByUrl`（iOS，6.6 iOS 回放用例，回放，用 `core/clear-media`）：同上

<a id="c3"></a>

### C3 离线补拉取本地连续段末尾并检测空洞

- 平台：Android、Web、iOS
- 参照服务：不适用
- 计划用例：
  - `C3_ResumeFromContiguousEnd`（Android，3.10 Android 回放用例，回放，用 `core/resubscribe`，已写）：重连后从本地连续段末尾按 afterSeq 补拉；被撤回的 seq 不算空洞（参见 core/recall-p2p）
  - `C3_ResumeFromContiguousEnd`（Web，5.8 Web 回放用例，回放，用 `core/resubscribe`，已写）：同上
  - `C3_ResumeFromContiguousEnd`（iOS，6.6 iOS 回放用例，回放，用 `core/resubscribe`）：同上

<a id="c4"></a>

### C4 缓存归属：换账号清空全部缓存表

- 平台：Android、Web、iOS
- 参照服务：不适用
- 计划用例：
  - `C4_SwitchingAccountClearsAllCaches`（Android，3.5 Android 存储，单测，已写）：换账号时清空全部缓存表，同账号重登保留
  - `C4_SwitchingAccountClearsAllCaches`（Web，5.4 Web 存储，单测，已写）：同上
  - `C4_SwitchingAccountClearsAllCaches`（iOS，6.3 iOS 存储，单测）：同上

<a id="c5"></a>

### C5 在线三态加过期时间，只有一个真相源

- 平台：Android、Web、iOS
- 参照服务：不适用
- 计划用例：
  - `C5_PresenceTriStateWithExpiry`（Android，3.7 Android 拆仓储，单测，已写）：在线、离线、未知三态，过期后回到未知；会话列表与聊天页读同一个来源
  - `C5_PresenceTriStateWithExpiry`（Web，5.2 Web 客户端，单测，已写）：同上
  - `C5_PresenceTriStateWithExpiry`（iOS，6.1 iOS 核心，单测）：同上

<a id="c6"></a>

### C6 通知抑制条件、渠道静音由 App 播声、来电通知时序与取消

- 平台：Android
- 参照服务：不适用
- 说明：iOS、Web 的通知规则另列。
- 计划用例：
  - `C6_NotificationDecision`（Android，3.8 Android 通知，单测，已写）：正在看该会话且 App 在前台时不发通知；渠道静音、由 App 播放提示音；来电通知在所有结束路径上取消
  - `C6_DeviceSoundAndCallTiming`（Android，4.5 Android sample-app，真机）：真机上提示音实际响起；来电先发全屏通知再起麦克风前台服务

<a id="c7"></a>

### C7 媒体缓存；语音气泡随时长变宽；占位符用类型字段

- 平台：Android、Web、iOS
- 参照服务：不适用
- 计划用例：
  - `C7_MediaCacheHitsSecondPlay`（Android，3.9 Android 媒体，单测，已写）：语音、视频第二次播放走本地缓存
  - `C7_BubblesFromContentType`（Android，4.3 Android 消息类型注册表，UI 测试，已写）：气泡按内容类型渲染，不靠占位文字；语音气泡宽度随时长变化
  - `C7_BubblesFromContentType`（Web，5.9 Web React 组件，UI 测试，已写）：同上
  - `C7_BubblesFromContentType`（iOS，6.7 iOS UI 组件，UI 测试）：同上

<a id="c8"></a>

### C8 按住说话单手势；弹层语言跟随

- 平台：Android
- 参照服务：不适用
- 计划用例：
  - `C8_HoldToTalkSingleGesture`（Android，4.1 Android 组件，UI 测试，已写）：按下、移动、抬起是一个手势，手指移动不会提前发送
  - `C8_SheetsFollowAppLocale`（Android，4.2 Android 主题与文案，UI 测试，已写）：底部弹层的文案跟随 App 内选择的语言
  - `C8_DeviceHoldToTalk`（Android，4.5 Android sample-app，真机）：真机上按住说话、上滑取消

<a id="c9"></a>

### C9 本人群角色从名册取、群同步推送触发刷新、失败回落普通成员

- 平台：Android、Web、iOS
- 参照服务：不适用
- 计划用例：
  - `C9_RoleFromRoster`（Android，3.10 Android 回放用例，回放，用 `core/group-guards`，已写）：本人角色取自 get_members 名册；grp_sync 触发名册刷新
  - `C9_RoleFromRoster`（Web，5.8 Web 回放用例，回放，用 `core/group-guards`，已写）：本人角色取自 get_members 名册；grp_sync 触发名册刷新；名册拉取失败按普通成员处理
  - `C9_RoleFromRoster`（iOS，6.6 iOS 回放用例，回放，用 `core/group-guards`）：本人角色取自 get_members 名册；grp_sync 触发名册刷新；名册拉取失败按普通成员处理
  - `C9_UnknownRosterMeansMember`（Android，3.10 Android 回放用例，回放 + 故障注入，用 `core/group-guards`，已写）：回放丢弃第 18 步的名册应答：本人角色按普通成员处理，下一次名册拉取成功后恢复
  - `C9_UnknownRosterMeansMember`（Web，5.8 Web 回放用例，回放 + 故障注入，用 `core/group-guards`，已写）：同上

<a id="c10"></a>

### C10 E2EE 失败原因分类

- 平台：Android、iOS
- 参照服务：不适用
- 计划用例：
  - `C10_E2EEFailureReasons`（Android，7.1 E2EE 模块，单测，已写）：失败原因按类区分（如对方未发布密钥），界面不显示笼统的稍后重试
  - `C10_E2EEFailureReasons`（iOS，7.1 E2EE 模块，单测）：同上

