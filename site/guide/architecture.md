---
title: "架构与概念"
description: "Talkwire 的组件之间怎么通信，以及 uid、topic、seq、订阅、离线补投和可选模块这几个贯穿全书的概念。"
---

# 架构与概念

Talkwire 由四方组成：客户端、IM 服务端、宿主和 PostgreSQL。客户端只和 IM 服务端说线协议；账号、好友关系、资料、媒体和一切业务都在宿主那里，IM 服务端需要时向宿主查询。

## 组件与连线

```text
          客户端（Android、Web）
          │                       │
          │ WebSocket /ws/im      │ HTTP：宿主自己的接口
          │ 线协议                │ （登录、联系人、资料、媒体）
          ▼                       ▼
     IM 服务端（Go） ── 宿主接口 ──▶ 宿主（业务后端）
          │          ◀── 内部接口 ──
          ▼
      PostgreSQL
```

| 连线 | 方向 | 内容 | 鉴权 |
| --- | --- | --- | --- |
| 线协议 | 客户端 ⇄ IM 服务端 | `GET /ws/im`（或等价的 `/ws`）升级为 WebSocket；文本帧，每帧一个 JSON 对象 | 登录帧带宿主签发的用户访问令牌 |
| 宿主接口 | IM 服务端 → 宿主 | `MAIN_API_BASE_URL`（默认 `http://127.0.0.1:6001`）下的 `/api/v1/internal/im/*`：好友关系、资料、运行配置、删媒体，以及各模块的端点 | 内部令牌 |
| 内部接口 | 宿主 → IM 服务端 | `/api/v1/internal/im/*`（`contact-sync`、`custom-broadcast`、`call-broadcast` 等）与后台用的 `/api/v1/internal/admin/im/*` | 内部令牌 |
| 应用接口 | 客户端 → 宿主 | 由宿主自己定；Node 宿主 SDK 提供 `/api/v1/chat/*`（资料、联系人）、上传 `/api/v1/chat/upload` 与读取 `/media` | 宿主自己的登录态 |

两个方向的内部令牌是同一种：HS256 JWT，密钥是双方共享的 `JWT_SECRET`，claims 为 `{"role": "internal_service", "exp": <现在 + 60 秒>}`。IM 服务端从不重试失败的宿主调用，每个端点的超时与失败时的处理写在契约里，见[宿主接口契约](/host/)。

媒体不经过 IM 服务端。客户端把文件传给宿主，消息里只带文件地址；某个文件不再被任何消息引用时，IM 服务端调宿主的 `delete-media`。

### IM 服务端

服务端的核心是一个 Go 包，按职责分文件；宿主接口与它的实现在 `server/host/`。数据只存 PostgreSQL，启动时自动执行尚未执行过的迁移，记录在 `talkwire_schema_migrations`。

服务端经六个接口使用宿主：身份（验证令牌）、uid 映射、关系（是不是好友）、资料、媒体、运行配置。它们有两种实现，用 `TALKWIRE_HOST_MODE` 选：

| 取值 | 宿主 |
| --- | --- |
| `http`（默认） | 宿主是一个 HTTP 服务，地址是 `MAIN_API_BASE_URL` |
| `standalone` | 没有宿主进程；好友、资料、运行配置来自 `TALKWIRE_STANDALONE_CONFIG` 指向的文件，不给文件时所有人互为好友、没有资料、用内置配置。只支持 `groupjoin` 与 `e2ee` 两个模块 |

用户令牌默认用 `JWT_SECRET` 验；设了 `TALKWIRE_JWKS_URL` 时改用宿主公布的 JWKS 验 RS256、ES256 令牌，内部接口仍用 `JWT_SECRET`。配置项的完整列表见[环境变量](/server/env)。

### 客户端 SDK

Android 与 Web 两端的结构相同，行为都以协议规范为准：

| 层 | Android | Web |
| --- | --- | --- |
| 连接：登录、重连与退避、请求与应答的关联 | `talkwire-core` 的 `ImClient` | `@talkwire/client` 的 `ImClient` |
| 同步与本地状态：发送与重发、补拉、已读、在线、群 | `talkwire-core` 的 `ChatEngine` | `@talkwire/client` 的 `ChatEngine` |
| 本地存储 | `talkwire-storage-room`（Room） | `@talkwire/storage-idb`（IndexedDB） |
| 界面 | `talkwire-ui`（Compose） | `@talkwire/react`（React） |

框架只认 uid，人的名字、头像、谁是好友都归宿主。界面套件经一个接口 `ImUiHost` 向宿主要联系人、批量查资料、解析媒体地址，这是宿主在客户端唯一要实现的接口。`talkwire-core` 是纯 JVM 库，不依赖 Android、Room 和 Compose。

## uid

uid 是用户在线协议上的 id，由宿主用户 id（`appUserId`）推导：

```text
uid = "uid_" + base64url_nopad( SHA-256( UTF-8(appUserId) )[0:8] )
```

结果固定 15 个字符。服务端和宿主各自计算，结果必须相同；客户端也能本地算出任何用户的 uid，但不能反推宿主用户 id。

::: code-group

<<< @/../server/host/uids.go [服务端]

<<< @/../host-sdk-node/src/uid.ts [宿主]

:::

[`uid-vectors.json`](https://github.com/dishikun2786-prog/Talkwire/blob/main/contract/host-api/uid-vectors.json) 钉住了这个算法，任何实现都必须逐条复现。改算法会让已存的 uid 全部失效。宿主要能从 uid 查回用户（扫码加好友、按 uid 批量查资料、后台显示聊天记录），所以要把 uid 和用户一起存下来。

## topic

topic 是会话，有三种：

| 种类 | 形式 | 来源 |
| --- | --- | --- |
| 单聊 | `p2p_` 加 11 个字符 | 由两人的 uid 推导：两个 uid 按字节升序排列，取 `SHA-256(a + ":" + b)` 前 8 字节的 base64url（不补齐） |
| 群 | `grp_` 加 12 个字母或数字 | 服务端建群时生成，客户端当作不透明字符串 |
| 保留 | `me`、`fnd`、`*` | `*` 只用于在线状态查询，表示所有与我共享会话的人 |

`p2p_…` 与 `grp_…` 叫规范 topic，是服务端存储会话用的名字。同一个单聊在线上还会写成对方或自己的 uid，客户端要按规则映射回同一个会话，见[规范全文](/protocol/spec)第 5.5 节。

协议里没有“列出我的单聊”这个调用。单聊在第一次使用时由服务端向宿主核对好友关系后建立；宿主在关系被接受时调 `contact-sync`，服务端立即建好两人的单聊。客户端的单聊列表按宿主的联系人建立。

## seq

seq 是消息在 topic 内的序号：从 1 开始，严格递增，不复用。客户端用它排序、去重和补拉。

- 拉历史的 `get` 可以带 `afterSeq` 或 `beforeSeq`，消息总按 seq 升序返回；返回条数小于 `limit` 即到头，`limit` 默认 50。
- 撤回的消息不再返回，但它的 seq 不复用，会在本地留下空缺。客户端补拉时把服务端没返回的 seq 记为已撤回，下次从本地连续段的末尾接着拉，见[同步与补拉](/protocol/sync)。
- 发送时带 `clientMsgId`，同一 topic 下重复的 `clientMsgId` 得到原来的 seq，不会存两条。

## 订阅与同步

服务端只把推送发给已订阅对应 topic 的连接。订阅属于连接，断线即消失，登录不会恢复订阅；发 `sub`、`get`、`pub` 都会订阅该 topic。

所以客户端每次登录成功后都要重建状态：拉群列表，对每个会话从本地 seq 连续段的末尾往后补拉，按清空水位和已读位置校正本地。strict 档的服务端另有 `sync_summary`：一次往返列出全部会话的最新 seq 与清空、已读状态，并把连接订阅到全部会话，客户端只对有变化的会话再 `get`。

## 离线补投与推送

消息本身不排队：它们已经入库，客户端上线后用 `get` 补拉。服务端只为不在线的设备排队三类事件，在下次登录时补投：

| 事件 | 保留 | 回执 |
| --- | --- | --- |
| 会话被清空（`topic_cleared`） | 30 天 | `ack_topic_cleared`，不回执的每次登录都会重放 |
| 群事件（入群、被踢、解散等） | 30 天 | `ack_group_event`，同上 |
| 端到端加密的密文（`e2e_deliver`） | 默认 14 天，宿主可调 | 补投后即从服务端删除 |

宿主经 `custom-broadcast` 推的卡片状态更新（`custom_update`）不进离线队列，只发给此刻在线的连接；客户端打开卡片时要再向宿主拉一次当前状态。

离线推送只发给没有连接的设备注册过的推送令牌。客户端每次登录后用 `reg_token` 注册令牌，服务商可以是 `fcm`、`apns` 或 `webpush`。

## 多端在线

同一账号可以在多台设备上同时在线。客户端登录时带一个本机持久保存的设备名 `device`，只有同名设备的新登录才顶替旧连接；一个账号最多同时在 `TALKWIRE_MAX_DEVICES_PER_USER`（默认 10）台设备上在线。不带 `device` 的客户端和原版一样，同一账号的连接互相顶替。

自己发的消息、已读、撤回、清空等会同步到自己的其他设备，离线补投按设备记账。规则见[多端与推送](/protocol/devices-push)。

## 协议档位

服务端有两档协议，用 `TALKWIRE_PROTOCOL_PROFILE` 选，默认 `strict`：

- `legacy`：对外的帧与原版服务（小桃子的 IM 服务）逐帧一致，包括它的已知缺陷。迁移原版部署时用这一档，原版客户端不用升级。
- `strict`：修正已知缺陷 D1–D5、D7–D9，并加上新增字段（错误帧的 `errCode`，登录应答的 `protocol`、`capabilities` 等）和新增动作 `sync_summary`。新项目用这一档。

strict 档的登录应答带 `capabilities`，列出服务端支持的能力、已装的模块和已配置的推送服务商；客户端按它决定用不用新功能，并且要在两档下都能工作。见 [legacy 与 strict](/protocol/profiles)。

## 可选模块

核心之外的能力是四个可选模块：通话（`call`）、扫码入群（`groupjoin`）、群机器人（`groupai`）、端到端加密（`e2ee`）。每个模块都能单独装上、单独拿掉：

| 位置 | 装上 | 拿掉 |
| --- | --- | --- |
| 服务端 | `TALKWIRE_MODULES` 列出模块名；不设时装宿主支持的全部模块 | 从列表里去掉，或设为 `none`；没装的模块的动作得到 501 `module not enabled: <名>` |
| 宿主（Node SDK） | 给 `talkwireHostPlugin` 对应的选项（`calls`、`groupAi`、`config`） | 不给，对应端点答 404 |
| 客户端 | 装对应的包：Web 是 `@talkwire/<模块>`，Android 是 `talkwire-<模块>` | 不装，核心包不引用模块包 |

模块有自己的迁移脚本，装上时建表；拿掉模块不删表，再装上时接着用。显式列出的模块在宿主缺对应能力时，服务端启动即报错。

钱和规则归宿主：谁能打给谁、RTC 频道与凭证、通话计费、机器人回答什么，都由宿主决定，IM 只负责把信令、问题和卡片状态送到。各模块的细节见[音视频通话](/modules/call)等页面。
