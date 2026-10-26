---
title: "端到端加密"
description: "客户端用 libsignal 加密单聊与群聊，服务端只存转不透明的密文；客户端模块单独发布，许可证 AGPLv3。"
---

# 端到端加密

端到端加密模块（`e2ee`）让加密会话的正文只在两端可读：客户端用 libsignal 加解密，服务端只存转不透明的密文。服务端不链接 libsignal，仍是 Apache-2.0；链接 libsignal 的客户端模块单独发布，许可证 AGPLv3。线协议在规范第 13 节，见 [规范全文](/protocol/spec)。

## 装上与拿掉

目前只有 Android 有客户端模块。

| 位置 | 装上 | 拿掉 |
| --- | --- | --- |
| 服务端 | `TALKWIRE_MODULES` 列出 `e2ee`；不设这个变量时装宿主支持的全部模块；standalone 宿主也支持 | 从列表里去掉；加密动作得到 501 `module not enabled: e2ee` |
| 宿主 | `ConfigSource.e2eeConfig` 下发 `enabled`、`groupEnabled`、`envelopeTtlHours` | 不给时答 404，服务端按全部开启、密文保留 336 小时处理 |
| Android | `talkwire-e2ee`（AGPLv3） | 不装；其他模块都不依赖它 |
| Web、iOS | v1 的 Web 不做；iOS 待做（Phase 6） | — |

模块有自己的迁移脚本 `server/migrations/e2ee/`，装上时建表；拿掉模块不删表。

## 服务端只转发

加密消息不走 `pub` 和 `get`：服务端不解密，也不把密文写进消息历史。

- **转发**：`e2e_send` 把密文原样交给接收方的 `e2e_deliver`；libsignal 的消息类型 `e2eMsgType` 原样转发。
- **离线暂存**：接收方不在线时密文暂存，默认 14 天（`envelopeTtlHours` 336）；下次登录按发送顺序补投，补投后即从服务端删除，不会重复投递。
- **队列满**：给在线接收方转发时发送队列已满，信封转入离线队列、不断开连接，下次登录补投（红线 R10）。
- **推送**：加密消息的离线推送不带内容预览（红线 R12）。
- **取密钥包**：`e2e_fetch_bundle` 只在双方已有单聊、同在一个已加密的群、或宿主确认双方是好友（每次都问宿主，不缓存）时返回密钥。否则返回 `found:false`，与“对方从没发布过”刻意不区分，以免借此探测关系。

宿主的开关：`enabled:false` 时发布与发送被拒绝、取密钥包返回 `found:false`，但结束会话照常可用，已排队的密文照常补投。`groupEnabled:false` 只禁止新开启群加密，已加密的群不受影响。

## 单聊

单聊用 libsignal 的会话：先取对方的密钥包建会话，再逐条加密。

- **密钥包**：身份公钥、签名预密钥与 Kyber 预密钥（后两者带签名），外加一批一次性预密钥。服务端只检查必填字段非空，不校验密钥本身。别人取密钥包时附带一个一次性预密钥，服务端随即删掉它；用完时其余字段照常返回。
- **补充预密钥**：服务端剩下的一次性预密钥少于 5 个时，Android 模块再发布 20 个（`E2eeConfig` 的 `oneTimeKeyLowWater`、`oneTimeKeyBatch`）。
- **会话的 topic**：用规范的单聊 topic。服务端不校验 `topic`、原样转发（F43），所以收到时按发送者归档，不看对方带的标签。
- **清空与结束**：`e2e_clear` 只清消息、保留会话；`e2e_terminate` 结束会话。这两种通知只发给当时在线的人、不进离线队列，客户端应当在对方重新上线后重发或在界面上提示；Android 模块的界面目前两者都没做。

## 群：发送密钥

群用发送密钥：每个成员有自己的一把，先分发给其他成员，再用它加密群消息。

- **开启**：群主发 `e2e_set_group_encrypted`。加密**不能关闭**；开启后普通 `pub` 返回 409 `group_encrypted`，同群成员之间可以互取密钥包，不需要是好友。
- **分发**：我的发送密钥经与每个成员的单聊会话包一层、走单聊通道发给他。发在群主题上会被服务端扇出给全体成员。确认发出后才记为已分发。
- **换钥**：收到任何 `e2e_set_group_encrypted` 推送（开启、有人被踢、有人退群）都重新生成并分发自己的发送密钥。换钥与“分发加加密”放在同一把锁里，否则成员可能拿到旧钥、消息却用新钥。
- **密钥晚到**：先于发送密钥到达的群消息先挂起，密钥一到就补解。
- **清空**：`e2e_group_clear` 只有管理员能调，通知只发给在线成员。

与参考部署互通：参考部署把发送密钥发在群主题上，本模块收到不是给自己的那份时解不开，按预期忽略。

## 每个账号只有一台加密设备

v1 不支持多设备加密：发布身份密钥的那台设备是这个账号的加密设备。`e2e_deliver` 及其补投、`e2e_terminated`、`e2e_cleared` 和加密消息的推送通知都只给这台设备，同一账号的其他设备（例如没有密钥的网页端）收不到。另一台设备重新发布密钥包即接管。

所以客户端每次登录都要发布密钥包。另有两种情况必须重新发布一次：

- **开始带 `device` 登录之后**：不带 `device` 的登录发布的身份属于设备 `""`；客户端改为带 `device` 登录后不重新发布，加密消息会一直等着设备 `""`。多端登录见 [多端与推送](/protocol/devices-push)。
- **项目改名为 Talkwire 之后**：Android 的加密库文件名换了，旧的不迁移，升级的安装要重新发布，见 [`CHANGELOG.md`](https://github.com/dishikun2786-prog/Talkwire/blob/main/CHANGELOG.md)。

Android 模块的 `E2eeChats.start()` 在每次连上服务端时发布密钥包，这两种情况都已覆盖。

## 为什么单独发布、用 AGPLv3

libsignal 是 AGPLv3，所以链接它的客户端模块单独发布、采用 AGPLv3。这样不用加密的项目只拿 Apache-2.0 的核心，依赖树里没有 libsignal：

- `android/talkwire-e2ee/LICENSE` 是 AGPLv3 全文，每个源文件带 `SPDX-License-Identifier: AGPL-3.0-only`。
- 其余模块都是 Apache-2.0，不依赖它，源文件也不许带这一行。
- 服务端只存转密文、不链接 libsignal，仍是 Apache-2.0。

`tools/depcheck` 检查这几点（许可证标注、依赖方向）；CI 的 Android 作业另查解析后的依赖图：核心与其他模块里没有 libsignal。见 [测试与红线](/contributing/testing)。

## Android：`talkwire-e2ee`

`talkwire-e2ee` 是 Compose 模块，依赖 `talkwire-ui`、`org.signal:libsignal-android:0.73.0` 与 Room 2.6.1。

| 类 | 作用 |
| --- | --- |
| `E2eeSignaling` | 协议层，不链接 libsignal |
| `E2eeCrypto` | libsignal：身份、密钥包、单聊会话、群发送密钥、安全码 |
| `E2eeChats` | 收发、本机存储、清空与结束 |
| `EncryptedChatScreen` | 加密会话的界面 |
| `E2eeDatabase` | 独立的数据库文件 `talkwire-e2ee.db` |

- **libsignal 钉死在 0.73.0**：会话两端必须用同一版。单测里的对端用同一版本的 JVM 构建 `libsignal-client:0.73.0`，加解密是真跑的；CI 检查解析后的依赖图里 libsignal 没被升级。
- **核心库脱糖**：打 APK 的 App 必须开 `isCoreLibraryDesugaringEnabled = true` 并加 `coreLibraryDesugaring("com.android.tools:desugar_jdk_libs:…")`。这是 libsignal 的 AAR 元数据要求的，minSdk 26 实际用不到。
- **本机数据**：`talkwire-e2ee.db` 存身份私钥、会话棘轮与解密后的文本，App 应把它排除在备份之外。一个文件只属于一个账号：换账号时调 `wipe()`，用另一个 uid 打开会先清空。给了 `KeyWrapper`（如 `AndroidKeystoreKeyWrapper`）时文本按会话加密存放，清空即销毁该会话的数据密钥。
- **只有文本**：加密会话目前只收发文本消息，内容是 UTF-8。
- **重发**：发送失败的消息保留密文，重发用同一份密文；对方已收到时会当重复丢弃。

### 失败分七类

每个加密操作的结果是一个值，或七类失败之一（红线 C10）。`e2eeFailureText` 给每类一句可操作的文案（英文、简体中文、越南文），只有“离线”一类提示重试。

| `E2eeFailure` | 含义 |
| --- | --- |
| `PEER_KEYS_UNAVAILABLE` | 取不到对方的密钥。服务端刻意不区分“没开启”与“不是可以联系的人”，只在已知是联系人的地方（例如从已有会话进入）读成“对方还没开启” |
| `NOT_ALLOWED` | 服务端拒绝：不是联系人，或不是群主、管理员 |
| `GROUP_NOT_ENCRYPTED` | 群还没开启加密，要群主先开 |
| `DISABLED` | 宿主关了加密，或服务端没装这个模块 |
| `OFFLINE` | 没连上或没有应答；唯一可以原样重试的一类 |
| `CRYPTO` | 本机的密钥或会话用不了（损坏，或对方的密钥包验不过） |
| `SERVER` | 服务端的应答不在预期之内 |

## 验证状态

加解密用真实 libsignal 跑过两端与群，但没有上过真机。

- **服务端**：黄金对话 `module/e2ee-p2p`、`module/e2ee-group`；单测 `TestR10_E2EEQueueFullFallsBackToOffline`、`TestR12_EncryptedMessageHasNoPreview`。
- **Android**：`E2eeSignalingReplayTest`（两份黄金对话，A、B、C 三个角色）、`E2eeCryptoTest`（真实 libsignal 的会话、乱序、重复、无一次性预密钥、群与换钥、换账号；含 R17 的身份初始化互斥）、`E2eeChatsTest`（内存服务端加一个按第 13 节写的转发替身）、`E2eeScreensTest`（含 C10）。
- **没做的**：没有真机验证；示例 App 没接（要开脱糖）；只有文本消息；多设备加密（v1 不做）；对方离线时清空与结束的通知不排队，界面没有补发或提示。
