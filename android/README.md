# ImKit Android SDK

Phase 3 的成果：无 UI 的核心 SDK。Compose 组件与 sample-app 属于 Phase 4，还没开始。

包名 `io.imkit` 是占位符，定名后统一替换。

## 模块

| 模块 | 类型 | 内容 | 主要依赖 |
| --- | --- | --- | --- |
| `imkit-core` | 纯 Kotlin/JVM 库 | `ImClient`：连接、登录、多线路地址、重连与退避、请求关联。`ChatEngine`：发送队列与重试（C2）、接收排序与空洞检测（C3）、撤回墓碑、已读游标、未读与草稿、群名册与本人角色（C9）、清空水位、缓存归属（C4）。另有 `ChatStore` 接口与内存实现、在线三态（C5）、通知决策（C6 的纯函数部分）、推送令牌注册、媒体上传接口、线协议常量与 JSON。 | kotlinx-coroutines 1.9.0、OkHttp 4.12.0。不依赖 Android、Room、Compose。 |
| `imkit-storage-room` | Android 库 | 用 Room 实现的 `ChatStore`：新库从 v1 开始，schema 导出在 `schemas/`。可选的加密粉碎：每个会话一把数据密钥，由宿主提供的 `KeyWrapper` 包裹；删会话、清空、换账号时连密钥一起销毁。另有源项目 v18 旧库的只读导入器 `LegacyChatImporter`。 | Room 2.6.1 |
| `imkit-android` | Android 库 | 通知渲染：静音渠道，提示音由 App 播放，来电走全屏通知。`NotificationController` 把引擎事件接到通知上。另有媒体缓存（默认 300 MB）、图片压缩，以及用 AndroidKeyStore 实现的 `KeyWrapper`。 | core-ktx 1.12.0、media3 1.4.1 |
| `imkit-push-fcm` | Android 库 | FCM：令牌来源 `FcmTokenSource`，消息服务基类 `ImkitMessagingService`。是独立产物，不用 FCM 的 App 不必引入 Firebase。 | Firebase BOM 33.7.0 |

构建环境与源项目一致：AGP 8.4.2、Kotlin 1.9.24、Gradle 8.7、JDK 17、compileSdk 34。minSdk 是 26。计划 3.1 写的是 27，但源项目自己是 26，这里先按源项目，待定。

`imkit-core` 的测试夹具（`src/testFixtures`）提供存储契约 `ChatStoreContract`，以及内存服务器 `MiniServer`、`FakeServer`，其他模块的测试共用。

## 构建与测试

需要 Android SDK（platform 34），路径写在 `android/local.properties` 的 `sdk.dir`（不进仓库）。

```bash
cd android
./gradlew :imkit-core:test :imkit-storage-room:testDebugUnitTest :imkit-android:testDebugUnitTest :imkit-push-fcm:testDebugUnitTest
./gradlew :imkit-core:jar :imkit-storage-room:assembleRelease :imkit-android:assembleRelease :imkit-push-fcm:assembleRelease
```

Android 模块的单测跑在 Robolectric 4.12.2（SDK 34）上。第一次运行时它会下载 Android 运行时（约 144 MB）到 Maven 本地缓存，离线环境要先备好。

两组测试依赖仓库里的其他产物，缺了就跳过（显示为 skipped），不算失败：

- **回放测试**（`replay/ReplayScenarioTest`）需要客户端回放服务器：

  ```bash
  cd tools/conformance && go build -o bin/replay.exe ./cmd/replay
  ```

  它用黄金对话扮演服务端，客户端多发、少发或发错一帧都会失败；还能注入断线、丢应答、重复帧。

- **M1 验收测试**（`m1/M1RealServerTest`）需要服务端二进制与一个可以随便写的 PostgreSQL 库：

  ```bash
  cd server && go build -o bin/imkit-server.exe .
  ```

  连接串放在环境变量 `IMKIT_TEST_DSN`（libpq 的 `key=value` 格式，和服务端自己的数据库测试共用）。测试以独立模式（不连宿主）启动服务端，strict、legacy 两档各跑一遍。口令只经环境变量交给服务端进程，不进日志。已知问题：服务端拼连接串时不给值加引号，口令里有空格会连不上。

## M1 验收

计划的验收条件：JVM 集成测试对独立运行的服务端跑通“登录 → 单聊文本 → 确认与幂等 → 历史分页 → 离线后补拉 → 断线重连 → 撤回 → 清空 → 已读 → 在线状态”；C1–C5、C9 各有用例；`core` 模块不依赖 Compose 和 Room。

- `M1RealServerTest` 的 `strictProfile`、`legacyProfile` 覆盖上面整条流程，另加群聊往返与本人角色、多页补拉。两档都对真实服务端通过（连续 3 次）。
- C1–C5、C9 的用例见下一节。
- `imkit-core` 是纯 JVM 模块，依赖只有协程与 OkHttp。

## 红线用例

用例名带红线编号，`tools/conformance` 下的 `go run ./cmd/redlines check` 会扫描各模块 `src/test` 里的 Kotlin 测试，在 `contract/redlines/matrix.md` 里标“已写”。按红线命名却没登记在对照表里的测试会被报出来。

| 红线 | 用例 | 位置 | 方式 |
| --- | --- | --- | --- |
| R7 清空对离线成员生效 | `R7_ClearOfflineReplay` | core `ReplayScenarioTest` | 回放 `core/clear-offline`：离线一方上线收到 `topic_cleared`，清本地缓存并回 ack，再登录不重复处理 |
| R19 只对旧服务端的登录与在线快照按 action 关联 | `R19_CorrelateLegacyRepliesByAction` | core `ReplayScenarioTest` | 回放 `core/login`：两种应答不带 id 也能关联，带 id 的第二次登录应答按 id 交给发它的请求 |
| C1 重连 | `C1_ReconnectAfterInjectedDisconnect`、`C1_IgnoresDuplicatedFrames` | core `ReplayScenarioTest` | 回放 `core/p2p`，注入断线、每帧重复 |
| C2 发送 | `C2_LostPublishReplyStaysSending` | core `ReplayScenarioTest` | 回放 `core/group-messaging`，丢掉发送应答：保持“发送中”、不自动重发，重试沿用同一个 `clientMsgId` |
| | `C2_MediaReconciledByUrl` | core `ChatEngineTest` | 媒体消息的应答丢失后，按 `fileUrl` 与历史里的那条对账 |
| C3 补拉 | `C3_ResumeFromContiguousEnd` | core `ReplayScenarioTest` | 回放 `core/resubscribe`：从本地连续段末尾补拉 |
| C4 缓存归属 | `C4_SwitchingAccountClearsAllCaches` | storage-room `RoomEngineTest` | 真实 Room 库：换账号清空全部表（含密钥），同账号保留 |
| C5 在线三态 | `C5_PresenceTriStateWithExpiry` | core `SyncAndPresenceTest` | 在线、离线、未知，过期回到未知 |
| C6 通知 | `C6_NotificationDecision` | android `NotificationControllerTest` | 决策表、静音渠道由 App 播声、来电通知在每条结束路径上取消 |
| C7 媒体缓存 | `C7_MediaCacheHitsSecondPlay` | android `MediaCacheTest` | 第二次播放走磁盘缓存；另有用例说明服务端必须带 `Content-Length`，否则不缓存 |
| C9 群角色 | `C9_RoleFromRoster`、`C9_UnknownRosterMeansMember` | core `ReplayScenarioTest` | 回放 `core/group-guards`：角色取自名册，`grp_sync` 后重新拉；拉不到按普通成员 |

对照表里 Android 还剩 7 个用例没写：C6、C8 的真机用例与 C7、C8 的界面用例属于 Phase 4，R17、C10 属于 E2EE 模块（7.1）。

## 从源项目迁来的单测（3.10）

源项目 `app/src/test/.../chat` 下约 1.5k 行聊天单测，逐个文件看过。适用于无 UI 核心 SDK 的部分如下；行为已由现有用例覆盖的不重复搬，没覆盖到的移植成新用例（core `PortedChatBehaviorsTest`，以及 `ImClientTest.aLateReplyIsNotHandedToTheNextRequest`）。

| 源测试 | 适用部分 | 在这里 |
| --- | --- | --- |
| `ImResponseCorrelationTest` | 按 id 精确关联；超时后迟到的应答不改投；不同 action 不串（get 与 get_members、del 与 del_topic） | `ImClientTest.repliesAreMatchedByIdAndActionOnly`、`aLateReplyIsNotHandedToTheNextRequest`（新移植）。源项目“无 id 时按 action 兜底”的做法按 3.4 改为只对旧服务端的登录与在线快照兜底，见 `legacyPresenceSnapshotWithoutIdIsMatched`、`strictPresenceSnapshotNeedsItsId` |
| `ChatRepositoryTest` | 乐观消息、应答与服务端回声合成一条；乱序与重复的消息不丢不重、按 seq 排；未读在打开会话后清零；已读回执只推进对方位置；补拉合并重复、本地待发排在服务端消息之后 | `PortedChatBehaviorsTest.ownEchoBeforeTheAckIsOneMessage`、`aBurstOutOfOrderIsKeptOnceInSeqOrder`、`readNotesMoveTheRightCursor`（新移植）；`ChatEngineTest.sendsAndReceives`；`ChatStoreContract.latestIsTheNewestStoredThenEveryPendingInTimelineOrder` |
| `ChatRepositoryLogicTest` | 输入中提示的设置与过期；未读不计自己与系统消息 | `PortedChatBehaviorsTest.typingExpiresAndMineIsNotShown`（新移植）；`ChatStoreContract.unreadCountsOthersNonSystemMessagesAfterTheReadPosition` |
| `ChatIntegrationTest` | 按 seq 去重与排序、待发排在后、状态流转、未读、重试退避 | `ChatStoreContract.putReplacesByKey`、`latestIs…`、`unreadCounts…`；`ChatEngineTest` 的 C2 用例；`ImClientTest.c1_backoffIsJitteredAndCapped` |

不适用、未迁移的：

- `ChatUtilitiesTest`、`ChatUITest`：时间格式化、消息气泡分组、文件图标、输入框、草稿界面状态，属于 Phase 4 的 UI。其中“占位文字不当类型用”（C7）在 4.3 做。
- `ConversationPeerNameTest`、`NotificationSenderNameTest`、`AvatarProfileTest`、`ChatApiModelsTest`，以及 `ChatIntegrationTest` 里的联系人部分：备注、昵称回退、头像、自定义聊天号、联系人解析都归宿主（框架不管人的资料，只给 uid）。
- 源项目自己的实现细节（`keyAction`、`ConversationLoader`、`conversation tick`）随实现一起换掉了。
- 源项目 `searchMessages` 的本地搜索：核心 SDK 暂未提供。

## 已知缺口

- **没有任何真机验证。** AndroidKeyStore（Robolectric 没有这个提供者，测试用软件密钥代替）、通知实际响铃与来电时序、FCM 实际收发、Room 在真机上的表现，都只验证到 JVM 层。
- 3.9 只做了媒体缓存与图片压缩，**视频压缩与缩略图未做**。
- 旧库导入只用按源项目 v18 schema 构造的库测过，没有拿真实用户的旧库验证。
- 本地消息搜索未做（见上）。
