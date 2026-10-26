---
title: "简介"
description: "Talkwire 是什么、宿主要实现什么、能得到什么，以及各部分目前做到了哪一步。"
---

# 简介

Talkwire 是一个自己部署的开源即时通讯框架：一个 Go 实时服务端、一个宿主接入 SDK、Android 与 Web 两端的客户端 SDK，加上一份写到“照着就能实现客户端”程度的线协议。业务系统只实现少量宿主接口（身份、好友关系、资料、推送、媒体），就能得到单聊、群聊、媒体消息、已读、撤回、在线状态与离线推送。

Talkwire 从小桃子自研的即时聊天系统中提炼而来，原名 ImKit，2026-10-26 改名。线协议没有因改名变化，改名对已有部署的影响见 [CHANGELOG.md](https://github.com/dishikun2786-prog/Talkwire/blob/main/CHANGELOG.md)。

## 组成

| 部分 | 目录 | 内容 |
| --- | --- | --- |
| 实时服务端 | `server/` | Go 写的 IM 服务端：WebSocket 连接、消息与群、离线补投、FCM/APNs/Web Push 推送，数据存在 PostgreSQL |
| 宿主 SDK | `host-sdk-node/` | `@talkwire/host-node`：把宿主一侧做成 Fastify 4 插件，附存储接口与内存版、Prisma 版参考实现 |
| Android SDK | `android/` | 纯 JVM 的核心库、Room 存储、通知与 FCM、Compose 界面套件、示例 App |
| Web SDK | `web/` | 协议类型、客户端与同步引擎、IndexedDB 存储、React 界面套件、示例与端到端用例 |
| 线协议 | `contract/protocol/` | 规范、JSON Schema、黄金对话 |
| 宿主契约 | `contract/host-api/` | IM 服务端与宿主之间的两份 OpenAPI 3.1、uid 黄金向量 |
| iOS SDK | — | 计划中，尚未开始 |

各部分之间怎么通信见[架构与概念](/guide/architecture)。

## 宿主要实现什么

宿主是接入 Talkwire 的业务后端：它知道用户是谁、谁和谁是好友、每个人叫什么，并拥有媒体文件和一切业务。IM 服务端只管连接、消息、群和推送。

| 职责 | 宿主做什么 |
| --- | --- |
| 身份 | 给客户端签发用户访问令牌：HS256 JWT，`sub` 是宿主用户 id，`type` 为 `access`，`role` 在 `TALKWIRE_TOKEN_USER_ROLES` 里（默认 `app_user`），密钥与服务端的 `JWT_SECRET` 相同；也可以让服务端用宿主公布的 JWKS 验令牌（`TALKWIRE_JWKS_URL`）。见[签发 IM 令牌](/host/token) |
| 好友关系 | 实现 `friendship` 端点（两人的关系是否已被接受）；关系被接受或解除时调 IM 的 `contact-sync` |
| 资料 | 实现 `profile` 端点（显示名与头像，先给查看者自己设的备注）；客户端界面经 `ImUiHost` 向宿主批量查资料 |
| 推送 | 推送由 IM 服务端发出，服务商的凭据配在服务端的环境变量里；宿主提供通知标题里的名字（`profile` 端点）和文案（运行配置 `pushText`） |
| 媒体 | 上传、存储、签发文件地址；IM 只存地址，消息不再引用某个文件时调宿主的 `delete-media` |

`friendship` 与 `profile` 是必须实现的端点，其余（运行配置、删媒体、各模块的端点）是可选的。用 Node 写宿主时可以直接用 [Node 宿主 SDK](/host/node-sdk)；用其他语言见[用其他语言实现](/host/other-languages)。完整契约见[宿主接口契约](/host/)。

## 能得到什么

服务端核心提供这些能力，两种协议档位（legacy、strict）都有：

- 单聊与群聊：建群、邀请、踢人、退群、改群名、设管理员、转让群主、解散。
- 文本与媒体消息、转发、撤回、清空会话、已读位置、输入中、在线状态。
- 离线补拉与补投：消息按序号补拉，清空和群事件在下次登录时补投。
- 同一账号多台设备同时在线（客户端登录时带设备名才启用，每个账号默认最多 10 台）。
- 离线推送：FCM、APNs、Web Push，配置了才启用。
- 宿主自定义消息卡片（红包、订单等）的状态推送 `custom_update`。

客户端 SDK 在此之上提供连接与重连、同步引擎、本地存储（Android 用 Room，Web 用 IndexedDB，都可以按会话加密）、通知决策、自定义消息类型注册表，以及整套聊天界面（Compose、React，自带英文、简体中文、越南文文案）。Web 端同一账号的多个标签页共用一条连接。

可选模块按需装，每个都能单独拿掉：

| 模块 | 内容 |
| --- | --- |
| [音视频通话](/modules/call) | 信令走 IM，媒体走宿主自己的 RTC 服务；附阿里云 ARTC 3.0 的适配器 |
| [扫码入群](/modules/group-join) | 群二维码令牌、凭码入群、入群审核，宿主不需要实现接口 |
| [群助手钩子](/modules/bot-hook) | 群成员“@助手”提问时由宿主回答，IM 以助手身份发进群 |
| [端到端加密](/modules/e2ee) | 服务端只存转密文；客户端模块链接 libsignal，目前只有 Android |

另有可嵌入宿主管理后台的[后台页面](/modules/admin) `@talkwire/admin`：查看聊天记录、为双方清空单聊、查看通话记录。

## 当前状态

Talkwire 还没有发布任何版本。npm 包名 `@talkwire/*`、Android 包名 `io.talkwire` 都是占位符，目前只能在仓库里以 npm 工作区包和 Gradle 模块的形式使用。

| 部分 | 状态 |
| --- | --- |
| 协议规范 | 草稿，待审阅 |
| 服务端 | 完成。legacy 档与原版服务的 33 个黄金对话逐帧一致；strict 档同样 33/33（去掉新增字段、有意偏离的 6 个场景另录）；不连宿主的 standalone 模式 30/33（依赖宿主的 3 个场景必然失败） |
| 宿主契约与 Node 宿主 SDK | 完成。用 SDK 替换测试用的假宿主后黄金对话 33/33，契约测试双向通过 |
| Android | 核心、Room 存储、通知与 FCM、Compose 套件、示例 App 完成。真机只验证过一台小米（Android 15）：基本聊天流程通过，2026-10-26 与 Web 示例互通了撤回、引用回复、语音通话信令与铃声、扫码入群。FCM 的实际收发、相册与文件选择器、视频压缩、端到端加密、ARTC 适配器都没有在真机上验证 |
| Web | 完成。与 Android 示例在真机上互发、同一账号手机与网页同时在线已验收。Web Push 只做了订阅与显示，没有接推送服务，只在 Edge 上跑过；ARTC 适配器只用模拟的 SDK 测过 |
| iOS | 未开始，需要 macOS |
| 后台页面 | 宿主一侧的后台接口与 `@talkwire/admin` 完成 |
| CI 与压测 | 全量检查脚本 `tools/ci.sh` 与 GitHub Actions 配置已写好，还没在 GitHub 上跑过；压测工具 `tools/loadtest` 只在一台机器上试跑过，正式压测待在 Linux 上做 |

各部分的详细结果见各目录的 README 与 [docs/plan.md](https://github.com/dishikun2786-prog/Talkwire/blob/main/docs/plan.md)。

## 许可证

核心以 Apache-2.0 发布，见 [LICENSE](https://github.com/dishikun2786-prog/Talkwire/blob/main/LICENSE)。链接 libsignal 的端到端加密客户端模块单独成模块，采用 AGPLv3：目前是 Android 的 `android/talkwire-e2ee`，目录里有它自己的 LICENSE。

服务端的端到端加密只存转不透明的密文，不链接 libsignal，仍是 Apache-2.0。核心产物的依赖树里没有 libsignal 和任何 RTC SDK，`tools/depcheck` 与 `tools/ci.sh` 检查这一点。

## 下一步

- [架构与概念](/guide/architecture)：组件之间怎么通信，uid、topic、seq 这些概念是什么意思。
- [快速开始](/guide/quickstart)：在本机跑起服务端、开发宿主、Web 示例和 Android 示例。
- [从现有 IM 系统迁移](/migrate/)：已经在跑原版（小桃子的 IM 服务）时怎么换过来。
