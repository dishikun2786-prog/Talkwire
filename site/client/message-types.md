---
title: "自己的消息类型"
description: "红包、转账、订单这类业务卡片不进 SDK：发一条自定义 contentType 的普通消息，在客户端注册一个消息类型，状态由宿主经 custom_update 推送。"
---

# 自己的消息类型

业务卡片（红包、转账、订单）不进 SDK：它是一条带自定义 `contentType` 的普通消息，客户端注册一个消息类型来画它，卡片状态由宿主推送。业务的一切（谁能领、每人多少、钱）都归宿主，Talkwire 只负责两件事：发出那条显示卡片的消息，以及把卡片的新状态实时推给在线的成员。

完整的例子是 `examples/red-packet`：服务端一侧在 `src/red-packets.ts`，Android 一侧在 `android/`，`sample-app` 接了它。Web 示例没有接红包卡片，Web 一侧按同样的接口自己写。

## 三个部分

| 部分 | 谁做 | 怎么做 |
| --- | --- | --- |
| 卡片消息 | 发送者的客户端 | 用 `engine.send` 发一条普通消息：`contentType` 是宿主自定的类型，`extra` 带业务 id，`content` 放兜底文字 |
| 卡片的样子 | 每个客户端 | 注册一个 `MessageType`，按 `contentType`（和 `extra`）认出这条消息并画出来 |
| 卡片的状态 | 宿主 | 业务变化后调服务端的 `custom-broadcast`，在线客户端收到 `custom_update`；打开卡片时向宿主拉当前状态 |

服务端不解释 `contentType`，`extra` 是任意 JSON 值，两者都原样存储与转发（协议第 9.1 节）。不认识这个类型的客户端照常显示 `content` 的文字，所以兜底文字要能独立看懂。

## 发卡片消息

以红包为例，发送者先调宿主建红包，宿主核对他是群成员后返回红包 id 和下一步要发的消息。客户端在群里发出：

```json
{"action": "pub", "topic": "grp_…", "contentType": "red_packet", "content": "[Red packet]", "extra": {"redPacketId": "…"}}
```

在客户端这就是一次普通发送：把宿主返回的 `content`、`contentType`、`extra` 交给 `engine.send`（Web 的 `OutgoingMessage` 与 Android 的 `OutgoingMessage` 字段相同）。示例的 `RedPacketSender` 把这个入口放进附件面板，经 `ImUiHost.attachmentActions` 接入，只对群显示。

## 注册消息类型

类型按 `contentType`（和 `extra`）认，从不看 `content` 的文字（红线 C7）。注册的类型排在内置类型之前，所以也可以接管一个内置的 `contentType`。

::: code-group

```ts [Web]
new MessageTypeRegistry.Builder().register(type).build()
```

```kotlin [Android]
val types = MessageTypeRegistry.Builder().register(RedPacketType(store)).build()
val state = rememberImChatState(engine, host, types)
```

:::

Web 把注册表传给 `TalkwireProvider` 的 `types`，Android 传给 `rememberImChatState`。类型的名字与查找顺序两端一致：先是应用自己的，再是内置的撤回、文本、图片、视频、语音、文件、系统，最后是兜底的 `UnknownType`，它显示 `content` 的文字。

### MessageType 接口

| Web（`@talkwire/react`） | Android（`talkwire-ui`） | 说明 |
| --- | --- | --- |
| `name` | — | 稳定的名字，用于测试与调试 |
| `matches(message)` | `matches(message)` | 是否由这个类型来画 |
| `framed` | `framed` | `true`（默认）画在标准气泡里；`false` 是自己画外框的卡片 |
| `render(message, scope)` | `@Composable Content(message, scope)` | 画出来 |
| `preview(message, strings)` | `preview(message, context)` | 会话列表与通知里的一行文字 |
| `actions(message)` | `actions(message)` | 长按菜单给哪些操作。Web 不实现时没有菜单；Android 默认是回复、转发、多选、撤回 |

两端的 `MessageScope` 都带 `isMine`、`resolveUrl`（把存下的媒体地址变成可加载的地址）与 `openImage`。Web 还带 `strings`；Android 还带 `onLongPress`（打开消息操作）与 `nameOf(uid)`。类型需要的其他东西（HTTP 客户端、宿主的状态）由类型自己带进来，例如 `RedPacketType(store)`。

菜单里可用的操作：Web 是 `copy`、`save`、`reply`、`forward`、`select`、`recall`、`retry`、`discard`，其中 `recall`、`retry`、`discard` 只对自己的消息出现；Android 是 `COPY`、`SAVE`、`SHARE`、`REPLY`、`FORWARD`、`SELECT`、`RECALL`。红包卡片没有可复制的文字，`RedPacketType.actions` 返回空集合。

## 推送卡片状态

宿主在业务变化之后调服务端的 `custom-broadcast` 接口。用 Node 宿主 SDK 时是 `customBroadcast(target, kind, payload)`，`target` 是 `{ topic }`（按会话）或 `{ targetUid }`（按用户）。红包示例在有人领取后这样推：

```ts
await im.customBroadcast({ topic: packet.topic }, "red_packet", cardFor(packet, ""));
```

在线的成员收到：

```json
{"action": "custom_update", "topic": "grp_…", "what": "red_packet", "extra": {"id": "…", "claimed": 2, "remaining": 1, …}}
```

协议对 `custom_update` 的规定（附录 B）：

- 按会话推送时，订阅了该会话的连接都收到，帧里带 `topic`；按用户推送时，该用户的每个连接都收到，帧里没有 `topic`。
- `what` 符合 `^[a-z][a-z0-9_.-]{0,63}$`，含义由宿主约定。客户端应当按 `what` 分派，必须忽略不认识的 `what`。
- 不进离线队列，也不重发。客户端打开会话或卡片时应当向宿主拉取当前状态，不能只靠这个推送。

客户端在 `ImClient.events` 上收到它，引擎不处理：Web 是 `{ kind: "custom", topic?, what, extra }`，Android 是 `ImEvent.Custom(topic, what, extra)`。参考部署的旧推送 `red_packet_update` 也会转成 `what` 为 `red_packet` 的同一种事件；新宿主不应当再用它。

宿主相关的接口见 [Node 宿主 SDK](/host/node-sdk)，帧的完整定义见[规范全文](/protocol/spec)。

## 红包示例

1. 发送者调宿主 `POST /api/v1/red-packets`（`topic`、`total`、`count`），宿主核对他是群成员后建红包，返回红包 id 和下一步要发的消息。
2. 发送者的客户端在群里发一条 `contentType` 为 `red_packet` 的消息。
3. 成员调宿主 `POST /api/v1/red-packets/:id/claim`。宿主用 `topicMembersResolve` 向 IM 服务端核对成员身份，记下这次领取，再用 `customBroadcast` 推送；客户端按 `what` 找到对应的卡片并刷新。
4. 因为 `custom_update` 不进离线队列，客户端打开卡片时调 `GET /api/v1/red-packets/:id` 拉当前状态。

推送给全群的状态不含任何人的金额；每个人只在自己的应答里看到自己领了多少（`myAmount`）。示例的账本在内存里，只为让示例短。真实的宿主在自己的数据库里扣发送者、给领取者入账，与“记下领取”放在同一个事务里，提交之后再推送。

Android 一侧的三个类：

- `RedPacketType`：消息类型。`matches` 只看 `contentType`，打开时调 `store.refresh(id)` 拉状态，点击时领取。
- `RedPacketStore`：卡片状态。收集 `client.events` 里 `what == "red_packet"` 的 `ImEvent.Custom` 合并进来；推送不带个人金额，所以保留先前应答里本人的 `myAmount`。
- `RedPacketSender`：附件面板入口与建红包的对话框，建好后用 `engine.send` 发出卡片消息。

::: details RedPacketType.kt
<<< @/../examples/red-packet/android/src/main/kotlin/io/talkwire/examples/redpacket/RedPacketType.kt
:::

::: details RedPackets.kt
<<< @/../examples/red-packet/android/src/main/kotlin/io/talkwire/examples/redpacket/RedPackets.kt
:::

::: details 宿主一侧：red-packets.ts
<<< @/../examples/red-packet/src/red-packets.ts
:::

运行示例的测试：先构建 Node 宿主 SDK，再在示例目录里安装与测试。

```bash
cd host-sdk-node && npm install && npm run build
cd ../examples/red-packet && npm install
npm run typecheck
npm test
```

Android 一侧的单测是 `:example-red-packet:testDebugUnitTest`（在 `android/` 下用 Gradle 运行）。红包卡片、会话列表预览、领取与实时更新都在一台小米（Android 15）真机上看过。
