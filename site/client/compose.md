---
title: "Compose 界面套件"
description: "talkwire-ui 提供整套 Compose 聊天界面：主题、导航状态、宿主接口 ImUiHost、通知与横幅、talkwire:// 深链与多语言。"
---

# Compose 界面套件

`talkwire-ui` 是建在 [Android SDK](/client/android) 的 `ChatEngine` 之上的整套 Compose 聊天界面：会话列表（带搜索）、单聊与群聊、通讯录与选人、群信息与成员管理、消息操作、按住说话、表情与附件面板、已读名单、前台横幅。宿主唯一要实现的接口是 `ImUiHost`。

## 挂载

界面放在 `ImTheme` 里，状态由 `rememberImChatState` 创建，`ImChatApp` 画出整套界面。下面摘自 `android/QUICKSTART.md`：

```kotlin
setContent {
    ImTheme(locale = appLocale /* null 跟随系统 */) {
        val state = rememberImChatState(engine, MyHost(media))
        ImChatApp(state)
    }
}
```

`engine` 与 `media` 的建立见 [Android SDK](/client/android#装起来)。依赖 `talkwire-ui` 会连带引入 `talkwire-android` 与 `talkwire-core`。

## ImTheme

`ImTheme` 给套件提供颜色、尺寸与语言，每个组件都从它读取，没有写死的颜色。

| 参数 | 默认 | 说明 |
| --- | --- | --- |
| `darkTheme` | `isSystemInDarkTheme()` | 是否深色 |
| `colors` | 深色时 `ImColors.dark()`，否则 `ImColors.light()` | 颜色 |
| `dimens` | `ImDimens()` | 尺寸与形状 |
| `locale` | `null` | 套件文案的语言；`null` 跟随 App 的 context |
| `content` | 必填 | |

`ImColors.light(accent)` 的默认强调色是 `0xFFD67744`，`ImColors.dark(accent)` 是 `0xFFB8623A`；自己的气泡、已读勾与强调色同色。换品牌色只要传 `accent`，例如 `ImColors.light(accent = …)`；也可以直接构造 `ImColors`，它的颜色项是：

| 颜色项 | 用途 |
| --- | --- |
| `accent`、`onAccent` | 品牌色：自己的气泡、链接、按钮、发送；其上的文字 |
| `background`、`chatBackground` | 页面与顶栏；消息列表背后 |
| `surface`、`inputBackground` | 卡片、弹层；输入框 |
| `bubbleMine`、`bubbleTheirs`、`onBubbleMine`、`onBubbleTheirs` | 气泡与其中的文字 |
| `textPrimary`、`textSecondary`、`textHint` | 文字；次要文字在 `background` 上对比度不低于 4.5:1 |
| `divider` | 分隔线 |
| `online` | 在线圆点，刻意不用品牌色 |
| `read` | 已读勾 |
| `error` | 未读角标、发送失败、危险操作 |
| `mentionOnMine`、`mentionOnTheirs` | 提及 |
| `recording`、`recordingCancel` | 录音浮层的正常色与取消色 |
| `avatarPalette` | 没有头像图片时按名字挑的背景色 |

`ImDimens` 有媒体预览的宽度与高度范围（`mediaWidth`、`mediaMinHeight`、`mediaMaxHeight`、`mediaFallbackHeight`）、列表与聊天里的头像大小（`avatarInList`、`avatarInChat`）以及气泡与媒体的形状。组件里用 `ImTheme.colors`、`ImTheme.dimens` 读取当前值。

## 状态与导航

`rememberImChatState(engine, host, types, config, start)` 创建界面状态。`start` 只用一次；之后返回栈在旋转与进程被杀后都能恢复。

| 参数 | 默认 | 说明 |
| --- | --- | --- |
| `engine` | 必填 | `ChatEngine` |
| `host` | 必填 | `ImUiHost` |
| `types` | `MessageTypeRegistry.Default` | 消息类型，见[自己的消息类型](/client/message-types) |
| `config` | `ImUiConfig()` | 界面的限制与开关 |
| `start` | `ImDeepLink.Conversations` | 起始位置 |

| `ImUiConfig` | 默认 | 说明 |
| --- | --- | --- |
| `messageMaxLength` | 20000 | 一条消息的最大长度 |
| `typingThrottleMs` | 3000 | 发送“正在输入”的最小间隔 |
| `maxAdmins` | 10 | 群管理员上限，按宿主启动时报告的服务端配置传 |
| `groupMembersCanOpenProfiles` | `false` | 群成员能否互看资料 |
| `chatsFromContacts` | `true` | 协议不列出单聊，新设备或换账号后单聊会一直看不到，直到有人发消息。打开时宿主的每个联系人都有一个单聊会话并同步一次，列表只显示有消息的 |

`ImChatState` 管返回栈：`current`、`backStack`、`navigate(destination)`、`replace(destination)`、`back()`、`open(link)`。目的地 `ImDestination` 有 `Conversations`、`Chat(id)`、`Info(id)`、`Contacts`、`PickMembers(topic)`。`openConversation` 是当前打开的会话的 `StateFlow`，交给通知控制器用（见下文）。

`ImChatApp(state, modifier, onExit)` 按返回栈画出当前页面，处理系统返回键；每个目的地有自己的保存状态，所以列表保留滚动位置。`onExit` 不为 `null` 时会话列表带一个返回箭头，用于聊天界面不是根页面的情况。

## ImUiHost

框架只认 uid，人的名字、头像、谁是好友都归宿主。下面摘自 `android/QUICKSTART.md`：

```kotlin
class MyHost(override val media: MediaSender) : ImUiHost {
    override val contacts = MutableStateFlow<List<ImProfile>>(emptyList())
    override suspend fun refreshContacts() { contacts.value = api.contacts() }        // 发起单聊、建群时用
    override suspend fun profiles(uids: Collection<String>) = api.profiles(uids)       // 名字与头像，批量
    override fun resolveMediaUrl(url: String?) = url?.let { baseUrl.toHttpUrl().resolve(it).toString() }
}
```

用 Node 宿主 SDK（`@talkwire/host-node`）时，`/api/v1/chat/contacts` 与 `/api/v1/chat/profiles` 已经现成，`sample-app` 的 `SampleHost` 就是这样接的。

| 成员 | 默认 | 说明 |
| --- | --- | --- |
| `profiles(uids)` | 必须实现 | 返回 uid 到 `ImProfile(uid, name, avatarUrl)` 的映射；缺的人显示缩短的 uid |
| `contacts` | 必须实现 | 联系人 `StateFlow`；没有联系人的 App 给空列表 |
| `refreshContacts()` | 什么都不做 | 重新拉联系人，例如通讯录打开时 |
| `media` | `null` | `MediaSender`；`null` 表示 App 不支持媒体 |
| `resolveMediaUrl(url)` | 原样（空白返回 `null`） | 把存下的媒体地址变成可加载的地址 |
| `attachmentActions(conversation)` | 空 | 附件面板里在照片、拍摄、文件之后加的入口（`AttachmentAction`） |
| `topBarActions(conversation)` | 空 | 会话页顶栏的额外按钮（`TopBarAction`） |
| `topBarExtras` | `null` | 顶栏里需要组合的内容，例如先要权限的通话按钮 |
| `groupInfoExtras` | `null` | 群信息页里成员之后、聊天设置之前的内容，带本人角色 |
| `onOpenProfile(uid)` | 什么都不做 | 点了某人的头像；默认群成员之间不能互看资料 |

`topBarExtras` 与 `groupInfoExtras` 是可选模块接进聊天界面的两个槽位，SDK 本身不依赖这些模块。接法见[音视频通话](/modules/call)与[扫码入群](/modules/group-join)，`sample-app` 两个都接了。

## 通知与横幅

通知由 `talkwire-android` 的 `ImNotifier` 与 `NotificationController` 负责，前台横幅由 `talkwire-ui` 的 `ImBannerState` 与 `ImBannerHost` 负责。下面摘自 `android/QUICKSTART.md`：

```kotlin
val notifier = ImNotifier(context, ImNotifier.Channels("Messages", "Calls", R.drawable.ic_notification))
val banners = ImBannerState()
val notifications = NotificationController(engine, notifier, SoundPoolAlertPlayer(context, R.raw.ding),
    { m, group -> MessageNotification(m.conversation, group ?: name(m.from), types.preview(m, context), openIntent(m.conversation)) },
    banners.sink, scope)
notifier.createChannels(); notifications.start()
```

- 把 `state.openConversation` 同步到 `notifications.openConversation`，Activity 的 `onStart`/`onStop` 设 `appInForeground`：正在看的会话不弹通知，App 在前台时弹横幅（`ImBannerHost`）。
- 渠道是静音的，提示音由 App 播（红线 C6），因为有的厂商系统无论渠道怎么设都不放 App 渠道的声音。
- 通知点开用链接：`ImDeepLink.Conversation(id).uri`，在 Activity 里 `state.open(ImDeepLink.parse(intent.data))`。

`NotificationController` 的决定规则（`NotificationDecider`）：

| 情况 | 结果 |
| --- | --- |
| 宿主设了 `suppressAll`（隐私屏、勿扰） | 不提醒 |
| 免打扰的会话 | 不提醒 |
| App 在前台且正在看这个会话 | 不提醒 |
| App 在前台，在别处 | 横幅 |
| App 在后台 | 系统通知，每个会话一条，新的替换旧的 |

提示音节流：一串消息只响一次，单聊 2 秒内、群 10 秒内不重复。

| 类 | 要点 |
| --- | --- |
| `ImNotifier(context, Channels)` | `Channels(messagesName, callsName, smallIcon)`，渠道 id 默认 `talkwire_messages`、`talkwire_calls`。`createChannels()` 在启动时调一次。Android 13 起要 `POST_NOTIFICATIONS` 权限，没有时发通知的方法返回 `false` |
| `NotificationController` | 属性 `appInForeground`、`openConversation`（设置时同时撤掉该会话的通知）、`suppressAll`；`start()`、`stop()`；`onMessage(message, groupName)` 也可以直接调用，例如处理离线推送 |
| `SoundPoolAlertPlayer(context, sound, vibrateMs = 0)` | 在通知音量流上播放 `res/raw` 里的短音，静音模式下不响；要振动时 App 清单里要有 `VIBRATE` |
| `MessageNotification` | `conversation`、`title`、`text`、`contentIntent`、`largeIcon`；名字由宿主给 |
| `ImBannerState` | `sink` 交给 `NotificationController`，`dismiss()` 收起；Composable 里用 `rememberImBannerState()` |
| `ImBannerHost(banners, chat, onOpen, modifier, visibleMs = 4000)` | 画在 App 内容之上；点一下打开会话，上滑收起 |

离线推送见 [Android SDK](/client/android#推送)。

## 深链

`ImDeepLink` 是进入聊天界面的链接，供通知、横幅和 App 自己的页面使用。scheme 是 `talkwire`。

| 链接 | 对应 | 打开后 |
| --- | --- | --- |
| `talkwire://chats` | `ImDeepLink.Conversations` | 会话列表 |
| `talkwire://chat/{会话编号}` | `ImDeepLink.Conversation(id)` | 列表之上打开该会话 |
| `talkwire://group/{grp_…}` | `ImDeepLink.GroupInfo(topic)` | 列表、该群的聊天，再打开群信息 |

`ImDeepLink.parse(uri)` 对不是 Talkwire 的链接返回 `null`；`group` 只接受 `grp_` 开头的 topic。`sample-app` 在清单里给 Activity 加了 `android:scheme="talkwire"` 的 `VIEW` intent-filter 来接这些链接。

## 语言

套件的文案自带英文、简体中文、越南文（`res/values`、`values-zh`、`values-vi`）。`ImTheme(locale = …)` 让套件说指定的语言，`null` 跟随 App 的 context。

底部弹层与对话框是独立窗口，会从窗口重新取 context，丢掉 `ImTheme` 设的语言（红线 C8）。套件自己的弹层用 `LocalizedSheet`、`LocalizedDialog` 保住语言与主题；App 在聊天界面里加自己的弹层时也应当用它们，而不是直接用 `ModalBottomSheet`。
