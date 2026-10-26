---
title: "Web SDK"
description: "浏览器端的连接、同步、本地存储、多标签共用连接与通知：@talkwire/protocol、@talkwire/client、@talkwire/storage-idb。"
---

# Web SDK

Web SDK 在浏览器里完成登录、收发、离线补拉、已读与多标签共用连接，行为与 Android SDK 一致，依据是协议规范与红线（`contract/protocol/spec/protocol-v1.md`、`contract/redlines/`）。只要逻辑、自己画界面时用本页的三个包；要现成的聊天界面再加 [React 界面套件](/client/react)。许可证 Apache-2.0。

## 包

| 包 | 内容 |
| --- | --- |
| `@talkwire/protocol` | 由 `contract/protocol/schema` 生成的帧类型（`src/generated/`，不要手改）；uid 与会话编号的计算（`uidOf`、`p2pTopic`、`conversationOf`、`isGroupTopic`、`isP2pTopic`）；帧的解析与编码（`parseServerFrame`、`encodeClientFrame`，单帧上限 `MAX_FRAME_BYTES` 为 256 KiB） |
| `@talkwire/client` | `ImClient`（连接、登录、请求与推送）、`ChatEngine`（同步、本地会话与消息、发送与重发、已读、在线、群）、多标签共用连接、媒体上传与录音、通知决策与 Web Push、内存版存储 `MemoryChatStore` |
| `@talkwire/storage-idb` | `IdbChatStore`：IndexedDB 存储，可选加密，见[本地存储与加密](/client/storage) |

包名 `@talkwire/*` 是占位符，在本仓库里是 `web/packages/` 下的工作区包。

```bash
npm install @talkwire/client @talkwire/storage-idb @talkwire/react   # 只要逻辑、自己画界面时不装 @talkwire/react
```

在仓库里开发时，`web/` 下的 `npm run gen` 由 JSON Schema 重新生成帧类型，`npm run gen:check` 只检查是否最新。

## 装起来

接入方只写三样东西：令牌、联系人与资料、通知。下面几行摘自 `web/QUICKSTART.md`，顺序有讲究：先 `engine.open()`，再 `client.start()`。

```ts
const tabs = createTabTransport({ name: userId, sharedWorker: () => new TalkwireWorker({ name: "talkwire:" + userId }) });
const client = new ImClient({ endpoints: ["wss://im.example.com/ws/im"], tokenProvider, transport: tabs.transport, deviceName });
const store = await IdbChatStore.open({ name: "talkwire", encrypt: true });
const engine = new ChatEngine(client, store, { appUserId: userId });
await engine.open();   // 换了账号时先清掉上一个账号的本地数据
client.start();
```

- `engine.open()` 认领存储：存储属于另一个账号或从未认领时整个清空（红线 C4），再载入会话列表并开始监听连接。
- `client.start()` 立即返回，状态变为 `connecting`；登录成功后引擎自动做一次完整同步。
- 退出登录：`engine.close()`、`tabs.close()`，再清掉自己的会话。示例在此之外还调了 `client.stop()` 和 `store.close()`。

完整的可运行例子见文末的[示例代码](#示例代码)。

## ImClient

`ImClient` 管一条连接，协议里的每个动作对应一个方法，请求只带规范列出的字段。大多数应用只通过 `ChatEngine` 用它，自己处理推送（例如[自定义卡片的状态更新](/client/message-types)）时才直接订阅 `client.events`。

### 令牌

IM 服务端只认宿主签发的访问令牌，签发方法见[签发 IM 令牌](/host/token)。页面用自己的登录拿到它，交给 `tokenProvider`：

```ts
const tokenProvider = (forceRefresh?: boolean) => myAuth.accessToken(forceRefresh); // 可以返回 Promise；返回 null 表示未登录
```

令牌被拒时客户端会带 `forceRefresh = true` 再要一次。连续被拒 `maxAuthFailures` 次后停下，原因是 `auth_rejected`；`tokenProvider` 返回 `null` 时停下，原因是 `no_credentials`。

### 配置

| 选项 | 默认 | 说明 |
| --- | --- | --- |
| `endpoints` | 必填 | IM 服务端的 WebSocket 地址（`…/ws/im`），按顺序尝试；上一次连通的地址排到最前。为空时构造函数抛错 |
| `tokenProvider` | 必填 | 见上一节 |
| `deviceName` | 无 | 本浏览器的设备名，用于同一账号多设备同时在线（协议第 6.2 节）：1 到 64 个 `A–Z a–z 0–9 . _ : -`，同一浏览器每次登录都一样。格式不对时构造函数抛错；不支持多设备的服务端忽略它 |
| `transport` | `WebSocketTransport` | 底层管道。多标签共用连接时传 `tabs.transport` |
| `requestTimeoutMs` | 10000 | 普通请求的应答超时 |
| `publishTimeoutMs` | 15000 | 发送消息的应答超时 |
| `loginTimeoutMs` | 15000 | 等登录应答的时间 |
| `reconnectBaseDelayMs` | 1000 | 重连的起始间隔 |
| `reconnectMaxDelayMs` | 30000 | 重连间隔上限 |
| `maxAuthFailures` | 3 | 令牌连续被拒多少次后放弃 |
| `idPrefix` | 随机 | 请求 id 前缀；共用一条连接的标签页之间必须不同 |
| `watchNetwork` | 有 `window` 时为 `true` | 浏览器报告网络恢复（`online` 事件）时立即重连 |

示例把 `deviceName` 存在 localStorage 里（`"web-"` 加一段随机串），所以同一账号的手机和网页能同时在线。

### 连接状态

`client.state` 是一个信号（`value` 加 `subscribe`），取值如下：

| `kind` | 含义 |
| --- | --- |
| `idle` | 还没有 `start()` |
| `connecting` | 正在连 `endpoint`，`round` 是第几轮 |
| `connected` | 服务端已接受登录；带 `session`（`uid`、`protocol`、`capabilities`、`endpoint`，服务端有 Web Push 密钥时还有 `vapidKey`）。旧服务端的 `protocol` 为 0 |
| `waiting` | 等待下一次重连，带 `round`、`delayMs`、`lastError` |
| `stopped` | 不会自己重连，带 `reason`；再调 `start()` 才会重新开始 |

只有登录应答到达才算 `connected`（红线 C1）。断线后按指数退避重连，每轮间隔在基础值上浮动 ±20%，避免一起断线的客户端一起回来；一条连接活过 30 秒后退避重新计数。

| `reason` | 何时 |
| --- | --- |
| `stopped` | 调了 `client.stop()` |
| `replaced` | 同一设备的另一次登录顶替了这个会话（协议第 6.2 节）。重连只会把会话抢回来，所以永久停下 |
| `auth_rejected` | 令牌连续被拒 `maxAuthFailures` 次 |
| `no_credentials` | `tokenProvider` 返回 `null` |
| `too_many_devices` | 登录被拒，账号在线的设备已满（409） |
| `invalid_device` | 服务端不接受 `deviceName` |

其他方法：`start()`、`stop()`、`onNetworkAvailable()`（跳过当前的重连等待）、`awaitConnected(timeoutMs)`（连上时返回会话，停下或超时返回 `null`）、`session`、`isConnected`。

### 推送

`client.events.on(listener)` 收到连接变化与服务端推送，返回取消函数：

| `kind` | 内容 |
| --- | --- |
| `connected`、`disconnected` | 登录成功；连接断开（`willReconnect` 表示是否会重连） |
| `message` | 一条新消息 |
| `recalled` | 某条消息被撤回 |
| `topicCleared` | 会话被清空；离线期间的清空带 `pendingId` |
| `presence`、`typing`、`read` | 在线状态、输入中、已读 |
| `groupSync`、`groupRemoved` | 群有变化；我不在群里了 |
| `contactSync` | 宿主改了好友关系 |
| `custom` | 宿主自定义卡片的状态更新（`custom_update`），带 `what`、`extra`，按会话推送时带 `topic` |
| `module` | 可选模块的帧（通话、端到端加密、扫码入群、群助手）或不认识的推送 |

`ChatEngine` 已经处理了其中与聊天有关的部分，包括离线清空与离线群事件的回执；`custom` 与 `module` 它不处理，留给应用和可选模块。

## ChatEngine

`ChatEngine` 是本机的会话与消息视图，按协议第 8 节的同步算法与服务端保持一致，并且是存储的唯一写入者。

```ts
new ChatEngine(client, store, { appUserId: userId })
```

`appUserId` 是宿主的用户 id，引擎用它算出本人的 uid 并以此认领存储；也可以直接给 `uid`。两者都不给时构造函数抛错。

| `EngineConfig` | 默认 | 说明 |
| --- | --- | --- |
| `pageSize` | 50 | 拉历史的每页条数 |
| `offlinePullMax` | 500 | 一次补拉每个会话最多拉多少条 |
| `autoSync` | `true` | 每次登录后做完整同步 |
| `maxAutoRetries` | 5 | 连接断掉的消息最多自动重发几次 |
| `staleSendingMs` | 300000 | “发送中”超过这么久且没有请求在途，判为失败 |
| `typingTtlMs` | 10000 | “正在输入”持续多久 |
| `aiTypingTtlMs` | 15000 | 群助手“正在输入”在没等到回复时持续多久 |
| `presenceTtlMs` | 180000 | 在线状态的有效期，过期回到“未知” |
| `textContentType` | `"text"` | 纯文本的 `contentType` |
| `windowSize` | 50 | 打开的会话在内存里保留多少条，更早的用 `loadOlder` 翻 |
| `clock` | `Date.now` | 时钟 |

### 会话

会话编号（`ConversationId`）在单聊时是对方的 uid，在群里是 `grp_…` topic（协议第 5.5 节）。规范的 `p2p_…` 只出现在历史应答里，存储前换回对方的 uid。

- `engine.conversations`：会话列表的信号，每项有 `title`、`lastMessage`、`unread`、`draft`、`pinned`、`muted`、`left`（已不在群里）、`lastReadSeq`、`peerReadSeq`、`isGroup`。
- `engine.conversation(id)`：取一个会话的摘要。
- `engine.ensureConversation(uid, name)`：协议不列出单聊，单聊按宿主的联系人建立。拉到联系人后对每个调一次；对方加你为好友时客户端收到 `contactSync` 事件，再拉一次。
- `engine.createGroup(name, members)`：建群，返回群的会话编号，建好立即按群名列出。服务端拒绝时抛 `RequestRefused`。
- `engine.deleteConversation(id)`：只删本机的这个会话和它的全部数据。
- `setDraft`、`setPinned`、`setMuted`：草稿、置顶、免打扰，只存在本机。
- `engine.messages(id)`：按时间线排好的消息信号，先从存储读最近 `windowSize` 条；`engine.loadOlder(id, count)` 先从存储、再从服务端翻更早的，返回新增条数，0 表示到头。

### 发送

`engine.send(conversation, outgoing)` 先把消息以“发送中”存进本地并显示，连着时立即发出，没连着时在下一次同步后发出。它返回这条待发消息。

| `OutgoingMessage` | 说明 |
| --- | --- |
| `content` | 文本；媒体消息里是兜底文字 |
| `contentType` | 不给时是纯文本（`textContentType`） |
| `fileUrl`、`fileName` | 媒体地址；媒体先传到宿主，只有地址经过 IM 连接 |
| `duration` | 音视频时长，秒 |
| `extra` | 任意 JSON 值，原样存储与转发 |

每条消息生成一个 `localId`，发送时作为 `clientMsgId`，重发时不变；服务端按它去重（协议第 9.1 节）。

| 状态 | 含义 |
| --- | --- |
| `sending` | 已发出、还没有应答，或者应答超时但连接还在 |
| `sent` | 服务端已存下，有 `seq` |
| `failed` | 服务端拒绝，或自动重试用完；`failure.code` 为 0 表示没有收到服务端应答 |
| `received` | 别人发的消息 |

- 应答超时而连接还在时，消息保持“发送中”，不在这条连接上自动重发（红线 C2）。
- 连接断了的消息在下一次同步后用同一个 `clientMsgId` 重发，最多 `maxAutoRetries` 次；超过次数或超过 `staleSendingMs` 判为失败，发出 `sendFailed` 事件。
- 本人的消息从历史里拉回来时与待发的那条对账：媒体按 `fileUrl`，文本按内容。
- `engine.retry(message)` 用同一个 `clientMsgId` 再发一次；`engine.discard(message)` 丢掉待发消息，返回它引用的媒体地址，交给宿主删除（`MediaUploader.deleteUnsent`）。

### 同步

`autoSync` 开着时，每次登录后引擎自动执行 `syncAll()`，完成后发出 `synced` 事件（`conversations`、`failed`、`pulled`）。

1. `get_groups`：不在列表里的群标为已离开，发出 `removedFromGroup`。
2. 服务端宣告了 `sync-summary` 时先发一次 `sync_summary`，只对有新消息、被清空或群里他人已读位置变了的会话发 `get`，其余只按摘要推进本人的已读位置；否则每个会话都 `get`。`pulled` 是真正拉了的会话数。
3. 每个会话从本地连续段末尾补拉（红线 C3），一页一页拉到不满一页为止，最多 `offlinePullMax` 条。服务端跳过的 seq 一律记为已撤回（协议第 8.2 节），否则每次重连都会从那里重拉。
4. 查一次单聊对方的在线状态，补发没发出去的已读，再重发待发消息。

群里他人的已读时刻只记在内存里，页面刷新后开着已读回执的群会各多拉一次。算法细节见[同步与补拉](/protocol/sync)。

### 已读回执

- `engine.markRead(id)`：把本人的已读位置推到本地最新的 seq，并告诉服务端；没连着时在下一次同步后补发。
- 本人在另一台设备上的已读会推进本地的已读位置；strict 档服务端还在 `get` 应答里带 `readSeq`，已读位置只前进不后退。
- 单聊：对方读到哪里在会话摘要的 `peerReadSeq` 里。
- 开了已读回执的群：`engine.readCursors(id)` 是 uid 到 seq 的信号。

### 输入中与在线状态

- `engine.sendTyping(id)` 发送“正在输入”，没连着时返回 `false`。
- `engine.typing` 是信号，`engine.typingIn(id)` 返回此刻在这个会话里输入的人；对方发来消息即结束他的输入状态。群助手被提问后以保留 uid `assistant_xiaotaozi` 显示输入中，持续 `aiTypingTtlMs`。
- `engine.presence` 是唯一的在线状态来源（红线 C5）：`state(uid)` 返回 `online`、`offline` 或 `unknown`，记录过了 `presenceTtlMs` 回到 `unknown`；`presRev` 更小的推送是过期的，丢弃。`engine.refreshPresence(uids)` 向服务端查一次。

### 撤回与清空

- `engine.recall(message)`：撤回自己已存下的消息，服务端拒绝时抛 `RequestRefused`。撤回的消息在本地变成墓碑：保留发送人与时间，没有内容，`contentType` 为 `application/vnd.talkwire.recalled`，从不发出，也不计未读。本机没收过的消息被撤回时只记 seq。
- `engine.clear(id)`：为所有成员永久清空会话（协议第 11.2 节）。别人清空时引擎发出 `conversationCleared`；离线期间的清空在登录后补投，引擎应用后自动回执。

### 群

| 方法 | 说明 |
| --- | --- |
| `engine.refreshGroups()` | 拉群列表，顺带找出离开期间被移出的群；失败返回 `null` |
| `engine.members(topic, refresh?)` | 群名册；缺失、过期或 `refresh` 时重拉，拉不到返回 `null` |
| `engine.myRole(topic)` | 本人角色，取自名册；拉不到时按普通成员（红线 C9） |
| `engine.noteGroupChanged(topic)` | 标记名册过期并发出 `groupChanged`，用于服务端不回显给本设备的变更（同意入群申请，F45） |
| `engine.groups` | 最近一次的群列表信号 |

加人、移出、改群名、设置角色、转让、解散、退群在 `client` 上：`invite`、`kick`、`renameGroup`、`setMemberRole`、`transferOwner`、`disbandGroup`、`leaveGroup`。转发用 `client.forwardBroadcast` 与 `client.forwardBundle`，单聊的目标用 `p2p_…` topic，不用对方 uid（F22）。

### 本地搜索

`engine.searchMessages(query, limit = 50, scanPerConversation = 500)` 读本机每个会话最近 500 条，只搜文字消息，不区分大小写，按时间倒序。它经存储读出，所以正文加密存放时也能搜；更早的消息不在搜索范围内。

### 事件

`engine.events.on(listener)` 收到引擎层的事件；协议层的推送仍在 `client.events` 上。

| `kind` | 何时 |
| --- | --- |
| `messageArrived` | 别人的新消息实时到达（同步期间拉到的不算），群消息带 `groupName` |
| `sendFailed` | 发送彻底失败 |
| `removedFromGroup` | 我不在这个群里了 |
| `conversationCleared` | 有人为所有人清空了会话 |
| `recalled` | 一条消息被撤回 |
| `groupChanged` | 群名或名册可能变了 |
| `contactChanged` | 宿主改了好友关系 |
| `synced` | 登录后的同步完成 |
| `wrongAccount` | 服务端登录的用户与引擎所属的不同；客户端已被停下 |

## 多标签共用连接

同一账号的所有标签页共用一条 WebSocket：服务端对同一用户的同一设备只保留一条连接，并告诉旧连接“已在其他设备登录”，各开各的标签页会互相顶替。`createTabTransport` 返回一个传输层，交给 `ImClient` 的 `transport`。

| `TabTransportOptions` | 说明 |
| --- | --- |
| `name` | 同名的标签页共用连接，每个账号一个 |
| `sharedWorker` | 返回一个运行 `serveSharedWorker` 的 SharedWorker；浏览器有 SharedWorker 时优先用它 |
| `transport` | 持有连接的那个标签页里真正使用的传输层 |
| `locks` | Web Locks；默认 `navigator.locks`，传 `null` 强制改用心跳选举 |

返回值有 `transport`、`role`（信号）和 `close()`。`role` 是当前的方式：

| `role` | 含义 |
| --- | --- |
| `worker` | 连接在 SharedWorker 里 |
| `leader` | 本标签页被选出来持有连接 |
| `follower` | 经另一个标签页收发 |
| `solo` | 自己单独连接 |

Worker 文件只有一行，示例里就是这个文件：

<<< @/../web/sample/src/talkwire-worker.ts

::: warning 注意
用 Vite 时 `new SharedWorker(new URL(…), …)` 的选项必须是常量。可以改用 `import TalkwireWorker from "./talkwire-worker.ts?sharedworker"`，再 `new TalkwireWorker({ name: "talkwire:" + userId })`，示例（`web/sample/src/stack.ts`）就是这样做的。
:::

- SharedWorker 起不来时（脚本加载失败，或浏览器拦截了它的网络请求），标签页改用 BroadcastChannel 选出一个标签持有连接，有 Web Locks 时用它，否则用心跳；已有的连接按断线处理并重连。
- 心跳选举可能同时选出两个标签。为此当选的标签先当 1.5 秒候选者，不接管连接；每个标签的连接只认最先应答的那个持有者。代价是心跳选举时首次连接晚 1.5 秒左右。没有 Web Locks 的情况包括非 localhost 的 http 页面和旧浏览器。
- 一个标签发出的消息、撤回、清空与已读，其他标签像第二台设备那样收到。
- 已知限制：旧服务端的在线状态查询应答不带 id，两个标签同时查询时可能拿到对方的结果。

## 媒体

媒体先传到宿主，IM 连接上只走它的地址。默认路径与 Node 宿主 SDK 一致，所以网页发的消息在手机上看起来一样。

```ts
host.media = new MediaSender(engine, new MediaUploader(hostBaseUrl, tokenProvider));
```

- `MediaUploader(hostBaseUrl, tokens, { uploadPath, fetch })`：`upload(file, fileName, mime?)` 以 multipart 字段 `file` 发到 `uploadPath`（默认 `/api/v1/chat/upload`），返回绝对地址；`deleteUnsent(url)` 删除从未发出的上传，宿主拒绝时返回 `false`。
- `MediaSender(engine, uploader, { maxBytes, fallbackText })`：`sendFile(conversation, file)` 与 `sendVoice(conversation, clip)` 上传并发送，结果是 `sent`、`tooLarge` 或 `failed`。`maxBytes` 默认 50 MB，与 Node 宿主 SDK 的默认上限相同。
- 图片带 `extra.imageWidth`、`imageHeight`；视频带 `extra.thumbUrl`、`thumbWidth`、`thumbHeight` 与 `duration`；语音是 `audio/*`，`duration` 以秒计。
- `content` 放一段兜底文字（默认 `[Image]`、`[Video]`、`[Voice]`、`[File]`），给不认识这个类型的客户端看；Talkwire 自己的界面不显示它。
- `VoiceRecorder` 录音：`start()`、`stop(minSeconds = 1)`（太短返回 `null`）、`cancel()`。

## 通知

`BrowserNotifier` 把引擎的 `messageArrived` 事件变成页内横幅或系统通知：

```ts
import { BrowserNotifier } from "@talkwire/client";

const notifier = new BrowserNotifier(engine, {
  openConversation: () => currentConversation,           // 正在看的会话不提醒
  render: (m, c, groupName) => ({ title: groupName ?? c?.title ?? "", body: m.content }),
  onBanner: (m, r) => showMyBanner(r),                    // 页面可见但不在这个会话：页内横幅
  onOpen: (c) => openConversation(c),                     // 点了系统通知
  playSound: () => myDing.play(),                         // 提示音由页面放
});
notifier.start();
void BrowserNotifier.requestPermission();
```

| 情况 | 结果 |
| --- | --- |
| 免打扰的会话、自己的消息、系统消息 | 不提醒 |
| 页面可见，正在看这个会话 | 不提醒 |
| 页面可见，在别处 | `onBanner` 加提示音 |
| 页面隐藏 | 系统通知，同一会话的新通知替换旧的，加提示音 |

`BrowserNotifier.requestPermission()` 要在用户操作里调用；没有通知权限时，页面隐藏期间的消息只放提示音，不弹系统通知。

页面关着时的离线推送用 Web Push：服务端配好 VAPID 密钥（`TALKWIRE_VAPID_KEY_PATH`）后登录应答带 `vapidKey`，页面注册一个 Service Worker，再 `enableWebPush(client, registration)`。它在每次登录后注册推送令牌，返回的函数用于退出登录时注销。Service Worker 里收到推送用 `showPushNotification(registration, payload, { appName, localize, icon })` 显示；端到端加密的消息没有正文，`localize` 把 `locKey` 换成应用自己的文案。推送的服务端一侧见[多端与推送](/protocol/devices-push)。

::: warning 注意
Web Push 只做了订阅与显示，没有接推送服务；只在 Edge（Chromium）上跑过，Firefox、Safari 未测。
:::

## 离线与断线

- 没连着时发送的消息存在本地，状态是“发送中”，下一次同步后用同一个 `clientMsgId` 发出。
- 断线后按退避重连；浏览器报告网络恢复时立即重连。重连登录后引擎从每个会话的本地连续段末尾补拉，不会重复，也不会漏掉中间被撤回的 seq。
- 断线期间“正在输入”清空；在线状态不清空，每条记录按 `presenceTtlMs` 自己过期。
- 没连着时的已读在下一次同步后补发。
- 离线期间的清空与群事件，服务端在登录后补投，引擎应用后回执。
- `custom_update` 不进离线队列，打开卡片时要向宿主拉当前状态，见[自己的消息类型](/client/message-types)。

## 示例代码

`web/sample/src/stack.ts` 是示例登录后建立的全部对象：经多标签传输层的客户端、IndexedDB 上的引擎、宿主适配与媒体。运行方法见[快速开始](/guide/quickstart)。

<<< @/../web/sample/src/stack.ts
