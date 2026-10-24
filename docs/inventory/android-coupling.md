# Android 聊天代码耦合清单（io.imkit 拆分前置调研）

- 源仓库：`D:\Program Files\roubao`，模块 `app`，包 `com.xiaotaozi.autopilot`
- 基线：分支 `feat/openclaw-runtime`，HEAD `33e23250`。会话开始时的 `git status` 快照里，本清单涉及的聊天文件**无未提交改动**（脏文件只有 `ScrcpyBridge.kt`/`ScrcpyCoordinator.kt`/admin-ui/video-relay 等非聊天文件）。
- 方法：只读。全部结论来自 `grep`/读源码核对；下文所有路径都相对 `app/src/main/java/com/xiaotaozi/autopilot/`，行号为当前 HEAD 实际行号。凡是推测都明确标注「推测」。
- 规模：聊天相关 96 个文件、24,258 行（`chat/` 24 个 + `chat/e2e` 5 + `chat/call` 8 + `chat/crypto` 1 + `ui/components/chat` 23 + `ui/screens/messages` 24 + `ui/screens/contacts` 6 + `ui/screens/call` 4 + `ui/ChatSubApp.kt`）。

## 结论摘要

1. **`ImWebSocketClient` 是全模块的枢纽而非纯传输层**：它在 10 类服务端推送和 3 类请求响应上直接调用 `ChatRepository`（27 处代码），还直调 `ChatNotifier`、`TinodeClientManager` 的群名缓存、业务仓储 `RedPacketRepository`，内含通话 DTO 与通话协议，并在建连时初始化 `ImConfig`/`E2eeConfig`/`ChatSettings`——core 与 repository/notify/call/e2e 之间全是双向依赖。
2. **业务消息类型硬编码在渲染、发送、预览、通知四条链路上**（转账、红包、商城卡片、掼蛋邀请、通话记录、群 AI、社交广场订单），而为此设计的 `MessageContent`/`toContent()` 在生产代码里**零调用**，只被两个单测使用。
3. **IM 编排大量落在宿主**：`MainActivity`（3002 行）里有 186 行命中聊天相关标识，负责登录建连、联系人 REST 同步、换账号清理、通话界面路由；联系人/好友 REST（`/api/v1/chat/contacts…`）没有任何仓储层，UI 与 `MainActivity` 直接调用。
4. **存储与工具被非 IM 功能借用**：`ChatDatabase` 里混有陪伴记忆两张表（被 3 个非 IM 文件使用）；`ImageResolver`/`MediaUtils`/`MediaCache` 等通用工具放在 `chat` 包里，被 28/9/4 个非聊天文件引用。
5. **i18n 与资源耦合普遍**：聊天代码共引用 618 个不同的 `R.string` 键（核心层 118 个），12 个核心文件经 `i18n.AppStrings` 取文案；`TinodeClientManager` 是一层以转发为主的兼容外壳，其中 3 个方法是死桩，且有 2 个死桩仍被调用。

---

## 1. 包依赖图（聊天代码 → 本项目非聊天包）

> 记号：`12i` 表示第 12 行的 import；不带 `i` 的是代码里的全限定名引用（FQN）。所有文件另外都依赖 `com.xiaotaozi.autopilot.R`，单独统计在表 1.4。

### 1.1 `chat/`、`chat/e2e/`、`chat/call/`、`chat/crypto/`（核心层）

| 文件 | 依赖的非聊天类（行号） |
|---|---|
| chat/ChatApiModels.kt | `i18n.AppStrings` 4i |
| chat/ChatNotifier.kt | `i18n.AppStrings` 16i；**`MainActivity`** 17i（用于 238/247/309/393/487/565/640/694 构造 `Intent`）；**`security.CalculatorLockManager`** 19i（116、126 读 `disguiseActive`） |
| chat/ChatRepository.kt | `i18n.AppStrings` 5i；`data.ImConfig.readReceiptEnabled` 878、890；`data.ImConfig.messageMaxLen` 1385 |
| chat/ConversationLoader.kt | `i18n.AppStrings` 4i |
| chat/GroupAiModels.kt | `i18n.AppStrings` 4i；`data.ImConfig.forwardBundleMax` 81 |
| chat/GroupMemberResolver.kt | `data.ApiClient` 3i（22 调 `getChatProfiles`，返回 `data.ChatProfileDto`） |
| chat/GuandanInviteModels.kt | `i18n.AppStrings` 4i |
| chat/ImWebSocketClient.kt | `i18n.AppStrings` 4i；`data.AuthManager` 256（`isChinaNetwork`）、2887（`accessToken`）；`data.ImConfig.init` 415、`historyPageSize` 748/2775、`readReceiptEnabled` 2231、`offlinePullMax` 2774；第三方 `FirebaseMessaging` 15i |
| chat/MallCardModels.kt | `i18n.AppStrings` 4i |
| chat/RedPacketModels.kt | `i18n.AppStrings` 4i |
| chat/RedPacketRepository.kt | `data.ApiClient` 4i |
| chat/TimeFormatter.kt | `i18n.AppStrings` 4i |
| chat/TinodeClientManager.kt | `data.ImConfig.historyPageSize` 202、210 |
| chat/TransferModels.kt | `i18n.AppStrings` 4i |
| chat/TransferRepository.kt | `i18n.AppStrings` 5i；`data.ApiClient` 6i |
| chat/call/CallFloatingBubbleService.kt | `MainActivity` 166 |
| chat/call/ImCallForegroundService.kt | `MainActivity` 14i（159 用）；`i18n.AppStrings` 16i |
| chat/call/ImCallManager.kt | `data.AuthManager` 20i（110 `init` 注入；193 `updateCredits`；337 `user.username`；351 `user.id`）；`i18n.AppStrings` 21i |
| chat/call/ImCallModels.kt | `i18n.AppStrings` 4i |
| 其余 19 个核心文件 | 只依赖 `R` 或不依赖任何本项目非聊天包：ChatDatabase、ChatForegroundState、ChatModels、ChatSettings（仅 `R.raw.msg_ding_dong`）、ImageResolver、MediaCache、MediaPlaceholder、MediaUtils、MessageContent、e2e 全部 5 个、call 的 AliRtcCallEngine/ArtcCallEngine/CallPermissions/SafeRtcSurfaceView、crypto/ChatContentCrypto |

### 1.2 聊天核心层内部的跨子包/跨层依赖（拆分时会变成模块间依赖）

| 源 → 目标 | 位置 | 说明 |
|---|---|---|
| chat → **ui.components.chat** | ChatNotifier.kt:20i（`ChatBannerState.show` 于 142/168/372/451/477/555） | 通知层直接写 UI 层的横幅状态 |
| chat → chat.call | ImWebSocketClient.kt:5i/6i（`CallType`、`CallVideoQuality`） | 传输层依赖通话模型 |
| chat → chat.crypto | ChatRepository.kt:4i（`ChatContentCrypto` 于 423–513 共 8 处） | 仓储层自己做落盘加密 |
| chat → chat.e2e | ImWebSocketClient.kt:416（`E2eeConfig.init`） | 传输层初始化 E2EE 配置 |
| chat.e2e → chat | E2eeRepository.kt:7i `ChatNotifier`、8i `ImWebSocketClient`、9i `ChatContentCrypto`；E2eeManager.kt:435 `ImWebSocketClient.myUserId` | |
| chat.call → chat | ImCallManager.kt:13–19i（`CallAcceptResult`/`CallAnsweredPush`/`CallInviteResult`/`IncomingCallRing`/`ChatForegroundState`/`ChatNotifier`/`ImWebSocketClient`）；ImCallModels.kt:3i `ChatMessage` | 通话 DTO 定义在 ImWebSocketClient.kt:191–239 |
| ui.components.chat → chat.call | CallLogCard.kt:32–34i | |

### 1.3 UI 层（`ui/components/chat`、`ui/screens/{messages,contacts,call}`、`ui/ChatSubApp.kt`）

| 目标包 | 使用者（文件:行） |
|---|---|
| `i18n.AppStrings` | components: CallLogCard 3i、GuandanInviteCard 3i、ImageViewerDialog 3i、MallShareCard 3i、MessageActionsSheet 3i、MessageComposer 3i、VideoMessageBubble 3i；messages: ChatAvatarCropScreen 36i、ChatProfileScreen 3i、ChatSettingsScreen 3i、ConversationScreen 25i、CreateGroupScreen 3i、GroupAiSettingsScreen 3i、GroupEncryptionScreen 21i、GroupInfoScreen 3i、GroupJoinPreviewScreen 3i、GroupJoinRequestsScreen 19i、GroupQrCodeScreen 3i、MyQrCodeScreen 3i、PaymentQrCodeScreen 25i、PaymentRequestConfirmScreen 3i、RedPacketDetailScreen 30i、RedPacketRevealScreen 50i、TransferDetailScreen 31i；contacts: AddContactScreen 3i、PaymentScanResolveScreen 3i、PendingRequestsScreen 3i、ScanResultContactScreen 3i；ChatSubApp 3i |
| `data.ApiClient` | ChatAvatarCropScreen 38i、ChatProfileScreen 19i、ConversationScreen 28i、PaymentQrCodeScreen 24i、PaymentRequestConfirmScreen 18i、RedPacketDetailScreen 38i、RedPacketRevealScreen 49i、TransferDetailScreen 40i、AddContactScreen 13i、BlockedUsersScreen 8i、PaymentScanResolveScreen 17i、PendingRequestsScreen 11i、ScanResultContactScreen 22i、ChatSubApp 24i |
| `data.ApiException` | ChatProfileScreen 224、AddContactScreen 55/89、PaymentScanResolveScreen 18i、ScanResultContactScreen 23i |
| `data.ImConfig` | MessageComposer 19i（44 `messageMaxLen`、45 `typingThrottleMs`）；GroupInfoScreen 19i（73 `nameMaxLen`）；ChatSubApp 23i（524/544 `maxAdmins`）；ConversationScreen 1311 `redPacketMaxTotalAmount`、1312 `redPacketMaxCount` |
| `data.AuthManager` + `data.KnowledgeClient` | GroupAiSettingsScreen 23i/24i（52 `KnowledgeClient(AuthManager(...))`） |
| `ui.components.TaoziAvatar` | ConversationItem 16i、GroupAiMessageBubble 25i、ReadReceiptRow 36i、WeChatBubble 44i、GroupJoinRequestsScreen 22i、MyQrCodeScreen 23i、PaymentQrCodeScreen 26i、PaymentRequestConfirmScreen 19i、RedPacketDetailScreen 39i、RedPacketRevealScreen 51i、RedPacketSendScreen 41i、ContactsScreen 20i、PaymentScanResolveScreen 19i、ScanResultContactScreen 24i、InCallVideoScreen 70i、InCallVoiceScreen 51i、IncomingCallScreen 38i、OutgoingCallScreen 45i |
| `ui.components.MessageStatusIcon` | WeChatBubble 43i（该组件本身又 import `chat.MsgStatus`，见 §2，成环） |
| `ui.components.SecureScreen` | MyQrCodeScreen 56、PaymentQrCodeScreen 69、PaymentRequestConfirmScreen 51、TransferSendScreen 46、PaymentScanResolveScreen 48 |
| `ui.theme.*` | `LocalTaoziColors`（42 个文件 import）、`WeChatColors`（ConversationItem、GroupAiMessageBubble、MessageComposer、MessageList、ReadReceiptRow、WeChatBubble、ConversationScreen、ContactsScreen、AttachmentPanel、ChatSubApp）、`TaoziShape`/`TaoziType`/`TaoziSpacing`/`TaoziMotion`/`TaoziElevation`/`tnum`/`tactile`/`Primary`/`TaoziColors`；业务配色 `TransferColors`（AttachmentPanel 27i、TransferCard 33i、TransferDetailScreen 44i、TransferSendScreen 22i）、`RedPacketColors`（AttachmentPanel 110、RedPacketCard 31i、RedPacketDetailScreen 42i、RedPacketRevealScreen 52i、RedPacketSendScreen 45i） |
| `utils.*` | ImageViewerDialog 16i `saveImageToGallery`；ConversationScreen 52–56i `openExternalLink`/`saveImageToGallery`/`saveVideoToGallery`/`saveFileToDownloads`/`shareFile`；GroupQrCodeScreen 32–34i、MyQrCodeScreen 28–30i、PaymentQrCodeScreen 33–35i `QrCodeUtils`/`saveBitmapToGallery`/`shareBitmap` |
| `guandan.GuandanActivity` | ConversationScreen 1048（`joinRoom`） |
| `ui.screens.socialsquare.*` | ConversationScreen 1395 `SocialSkillOrderUi`、1398 `SocialSquareClient`、1400 `SocialSquareClient.Result` |
| `ui.screens.discover.DiscoverScreen` | ChatSubApp 26i（872 使用） |
| `streaming.StreamingManager` | ChatSubApp 326（复用 `onContactAliasUpdated` 回调） |

### 1.4 资源依赖统计

| 分组 | import `R` 的文件 / 总文件 | 经 `AppStrings` 取文案的文件 | 不同的 `R.string` 键 |
|---|---|---|---|
| chat/ | 13 / 24 | 12 | 合计 118（chat/ + e2e + call） |
| chat/e2e | 1 / 5 | 0（E2eeRepository 引用 2 个键） | 同上 |
| chat/call | 4 / 8 | 3 | 同上 |
| ui/components/chat | 20 / 23 | 7 | UI 合计 518 |
| ui/screens/messages | 23 / 24 | 17 | 同上 |
| ui/screens/contacts | 6 / 6 | 4 | 同上 |
| ui/screens/call | 4 / 4 | 0 | 同上 |
| ui/ChatSubApp.kt | 1 / 1 | 1 | 同上 |

全部聊天代码合计引用 618 个不同的 `R.string` 键；另有 `R.raw.msg_ding_dong`（ChatSettings.kt:34）、`R.mipmap.ic_launcher`（ChatNotifier.kt 258/313/404/497/575/653/674/704）。

---

## 2. 反向依赖（聊天以外的代码 → 聊天代码）

扫描范围：`app/src/main/java` 下除聊天目录外的全部 `.kt`。共 54 个文件命中，其中 4 个只在注释里提到（`ui/BroadcastActivity.kt:25`、`ui/screens/broadcast/BroadcastSubApp.kt:60`、`sms/SmsDatabase.kt:21`、`security/CalculatorLockManager.kt:28`），其余 50 个有代码引用：2.1 两个（MainActivity、SocialSquareRoot）、2.2 十五个（含 MainActivity）、2.3 三十四个只借用通用工具的文件。`paodekuai/` 与 `wuxing/` **零引用**（已 grep 核实），它们只经 `MainActivity` 的 Discover 路由被打开。

### 2.1 只是打开聊天入口 / 复用聊天 UI（浅）

| 文件:行 | 引用 | 说明 |
|---|---|---|
| MainActivity.kt:58i、2108–2171 | `ui.ChatSubApp(...)` | 「消息」Tab 入口，传入 32 个参数（见 §5.3） |
| MainActivity.kt:890 | `ui.components.chat.IncomingMessageBannerHost` | 前台横幅宿主；892–896 再按伪 topic 路由 |
| MainActivity.kt:1591 | `ui.screens.messages.TransferDetailHost` | 积分明细跳转账详情 |
| ui/screens/socialsquare/SocialSquareRoot.kt:33i/95、36i/470 | `chat.ChatContact`、`ui.screens.contacts.BlockedUsersScreen` | 社交广场复用黑名单页 |

### 2.2 深度调用内部类

| 文件 | 行 | 引用的聊天类/成员 |
|---|---|---|
| App.kt | 11i、86、91、92；110 | `ChatForegroundState.isAppForeground`/`openConversationId`；`ImageResolver.init` |
| MainActivity.kt | 157–162 | 字段 `lateinit var chatRepo: ChatRepository`（157）；`Conversation`/`ChatContact` 类型的 `imConversations`/`imContacts`/`imPendingContacts`/`imBlockedContacts` StateFlow |
| MainActivity.kt | 389–394 | `new ChatRepository(TinodeClientManager, ChatDatabase.get(this))`，`ChatRepository.current = chatRepo` |
| MainActivity.kt | 403、409、410、417 | `E2eeRepository.init`、`ImCallManager.init`、`chatRepo.onAutoReplyTrigger`、`chatRepo.onDiscardOrphanUpload` |
| MainActivity.kt | 324–333、359–368 | `ChatNotifier.EXTRA_OPEN_SOCIAL_*`、`ImCallForegroundService.EXTRA_RESTORE_CALL`、`ImCallManager.setMinimized` |
| MainActivity.kt | 543、557、568 | `chatRepo.ensureCacheOwnership`、`MediaCache.clearForAccountSwitch`、`E2eeDatabase.wipeForAccountSwitch` |
| MainActivity.kt | 634–640、739 | `ImWebSocketClient.onContactSync` 赋值/清空 |
| MainActivity.kt | 645–673 | `StreamingManager.onContactProfileUpdated/onContactAliasUpdated` → `chatRepo.onPeerAvatarChanged/onPeerDisplayNameChanged/onPeerAliasChanged/cacheContacts` |
| MainActivity.kt | 679–712 | `getCachedContacts`/`seedPeerAliases`/`getCachedConversations`、`TinodeClientManager.connectAndLoginMultiLeg(authManager.imWsUrls, …)`、轮询 `state` |
| MainActivity.kt | 729–735 | 退出登录：`chatRepo.clear`、`ImWebSocketClient.unregisterFcmToken`、`TinodeClientManager.disconnect` |
| MainActivity.kt | 942–1015 | `ImCallManager.uiState/setMinimized/showOverlayRationale/dismissOverlayRationale`，按 `CallUiState` 路由 `IncomingCallScreen`/`OutgoingCallScreen`/`InCallVideoScreen`/`InCallVoiceScreen`/`CallEndedOverlay` |
| MainActivity.kt | 1321、2157 | `ChatNotifier.taskIdFromTopic`、`isTaskChatTopic`（需求广场伪 topic） |
| MainActivity.kt | 1763、2434 | `TinodeClientManager.getOrCreateP2PTopic` |
| MainActivity.kt | 2037–2039、2060–2066 | `chatRepo.conversationTick/peerNames/presence`；重连轮询 |
| MainActivity.kt | 2349–2537 | `refreshImProfile`（`/api/v1/chat/me`）、`refreshImContacts`（`/api/v1/chat/contacts?...` 2380/2382/2444/2449 + `toChatContacts` 2386/2445/2450）、`pruneContactLocally` 2473、`pruneContactLocallyById` 2500、`refreshImConversations`（2523 `ConversationLoader.build`、2534 `queryAllPresence`） |
| agent/AgentImClient.kt | 4i、31–33、41、75、77、78、112、116、131 | `ChatRepository.current`、`getCachedConversations`、`loadMoreMessages`、`getMessages`、`sendMessage`、`markConversationRead`（供 agent/node/AndroidNodeCommands.kt:723–729 的 `im.*` 命令） |
| agent/ImAutoReplyController.kt | 12–13i、102、141 | `ChatMessage`、`ChatRepository.current.getCachedConversations` |
| data/BootstrapInfo.kt | 236–237 | `e2e.E2eeConfig.applyFromServer`、`E2eeConfigSnapshot.fromJson`（`ImConfig` 本身定义在本文件 16/91 行） |
| data/CompanionMemoryRepository.kt | 5–7i、19、25、28–98 | `ChatDatabase`、`CompanionMemoryEntity`、`CompanionMemorySyncState`（陪伴记忆借用聊天库） |
| ui/screens/CompanionChatScreen.kt | 67–69i、75i、117、127、1092、1207、1450 | `ChatDatabase.get`、`CompanionMemoryEntity`、`ImageViewerDialog`、`MediaCache.dataSourceFactory` |
| ui/screens/VoiceCallScreen.kt | 46i、70 | `ChatDatabase.get`（构造 `CompanionMemoryRepository`） |
| guandan/GuandanActivity.kt | 71–73 | 第二个 `ChatRepository(TinodeClientManager, ChatDatabase.get(...))` 实例 |
| guandan/ui/GuandanTableScreen.kt | 92、275、374、384–394 | `ChatRepository` 参数、`ForwardDialog`、`chatRepo.sendMessage` + `guandanInvitePlaceholderText` + `GUANDAN_INVITE_*` 常量 |
| service/FcmService.kt | 8–9i、29、49、69 | `TinodeClientManager.registerFcmToken`、`ChatNotifier.onIncomingMessage`（含 e2e 分支） |
| streaming/StreamingManager.kt | 441、471–473、503、543、556、574、587 | `TransferRepository.applyFromJson`（`transfer_update`）；`ChatNotifier.onIncomingMessage/taskChatTopic`（`task_message`）、`onFriendRequest`（`contact_request`）、`onSocialSquareContactInitiated/InterestReceived/Matched/OnlineStatusChanged` |
| ui/CloudCustomerServiceActivity.kt | 846、856 | `Conversation`、`ChatRepository.current.getCachedConversations` |
| ui/screens/TaskDetailScreen.kt | 144–147 | `ChatNotifier.taskChatTopic/cancel`、`ChatForegroundState.openConversationId` |
| ui/components/MessageStatusIcon.kt | 16i、30–62 | `chat.MsgStatus`（被 WeChatBubble 反向使用，跨包成环） |

### 2.3 借用聊天包里的通用工具（非 IM 语义）

| 聊天包里的类 | 非聊天使用文件数 | 使用者 |
|---|---|---|
| `chat.ImageResolver` | 28 | App；VideoDownloadScreen、VideoDubScreen；broadcast/{AvatarLibrary,BatchReview,BroadcastResult,CastPick,Rendering}；moments/{MomentCompose,MomentCoverEditor,MomentCoverHeader,MomentPostCard,MomentsRepository,MomentsScreen,MomentUserProfile}；socialsquare 下 13 个文件 |
| `chat.MediaUtils` | 9 | data/CompanionChatClient、broadcast/AvatarConfirmScreen、AvatarLibraryScreen、moments/MomentComposeScreen、socialsquare/{SkillEditSheet,SocialSquareCreateWizard,SocialSquareEditProfileScreen,SocialSquareMediaEditors}、world/WorldViewerScreen |
| `chat.MediaCache` | 4 | CompanionChatScreen、music/MusicPlayerScreen、socialsquare/SocialVideoPlayerOverlay、MainActivity |
| `chat.TimeFormatter` | 2 | moments/MomentNotificationsScreen、moments/MomentPostCard |
| `ui.components.chat.ImageViewerDialog` | 4 | CompanionChatScreen、MomentsScreen、MomentUserProfileScreen、SocialProfileDetailScreen |
| `ui.components.chat.FullscreenVideoPlayer`（定义在 VideoMessageBubble.kt） | 5 | BroadcastResultScreen、MomentsScreen、MomentUserProfileScreen、SocialProfileDetailScreen、WorldViewerScreen |
| `ui.components.chat.VoiceRecorder`（定义在 VoiceRecordOverlay.kt:165） | 3 | TaskVoicePublishSheet、broadcast/ConsentRecordScreen、socialsquare/SocialVoiceIntroEditor |

---

## 3. `TinodeClientManager` 的全部引用

`chat/TinodeClientManager.kt` 是一个 258 行的 `object`。除它自身外，**13 个文件**提到它，其中 2 个仅在注释里提到。

### 3.1 调用点与使用的成员

| 文件 | 行 | 使用的成员 |
|---|---|---|
| chat/ChatRepository.kt | 41 | 构造参数类型（对 `object` 传实例，无意义） |
| 同上（经实例 `tinodeManager.`） | 264、688、689、700、880、1013、1602、1618、1677、1684 | `myUserId`、`getOrCreateP2PTopic`、`getOrCreateGrpTopic`、`publishMessage`、`markRead`、`createGroup`、`fetchGroupAiSettings`、`updateGroupAiSettings`、`forwardBundle`、`forwardBroadcast` |
| 同上（静态 `TinodeClientManager.`） | 1041、1071、1164 | `deleteMessage`、`deleteTopic`、`loadHistoryMessages`（同一个类里实例/静态两种访问混用；另有 1359/1645/1669 绕过它直调 `ImWebSocketClient`） |
| chat/ConversationLoader.kt | 69（参数 `tm`）、76、79、80、81、97、104、166、173 | `getOrCreateP2PTopic`、`getSubscriptions`、`ensureSubscribed`×3、`getGroupTopics`、`getGroupName`、`getPeerDisplayName`（死桩，恒 null） |
| chat/ImWebSocketClient.kt | 644、1177、1238、2260、2558、2559、2617、2738 | `clearGroupNameCache`、`invalidateGroupNameCache`×5、`cachedGroupNameOrNull`、`cacheGroupName`（**下层反调上层外壳，成环**） |
| guandan/GuandanActivity.kt | 72 | 作为 `ChatRepository` 构造参数传入 |
| guandan/ui/GuandanTableScreen.kt | 90 | 仅注释 |
| MainActivity.kt | 390；694、701、707–708；735；1763；2060–2066；2434；2524；2534 | 构造参数；`connectAndLoginMultiLeg` + `state`/`State.CONNECTED/DISCONNECTED`；`disconnect`；`getOrCreateP2PTopic`；（重连）`state`+`connectAndLoginMultiLeg`；`getOrCreateP2PTopic`；传给 `ConversationLoader.build`；`queryAllPresence` |
| service/FcmService.kt | 9i、29 | `registerFcmToken` |
| ui/ChatSubApp.kt | 206、208、365、385、396、420、442、528、586、592、607、619、627、653、766–781、879 | `getGroupMembers`、`myUserId`（×7）、`inviteToGroup`、`setMemberRole`、`transferOwnership`、`disbandGroup`、作为 `tinodeManager` 传给 GroupInfoScreen、`kickMember`、`leaveGroup`、`state`/`State.*` |
| ui/components/chat/ForwardDialog.kt | 22i、56 | 传给 `ConversationLoader.build` |
| ui/screens/messages/ChatProfileScreen.kt | 145 | `updateMyDisplayName` —— **死桩（192 行空实现），改昵称成功后调用它什么也不做** |
| ui/screens/messages/ConversationScreen.kt | 27i、196、220、222、224、231、1152 | `queryPresence`、`awaitConnected`、`getOrCreateP2PTopic`、`getOrCreateGrpTopic`、`loadHistoryMessages`、`noteKeyPress` |
| ui/screens/messages/GroupInfoScreen.kt | 18i、27、82、116 | 参数类型、`getGroupName`、`updateGroupName` |
| ui/screens/socialsquare/SocialSquareRoot.kt | 117 | 仅注释 |

外部从未使用的成员：`connectAndLogin`（单腿版，连带 `buildWsUrl` 253–257）、`isConnected` 75、`resubscribeAll` 98、`loadNewerMessages` 208–215、`isGroupOwner` 226–230、`getTotalUnread` 252（死桩，恒 0）、`tokenProvider` 19–21（ImWebSocketClient.kt:2877 的注释也确认全仓库从未赋值）。

### 3.2 自身含有的真实逻辑（非纯转发）

| 行 | 逻辑 | 拆分去向建议 |
|---|---|---|
| 15–34 | 自有 `State` 枚举 + 常驻协程把 `ImWebSocketClient.state` 一一映射过来（两套等价枚举） | 删除，直接暴露 core 的连接状态 |
| 49–74 | `connectAndLoginMultiLeg`：登记重连候选线路、逐腿 `autoReconnectOnFailure=false` 尝试、全败后 `scheduleReconnect()` | core（连接管理器） |
| 88–95 | `awaitConnected`：200ms 轮询等待 CONNECTED | core |
| 114–121 | `getGroupTopics`：失败返回 null 而非空列表（防止误清会话缓存） | repository |
| 143–190 | 群名缓存：5 分钟 TTL 的 `ConcurrentHashMap`，`getGroupName`/`invalidateGroupNameCache`/`clearGroupNameCache`/`cacheGroupName`/`cachedGroupNameOrNull` | repository（目前被传输层反调，见 §4） |
| 193–196 | `publishMessage` 发送前 `addKnownTopic` | core |
| 200–215 | 历史拉取：把结果折叠成 Boolean | repository |
| 216–220、226–230 | `getGroupMembers` 吞异常返回空；`isGroupOwner` 比较 owner 与 `myUserId` | repository |
| 191、192、252 | 死桩：`getPeerDisplayName`→null、`updateMyDisplayName`→无操作、`getTotalUnread`→0 | 删除；前两个仍有调用方（ConversationLoader:173、ChatProfileScreen:145），需同步删调用 |
| 253–257 | `buildWsUrl`：拼 `wss://$host/ws/im`（仅单腿 `connectAndLogin` 使用，而它无人调用） | 删除 |

---

## 4. 连接层直接调用仓储/通知的位置（`chat/ImWebSocketClient.kt`）

分发入口：`handleServerMessage` 1966–2051，先按 `action` 字段、通话类推送按 `type` 字段（2025–2039 的注释说明了 `action`/`type` 混用的来历）。`repository` 字段在 287 声明，410 在 `connectAndLoginLocked` 里赋值。

### 4.1 每个调用点与触发它的服务端消息

| 行 | 调用 | 触发（服务端消息 / 客户端动作） |
|---|---|---|
| 644 | `TinodeClientManager.clearGroupNameCache()` | 客户端 `disconnect()`（退出登录/换账号） |
| 774 | `repository?.applyRemoteClear(...)` | `action=get` 的响应（`getMessages`，含历史/离线补拉） |
| 791 | `repository?.applyReadCursorSnapshot(...)` | 同上（响应带 `readCursors`） |
| 802 | `repository?.onIncomingMessage(...)` | 同上（逐条历史消息） |
| 883 | `repository?.applyRemoteClear(...)` | `action=del_topic` 的响应 |
| 1177、1181 | `TinodeClientManager.invalidateGroupNameCache`、`repository?.bumpConversationTick()` | `action=leave_grp` 的 200 响应 |
| 1238 | `TinodeClientManager.invalidateGroupNameCache` | `action=update_grp` 的 200 响应 |
| 2073、2080、2081 | `repository?.purgeMisfiledMessages`、`repository?.retryFailedMessages`、`pullOfflineMessages()`（→2782 `repo.getResumeSeq` → `getMessages` → 774–802） | `action=login`（code 200） |
| 2085 | `onLoggedIn?.invoke()` | `action=login`（code 200） |
| 2156、2210、2220、2251、2260、2263 | `repo.getMessages`（seq 去重）、`repo.onIncomingMessage`、`repo.resolveSenderName`、`TinodeClientManager.cachedGroupNameOrNull`、`ChatNotifier.onIncomingMessage` | `action=data` |
| 2230–2232 | `ChatSettings.readReceiptsEnabled` → `markRead` | `action=data`（对端消息） |
| 2283 | `repo.removeMessageBySeq` | `action=recalled` |
| 2302、2303 | `repo.applyRemoteClear` / `repo.clearConversationLocal` | `action=topic_cleared` |
| 2409 | **`RedPacketRepository.applyProgressPush`（业务仓储）** | `action=red_packet_update` |
| 2526、2539 | `repo.updatePresence` | `action=pres`（`what=snapshot` / `on`/`off`） |
| 2558–2559、2563、2572、2577 | `TinodeClientManager.cacheGroupName`/`invalidateGroupNameCache`、`repository?.bumpConversationTick`、`repository?.onGroupMembersChanged`、`repository?.onGroupRosterChanged` | `action=grp_sync` |
| 2617、2619、2620 | `invalidateGroupNameCache`、`bumpConversationTick`、`repository?.onGroupDisbanded` | `action=grp_disband` |
| 2631 | `repository?.onGroupJoinRequestChanged` | `action=group_join_request` |
| 2642 | `repository?.onGroupJoinReviewed` | `action=group_join_reviewed` |
| 2657、2660、2662 | `repo.onReadReceipt`、`repo.onReaderCursorUpdate`、`repo.updateTyping` | `action=info`（`what=read` / `what=kp`） |
| 2738 | `TinodeClientManager.invalidateGroupNameCache` | 客户端 `forgetKnownTopic()`（唯一调用方 ChatRepository.kt:1359） |
| 2743 | `repo.getKnownTopicDao()`（把 Room DAO 交给传输层） | `resubscribeAll()`（login 后） |
| 2875、2899 | 把 `repository` 再传回 `connectAndLogin` | 自动重连 `scheduleReconnect()`（`onClosed`/`onFailure`/网络恢复/被踢下线） |

另外，`ImWebSocketClient` 在建连时还会初始化非传输的东西：375 `ChatSettings.init`、414 `refreshChinaNetworkFlag`（读 `AuthManager`）、415 `ImConfig.init`、416 `E2eeConfig.init`；2134 直接调用 `FirebaseMessaging` 上传推送 token。

### 4.2 `ImWebSocketClient` 的 14 个 `var onXxx` 回调及赋值方

| 回调（声明行） | 由谁触发 | 赋值方（文件:行） |
|---|---|---|
| `onContactSync` (2324) | `action=contact_sync` | MainActivity.kt:634（739 置 null） |
| `onE2eDeliver` (2329) | `action=e2e_deliver` | chat/e2e/E2eeRepository.kt:82 |
| `onE2eTerminated` (2331) | `action=e2e_terminated` | E2eeRepository.kt:85 |
| `onE2eCleared` (2333) | `action=e2e_cleared` | E2eeRepository.kt:100 |
| `onE2eGroupCleared` (2336) | `action=e2e_group_cleared` | E2eeRepository.kt:113 |
| `onE2eGroupEncrypted` (2341) | `action=e2e_set_group_encrypted` | E2eeRepository.kt:122 |
| `onLoggedIn` (2349) | `action=login` 成功 | E2eeRepository.kt:138 |
| `onCallRing` (2354) | `type=call_ring` | chat/call/ImCallManager.kt:183 |
| `onCallAnswered` (2355) | `type=call_answered` | ImCallManager.kt:184 |
| `onCallEnded` (2358) | `type=call_ended` | ImCallManager.kt:185 |
| `onCallUpgradeRequest` (2359) | `type=call_upgrade_request` | ImCallManager.kt:186 |
| `onCallUpgraded` (2360) | `type=call_upgraded` | ImCallManager.kt:187 |
| `onCallUpgradeDeclined` (2361) | `type=call_upgrade_declined` | ImCallManager.kt:188 |
| `onCallCreditBalance` (2364) | `type=credit_balance` | ImCallManager.kt:193（→ 宿主 `AuthManager.updateCredits`） |

全部是单槽 `var`，后赋值者覆盖先赋值者。另有 `var tokenProvider`（347）从未被赋值。

### 4.3 `ChatRepository` 上由连接层触发、由 UI/宿主赋值的回调（同样是单槽 `var`）

| 回调（声明行） | 赋值方 |
|---|---|
| `onSendFailed` (1372) | ui/screens/messages/ConversationScreen.kt:243 |
| `onGroupDisbanded` (1391) | ui/ChatSubApp.kt:228 |
| `onGroupMembersChanged` (1406) | ui/ChatSubApp.kt:238 |
| `onGroupRosterChanged` (1417) | ui/ChatSubApp.kt:247 |
| `onGroupJoinRequestChanged` (1421) | ui/ChatSubApp.kt:171 |
| `onGroupJoinReviewed` (1424) | ui/ChatSubApp.kt:178 |
| `onDiscardOrphanUpload` (1444) | MainActivity.kt:417（调 `DELETE /api/v1/chat/upload/...`） |
| `onAutoReplyTrigger` (1454) | MainActivity.kt:410（→ `agent.ImAutoReplyController.onNewMessage`） |

---

## 5. 业务类型渗入核心的位置

### 5.1 `chat/MessageContent.kt` 的业务子类

| 子类（行） | 业务 | 模型/常量所在 |
|---|---|---|
| `Transfer` (17) | 积分转账 | TransferModels.kt（`TRANSFER_CONTENT_TYPE="transfer"` 15、`transferId()` 107、`transferPlaceholderText` 120） |
| `MallCard` (20) | SaaS 商城分享 | MallCardModels.kt（`"mall_card"` 13、`mallCard()` 25、`mallCardPlaceholderText` 36） |
| `GuandanInvite` (23) | 掼蛋房间邀请 | GuandanInviteModels.kt（`"guandan_invite"` 18、`guandanInvite()` 32、`guandanInvitePlaceholderText` 45） |

不在密封接口里、但同样按 `contentType` 分支的业务类型：红包（RedPacketModels.kt：`"red_packet"` 13、`redPacketId()` 134、`redPacketPlaceholderText` 143）、通话记录（chat/call/ImCallModels.kt：`"im_call_log"` 213、`callLog()` 253、`callLogPlaceholderText` 268）、群 AI 回复（GroupAiModels.kt：`SENDER_KIND_AI_ASSISTANT` 18，按 `senderKind` 路由）。

**关键事实**：`toContent()`（MessageContent.kt:26–46）在生产代码中**零调用**；仅 `app/src/test/.../chat/ChatUITest.kt`（8 处）与 `ChatUtilitiesTest.kt`（8 处）使用。生产渲染全部绕开它，由 WeChatBubble 自行分支。

### 5.2 业务类型的使用点

| 位置 | 内容 |
|---|---|
| ui/components/chat/WeChatBubble.kt:80–96 | 参数 `transferState`、`onTransferClick`、`redPacketState`、`onRedPacketClick`、`myAppUserId`、`onMallCardClick`、`onGuandanInviteClick`、`onCallLogClick` |
| WeChatBubble.kt:129–143 | 判定 `isTransfer`/`isRedPacket`/`mallCard()`/`guandanInvite()`/`callLog()` |
| WeChatBubble.kt:257、270、277、284、292 | 渲染 `TransferCard`、`RedPacketCard`、`MallShareCard`、`GuandanInviteCard`、`CallLogCard` |
| WeChatBubble.kt:360 | 文字气泡抑制条件里排除上述 5 种业务卡片 |
| ui/components/chat/MessageList.kt:39–49 | 参数 `transferStates`、`onTransferClick`、`redPacketStates`、`onRedPacketClick`、`myAppUserId`、`onMallCardClick`、`onGuandanInviteClick`、`onCallLogClick` |
| MessageList.kt:114–121、138–145 | `SENDER_KIND_AI_ASSISTANT` → `GroupAiMessageBubble`；`transferId()`/`redPacketId()` 查状态后透传 |
| ui/components/chat/AttachmentPanel.kt:47–51、110 | 附件面板槽位 `onTransfer`、`onRedPacket`、`onMallShare`；红包配色 |
| ui/screens/messages/ConversationScreen.kt:31–46i | `TRANSFER_CONTENT_TYPE`（31）/`TransferRepository`/`TransferResult`（33–34）/`MALL_CARD_CONTENT_TYPE`（39）/`mallCard`/`mallCardPlaceholderText`/`guandanInvite`/`callLog`（43–46） |
| ConversationScreen.kt:107–141 | 参数 `peerAppUserId`（转账）、`onOpenEncryptedThread`/`hasEncryptedThread`（E2EE）、`onVoiceCallClick`/`onVideoCallClick`（通话）、`pendingOrderId`（社交广场订单） |
| ConversationScreen.kt:262–298 | 转账/红包状态收集与按可见消息刷新 |
| ConversationScreen.kt:320–323 | 清空聊天前检查未领取转账 |
| ConversationScreen.kt:773、1392–1441 | 社交广场订单卡片 `OrderInfoCard`（`SocialSquareClient`） |
| ConversationScreen.kt:869–873、891–892、916–929 | 长按菜单里的转账/红包分支（含 `TransferRepository.revoke`） |
| ConversationScreen.kt:1033–1063 | 卡片点击：红包领取/详情、转账详情、商城外链、`GuandanActivity.joinRoom`、通话记录回拨 `ImCallManager.startOutgoingCall` |
| ConversationScreen.kt:1163–1215 | 附件面板：读 `/api/v1/credits/balance`、商城卡片 `getSaasMallShareCard` + `sendMessage(MALL_CARD_CONTENT_TYPE)` |
| ConversationScreen.kt:1242–1290、1292–1300 | `TransferSendScreen` + `TransferRepository.create/bindSeq`；`TransferDetailHost` |
| ConversationScreen.kt:1306–1345、1351–1372 | `RedPacketSendScreen` + `RedPacketRepository.create/bindSeq`；`RedPacketDetailHost`/`RedPacketRevealHost` |
| ConversationScreen.kt:1006、1081 | AI 小桃子进入昵称表与 @ 候选列表 |
| chat/ChatRepository.kt:341–344 | 会话预览兜底文案按 `TRANSFER/RED_PACKET/MALL_CARD/GUANDAN_INVITE` 分支 |
| chat/ChatRepository.kt:1585–1622 | 群 AI 设置的缓存与读写 |
| chat/ImWebSocketClient.kt:2407–2411 | `red_packet_update` 推送直接写 `RedPacketRepository` |
| chat/ChatNotifier.kt:43–61、324–328、350–590 | 好友请求/社交广场/需求广场的伪 topic、专用通知与横幅 |
| ui/components/chat/IncomingMessageBanner.kt:56–83 | `BannerData.orderId`（社交广场技能订单专用字段） |
| data/BootstrapInfo.kt（`ImConfig`） | IM 参数里混有 `redPacketMaxTotalAmount`/`redPacketMaxCount`（ConversationScreen.kt:1311–1312 使用） |
| streaming/StreamingManager.kt:439–443 | `transfer_update` 推送写 `TransferRepository` |

### 5.3 `ui/ChatSubApp.kt` 的全部函数参数（48–75 行，共 32 个）

| # | 参数 | 类型 | 性质 |
|---|---|---|---|
| 1 | `repo` | `ChatRepository` | IM |
| 2 | `conversations` | `List<Conversation>` | IM（由宿主维护） |
| 3 | `contacts` | `List<ChatContact>` | IM（由宿主维护） |
| 4 | `pendingContacts` | `List<ChatContact>` | IM |
| 5 | `pendingCount` | `Int` | IM |
| 6 | `blockedContacts` | `List<ChatContact>` | IM |
| 7 | `myChatId` | `String?` | 宿主身份（「桃子号」） |
| 8 | `myDisplayName` | `String` | 宿主身份 |
| 9 | `apiClient` | `ApiClient` | **宿主 REST 客户端** |
| 10 | `myAvatarUrl` | `String?` | 宿主身份 |
| 11 | `onDisplayNameChanged` | `(String)->Unit` | 宿主状态回写 |
| 12 | `onChatIdChanged` | `(String)->Unit` | 宿主状态回写 |
| 13 | `onAvatarChanged` | `(String?)->Unit` | 宿主状态回写 |
| 14 | `onRefreshData` | `()->Unit` | 宿主刷新（联系人/会话由宿主拉取） |
| 15 | `onContactRemoved` | `(ChatContact)->Unit` | 宿主状态回写 |
| 16 | `onReturnToMain` | `()->Unit` | **宿主导航**（「主菜单」Tab） |
| 17 | `onNavigateToMain` | `(String)->Unit` | **业务入口**：Discover 路由 `world/moments/slotMachine/cardShop/socialSquare/paodekuai/guandan/wuxing/taskSquare/history/settings`（MainActivity.kt:2123–2152） |
| 18 | `modifier` | `Modifier` | — |
| 19 | `pendingOpenTopicId` | `String?` | 通知/深链打开会话 |
| 20 | `onPendingOpenConsumed` | `()->Unit` | 同上 |
| 21 | `pendingOpenOrderId` | `String?` | **业务入口**：社交广场技能订单 |
| 22 | `onPendingOpenOrderIdConsumed` | `()->Unit` | 同上 |
| 23 | `openPendingRequests` | `Boolean` | 好友请求 |
| 24 | `onOpenPendingRequestsConsumed` | `()->Unit` | 同上 |
| 25 | `pendingScanContactUid` | `String?` | App Link 加好友 |
| 26 | `onPendingScanContactConsumed` | `()->Unit` | 同上 |
| 27 | `pendingScanGroupToken` | `String?` | App Link 进群 |
| 28 | `onPendingScanGroupTokenConsumed` | `()->Unit` | 同上 |
| 29 | `pendingScanPaymentUid` | `String?` | **业务入口**：积分收款码（任意金额） |
| 30 | `onPendingScanPaymentConsumed` | `()->Unit` | 同上 |
| 31 | `pendingScanPaymentRequestId` | `String?` | **业务入口**：积分收款码（指定金额） |
| 32 | `onPendingScanPaymentRequestConsumed` | `()->Unit` | 同上 |

ChatSubApp 内部还直接承载业务页：`PaymentQrCodeScreen` 395、`PaymentScanResolveScreen` 418、`PaymentRequestConfirmScreen` 431、`GroupAiSettingsScreen` 481、`DiscoverScreen` 872；并直接调用 REST（306/853/863 `/api/v1/chat/contacts/{id}`）与 `ImWebSocketClient`（174/221/223/646/664/700）。

---

## 6. Room

### 6.1 `chat/ChatDatabase.kt`（数据库文件 `chat_cache.db`，见第 645 行；`@Database` 在第 627 行，version=18，exportSchema=false）

**实体（12 个）**

| 实体（行） | 表 | 主键 | 是否 IM |
|---|---|---|---|
| `MessageEntity` (9–34) | `chat_messages` | `stableKey`；索引 `topicId`、`(topicId,fromUser,status)`、`(topicId,fromUser,serverSeq)` | 是 |
| `CachedContactEntity` (117) | `cached_contacts` | `id` | 是 |
| `CachedConversationEntity` (131) | `cached_conversations` | `topicId` | 是 |
| `KnownTopicEntity` (157) | `known_topics` | `topicId` | 是 |
| `RecalledSeqEntity` (180) | `recalled_seqs` | `(topicId, seq)` | 是 |
| `ReadCursorEntity` (213) | `read_cursors` | `(topicId, uid)` | 是 |
| `DeletedConversationEntity` (251) | `deleted_conversations` | `topicId` | 是 |
| `TopicClearStateEntity` (269) | `topic_clear_state` | `topicId` | 是 |
| `ContentKeyEntity` (297) | `content_keys` | `topicId` | 是（本地落盘加密的会话 DEK） |
| `CompanionMemoryEntity` (315) | `companion_memory_cache` | `(userId, id)` | **否**（陪伴记忆） |
| `CompanionMemorySyncState` (337) | `companion_memory_sync_state` | `userId` | **否** |
| `ChatCacheOwnerEntity` (540) | `chat_cache_owner` | `id`（单行，=0） | 是（缓存归属账号） |

**DAO（12 个）**：`ChatMessageDao` 37、`RecalledSeqDao` 187、`ReadCursorDao` 222、`KnownTopicDao` 236、`DeletedConversationDao` 258、`TopicClearStateDao` 278、`ContentKeyDao` 306、`CompanionMemoryDao` 345（**非 IM**）、`CompanionMemorySyncDao` 357（**非 IM**）、`CachedContactDao` 369、`CachedConversationDao` 377、`ChatCacheOwnerDao` 548。

**迁移（16 个，2→18；没有 1→2）**

| 迁移（行） | 内容 |
|---|---|
| 2→3 (412) | 新建 `cached_contacts`、`cached_conversations` |
| 3→4 (430) | 新建 `known_topics` |
| 4→5 (148) | `cached_conversations` 加 `isPinned`、`isMuted` |
| 5→6 (439) | `chat_messages` 加两个复合索引 |
| 6→7 (447) | `chat_messages` 加 `duration` |
| 7→8 (454) | 新建 `deleted_conversations` |
| 8→9 (462) | 新建 `topic_clear_state` |
| 9→10 (471) | 新建 `companion_memory_cache`/`companion_memory_sync_state`（**非 IM**） |
| 10→11 (493) | 重建 `companion_memory_cache` 为 V2 字段并清空同步游标（**非 IM**） |
| 11→12 (519) | `chat_messages` 加 `extra` |
| 12→13 (553) | 新建 `chat_cache_owner` |
| 13→14 (564) | `chat_messages` 加 `forwardFromMsgId`、`forwardFromTopic`、`senderKind` |
| 14→15 (600) | `DELETE FROM topic_clear_state`（跨会话串号脏数据修复） |
| 15→16 (607) | 新建 `recalled_seqs` |
| 16→17 (614) | 新建 `read_cursors` |
| 17→18 (621) | 新建 `content_keys` |

其他：未调用 `fallbackToDestructiveMigration`，但注册了 `onDestructiveMigration` 回调（663）；`onOpen` 执行 `PRAGMA secure_delete = ON`（658）；扩展函数 `secureCheckpoint()`（683）做 `wal_checkpoint(TRUNCATE)`。`ChatRepository` 直接持有全部 10 个 IM DAO（ChatRepository.kt:60–69），并把 `KnownTopicDao` 交给 `ImWebSocketClient`（2743）。陪伴记忆两张表的使用者全在聊天之外：`data/CompanionMemoryRepository.kt`、`ui/screens/CompanionChatScreen.kt`、`ui/screens/VoiceCallScreen.kt`。

### 6.2 `chat/e2e/E2eeDatabase.kt`（数据库文件 `e2ee_vault.db`，见第 291 行；`@Database` 在第 262 行，version=2）

**实体（12 个，全部属于 E2EE）**

| 实体（行） | 表 | 主键 |
|---|---|---|
| `OwnIdentityEntity` (24) | `e2e_own_identity` | `id`（单行，=0） |
| `E2ePreKeyEntity` (39) | `e2e_pre_keys` | `keyId` |
| `E2eSignedPreKeyEntity` (56) | `e2e_signed_pre_keys` | `keyId` |
| `E2eKyberPreKeyEntity` (73) | `e2e_kyber_pre_keys` | `keyId` |
| `E2eSessionEntity` (90) | `e2e_sessions` | `(address, deviceId)` |
| `E2eRemoteIdentityEntity` (111) | `e2e_remote_identities` | `(address, deviceId)` |
| `E2eSenderKeyEntity` (127) | `e2e_sender_keys` | `(senderName, senderDeviceId, distributionId)` |
| `E2eThreadEntity` (145) | `e2e_threads` | `topicId` |
| `E2eMessageEntity` (169) | `e2e_messages` | `id`（自增），索引 `topicId` |
| `E2eGroupKeyDistributedEntity` (192) | `e2e_group_key_distributed` | `(topicId, memberUid)` |
| `E2eGroupSenderStateEntity` (215) | `e2e_group_sender_state` | `topicId` |
| `E2eContentKeyEntity` (239) | `e2e_content_keys` | `topicId` |

**DAO（12 个）**：`OwnIdentityDao` 32、`E2ePreKeyDao` 43、`E2eSignedPreKeyDao` 60、`E2eKyberPreKeyDao` 77、`E2eSessionDao` 94、`E2eRemoteIdentityDao` 120、`E2eSenderKeyDao` 136、`E2eThreadDao` 156、`E2eMessageDao` 180、`E2eGroupKeyDistributedDao` 196、`E2eGroupSenderStateDao` 219、`E2eContentKeyDao` 248。

**迁移（1 个）**：`E2E_MIGRATION_1_2`（256）新建 `e2e_content_keys`。另有 `wipeForAccountSwitch`（315，`clearAllTables` + 截断 WAL，由 MainActivity.kt:568 调用）和 `secure_delete`（300）。无非 IM 实体。

---

## 7. 硬编码中文与魔法字符串（作为逻辑判据或跨端约定使用）

### 7.1 中文字符串

| 字符串（定义位置） | 生产方 | 消费方 / 判据 | 风险 |
|---|---|---|---|
| `MediaPlaceholder.IMAGE/VIDEO/VOICE/AUDIO/FILE/ATTACHMENT` = `[图片]/[视频]/[语音]/[音频]/[文件]/[附件]`（MediaPlaceholder.kt:24–29，集合 `ALL` 32） | ImWebSocketClient.kt:812–815（历史消息）、2189–2192（实时 `data`）；ConversationScreen.kt:429–431（上传后发送，audio 落 `FILE`） | WeChatBubble.kt:53、151（`content.trim() in MEDIA_PLACEHOLDERS` 时不再画文字气泡） | 值会写进 `chat_messages.content` 与会话预览缓存（见 MediaPlaceholder.kt:9–16 注释），不能翻译、不能改值 |
| `AI_ASSISTANT_DISPLAY_NAME = "小桃子"`（GroupAiModels.kt:28） | ConversationScreen.kt:1081（作为 @ 候选插入正文，形成 `@小桃子`） | 服务端 `server/im-go/hub.go:3531` 的 `aiMentionRegex` 按纯文本匹配 `@小桃子`；另用于显示 ImWebSocketClient.kt:2250、GroupAiMessageBubble.kt:74/95、ConversationScreen.kt:1006 | 跨端协议依赖一个中文品牌名，改名或本地化会让群 AI 失效 |
| `"用户"`（ConversationLoader.kt:37；RedPacketRepository.kt:110） | 显示兜底 | 仅显示 | 未走资源，非中文界面会露出中文 |
| `"新消息"`（ChatNotifier.kt:36，通知渠道名） | 615 建渠道 | 系统设置里可见 | 未走资源 |
| `"对方"`（agent/ImAutoReplyController.kt:145） | 显示兜底 | 仅显示 | 在宿主侧 |

### 7.2 非中文魔法字符串

| 类别 | 定义位置 | 使用位置 |
|---|---|---|
| 伪 topic：`__friend_request__`、`__social_interest__`、`__social_my_profile__`、前缀 `__task__:` | ChatNotifier.kt:43、48、55、61 | MainActivity.kt:892–896、1321、2157；TaskDetailScreen.kt:144；StreamingManager.kt:473 |
| topic 前缀 `"grp"` vs `"grp_"` 不一致 | ChatRepository.kt:689/877/889/913/1230/1346；ConversationLoader.kt:79/80/114/164；ImWebSocketClient.kt:2222 用 `"grp"`；E2eeRepository.kt:222/232 用 `"grp_"` | — |
| 用户 uid 前缀 `"uid_"`/`"usr"` | ChatModels.kt:72 `isUserUid` | ChatRepository.kt:688、ConversationLoader.kt:79、ImWebSocketClient.kt:2953 |
| E2EE 线程 id `"e2e_<a>_<b>"` | E2eeRepository.kt:396 | ChatSubApp.kt:666、727 |
| 服务端英文错误原因 → 本地文案映射 | ChatRepository.kt:1381–1387（`"not authenticated"`、`"topic required"`、`"cannot publish to me/fnd"`、`"message too long"`、`"not a topic member"`） | 依赖服务端 `reason` 原文不变 |
| 业务 contentType | `"transfer"` TransferModels.kt:15、`"red_packet"` RedPacketModels.kt:13、`"mall_card"` MallCardModels.kt:13、`"guandan_invite"` GuandanInviteModels.kt:18、`"im_call_log"` ImCallModels.kt:213 | 见 §5 |
| senderKind / AI uid | `"user"` GroupAiModels.kt:14、`"ai_assistant"` 18、`"assistant_xiaotaozi"` 27 | MessageList.kt:114、ConversationScreen.kt:1003/1006 |
| E2EE 推送原因/种类 | E2eeRepository.kt:128、130（`member_removed`/`member_left`/`owner_enabled`），220–221（`sender_key`/`msg`） | — |
| 协议子类型 | ImWebSocketClient.kt:2520（`snapshot`）、2539（`on`）、2656（`read`）、2662（`kp`）；MainActivity.kt:635（`contact_sync` 的 `removed`） | — |
| 域名与二维码前缀 | ImageResolver.kt:6 `https://taozi.openedskill.com`；ScanQrScreen.kt:45–48、PaymentQrCodeScreen.kt:41–42、MyQrCodeScreen.kt:65、GroupQrCodeScreen.kt:74（`/qr/c/`、`/qr/g/`、`/qr/p/`、`/qr/pr/`） | 宿主 App Link（AndroidManifest.xml:206 `pathPrefix="/qr/"`） |
| 宿主 SharedPreferences 键 | ImageResolver.kt:7–8（`autopilot_prefs`/`server_base_url`） | 读宿主的服务器地址设置 |
| 设置键在两处重复 | ChatSettings.kt:18、39–42（`chat_settings`、`sound`/`vibrate`/`read_receipts`/`allow_stranger_msg`） | ChatSettingsScreen.kt:16、24–27、52–62 各自读写同名键；`allowStrangerMsg` 写了但无任何逻辑读取 |
| 通知渠道 id | ChatNotifier.kt:35 `xiaotaozi_chat_v6`、184 `xiaotaozi_call_v1`；ImCallForegroundService.kt:185 `xiaotaozi_im_call_fgs` | — |
| FileProvider authority | ConversationScreen.kt:492 `"${packageName}.fileprovider"` | 依赖宿主 Manifest 声明 |
| 硬编码 REST 路径 | 联系人：AddContactScreen.kt:53/82、ScanResultContactScreen.kt:72/170、PaymentScanResolveScreen.kt:72、BlockedUsersScreen.kt:23、PendingRequestsScreen.kt:39/43、ChatSubApp.kt:306/853/863、MainActivity.kt:2380/2382/2444/2449；资料：ChatProfileScreen.kt:143/208、ChatAvatarCropScreen.kt:117/120、MainActivity.kt:2351；上传：ConversationScreen.kt:422/439/603；转账/红包：TransferRepository.kt:46–101、RedPacketRepository.kt:64–141；积分：ConversationScreen.kt:1169/1181、PaymentScanResolveScreen.kt:99/209、PaymentRequestConfirmScreen.kt:66/162、PaymentQrCodeScreen.kt:242 | 联系人 REST 没有任何仓储层 |

---

## 8. 第三方依赖（按实际 import 归类）

版本取自 `app/build.gradle.kts`（只读）。

| 库（坐标，build.gradle.kts 行） | 使用文件:行 | 建议归属 |
|---|---|---|
| OkHttp `com.squareup.okhttp3:okhttp:4.12.0`（345） | chat/ImWebSocketClient.kt:21（`okhttp3.*`） | core |
| Firebase Messaging（BOM 33.7.0，454/457） | chat/ImWebSocketClient.kt:15（2134 `FirebaseMessaging.getInstance().token`）；宿主 service/FcmService.kt | 不进 core；可选 notify-fcm 或留宿主，core 只暴露 `registerPushToken(token)` |
| Coil `io.coil-kt:coil-compose:2.6.0`（372） | chat/ChatNotifier.kt:14–15（`ImageLoader`/`ImageRequest`，723–726 加载头像）；`coil.compose.AsyncImage`：ImageViewerDialog:14、IncomingMessageBanner:44、MallShareCard:26、VideoMessageBubble:36、WeChatBubble:35、ChatAvatarCropScreen:35、ChatProfileScreen:17、ConversationScreen:24、GroupAdminScreen:21、GroupInfoScreen:13 | ui-compose；notify 改为注入头像位图加载器 |
| Media3 1.4.1（391–397） | chat/MediaCache.kt:4–8（database/datasource/cache）；chat/MediaUtils.kt:7–9（transformer）；ui/components/chat/VideoMessageBubble.kt:29–35（exoplayer/ui）；VoiceMessageBubble.kt:30–35（exoplayer） | ui-compose（播放）；MediaCache/MediaUtils 留宿主并以接口注入 |
| Room 2.6.1（420–422，kapt） | chat/ChatDatabase.kt:5–7；chat/e2e/E2eeDatabase.kt:5–7 | storage-room；e2e 自带一份 |
| libsignal `org.signal:libsignal-android:0.73.0`（439） | chat/e2e/E2eProtocolStore.kt:3–8；E2eeManager.kt:10–21；E2eeRepository.kt:16 | e2e |
| DingRTC `com.ding.rtc:dingrtc-full:3.0.0`（451） | chat/call/AliRtcCallEngine.kt:7–9；`org.webrtc.mozi.*`（随 DingRTC 提供，推测）chat/call/SafeRtcSurfaceView.kt:9–10 | call |
| ML Kit `com.google.mlkit:barcode-scanning:17.3.0`（417） | ui/screens/messages/ScanQrScreen.kt:38–40 | ui-compose 的可选扫码子包或 examples |
| CameraX 1.3.4（400–407） | ScanQrScreen.kt:7–13 | 同上 |
| ZXing `com.google.zxing:core:3.5.3`（414） | 聊天文件未直接 import；经宿主 utils/QrCodeUtils.kt:11–15，被 GroupQrCodeScreen:32、MyQrCodeScreen:28、PaymentQrCodeScreen:33 使用 | 二维码生成器以接口注入；实现留宿主或 ui-compose 可选 |
| androidx.lifecycle | chat/call/CallFloatingBubbleService.kt:43–49 | call |
| androidx.activity（权限/选图 launcher） | CallPermissions.kt:11–12、ChatAvatarCropScreen.kt:9、ChatProfileScreen.kt:4–5、ConversationScreen.kt:5–6、ScanQrScreen.kt:5–6 | ui-compose / call |
| androidx.core `NotificationCompat` | ChatNotifier.kt:13、ImCallForegroundService.kt:12 | notify / call |
| Android Keystore + JCA | chat/crypto/ChatContentCrypto.kt:3–13 | core（被 storage-room 与 e2e 共用） |

---

## 9. 拆分建议（每个现有文件的目标模块）

前提假设（推测，供讨论）：`ui-compose` 不依赖 libsignal 与 DingRTC，因此 E2EE 与通话的界面放进各自模块的 ui 子包；ML Kit/CameraX 扫码作为可选能力。

### 9.1 `chat/`

| 文件 | 目标 | 理由 |
|---|---|---|
| ChatApiModels.kt | core（`toChatContact(s)`）+ 宿主（`ChatIdRules`） | JSON→模型属核心；「桃子号」规则与中文校验文案属宿主 |
| ChatDatabase.kt | storage-room（去掉 CompanionMemory* 两实体两 DAO） | 陪伴记忆非 IM，需在宿主另建库并处理 9→10、10→11 两段迁移的历史 |
| ChatForegroundState.kt | core | notify 与 call 都读它，App/UI 写它 |
| ChatModels.kt | core | 纯模型 |
| ChatNotifier.kt | notify（IM 消息 128、来电 228、未接 303）+ 宿主（好友请求 159/685、需求广场伪 topic 324–328/593、社交广场 340–590） | 需注入「点击 Intent」「伪装锁判据」，并去掉对 `ChatBannerState` 的直写 |
| ChatRepository.kt | repository | 预览业务分支（341–344）与群 AI（1585–1622）改为注册表/扩展；去掉对 `ImWebSocketClient` 的 3 处直调 |
| ChatSettings.kt | notify（提示音/震动）+ core（读回执开关改为配置接口） | `readReceiptsEnabled` 被传输层 2230 读取 |
| ConversationLoader.kt | repository | 会话列表构建 |
| GroupAiModels.kt | core（`SENDER_KIND_USER`、转发 DTO）+ examples（AI 相关） | 群 AI 是宿主业务 |
| GroupMemberResolver.kt | repository | `ApiClient.getChatProfiles` 改为注入 ProfileProvider |
| GuandanInviteModels.kt | examples / 宿主 | 掼蛋业务 |
| ImWebSocketClient.kt | core | 需拆出：通话 DTO 与 `sendCall*`/`call_*` 分发 → call；`e2e_*` → e2e；`red_packet_update` → 宿主扩展；对仓储/通知的直调改为事件流 |
| ImageResolver.kt | 留在宿主 | 28 个非 IM 文件使用；IM 只定义 URL 解析接口 |
| MallCardModels.kt | examples / 宿主 | 商城业务 |
| MediaCache.kt | 留在宿主 | 音乐/社交/陪伴共用；ui-compose 通过接口拿 DataSource.Factory |
| MediaPlaceholder.kt | core | 与服务端、Room 共享的协议哨兵 |
| MediaUtils.kt | 留在宿主 | 9 个非 IM 文件使用；ui-compose 发送媒体时注入压缩器 |
| MessageContent.kt | core（仅保留通用子类 + 可扩展自定义类型） | 业务子类移出；当前生产零调用，可借拆分让它成为真正的渲染入口 |
| RedPacketModels.kt | examples / 宿主 | 红包业务 |
| RedPacketRepository.kt | examples / 宿主 | 红包业务（REST） |
| TimeFormatter.kt | ui-compose | 展示格式化，依赖文案资源；moments 也用，宿主可依赖 ui-compose |
| TinodeClientManager.kt | 删除，真实逻辑并入 core（多腿连接、等待连接）与 repository（群名缓存等） | 主要是转发外壳，含 3 个死桩 |
| TransferModels.kt | examples / 宿主 | 转账业务 |
| TransferRepository.kt | examples / 宿主 | 转账业务（REST） |

### 9.2 `chat/e2e/`、`chat/call/`、`chat/crypto/`

| 文件 | 目标 | 理由 |
|---|---|---|
| e2e/E2eProtocolStore.kt | e2e | libsignal 存储实现 |
| e2e/E2eeConfig.kt | e2e | 由宿主 bootstrap 下发（BootstrapInfo.kt:236），改为宿主调用 e2e 的公开 API |
| e2e/E2eeDatabase.kt | e2e | 独立加密库 |
| e2e/E2eeManager.kt | e2e | 已基本依赖注入，仅 435 行直读 `ImWebSocketClient.myUserId` |
| e2e/E2eeRepository.kt | e2e | 依赖 core 的 E2EE 收发 API 与 notify |
| call/AliRtcCallEngine.kt | call | DingRTC 实现 |
| call/ArtcCallEngine.kt | call | 引擎抽象 |
| call/CallFloatingBubbleService.kt | call | 需注入回到宿主界面的 Intent（166） |
| call/CallPermissions.kt | call | Compose 权限封装 |
| call/ImCallForegroundService.kt | call | 需注入点击 Intent（159） |
| call/ImCallManager.kt | call | `AuthManager` 依赖改为注入当前用户与积分回调 |
| call/ImCallModels.kt | call | 含通话记录消息类型，作为 call 注册的自定义消息 |
| call/SafeRtcSurfaceView.kt | call | DingRTC 视图 |
| crypto/ChatContentCrypto.kt | core | 被 storage-room 与 e2e 共用的 Keystore 加密工具 |

### 9.3 `ui/components/chat/`

| 文件 | 目标 | 理由 |
|---|---|---|
| AttachmentPanel.kt | ui-compose | 业务格（转账/红包/商城）改为宿主提供的动作列表 |
| CallLogCard.kt | call（ui 子包） | 依赖 call 模型 |
| ConversationItem.kt | ui-compose | 会话行 |
| EmojiPicker.kt | ui-compose | 通用 |
| ForwardDialog.kt | ui-compose | 依赖 repository 的会话列表 |
| GroupAiMessageBubble.kt | examples | 群 AI 业务渲染器 |
| GuandanInviteCard.kt | examples / 宿主 | 掼蛋业务卡片 |
| ImageViewerDialog.kt | ui-compose | 宿主也在用；保存图片动作需注入 |
| IncomingMessageBanner.kt | notify（`ChatBannerState`）+ ui-compose（`IncomingMessageBannerHost`） | 状态与界面分开，去掉 `orderId` 业务字段 |
| MallShareCard.kt | examples / 宿主 | 商城业务卡片 |
| MediaBubbleSizing.kt | ui-compose | 通用 |
| MessageActionsSheet.kt | ui-compose | 通用 |
| MessageComposer.kt | ui-compose | `ImConfig` 参数改为配置接口 |
| MessageList.kt | ui-compose | 业务状态参数改为渲染器注册表 |
| NumericKeypad.kt | 留在宿主 | 仅转账/红包/收款使用 |
| ReadReceiptRow.kt | ui-compose | 通用 |
| RedPacketCard.kt | examples / 宿主 | 红包业务卡片 |
| SetAliasDialog.kt | ui-compose | 联系人备注 |
| TransferCard.kt | examples / 宿主 | 转账业务卡片 |
| VideoMessageBubble.kt | ui-compose | 含 `FullscreenVideoPlayer`，宿主 5 处复用 |
| VoiceMessageBubble.kt | ui-compose | 通用 |
| VoiceRecordOverlay.kt | ui-compose | 含 `VoiceRecorder`，宿主 3 处复用 |
| WeChatBubble.kt | ui-compose | 5 个业务卡片分支改为按 contentType 查渲染器 |

### 9.4 `ui/screens/messages/`、`ui/screens/contacts/`、`ui/screens/call/`、`ui/ChatSubApp.kt`

| 文件 | 目标 | 理由 |
|---|---|---|
| messages/ChatAvatarCropScreen.kt | ui-compose | 上传/资料 REST 改为注入 ProfileService |
| messages/ChatProfileScreen.kt | 留在宿主 / examples | 「我」页、桃子号、资料 REST，宿主账号概念 |
| messages/ChatSearchScreen.kt | ui-compose | 通用 |
| messages/ChatSettingsScreen.kt | ui-compose | 与 ChatSettings 共享键改为同一配置对象 |
| messages/ConversationScreen.kt | ui-compose | 先剥离业务（§5.2 列出的约 400 行）为插槽 |
| messages/CreateGroupScreen.kt | ui-compose | 通用 |
| messages/EncryptedConversationScreen.kt | e2e（ui 子包） | 依赖 E2EE |
| messages/GroupAdminScreen.kt | ui-compose | 通用 |
| messages/GroupAiSettingsScreen.kt | 留在宿主 / examples | 知识库、AI 业务 |
| messages/GroupEncryptionScreen.kt | e2e（ui 子包） | 依赖 E2EE |
| messages/GroupInfoScreen.kt | ui-compose | 通用 |
| messages/GroupJoinPreviewScreen.kt | ui-compose | 通用 |
| messages/GroupJoinRequestsScreen.kt | ui-compose | 通用 |
| messages/GroupQrCodeScreen.kt | ui-compose | 二维码生成器与链接前缀需注入 |
| messages/MessagesScreen.kt | ui-compose | 会话列表 |
| messages/MyQrCodeScreen.kt | ui-compose | 同 GroupQrCodeScreen |
| messages/PaymentQrCodeScreen.kt | 留在宿主 | 积分收款 |
| messages/PaymentRequestConfirmScreen.kt | 留在宿主 | 积分收款 |
| messages/RedPacketDetailScreen.kt | examples / 宿主 | 红包 |
| messages/RedPacketRevealScreen.kt | examples / 宿主 | 红包 |
| messages/RedPacketSendScreen.kt | examples / 宿主 | 红包 |
| messages/ScanQrScreen.kt | ui-compose（可选扫码子包） | ML Kit/CameraX 依赖重，前缀路由表需注入 |
| messages/TransferDetailScreen.kt | examples / 宿主 | 转账 |
| messages/TransferSendScreen.kt | examples / 宿主 | 转账 |
| contacts/AddContactScreen.kt | ui-compose | 联系人 REST 需改为注入 ContactService |
| contacts/BlockedUsersScreen.kt | ui-compose | 社交广场也复用 |
| contacts/ContactsScreen.kt | ui-compose | 通用 |
| contacts/PaymentScanResolveScreen.kt | 留在宿主 | 积分收款 |
| contacts/PendingRequestsScreen.kt | ui-compose | 好友请求 |
| contacts/ScanResultContactScreen.kt | ui-compose | 扫码加好友 |
| call/InCallVideoScreen.kt | call（ui 子包） | 通话界面 |
| call/InCallVoiceScreen.kt | call（ui 子包） | 通话界面 |
| call/IncomingCallScreen.kt | call（ui 子包） | 通话界面 |
| call/OutgoingCallScreen.kt | call（ui 子包） | 含 `CallEndedOverlay` |
| ui/ChatSubApp.kt | examples（宿主装配） | 4 个 Tab 外壳、Discover、收款码等是装配层；可抽出不含业务的导航到 ui-compose |

### 9.5 相关宿主文件

| 文件 | 目标 | 理由 |
|---|---|---|
| MainActivity.kt | 留在宿主 | 登录建连、联系人同步（2349–2537）应下沉为 repository 的 ContactSync；通话路由保留在宿主 |
| App.kt | 留在宿主 | 只做初始化调用 |
| service/FcmService.kt | 留在宿主 | 调 notify/core 的公开 API |
| streaming/StreamingManager.kt | 留在宿主 | 设备通道推送，经扩展点转给 notify |
| data/AuthManager.kt | 留在宿主 | `imWsUrls`（154）以端点提供者注入 core |
| data/ApiClient.kt | 留在宿主 | `getChatProfiles`/`ChatProfileDto`（23、375）作为 ProfileProvider 的宿主实现 |
| data/BootstrapInfo.kt | 留在宿主（`ImConfig` 抽象移入 core） | `ImConfig` 里的红包上限属业务，需拆开 |
| ui/components/MessageStatusIcon.kt | ui-compose | 依赖 `MsgStatus` 且被 WeChatBubble 使用 |
| agent/AgentImClient.kt、agent/ImAutoReplyController.kt | 留在宿主 | 使用 repository 公开 API |
| guandan/GuandanActivity.kt、guandan/ui/GuandanTableScreen.kt | 留在宿主 | 使用 repository 与 ForwardDialog |
| data/CompanionMemoryRepository.kt | 留在宿主 | 迁到宿主自己的数据库 |
