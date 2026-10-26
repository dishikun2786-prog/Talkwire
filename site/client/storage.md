---
title: "本地存储与加密"
description: "客户端在本机存什么、换账号时怎样清空，以及 Web 的 IdbChatStore 与 Android 的 RoomChatStore 如何按会话加密并做加密粉碎。"
---

# 本地存储与加密

客户端把会话与消息存在本机，`ChatEngine` 是存储的唯一写入者，存储本身只负责记住。Web 用 IndexedDB 上的 `IdbChatStore`，Android 用 Room 上的 `RoomChatStore`，两者的逻辑表一一对应；都可以按会话加密正文，删会话时销毁密钥。

## 存了什么

两个平台都实现同一个 `ChatStore` 接口，表也相同：

| 表 | 主键 | 内容 |
| --- | --- | --- |
| `messages` | 会话、`seq` | 服务端已存下的消息 |
| `pending_messages` | 会话、`localId` | 还没有 `seq` 的本人消息（发送中或失败）；服务端存下后在一个事务里移到 `messages` |
| `conversations` | 会话 | 标题、本人与对方的已读位置、草稿、置顶、免打扰、是否已离开、最后活动时间 |
| `recalled_seqs` | 会话、`seq` | 已撤回的 seq；补拉时它们不算空洞（红线 C3） |
| `clear_states` | 会话 | 清空水位（`clearVersion`、`clearBeforeSeq`） |
| `read_cursors` | 会话、uid | 开了已读回执的群里每个成员读到哪里 |
| `owner` | — | 这份数据属于哪个 uid（红线 C4） |
| `content_keys` | 会话 | 加密时每个会话的数据密钥 |

不想持久化时可以用内存实现：Web 是 `MemoryChatStore`（`@talkwire/client` 导出，`@talkwire/storage-idb` 也转出），Android 是 `talkwire-core` 的 `InMemoryChatStore` 或 `RoomChatStore.inMemory(context)`。

## 换账号

`engine.open()` 先看存储的 `owner`：属于另一个账号，或从未被认领过，就清空全部表再认领给当前用户；是同一个账号则原样保留。这是红线 C4，两端都有 `C4_SwitchingAccountClearsAllCaches` 用例：换账号清空全部表（含密钥），同账号重登保留。

示例里一台设备先后登录两个名字时，切换账号会把本地缓存整个清掉，不会串号。

## Web：IdbChatStore

```ts
const store = await IdbChatStore.open({ name: "talkwire", encrypt: true });
```

| 选项 | 默认 | 说明 |
| --- | --- | --- |
| `name` | `"talkwire"` | 数据库名，每个应用一个 |
| `encrypt` | `false` | 用每个会话一把不可导出的密钥加密正文、`extra` 与草稿 |
| `indexedDB` | `globalThis.indexedDB` | 注入 IndexedDB 实现，例如测试 |
| `crypto` | `globalThis.crypto` | 注入 WebCrypto 实现 |

`encrypt: true` 时：

- 每个会话第一次写入时，WebCrypto 生成一把 AES-GCM 256 位密钥，以不可导出方式创建并存在 `content_keys` 里：页面上的脚本能用它，但读不出它的内容。
- 消息的正文与 `extra`、会话的草稿用这把密钥加密，每次写入用新的随机 IV。`seq`、发送人、类型与文件地址保持可读，存储才能按它们查询。
- `deleteConversation` 在同一个事务里删掉这个会话的所有行，最后删密钥；磁盘上即使还残留着那些行的副本，也再也读不出来（加密粉碎）。
- 密钥已删或解不开的正文读出来是空字符串。
- 写入时不会让事务跨过 WebCrypto 调用：加解密在事务打开之前或完成之后进行。

`store.close()` 关闭数据库。

## Android：RoomChatStore

```kotlin
val engine = ChatEngine(client, RoomChatStore.open(context, keys = AndroidKeystoreKeyWrapper()), myUserId, scope, EngineConfig())
```

| 参数 | 默认 | 说明 |
| --- | --- | --- |
| `context` | 必填 | |
| `name` | `"talkwire_chat.db"` | 库文件名，在 App 的数据库目录下 |
| `keys` | `null` | `KeyWrapper`；给了就按会话加密 |

每个 App 开一个、一直开着、交给引擎，归属由引擎检查。新库从 schema v1 开始，schema 导出在 `talkwire-storage-room/schemas/`。

给了 `KeyWrapper` 时：

- 每个会话有一把 AES-256-GCM 数据密钥，用 `KeyWrapper` 包裹后存进 `content_keys`；包裹用的密钥由 `KeyWrapper` 持有，不在库里。
- 消息的正文与 `extra`、会话的草稿加密存放。加密失败时退回明文，聊天照常可用。
- 删会话、清空整个存储（换账号）时销毁这个会话的数据密钥，剩下的字节无法再读；密钥已没有的正文读出来是空的。
- 删除的行会被覆盖（SQLite 的 `secure_delete`），清空整个存储后还会对写前日志做检查点，旧页不会留在那里。

::: warning 注意
清空会话只删清空水位以下的消息（存储的 `deleteThrough`），这时密钥保留，因为水位之后的消息还要能读。两个平台的引擎应用清空时走的都是这条路径；销毁密钥的是删会话与换账号。
:::

`AndroidKeystoreKeyWrapper`（在 `talkwire-android` 里）用 Android Keystore 里一把不出 Keystore 的 AES-256 密钥包裹数据密钥，别名默认 `talkwire_chat_content_kek`。这把密钥不要求设备解锁，所以后台到达的消息也能存下。

粉碎有多彻底取决于包裹方式：有 TEE 或 StrongBox 的设备上，Keystore 的密钥由硬件保护、拷不出来；有些低端设备用软件模拟 Keystore，销毁数据密钥只有文件系统那么可靠。也可以实现自己的 `KeyWrapper`（`wrap`、`unwrap` 两个方法）。

`talkwire-storage-room` 还带一个只读导入器 `LegacyChatImporter`，导入源项目 v18 的旧库；它只用按那个 schema 构造的库测过，没有拿真实用户的旧库验证。

## 搜索本机消息

本地搜索在引擎上，两端都是 `searchMessages(query, limit = 50, scanPerConversation = 500)`：读每个会话最近 500 条，只搜文字消息，不区分大小写，按时间倒序。它经存储读出再比对，所以正文加密存放时同样能搜；更早的消息不在范围内。界面里的入口是会话列表顶上的搜索框，见 [React 界面套件](/client/react#conversationlist)。

## 端到端加密的本机数据

端到端加密模块用自己的数据库文件 `talkwire-e2ee.db`，与本页的消息库分开，存身份私钥、会话棘轮与解密后的文本，App 应把它排除在备份之外。细节见[端到端加密](/modules/e2ee)。

## 改名前的本地缓存

项目在 2026-10-26 由 ImKit 改名为 Talkwire。Android 的消息库与加密库文件名、密钥别名、通知渠道，Web 的多标签频道名都换了新名字，旧的不迁移：升级后本地消息缓存从服务端重新同步，端到端加密需要重新发布密钥包。改名前没有公开发布过版本，受影响的只有开发中的安装。见[更新日志](https://github.com/dishikun2786-prog/Talkwire/blob/main/CHANGELOG.md)。
