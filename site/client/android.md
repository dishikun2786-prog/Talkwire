---
title: "Android SDK"
description: "Android 端的模块划分与依赖方向，ImClient、ChatEngine、媒体与 FCM 推送的用法，以及目前的验证范围。"
---

# Android SDK

Android SDK 分成一个纯 Kotlin 的核心和几个 Android 库：核心 `talkwire-core` 不依赖 Android、Room、Compose，负责连接、同步与发送；存储、通知、推送、界面各在自己的模块里。行为依据与 [Web SDK](/client/web) 相同：协议规范与红线。包名 `io.talkwire` 是占位符，定名后统一替换。

## 模块

| 模块 | 类型 | 内容 |
| --- | --- | --- |
| `talkwire-core` | 纯 Kotlin/JVM 库 | `ImClient`（连接、登录、多线路地址、重连与退避、请求关联）、`ChatEngine`（发送队列与重试、接收排序与空洞检测、撤回墓碑、本地搜索、已读、未读与草稿、群名册与本人角色、清空水位、缓存归属）、`ChatStore` 接口与内存实现、在线三态、通知决策、推送令牌注册、媒体上传、线协议常量与 JSON。依赖只有 kotlinx-coroutines 1.9.0 与 OkHttp 4.12.0 |
| `talkwire-storage-room` | Android 库 | 用 Room 实现的 `ChatStore`，可选按会话加密与加密粉碎；见[本地存储与加密](/client/storage) |
| `talkwire-android` | Android 库 | 通知渲染（静音渠道，提示音由 App 播放，来电走全屏通知）与 `NotificationController`；媒体缓存（默认 300 MB）、图片压缩、`MediaSender`；用 AndroidKeyStore 实现的 `KeyWrapper` |
| `talkwire-push-fcm` | Android 库 | FCM：`FcmTokenSource`、`TalkwireMessagingService`。独立产物，不用 FCM 的 App 不必引入 Firebase |
| `talkwire-ui` | Android 库（Compose） | 整套聊天界面，见 [Compose 界面套件](/client/compose) |

可选模块各自成模块，SDK 本身不依赖它们：`talkwire-group-join`（[扫码入群](/modules/group-join)）、`talkwire-call` 与 RTC 适配器 `talkwire-call-dingrtc`（[音视频通话](/modules/call)）、`talkwire-video-compress`（[视频压缩](/modules/video-compress)）、`talkwire-e2ee`（[端到端加密](/modules/e2ee)，AGPLv3）。

依赖方向从界面指向核心，核心不依赖其他任何模块。下表按各模块 `build.gradle.kts` 里的 `api(project(…))` 列出：

| 模块 | 依赖 |
| --- | --- |
| `talkwire-ui` | `talkwire-android` |
| `talkwire-android` | `talkwire-storage-room`、`talkwire-core` |
| `talkwire-storage-room` | `talkwire-core` |
| `talkwire-push-fcm` | `talkwire-core` |
| `talkwire-core` | 无（只有协程与 OkHttp） |

构建环境：AGP 8.4.2、Kotlin 1.9.24、Gradle 8.7、JDK 17、compileSdk 34，minSdk 26。

## 引入

仓库里没有给出 Maven 坐标，`android/QUICKSTART.md` 以源码模块引用：

```kotlin
dependencies {
    implementation(project(":talkwire-ui"))           // 界面；连带 talkwire-android、talkwire-core
    implementation(project(":talkwire-storage-room")) // 本地消息库
    // implementation(project(":talkwire-push-fcm"))  // 用 FCM 推送时才加
}
```

只要逻辑、自己画界面时依赖 `talkwire-core`（加上需要的存储与通知模块）即可。

## 装起来

下面几行摘自 `android/QUICKSTART.md`，引擎先 `open()` 再启动客户端，`engine.start()` 两步一起做：

```kotlin
val client = ImClient(ImClientConfig(endpoints = listOf("wss://im.example.com/ws/im"), tokenProvider = tokens, deviceName = "android-$installId"), scope)
val engine = ChatEngine(client, RoomChatStore.open(context, keys = AndroidKeystoreKeyWrapper()), myUserId, scope, EngineConfig())
val media = MediaSender(context, engine, MediaUploader(hostBaseUrl, tokens))
scope.launch { engine.start() }
```

`engine.open()` 检查存储属于谁：属于另一个用户或从未认领时整个清空（红线 C4），可以重复调用。它在返回前就订阅了客户端的事件，所以紧接着 `client.start()` 也不会漏掉第一次登录。`engine.close()` 停止响应并取消在途工作，客户端交给调用方处理。

## TokenProvider

IM 服务端只认宿主签发的访问令牌，签发方法见[签发 IM 令牌](/host/token)。App 用自己的登录拿到它，交给 Talkwire：

```kotlin
val tokens = TokenProvider { forceRefresh -> myAuth.accessToken(forceRefresh) }
```

`TokenProvider` 是带一个挂起方法 `accessToken(forceRefresh)` 的函数式接口，每次登录尝试都会调用。令牌失效时 Talkwire 会带 `forceRefresh = true` 再要一次；返回 `null` 表示未登录。

## ImClient

`ImClient(config, parentScope)` 管一条连接。协议动作是 `ImApi.kt` 里的扩展函数（`publish`、`history`、`recall`、`clearConversation`、`createGroup`、`invite`、`kick`、`forwardBroadcast` 等），大多数应用只通过 `ChatEngine` 用它们。

| `ImClientConfig` | 默认 | 说明 |
| --- | --- | --- |
| `endpoints` | 必填 | WebSocket 地址（`wss://…/ws/im`），按顺序尝试；成功过的地址下一轮先试。为空时构造抛错 |
| `tokenProvider` | 必填 | 见上一节 |
| `deviceName` | `null` | 本安装的设备名，用于同一账号多设备同时在线（协议第 6.2 节）：1 到 64 个字母、数字与 `. _ : -`，每次登录都一样。为 `null` 时，同一账号的另一条连接会顶替这一条 |
| `transport` | `OkHttpTransport()` | 底层管道 |
| `loginTimeoutMs` | 10000 | 从打开连接到登录应答的时间 |
| `requestTimeoutMs` | 10000 | 普通请求的应答超时 |
| `publishTimeoutMs` | 20000 | 发送消息的应答超时；超时不算失败（红线 C2） |
| `reconnectBaseDelayMs` | 3000 | 第一次重连的间隔，每轮加倍 |
| `reconnectMaxDelayMs` | 30000 | 重连间隔上限 |
| `maxAuthFailures` | 3 | 令牌连续被拒多少次后放弃 |

`client.state` 是 `StateFlow<ConnectionState>`：

| 状态 | 含义 |
| --- | --- |
| `Idle` | 还没有启动 |
| `Connecting(endpoint, attempt)` | 正在连接 |
| `Connected(session)` | 登录应答已到（红线 C1）；`session` 有 `uid`、`protocol`（旧服务端为 0）、`capabilities`、`endpoint` |
| `WaitingToReconnect(attempt, delayMs, lastError)` | 等待重连，退避带随机抖动 |
| `Stopped(reason)` | 不会自己重连；`ImClient.start()` 重新开始 |

`StopReason` 有 `STOPPED`（调了 `stop()`）、`REPLACED`（同一设备的另一次登录顶替了会话，重连只会把它抢回来）、`AUTH_REJECTED`、`NO_CREDENTIALS`、`TOO_MANY_DEVICES`（账号在线设备已满）、`INVALID_DEVICE`（服务端不接受 `deviceName`）。

`client.events` 是 `SharedFlow<ImEvent>`，按到达顺序发出连接变化与推送：`Connected`、`Disconnected`、`Message`、`Recalled`、`TopicCleared`、`Presence`、`Typing`、`Read`、`GroupSync`、`GroupRemoved`、`ContactSync`、`Custom`（宿主卡片的状态更新）、`Module`（可选模块的帧）。它不限缓冲，收集方在处理函数里发请求不会卡住应答。其他方法：`start()`、`stop()`、`onNetworkAvailable()`（缩短当前的重连等待，但不打断正在进行的握手）、`awaitConnected(timeoutMs)`、`session`。

## ChatEngine

`ChatEngine(client, store, appUserId, parentScope, config)` 是一个已登录用户的聊天状态：时间线、会话列表、发送队列、同步、在线状态、已读位置与群名册。它响应 `client.events`，并且是存储的唯一写入者。`me` 是由 `appUserId` 算出的本人 uid。

| `EngineConfig` | 默认 | 说明 |
| --- | --- | --- |
| `pageSize` | 50 | 拉历史的每页条数 |
| `offlinePullMax` | 500 | 一次同步每个会话最多拉多少条 |
| `autoSync` | `true` | 每次登录后做完整同步（协议第 8 节） |
| `maxAutoRetries` | 5 | 连接断掉的消息最多自动重发几次 |
| `retryBaseDelayMs` | 2000 | 自动重发的起始间隔 |
| `retryMaxDelayMs` | 60000 | 自动重发的间隔上限 |
| `staleSendingMs` | 300000 | “发送中”超过这么久且没有请求在途，判为失败 |
| `typingTtlMs` | 10000 | “正在输入”持续多久 |
| `aiTypingTtlMs` | 15000 | 群助手“正在输入”在没等到回复时持续多久 |
| `presenceTtlMs` | 180000 | 在线状态的有效期 |
| `textContentType` | `"text"` | 纯文本的 `contentType` |
| `windowSize` | 50 | 打开的会话在内存里保留多少条 |
| `clock` | `System::currentTimeMillis` | 时钟 |

方法与 Web 版的 `ChatEngine` 一一对应，签名换成挂起函数与 Flow：

| 用途 | 成员 |
| --- | --- |
| 会话列表、群列表、输入中 | `conversations`、`groups`、`typing`（`StateFlow`）；`typingIn(conversation)` |
| 时间线 | `messages(conversation)` 返回 `StateFlow`，在后台从存储填充，应收集而不是读一次 `value`；`loadOlder(conversation, count)` |
| 发送 | `send(conversation, OutgoingMessage)`、`retry(message)`、`discard(message)` |
| 已读 | `markRead(conversation)`、`readCursors(conversation)` |
| 撤回与清空 | `recall(message)`（服务端拒绝时抛 `ImException.Refused`）、`clear(conversation)`、`deleteConversation(conversation)`（只删本机） |
| 本机设置 | `setDraft`、`setPinned`、`setMuted`、`ensureConversation(conversation, title)` |
| 群 | `refreshGroups()`、`members(topic, refresh)`、`myRole(topic)`、`noteGroupChanged(topic)` |
| 同步与在线 | `syncAll()`、`syncConversation(conversation, lastSeq)`、`presence` |
| 搜索 | `searchMessages(query, limit = 50, scanPerConversation = 500)` |

`ConversationId` 是 `data class ConversationId(val id: String)`：单聊是对方的 uid（`ConversationId.direct(peerUid)`），群是 `grp_…`（`ConversationId.group(topic)`），`isGroup` 区分两者。`OutgoingMessage` 的字段是 `content`、`contentType`（`null` 为纯文本）、`fileUrl`、`fileName`、`duration`（秒）、`extra`。

发送遵守红线 C2：应答超时而连接还在时，消息保持“发送中”，不在这条连接上重发；本人的消息从服务端回来时先按 `clientMsgId`、再按媒体的文件地址、再按文本对账，只在恰好一条候选时才合并。同步遵守红线 C3：从本地连续段末尾补拉，撤回的 seq 与清空水位不算空洞。本人在群里的角色取自名册，`grp_sync` 把名册标为过期，拉不到时按普通成员（红线 C9）。

`events` 是 `SharedFlow<ChatEvent>`：

| 事件 | 何时 |
| --- | --- |
| `MessageArrived(message, groupName)` | 别人的新消息实时到达，同步期间拉到的不算 |
| `SendFailed(message)` | 发送彻底失败：服务端拒绝，或自动重试用完 |
| `RemovedFromGroup(conversation)` | 我不在这个群里了 |
| `ConversationCleared(conversation, by)` | 有人为所有人清空了会话 |
| `GroupChanged(conversation)` | 群名或名册可能变了 |
| `ContactChanged(what, peerUid)` | 宿主改了好友关系 |
| `Synced(conversations, failed, pulled)` | 登录后的同步完成；`pulled` 是真正拉了的会话数 |
| `WrongAccount(expected, actual)` | 服务端登录的用户与引擎所属的不同，客户端已被停下 |

## 媒体

媒体先传到宿主，IM 连接上只走它的地址。默认路径与 Node 宿主 SDK 一致：`POST /api/v1/chat/upload`（multipart 字段 `file`），以及删除从未发出的上传用的 `DELETE /api/v1/chat/upload/:name`。

- `MediaUploader(hostBaseUrl, tokens, http, uploadPath)` 在 `talkwire-core` 里：`upload(file, fileName, mime)` 返回 `UploadedMedia`（`url`、`name`、`mime`、`size`），失败抛 `MediaUploadException`。
- `MediaSender` 在 `talkwire-android` 里，负责图片压缩、视频首帧、文件与语音上传，再交给引擎发送：

| `MediaSender` 参数 | 默认 | 说明 |
| --- | --- | --- |
| `context`、`engine`、`uploader` | 必填 | |
| `maxBytes` | 50 MB | 超过的文件在读取前就拒绝 |
| `fallbackText` | `defaultFallback` | 每种媒体放进 `content` 的兜底文字 |
| `videoCompressor` | `null` | 交给 `talkwire-video-compress` 的 `Media3VideoCompressor` 时，大视频先重新编码 |
| `compressVideosAboveBytes` | 50 MB | 超过多大的视频才重新编码 |

`sendPicked(conversation, uri)` 发用户选的照片、视频或文件，`sendVoice(conversation, file, seconds)` 发语音；结果是 `Result.Sent`、`Result.TooLarge` 或 `Result.Failed`。语音与视频的播放走 `MediaCache`（默认 300 MB 磁盘缓存，第二次播放读磁盘），前提是媒体服务器返回 `Content-Length`。

## 推送

离线推送用 FCM 时加 `talkwire-push-fcm`。它与 `talkwire-core` 里的推送令牌注册配合：

- `PushRegistration(client, source, scope)`：`start()` 后每次登录都注册一次令牌（协议第 17 节），不只在令牌变化时，因为那时连接通常不在；`unregister()` 在退出登录前、还连着时调用。
- `FcmTokenSource()`：令牌来源，`provider` 为 `"fcm"`。没有 Play 服务或没配 Firebase 时返回 `null`，设备只是收不到离线推送。
- `TalkwireMessagingService`：抽象基类，继承它并在清单里声明 `com.google.firebase.MESSAGING_EVENT` 的 intent-filter。服务端发的是只含数据的推送，后台时也会到 `onMessageReceived`；Talkwire 的推送解析成 `PushNotice` 交给 `onImPush(notice)`，其他推送交给 `onOtherMessage`。令牌变化时回调 `onTokenChanged`，连着时可以调 `PushRegistration.registerNow()`。

`PushNotice.kind` 是 `MESSAGE`、`E2E_MESSAGE`（加密消息，没有预览）、`GROUP_JOIN` 或 `UNKNOWN`。收到推送时 App 通常没有连接：发一条通知，消息本身留给下一次连接的同步去拉。通知怎么渲染见 [Compose 界面套件](/client/compose#通知与横幅)，推送的协议一侧见[多端与推送](/protocol/devices-push)。

::: warning 注意
`sample-app` 没有配 Firebase 项目，所以没有离线推送；FCM 的实际收发没有在真机上验证过。
:::

## 验证范围

单测与集成测试的组织见[测试与红线](/contributing/testing)。真机上的情况如下：

- 只在一台小米（Android 15）上跑过：2026-10-25 跑通登录、单聊、输入中与在线、双向已读、前台横幅与提示音、后台通知与点开、按住说话与上滑取消、建群、红包收发与实时更新、撤回、换账号；2026-10-26 与网页示例互通了文字、引用回复、双向撤回、本地搜索、双向语音通话（信令与铃声）、扫码入群（含审批）。逐项记录在 `android/DEVICE_CHECKS.md`。
- 还没在真机上验证的：相册、拍摄、文件三个附件入口，来电时序，FCM 实际收发，多设备同账号。
- 视频压缩只在 JVM 上测了发送逻辑；旋转视频编码后的方向没有拿真机拍的视频验证。
- 按摘要同步（`sync_summary`）与新设备上单聊的已读位置只在测试里验证过，没上真机。
- 本地搜索在真机上只用英文关键词试过。保存图片与视频到相册需要 Android 10（API 29）以上。
