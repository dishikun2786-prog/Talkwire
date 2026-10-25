# 从小桃子迁移到 ImKit

小桃子现网的即时聊天（Go 的 `server/im-go`、Node 后端里的聊天接口、Android 的 `chat/` 与聊天界面、后台的聊天与通话记录页）可以一块一块换成 ImKit，不需要停服搬数据。本文按类别列出原项目的每一块对应 ImKit 的什么，以及建议的顺序。

> 本表来自两边代码的对照，尚未在现网演练。每一步都先在生产库的副本上走一遍（见文末“每一步怎么验证”）。原项目的文件路径以 `D:\Program Files\roubao` 为准。

## 建议顺序

1. **换服务端**：用 ImKit 服务端替换 `im-go`，设 `IMKIT_PROTOCOL_PROFILE=legacy`，其余环境变量照旧，指向同一个库。legacy 档对外的帧与原版逐帧一致，现网的 APK 不用升级。
2. **换宿主接口**：Node 后端里 IM 调用的内部接口、App 用的聊天接口、媒体接口，逐个换成 `@imkit/host-node`。数据先留在原表里，在原表上实现 ImKit 的存储接口（见“宿主数据”）。
3. **换后台页面**：后台的聊天记录、通话记录页换成 `@imkit/admin`。
4. **换客户端**：新版 APK 用 `imkit-core`、`imkit-storage-room`、`imkit-android`、`imkit-ui`；业务卡片（红包、转账、商城、掼蛋邀请）注册成自定义消息类型。
5. **切 strict 档**：旧 APK 淘汰后把 `IMKIT_PROTOCOL_PROFILE` 改成 `strict`（默认值），得到协议缺陷的修正与新增字段。

## 服务端

| 小桃子 | ImKit | 说明 |
| --- | --- | --- |
| `server/im-go/`（`hub.go`、`store.go`、`protocol.go` 等） | `server/` | legacy 档与原版逐帧一致：33 个黄金对话全过 |
| `im_*` 表 | 同一批表 | 启动时基线迁移只补缺的部分，不重建。新增表 `imkit_schema_migrations`、`im_devices`、`im_pending_acks`；新增列 `im_fcm_tokens.provider`、`im_fcm_tokens.device_id`、`im_e2e_identities.owner_device` |
| 环境变量 `DB_*`、`JWT_SECRET`、`IM_PORT`、`MAIN_API_BASE_URL`、`IM_*` | 同名 | 默认值与原版写死的值相同（`TestR9_DefaultsEqualOriginalValues`） |
| `FCM_SERVICE_ACCOUNT_KEY_PATH`、`FCM_SERVICE_ACCOUNT_PATH` | 两个都读 | |
| `IM_FCM_HTTP_TIMEOUT_SEC` | `IMKIT_PUSH_HTTP_TIMEOUT_SEC` | 旧名也读 |
| 群 AI 问答（写死在服务端里） | `groupai` 模块 + 宿主的 `GroupAiHandler` | 助手名字与兜底文案进运行配置 `groupAi`，默认值与原版逐字相同 |
| 通话信令 | `call` 模块 | |
| 端到端加密的密钥与密文存转 | `e2ee` 模块 | |
| 扫码入群 | `groupjoin` 模块 | |
| `red-packet-broadcast` | 保留 | 新代码用通用的 `custom-broadcast` |

服务端还修了原版的几处并发缺陷（F58、F60、F61、F63），见 `server/README.md` 的“与原版的差异”。

## 宿主：IM 调用的内部接口

`server/src/routes/internal/` 下的处理器换成 `imkitHostPlugin` 的选项；路径（`/api/v1/internal/im/*`）与契约不变。

| 小桃子 | ImKit | 说明 |
| --- | --- | --- |
| `internal/imFriendship.ts` | `friendship`（`ContactStore`） | |
| `internal/imProfile.ts` | `profile`（`ProfileStore`，带按方向的备注） | |
| `internal/imRuntimeConfig.ts` | `runtime-config`（`ConfigSource.runtimeConfig`） | |
| `internal/e2eeConfig.ts` | `e2ee-config`（`ConfigSource.e2eeConfig`） | |
| `internal/imDeleteMedia.ts` | `delete-media`（`mediaDeleter` + `BlobStore`） | |
| `internal/imCalls.ts`，以及 `services/imCallBilling.ts`、`services/imCallArtc.ts` | `CallHandlers` + `recordingCalls` | 计费与 ARTC 凭证留在宿主：`answer` 里签频道令牌，`settle` 里结算 |
| `internal/imGroupQa.ts` | `GroupAiHandler` | 知识库、计费留在宿主 |
| `services/imAdminClient.ts`（调 IM 的内部接口） | `ImClient` | `contactSync`、`customBroadcast`、`callBroadcast`、`mediaRefCount`、`topicMembersResolve`、后台四个 |

## 宿主：App 用的接口

| 小桃子 | ImKit | 说明 |
| --- | --- | --- |
| `routes/chatBridge.ts`（我的资料、批量查资料、搜索、联系人、备注、加好友令牌） | `imkitAppRoutes` | 路径前缀相同（`/api/v1/chat`）。字段以 `imkitAppRoutes` 为准，旧 APK 用到的字段逐个对照后再切 |
| `routes/chatUpload.ts` | `imkitMediaRoutes` | |
| `routes/images.ts` | 只换聊天媒体的部分 | 朋友圈等非聊天图片也走这个路由，留在宿主 |

## 宿主数据

两种做法：在原表上实现 ImKit 的存储接口（不搬数据，建议先这样），或者把数据搬进 `prisma/imkit.prisma` 的 `imkit_*` 表、用 `@imkit/host-node/prisma`。

| 小桃子的表 | ImKit 接口 | 字段对应 |
| --- | --- | --- |
| `user_profiles` | `ProfileStore` | userId←`app_user_id`，handle←`chat_id`，handleKey←`chat_id_lower`，handleChangedAt←`chat_id_changed_at`，displayName←`display_name`（可空，空时给空串），avatarUrl←`avatar_url`。ImKit 要按 uid 反查资料，原表没有 uid 列：加一列，用 `imUid(app_user_id)` 回填（算法与原版相同，由 `contract/host-api/uid-vectors.json` 钉住）。`tinode_user`、`cover_url`、`cover_focal_y` 不归 IM |
| `contacts` | `ContactStore` | 都是每对好友一行。requesterId←`user_id`，recipientId←`contact_id`，requesterAlias←`alias`（`user_id` 给 `contact_id` 起的），recipientAlias←`contact_alias`（反方向）。状态值逐个核对；ImKit 用一对用户的唯一约束保证每对只有一行（Prisma 版是 `pair_key`），原表要补一个等价的唯一索引，`withPair` 用事务级 advisory lock |
| `admin_im_audit_logs` | `AuditStore` | adminId←`admin_user_id`，targetUserId←`target_app_user_id`，其余同名；`action` 在 ImKit 里是 `im.clear_topic` |
| `im_call_sessions` | `CallRecordStore` | callerUserId←`caller_id`，calleeUserId←`callee_id`，callType←`type`（原表只存最终类型，requestedType 也取它），answeredAt←`started_at`，endedAt、endReason、durationSec 同名。原表的 `missed`、`rejected` 等状态都是 ImKit 的 `ended` 加对应的结束原因。`credits_charged` 是宿主业务，后台要看就在宿主自己的页面里补 |

## Android

原项目 `app/src/main/java/com/xiaotaozi/autopilot/` 下的聊天代码与非聊天代码的耦合点，逐行列在 `docs/inventory/android-coupling.md`。

| 小桃子 | ImKit | 说明 |
| --- | --- | --- |
| `chat/ImWebSocketClient.kt` | `imkit-core` 的 `ImClient` | |
| `chat/ChatRepository.kt`、`chat/ConversationLoader.kt` | `imkit-core` 的 `ChatEngine` | |
| `chat/ChatDatabase.kt`（Room） | `imkit-storage-room` | 换账号清库（C4）、离线补拉游标（C3）的规则相同 |
| `chat/ChatNotifier.kt`、`chat/ChatSettings.kt` | `imkit-android` 的 `ImNotifier`、`NotificationController` | 渠道静音、提示音由 App 播（C6） |
| `ImWebSocketClient.kt` 里的 FCM 上报 | `imkit-push-fcm` | |
| `ui/ChatSubApp.kt`、`ui/screens/messages/`、`ui/screens/contacts/`、`ui/components/chat/` | `imkit-ui`（`ImChatApp`） | 文案自带三语，不再依赖 `i18n.AppStrings`；运行配置由服务端下发，不再依赖 `data.ImConfig` |
| `chat/RedPacket*`、`chat/Transfer*`、`chat/MallCardModels.kt`、`chat/GuandanInviteModels.kt` 与对应卡片 | 宿主自定义消息类型（`MessageType`） | 例子：`examples/red-packet/android` |
| `chat/call/` | 待做（7.3） | |
| `chat/e2e/` | 待做（7.1，AGPLv3 模块） | |
| `ui/screens/messages/GroupQrCodeScreen.kt`、`GroupJoin*Screen.kt` | 待做（7.6） | Web 已有 `@imkit/group-join` |

## 后台

| 小桃子 | ImKit | 说明 |
| --- | --- | --- |
| `admin-ui/src/pages/ImChatRecordsPage.tsx` + `routes/admin/imRecords.ts` | `@imkit/admin` 的 `ChatRecords` + `imkitAdminRoutes` | 清空仍是超级管理员、写理由、带幂等键、审计只记元数据；确认方式改为逐字输入 `CLEAR <会话>` |
| `admin-ui/src/pages/ImCallRecordsPage.tsx`、`ImCallRecordDetailPage.tsx` + `routes/admin/imCalls.ts` | `CallRecords`、`CallDetail` + `imkitAdminRoutes` 的 `calls` | 原页面的“消耗积分”一列不带（见上文 `im_call_sessions`） |

## 留在宿主的业务

| 小桃子 | 怎么接 |
| --- | --- |
| 红包 `routes/imRedPackets.ts`、转账 `routes/imTransfers.ts`、扫码付款 | 卡片是自定义消息类型；状态变化（被领完、过期）用 `ImClient.customBroadcast` 推给会话 |
| 群 AI 的计费与知识库 | `GroupAiHandler` |
| 通话计费、ARTC 凭证、余额不足时结束通话 | `CallHandlers`；提前结束用 `ImClient.callBroadcast` 推 `call_ended` |

## 协议与切换

| 事项 | 做法 |
| --- | --- |
| 旧 APK | legacy 档，逐帧一致；接受原版接受的全部令牌 |
| 新客户端 | 可以在 legacy 档上先上线；全部换完后切 strict |
| 令牌 | strict 档只接受 `type=access`、`role` 在 `IMKIT_TOKEN_USER_ROLES` 里的令牌（R18）。切 strict 时还有旧客户端用别的令牌，临时设 `IMKIT_ACCEPT_LEGACY_TOKENS=1` |
| 多端在线 | 新客户端登录带 `device`；旧 APK 不带，照旧同一账号互相顶替 |
| 协议缺陷 | strict 档修正 D1–D5、D7–D9（协议规范第 18 节），旧 APK 依赖的行为在 legacy 档保持原样 |

## 每一步怎么验证

- **换服务端之前**：在生产库的副本上起 ImKit 服务端，`tools/conformance` 的 `schema` 比较表结构，`check` 跑黄金对话（legacy 档）。
- **换宿主接口**：`host-sdk-node/conformance/host.ts` 让测试台用 SDK 扮演宿主跑全部黄金对话；在原表上自己实现的存储接口，套用 `host-sdk-node/test/stores.suite.ts` 那套用例。
- **换客户端**：Android 的 `SampleStackTest` 与真机检查单（`android/DEVICE_CHECKS.md`）。
- **切 strict 之前**：确认后台统计里已没有旧版本 APK 在线，再在副本上用 strict 档跑一遍。
