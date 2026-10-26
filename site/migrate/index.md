---
title: "从现有 IM 系统迁移"
description: "把正在运行的原版（小桃子的 IM 服务）一块一块换成 Talkwire，不停服、不搬消息：先换服务端并用 legacy 档兼容旧客户端，再换宿主接口与客户端，最后切到 strict。"
---

# 从现有 IM 系统迁移

正在运行的 IM 服务可以一块一块换成 Talkwire，不需要停服，也不需要搬消息数据。这套方法的关键是 legacy 档：它对外的帧与原版（小桃子的 IM 服务）逐帧一致，所以先换服务端时旧客户端不用升级。

::: warning 注意
本页的对应关系来自 Talkwire 与原版两边代码的对照，还没有在生产环境演练过。每一步都先在生产库的副本上走一遍，见[每一步怎么验证](#每一步怎么验证)。
:::

## 适用范围

现有服务就是原版或它的部署时，下面每一步都能直接用：legacy 档与原版的 33 个黄金对话逐帧一致，旧客户端能直接连上 Talkwire 服务端。

现有服务说的是别的协议时，“先换服务端、旧客户端不升级”这一步不成立，服务端和客户端要一起换。宿主接口、宿主数据与后台三部分的做法照样适用。

## 建议顺序

1. **换服务端**：用 Talkwire 服务端替换原版服务端，设 `TALKWIRE_PROTOCOL_PROFILE=legacy`，指向同一个数据库，其余环境变量照旧。
2. **换宿主接口**：IM 调用的宿主端点、App 用的聊天接口、媒体接口，逐个换成 `@talkwire/host-node`，或按契约自己实现。数据先留在原表里。
3. **换后台页面**：聊天记录、通话记录页换成 `@talkwire/admin`。
4. **换客户端**：新版 App 用 Talkwire 的客户端 SDK；业务卡片注册成自定义消息类型。新客户端可以先在 legacy 档上线。
5. **切 strict 档**：旧客户端淘汰后把 `TALKWIRE_PROTOCOL_PROFILE` 改成 `strict`（默认值），得到协议缺陷的修正与新增字段。

## 第 1 步：换服务端

Talkwire 服务端直接用原版已经建好的数据库。启动时基线迁移只补缺的部分，不重建；执行过的迁移记在 `talkwire_schema_migrations`，重启不会重复执行。

| 原版 | Talkwire | 说明 |
| --- | --- | --- |
| `im_*` 表 | 同一批表 | 新增表 `talkwire_schema_migrations`、`im_devices`、`im_pending_acks`；新增列 `im_fcm_tokens.provider`、`im_fcm_tokens.device_id`、`im_e2e_identities.owner_device` |
| 环境变量 `DB_*`、`JWT_SECRET`、`IM_PORT`、`MAIN_API_BASE_URL`、`IM_*` | 同名 | 默认值与原版写死的值相同，由单测 `TestR9_DefaultsEqualOriginalValues` 逐项比对 |
| `FCM_SERVICE_ACCOUNT_KEY_PATH`、`FCM_SERVICE_ACCOUNT_PATH` | 两个都读 | |
| `IM_FCM_HTTP_TIMEOUT_SEC` | `TALKWIRE_PUSH_HTTP_TIMEOUT_SEC` | 旧名也读 |
| 写死在服务端里的群 AI 问答 | `groupai` 模块与宿主的 `GroupAiHandler` | 助手的名字与兜底文案进运行配置 `groupAi`，默认值与原版逐字相同 |
| 通话信令 | `call` 模块 | |
| 端到端加密的密钥与密文存转 | `e2ee` 模块 | |
| 扫码入群 | `groupjoin` 模块 | |
| 内部接口 `red-packet-broadcast` | 保留 | 新代码用通用的 `custom-broadcast` |

legacy 档接受原版接受的全部令牌，旧客户端登录不受影响。帧以外，Talkwire 服务端修了原版的几处并发缺陷，并多了 APNs 与 Web Push、可选模块、多端在线等能力，逐项见服务端 README 的[与原版的差异](https://github.com/dishikun2786-prog/Talkwire/blob/main/server/README.md#与原版的差异)。

## 第 2 步：换宿主接口

IM 调用宿主的端点路径（`/api/v1/internal/im/*`）与契约不变，所以宿主一侧可以一个端点一个端点地换。用 Node 时换成 `talkwireHostPlugin` 的选项：

| IM 调用的端点 | Node 宿主 SDK | 说明 |
| --- | --- | --- |
| `friendship` | `ContactStore` | |
| `profile` | `ProfileStore` | 带按方向存的备注：先给查看者自己设的备注 |
| `runtime-config`、`e2ee-config` | `ConfigSource` | |
| `delete-media` | `mediaDeleter` 加 `BlobStore` | |
| `calls/create`、`answer`、`settle`、`upgrade` | `CallHandlers`，外面包一层 `recordingCalls` | 计费与 RTC 凭证留在宿主：`answer` 里签频道令牌，`settle` 里结算 |
| `group-ai-qa` | `GroupAiHandler` | 知识库与计费留在宿主 |

宿主调 IM 的内部接口换成 SDK 的 `ImClient`：`contactSync`、`customBroadcast`、`callBroadcast`、`mediaRefCount`、`topicMembersResolve`，以及后台用的四个。不用 Node 的宿主照[宿主接口契约](/host/)和[用其他语言实现](/host/other-languages)自己实现。

App 用的聊天接口换成 `talkwireAppRoutes`（默认路径前缀 `/api/v1/chat`），媒体上传与读取换成 `talkwireMediaRoutes`。字段以 `talkwireAppRoutes` 为准；旧客户端用到的字段逐个对照之后再切。

### 加好友：先观察，再强制

原版的加好友二维码里是裸 uid；Talkwire 的二维码是宿主签名的串，由 `GET /api/v1/chat/me/qr` 给出。群里人人看得到别人的 uid，裸 uid 能解析就等于群成员不用对方出示二维码就能互加（红线 R14）。

`talkwireAppRoutes` 的 `addTokenMode` 默认是 `"require"`：加好友必须带由搜索或扫码得到的令牌，只给裸 uid 的二维码被拒。迁移时先用 `"observe"`：不带令牌的请求和旧的裸 uid 码照常可用，每次经 `onAddTokenMissing` 报告；新版 App 都改用签名码之后再换成 `"require"`。

## 宿主数据

宿主数据有两种迁法：在原表上实现 Talkwire 的存储接口，不搬数据（建议先这样）；或者把数据搬进 `talkwire_*` 表，用 `@talkwire/host-node/prisma`。表结构见 [prisma/talkwire.prisma](https://github.com/dishikun2786-prog/Talkwire/blob/main/host-sdk-node/prisma/talkwire.prisma) 和不用 Prisma 时的 [prisma/talkwire.sql](https://github.com/dishikun2786-prog/Talkwire/blob/main/host-sdk-node/prisma/talkwire.sql)。

在原表上实现时，每个接口对原表的要求：

| 接口 | 原表要满足的 |
| --- | --- |
| `ProfileStore` | 能按 uid 反查资料。原表多半没有 uid 列：加一列，用 `imUid(用户 id)` 回填；算法由 [uid-vectors.json](https://github.com/dishikun2786-prog/Talkwire/blob/main/contract/host-api/uid-vectors.json) 钉住，与原版相同。公开的 handle 唯一、不分大小写；原表的显示名可空时，空值给空串 |
| `ContactStore` | 每对用户最多一条关系，记录谁先发起（`requesterId`、`recipientId`）、状态、各自给对方的备注（`requesterAlias`、`recipientAlias`）与谁拉黑的。原表要有保证一对用户只有一行的唯一索引；`withPair` 要让同一对用户的改动串行，Prisma 版用事务级 advisory lock |
| `AuditStore` | 幂等键唯一。后台清空记为 `action` `im.clear_topic`，只记元数据，不记消息内容 |
| `CallRecordStore` | 状态只有 `ringing`、`active`、`ended` 三种；原表里“未接”“已拒绝”这类状态都是 `ended` 加对应的结束原因（`no_answer`、`rejected` 等）。计费金额是宿主业务，不在通话记录里 |

## 第 3 步：换后台页面

后台的聊天记录与通话记录页换成 `@talkwire/admin`，后端换成 `talkwireAdminRoutes`：

| 页面 | Talkwire | 说明 |
| --- | --- | --- |
| 聊天记录与清空 | `ChatRecords` 加 `talkwireAdminRoutes` | 清空仍要超级管理员、写理由、带幂等键，审计只记元数据；确认方式是逐字输入 `CLEAR <会话>` |
| 通话记录与详情 | `CallRecords`、`CallDetail` 加 `talkwireAdminRoutes` 的 `calls` 选项 | 只有元数据，没有计费金额；要看就在宿主自己的页面里补 |

## 第 4 步：换客户端

客户端换成 Talkwire 的 SDK。Android 用 `talkwire-core`、`talkwire-storage-room`、`talkwire-android`、`talkwire-ui`，按需加可选模块：

| 原版客户端里的部分 | Talkwire | 说明 |
| --- | --- | --- |
| WebSocket 连接 | `talkwire-core` 的 `ImClient` | |
| 消息仓库、会话加载 | `talkwire-core` 的 `ChatEngine` | |
| 本地消息库（Room） | `talkwire-storage-room` | 换账号清库（C4）、离线补拉游标（C3）的规则相同；`LegacyChatImporter` 能只读导入原版 v18 结构的旧库，只用按这个结构构造的库测过 |
| 通知 | `talkwire-android` 的 `ImNotifier`、`NotificationController` | 渠道静音，提示音由 App 播（C6） |
| FCM 令牌上报 | `talkwire-push-fcm` | |
| 聊天界面 | `talkwire-ui`（`ImChatApp`） | 文案自带英文、简体中文、越南文 |
| 红包、转账等业务卡片 | 宿主自定义消息类型（`MessageType`） | 例子见 [examples/red-packet](https://github.com/dishikun2786-prog/Talkwire/tree/main/examples/red-packet) |
| 通话 | `talkwire-call` 的 `CallManager`、`CallSystem`、`CallScreen` | 信令按协议第 16 节；通话悬浮小窗没有移植 |
| 阿里云 ARTC 通话引擎 | `talkwire-call-dingrtc`（实现 `CallMediaEngine`） | 宿主签频道令牌，见 [examples/artc](https://github.com/dishikun2786-prog/Talkwire/tree/main/examples/artc) |
| 端到端加密 | `talkwire-e2ee`（AGPLv3）的 `E2eeChats`、`EncryptedChatScreen` | libsignal 同为 0.73.0，消息格式与原版互通；本机数据在独立的库里，不从原版的加密库迁移；App 要开核心库脱糖 |
| 群二维码与入群 | `talkwire-group-join` | 二维码的绘制与扫描仍由 App 提供 |
| 大视频压缩 | `talkwire-video-compress` 的 `Media3VideoCompressor` | 同样是大于 50 MB 才重新编码 |

Web 端用 `@talkwire/client`、`@talkwire/storage-idb`、`@talkwire/react`，接法见 [Web SDK](/client/web)。

## 留在宿主的业务

钱和规则都归宿主，IM 只负责把信令、问题和卡片状态送到：

| 业务 | 怎么接 |
| --- | --- |
| 红包、转账、扫码付款 | 卡片是自定义消息类型；状态变化（被领完、过期）用 `ImClient.customBroadcast` 推给会话 |
| 群 AI 的计费与知识库 | `GroupAiHandler` |
| 通话计费、RTC 凭证、余额不足时结束通话 | `CallHandlers`；提前结束用 `ImClient.callBroadcast` 给双方推 `call_ended` |

## 第 5 步：切到 strict

切 strict 之前，legacy 与 strict 的差异按下表处理：

| 事项 | 做法 |
| --- | --- |
| 旧客户端 | 只在 legacy 档上逐帧兼容；legacy 档接受原版接受的全部令牌 |
| 新客户端 | 可以先在 legacy 档上线；全部换完之后再切 strict |
| 令牌 | strict 档只接受 `type=access`、`role` 在 `TALKWIRE_TOKEN_USER_ROLES` 里的令牌（红线 R18）。切 strict 时若还有客户端用别的令牌，临时设 `TALKWIRE_ACCEPT_LEGACY_TOKENS=1`（启动时会打印警告），用完去掉 |
| 多端在线 | 新客户端登录带 `device`；旧客户端不带，照旧同一账号互相顶替 |
| 协议缺陷 | strict 档修正 D1–D5、D7–D9；旧客户端依赖的行为在 legacy 档保持原样 |

两档的差异逐项见 [legacy 与 strict](/protocol/profiles)。

## 每一步怎么验证

- **换服务端之前**：在生产库的副本上启动 Talkwire 服务端（legacy 档），确认迁移只补了缺的表和列。一致性测试台 `tools/conformance` 的 `schema` 以你的原版服务为参照（`-oracle`）比较两边建出的表结构，`check` 跑全部黄金对话。这两个命令每次运行都删除并重建所指数据库的 `public` schema，只能指向一次性的测试库，不要指向那份副本。
- **换宿主接口**：`host-sdk-node/conformance/host.ts` 让测试台用 SDK 扮演宿主跑全部黄金对话；在原表上自己实现的存储接口，套用 [host-sdk-node/test/stores.suite.ts](https://github.com/dishikun2786-prog/Talkwire/blob/main/host-sdk-node/test/stores.suite.ts) 那套用例。其他语言写的宿主实现一致性控制接口后，用 `conformance check -host <你的宿主>` 跑。
- **换客户端**：Android 的 `SampleStackTest`，加上真机检查，可以照 [android/DEVICE_CHECKS.md](https://github.com/dishikun2786-prog/Talkwire/blob/main/android/DEVICE_CHECKS.md) 的格式记录。
- **切 strict 之前**：确认已经没有旧客户端在线，再在副本上用 strict 档跑一遍。
