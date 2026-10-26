---
title: "术语表"
description: "手册与协议规范里反复出现的术语，每个一句话。"
---

# 术语表

本页列出手册与[协议规范](/protocol/spec)里反复出现的术语，每个一句话；前八个取自规范第 1 节。

## 身份与会话

| 术语 | 含义 |
| --- | --- |
| 宿主 | 接入 Talkwire 的业务系统，负责账号、好友关系、资料、推送通道和媒体存储 |
| `appUserId` | 宿主侧的用户 id，任意字符串，Talkwire 不解释；契约里也叫宿主用户 id |
| uid | Talkwire 侧的用户 id，`"uid_"` 加 `SHA-256(appUserId)` 前 8 字节的 base64url，共 15 个字符 |
| topic | 会话，分单聊、群、保留 topic 三种 |
| 规范 topic | 服务端存储会话用的名字：单聊是 `p2p_…`，群是 `grp_…` |
| 保留 topic | `me`、`fnd` 与只用于在线状态查询的 `*` |
| seq | 消息在 topic 内的序号，从 1 开始，严格递增，不复用 |
| 连接 | 一条 WebSocket 连接 |
| 订阅 | 连接与 topic 的关联；服务端只把推送发给已订阅该 topic 的连接，断线即失效 |
| 设备名（`device`） | 客户端登录时带的、本机持久保存的名字；同名设备才互相顶替，不同设备可以同时在线 |

## 令牌

| 术语 | 含义 |
| --- | --- |
| 用户令牌（访问令牌） | 宿主签发给客户端、用来登录 IM 的 JWT：`sub` 是宿主用户 id，`type` 为 `access`，`role` 在 `TALKWIRE_TOKEN_USER_ROLES` 里 |
| 内部令牌 | IM 服务端与宿主互相调用时用的 HS256 JWT，claims 为 `role: internal_service`、60 秒后过期，密钥是共享的 `JWT_SECRET` |
| JWKS | 宿主公布的公钥集合；设了 `TALKWIRE_JWKS_URL` 时服务端用它验 RS256、ES256 用户令牌 |

## 协议

| 术语 | 含义 |
| --- | --- |
| 原版、参考服务 | Talkwire 从中提炼出来的小桃子 IM 服务；legacy 档与它逐帧一致，一致性测试以它为参照 |
| legacy 档 | 对外的帧与原版逐帧一致、连同已知缺陷的协议档位，迁移原版部署时用 |
| strict 档 | 修正已知缺陷并加上新增字段与动作的协议档位，服务端默认用它 |
| action 帧、type 帧 | 线协议的两族帧，分别以 `action`、`type` 字段区分；客户端先看 `type` |
| `errCode` | strict 档错误帧里稳定的小写下划线错误代码，客户端按它判断失败原因，不解析 `reason` 的文字 |
| capability（能力） | strict 档登录应答 `capabilities` 里的一项，如 `multi-device`、`sync-summary`、`module:call`、`push:fcm` |
| `clientMsgId` | 客户端为每条消息生成的唯一 id；同一 topic 下重复发送只存一条 |
| 清空水位 | `clearVersion` 与 `clearBeforeSeq`：会话被清空的次数与清到的 seq，客户端据此删本地消息 |
| 已读位置 | 某人在会话里已读到的 seq（含）；strict 档 `get` 应答里本人的那一项叫 `readSeq` |
| 连续段 | 本地已有、中间没有空缺的 seq 范围；已撤回的 seq 和清空水位以下都算在内，补拉从段尾开始 |
| 撤回墓碑 | 客户端在本地替被撤回的消息留下的一条记录，只有发送人与时间，不发给服务端 |
| `sync_summary` | strict 档的会话变更摘要：一次往返列出全部会话的变化，客户端只对有变化的会话 `get` |
| 补投 | 服务端在下次登录时补发设备离线期间排队的清空、群事件与加密密文，前两种要回执 |
| `custom_update` | 宿主经内部接口 `custom-broadcast` 推给在线连接的卡片状态更新，不进离线队列 |
| 已知缺陷（D 编号） | 规范第 18 节列出的原版协议缺陷 D1–D9，strict 档修正除 D6 外的全部各项 |
| F 编号 | 协议事实的逐条出处，见 [protocol-findings.md](https://github.com/dishikun2786-prog/Talkwire/blob/main/docs/protocol-findings.md) |

## 扩展

| 术语 | 含义 |
| --- | --- |
| 模块 | 可以单独装上、单独拿掉的能力：通话 `call`、扫码入群 `groupjoin`、群机器人 `groupai`、端到端加密 `e2ee` |
| standalone 宿主 | 不需要宿主进程的运行方式（`TALKWIRE_HOST_MODE=standalone`），好友、资料、运行配置来自一个文件 |
| 运行配置 | 宿主经 `runtime-config` 下发的群人数上限、消息长度、推送文案等，服务端每 60 秒刷新一次 |
| 宿主接口契约 | IM 服务端与宿主之间的两份 OpenAPI 3.1：宿主实现的 `host.openapi.yaml` 与 IM 暴露的 `im-internal.openapi.yaml` |
| `ImUiHost` | 客户端界面套件向宿主要联系人、资料和媒体地址的接口，Android 与 Web 同名 |
| 自定义消息类型 | 宿主自己的 `contentType` 与 `extra`（红包、订单卡片），客户端在消息类型注册表里注册，不进 SDK |

## 测试

| 术语 | 含义 |
| --- | --- |
| 黄金对话 | 用真实 WebSocket 录下、归一化后的逐帧对话，主要对原版服务录制；用来比对服务端实现，也用来回放测客户端 |
| 一致性测试台 | `tools/conformance`：用真实 WebSocket 驱动服务端，与黄金对话逐帧比较 |
| 回放服务器 | 按一份黄金对话扮演服务端、测客户端 SDK 的工具，可注入断线、延迟、重复、丢帧 |
| 红线 | 必须守住的行为，服务端与宿主 R1–R22、客户端 C1–C10；每条都对应计划中的测试用例，能录到的还有黄金对话 |
