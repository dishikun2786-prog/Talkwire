# 5 分钟接入 ImKit（Android）

接入方只写三样东西：**令牌**、**联系人与资料**、**推送配置**。聊天界面、消息收发、已读、群管理、通知与横幅都由 ImKit 提供。完整的可运行例子是 `sample-app`（下文的代码都摘自那里）。

## 1. 依赖

```kotlin
dependencies {
    implementation(project(":imkit-ui"))           // 界面；连带 imkit-android、imkit-core
    implementation(project(":imkit-storage-room")) // 本地消息库
    // implementation(project(":imkit-push-fcm"))  // 用 FCM 推送时才加
}
```

## 2. 令牌

IM 服务端只认宿主签发的访问令牌（HS256，`sub` 为宿主用户 id，`type: "access"`，`role: "app_user"`，密钥与服务端的 `JWT_SECRET` 相同）。App 用自己的登录拿到它，交给 ImKit：

```kotlin
val tokens = TokenProvider { forceRefresh -> myAuth.accessToken(forceRefresh) }
```

令牌失效时 ImKit 会带 `forceRefresh = true` 再要一次；返回 `null` 表示未登录。

## 3. 联系人与资料：实现 `ImUiHost`

框架只认 uid，人的名字、头像、谁是好友都归宿主：

```kotlin
class MyHost(override val media: MediaSender) : ImUiHost {
    override val contacts = MutableStateFlow<List<ImProfile>>(emptyList())
    override suspend fun refreshContacts() { contacts.value = api.contacts() }        // 发起单聊、建群时用
    override suspend fun profiles(uids: Collection<String>) = api.profiles(uids)       // 名字与头像，批量
    override fun resolveMediaUrl(url: String?) = url?.let { baseUrl.toHttpUrl().resolve(it).toString() }
}
```

用 Node 宿主 SDK（`@imkit/host-node`）时，`/api/v1/chat/contacts` 与 `/api/v1/chat/profiles` 已经现成，`sample-app` 的 `SampleHost` 就是这样接的。可选：`attachmentActions`（附件面板里加自己的入口）、`topBarActions`、`onOpenProfile`（默认什么都不做：群成员之间不能互看资料），以及给可选模块用的两个界面槽位 `topBarExtras`（会话页头）、`groupInfoExtras`（群信息页，带本人角色），见第 7 节。

## 4. 装起来

```kotlin
val client = ImClient(ImClientConfig(endpoints = listOf("wss://im.example.com/ws/im"), tokenProvider = tokens, deviceName = "android-$installId"), scope)
val engine = ChatEngine(client, RoomChatStore.open(context, keys = AndroidKeystoreKeyWrapper()), myUserId, scope, EngineConfig())
val media = MediaSender(context, engine, MediaUploader(hostBaseUrl, tokens))
scope.launch { engine.start() }
```

界面：

```kotlin
setContent {
    ImTheme(locale = appLocale /* null 跟随系统 */) {
        val state = rememberImChatState(engine, MyHost(media))
        ImChatApp(state)
    }
}
```

`ImTheme` 的颜色、尺寸、语言都能换（`ImColors.light(accent = …)`）；文案自带英文、简体中文、越南文。

## 5. 推送与通知

```kotlin
val notifier = ImNotifier(context, ImNotifier.Channels("Messages", "Calls", R.drawable.ic_notification))
val banners = ImBannerState()
val notifications = NotificationController(engine, notifier, SoundPoolAlertPlayer(context, R.raw.ding),
    { m, group -> MessageNotification(m.conversation, group ?: name(m.from), types.preview(m, context), openIntent(m.conversation)) },
    banners.sink, scope)
notifier.createChannels(); notifications.start()
```

- 把 `state.openConversation` 同步到 `notifications.openConversation`，Activity 的 `onStart/onStop` 设 `appInForeground`：正在看的会话不弹通知，App 在前台时弹横幅（`ImBannerHost`）。
- 渠道是静音的，提示音由 App 播（红线 C6）。
- 通知点开用链接：`ImDeepLink.Conversation(id).uri`（`imkit://chat/…`），在 Activity 里 `state.open(ImDeepLink.parse(intent.data))`。
- 离线推送用 FCM 时加 `imkit-push-fcm`，继承 `ImkitMessagingService`；sample-app 没配 Firebase 项目，所以没有离线推送。

## 6. 自己的消息类型

业务卡片（红包、转账、订单）不进 SDK，注册一个 `MessageType` 就行：

```kotlin
val types = MessageTypeRegistry.Builder().register(RedPacketType(store)).build()
val state = rememberImChatState(engine, host, types)
```

类型按 `contentType`（和 `extra`）认，不看 `content` 的文字；`content` 是给不认识这个类型的客户端看的兜底文字。完整例子见 `examples/red-packet/android`。

## 7. 可选模块：通话与扫码入群

可选模块的界面经 `ImUiHost` 的两个槽位接进聊天界面，SDK 本身不依赖这些模块。`sample-app` 两个都接了：

```kotlin
val calls = CallManager(client, engine.me, MyRtcEngine(), scope)   // 媒体走自己的 RTC；NoMediaEngine 只有信令
val groupJoin = GroupJoinClient(client)

class MyHost(…, private val calls: CallManager, private val groupJoin: GroupJoinClient) : ImUiHost {
    // 单聊页头的语音、视频按钮。按下时先要麦克风（视频再要摄像头）权限
    override val topBarExtras: (@Composable RowScope.(ConversationId) -> Unit) = { id ->
        if (!id.isGroup) CallButtons(calls, peer = id.id, onRefused = { reason -> toast(reason) })
    }
    // 群信息页：二维码人人可见，换码与审批只给群主和管理员，审核开关只有群主能改
    override val groupInfoExtras: (@Composable (String, GroupRole) -> Unit) = { topic, role ->
        Column {
            GroupQrCard(groupJoin, topic, canRefresh = role.canManage, linkFor = { token -> "https://app.example.com/join/$token" })
            JoinApprovalSetting(groupJoin, topic, editable = role == GroupRole.OWNER)
            if (role.canManage) JoinRequestsPanel(groupJoin, engine, topic)
        }
    }
}
```

- 通话：`calls.start()` 之后再起 `CallSystem(context, calls, notifications, scope, incomingNotice, ongoingNotice)`，它负责来电全屏通知、来电铃声与回铃音、通话中的前台服务（铃声默认用系统的，`ringer = null` 静音；要振动，清单里加 `VIBRATE`）；通话界面 `CallScreen(calls)` 盖在聊天界面之上。来电要在锁屏上亮屏显示，响铃和通话期间对 Activity 调 `setShowWhenLocked(true)`、`setTurnScreenOn(true)`，清单里加 `USE_FULL_SCREEN_INTENT`。
- 扫码入群：二维码里放什么由 App 决定（`linkFor`），扫码与解析也是 App 的事；拿到令牌后显示 `JoinGroupScreen(groupJoin, engine, token, onOpen = …)`。`groupJoin.events` 是审批结果等推送，用 `groupJoinNotice(context, event, nameOf)` 得到一句可显示的话。`sample-app` 的二维码里放的是 Web 示例的入群页链接，同一个码浏览器和 App 都能打开；App 内可以粘贴链接，或打开 `imkitsample://join/<令牌>`。

## 在本机跑 sample-app

需要：构建好的服务端（`server/bin/imkit-server.exe`）、一个空的 PostgreSQL 库（服务端启动时自己建表）、Node 20+、一台打开了 USB 调试的手机。

两个进程必须用**同一个** `JWT_SECRET`：示例宿主用它签发登录令牌，IM 服务端用它验证。

```bash
# 1. 示例宿主（开发登录、联系人、媒体、红包），监听 127.0.0.1:6001
cd examples/sample-host
npm install
JWT_SECRET=<至少 32 个字符> npm start
```

```bash
# 2. IM 服务端（另开一个终端），监听 6062。宿主默认就是 http 模式、指向 127.0.0.1:6001
JWT_SECRET=<同上> DB_HOST=127.0.0.1 DB_PORT=5432 DB_USER=<用户> DB_PASSWORD=<密码> DB_NAME=<库名> server/bin/imkit-server.exe
```

```bash
# 3. 手机经 USB 访问电脑上的两个端口
adb reverse tcp:6001 tcp:6001
adb reverse tcp:6062 tcp:6062
```

```bash
# 4. 装上 sample-app（包名 io.imkit.sample，不影响手机上的其他 App）
cd android
./gradlew :sample-app:installDebug
```

Windows PowerShell 里环境变量写成 `$env:JWT_SECRET="…"; npm start`。环境变量的完整列表见 `server/README.md`。

**两个用户**：用两台手机各登录一个名字；只有一台手机时，先登录一个名字，点左下角「退出」，再用另一个名字登录（切换账号时本地缓存会整个清掉，不会串号）。后登录的人自动和之前登录过的人互为好友：会话列表右上角「＋ → 发起聊天」就能看到对方。

`/dev/login` 凭名字就能登录，只监听 127.0.0.1，只能在本机开发用。

不用手机也能把同一套东西跑一遍：`imkit-core` 的 `SampleStackTest` 会自己起示例宿主和 IM 服务端，用两个用户走一遍登录、单聊、语音、建群和红包（需要 `IMKIT_TEST_DSN` 指向一个测试库）。
