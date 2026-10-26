---
title: "帧格式与错误码"
description: "帧的两族与公共字段、错误码 code 与 errCode 的取值，以及按功能分组的全部客户端动作与服务端帧。"
---

# 帧格式与错误码

每帧是一个 JSON 对象，用 `action` 或 `type` 区分种类，错误用 `code` 表示，strict 档另带稳定的 `errCode`（规范第 3、4 节）。本页列出公共字段、全部错误码和全部帧名；每个动作的完整字段见[规范全文](/protocol/spec)和 JSON Schema。

## 两族帧

一帧只会有 `action` 或 `type` 其中一个判别字段（规范第 3.1 节）。

| 族 | 判别字段 | 用于 |
| --- | --- | --- |
| action 帧 | `action` | 绝大多数请求、应答和推送 |
| type 帧 | `type` | 转发结果、群 AI 设置、E2EE 密钥相关应答、通话信令推送 |

客户端解析时**必须**先看 `type`，没有再看 `action`（规范第 3.1 节）。客户端发出的请求一律是 action 帧；其中一部分用 `reqId` 关联，应答是 type 帧。

## 公共字段

下表是跨动作出现的字段；某个动作独有的字段见规范对应小节。

| 字段 | 出现在 | 说明 | 节 |
| --- | --- | --- | --- |
| `action` | action 帧 | 动作名。应答与请求同名，推送用自己的名字 | 3.1、3.4 |
| `type` | type 帧 | 帧种类 | 3.1 |
| `id` | 请求、应答 | 客户端生成的字符串，同一连接内唯一；应答原样带回。推送不带 | 3.3、3.4 |
| `reqId` | type 帧族的请求与应答 | 请求里放 `reqId`，应答带回同一个 | 3.3 |
| `code` | action 帧族的应答 | 状态码，见下文 | 4.1 |
| `reason` | 错误应答 | action 帧族里是给人看的不透明文本；type 帧族里是固定的小写英文枚举 | 4.1 |
| `errCode` | action 帧族的错误应答，仅 strict 档 | 小写下划线形式的稳定代码 | 4.3 |
| `ok` | type 帧族的应答 | `false` 时带 `reason` | 4.1 |

`id` 有三处特别（规范第 3.3、11.3 节）：

- 成功的 `login` 和 `pres` 快照应答在 legacy 档不带 `id`，按 `action`（快照另加 `what=snapshot`）关联；strict 档带 `id`。
- 失败的 `login` 和失败的 `pres` 在两档下都带 `id`。
- 登录后补投的 `topic_cleared` 带的 `id` 是离线记录的编号（数字字符串），`ack_topic_cleared` 用同一个字段把它带回；它不是请求关联用的 id。

字段为零值时服务端通常省略，客户端**必须**把缺省当作零值；type 帧里声明为必有的字段（如 `ok`、`found`、`results`）即使为零值也出现（规范第 3.2 节）。双方**必须**忽略不认识的字段。

## 错误帧

action 帧族的错误应答带 `code` 与 `reason`，`action` 与请求相同（规范第 4.1 节）。下面是黄金对话 [`core/errors`](https://github.com/dishikun2786-prog/Talkwire/blob/main/contract/protocol/transcripts/core__errors.json) 第 5 步对未知动作的应答（legacy 档）：

```json
{"action": "no_such_action", "code": 400, "id": "s5", "reason": "unknown action: no_such_action"}
```

strict 档的同类帧多一个 `errCode`。下例取自 [`transcripts-strict/core__leave-grp.json`](https://github.com/dishikun2786-prog/Talkwire/blob/main/contract/protocol/transcripts-strict/core__leave-grp.json) 第 11 步，从未入群的用户发 `leave_grp`：

```json
{"action": "leave_grp", "code": 401, "errCode": "not_member", "id": "s11", "reason": "not a member of this conversation"}
```

type 帧族的错误不用 `code`，而是 `ok: false` 加 `reason`，这里的 `reason` 可以用于判断（规范第 4.1 节）。

### `code` 的取值

客户端**必须**只根据 `code`（以及规范第 9 节明确列出的少数 `reason` 值）做逻辑判断，不得解析其他 `reason` 文本：`reason` 有中文也有英文，措辞随版本变化（规范第 4.1 节）。

| `code` | 含义 |
| --- | --- |
| 200 | 成功 |
| 201 | 已创建（只用于 `create_grp`） |
| 400 | 请求不合法 |
| 401 | 未登录，或没有权限（不是成员、不是好友、角色不够） |
| 404 | 对象不存在 |
| 409 | 状态冲突（如群主不能直接退群、群已加密） |
| 429 | 超过频率限制 |
| 500 | 服务端内部错误，可以重试 |
| 501 | 动作属于服务端没有安装的模块，`reason` 为 `module not enabled: <模块名>` |

501 是 Talkwire 服务端才有的，参考服务没有模块开关（规范第 4.1 节）。模块名是 `call`、`groupjoin`、`groupai`、`e2ee`，用 `TALKWIRE_MODULES`（逗号分隔的模块名，或 `none`）选择安装哪些，不设时安装宿主支持的全部（[`server/config.go`](https://github.com/dishikun2786-prog/Talkwire/blob/main/server/config.go)，见[环境变量](/server/env)）。

### 协议层错误

帧本身有问题时，服务端按下表处理（规范第 4.2 节）。

| 情况 | 服务端行为 |
| --- | --- |
| 帧不是合法 JSON | 回 `{"action":"error","code":400,"reason":"invalid JSON: …"}`，不带 `id`，连接继续可用 |
| `action` 为空 | 静默丢弃 |
| 未知 `action` | 回 400，`reason` 为 `unknown action: <名>`，带 `id` |
| 未登录就发其他请求 | 回 401 `not authenticated`，带 `id`；没有应答的那类动作直接丢弃 |
| 单帧超过 256 KB | 直接断开连接，不发错误帧（规范第 2 节） |

## `errCode`

`errCode` 是 strict 档错误帧里不随措辞变化的代码，客户端**应当**优先按它判断（规范第 4.1、4.3 节）。客户端**必须**容忍不认识的 `errCode`，退回按 `code` 处理。

服务端按以下顺序得出 `errCode`（规范第 4.3 节）：

1. `reason` 是下表对应的固定文案之一时，取表中的值。
2. `reason` 以 `not friends: `、`unknown action: `、`invalid JSON: `、`module not enabled: ` 开头时，分别取 `not_friends`、`unknown_action`、`invalid_json`、`module_not_enabled`。
3. `reason` 本身已是小写下划线形式（E2EE 的原因、宿主给的通话原因）时原样使用。
4. 否则按 `code` 取通用值：400 `bad_request`、401 `unauthorized`、404 `not_found`、409 `conflict`、429 `rate_limited`、501 `module_not_enabled`，其余 `internal_error`。

| 分类 | `errCode` | 含义 |
| --- | --- | --- |
| 登录 | `not_authenticated` | 未登录 |
| | `token_required` | 登录缺令牌 |
| | `invalid_token` | 令牌无效、过期，或 strict 档下不是访问令牌 |
| | `already_logged_in` | 连接已登录 |
| | `invalid_device` | 登录的 `device` 不合法 |
| | `too_many_devices` | 账号在线设备已达上限，新设备不能登录 |
| 请求 | `bad_request` | 请求不合法（通用） |
| | `missing_field` | 缺必填字段 |
| | `message_too_long` | 消息超长 |
| | `group_name_too_long` | 群名超长 |
| | `rate_limited` | 超过频率限制 |
| | `unsupported_topic` | 不能对 `me`、`fnd` 做这个操作 |
| | `not_a_group` | 只能对群做的操作用在了单聊上 |
| | `invalid_member_role` | 角色不合法 |
| | `invalid_call_type` | 通话类型不是 `voice` 或 `video` |
| | `cannot_target_self` | 不能对自己做（踢自己、改自己角色、呼叫自己） |
| | `target_not_member` | 对象不是群成员 |
| 关系与权限 | `not_friends` | 不是好友 |
| | `relationship_check_failed` | 向宿主核对关系失败，可以重试 |
| | `not_authorized` | 没有权限（通用） |
| | `not_member` | 不是该会话的成员 |
| | `not_group_manager` | 需要群主或管理员 |
| | `not_group_owner` | 需要群主 |
| 群 | `cannot_remove_owner` | 不能踢群主 |
| | `owner_cannot_leave` | 群主要先转让或解散才能退群 |
| | `admin_limit_reached` | 管理员人数已达上限 |
| | `group_not_found` | 群不存在 |
| | `group_full` | 群人数已达上限 |
| 消息 | `message_not_found` | 要撤回的消息不存在、已撤回或不是自己发的 |
| 扫码入群 | `qr_token_invalid` | 二维码令牌不存在或已过期 |
| | `join_request_handled` | 入群申请已被处理 |
| | `join_request_mismatch` | 入群申请不属于请求里的群 |
| 通话 | `call_not_found` | 通话不存在、已结束，或本设备不在这通电话里 |
| | `call_ended` | 通话已结束 |
| | `call_already_handled` | 通话已被接听、拒接或结束 |
| | `call_not_active` | 通话尚未接通 |
| | `already_video` | 已经是视频通话 |
| | `offline` | 对方不在线 |
| 协议层 | `unknown_action` | 未知动作 |
| | `invalid_json` | 帧不是合法 JSON |
| | `module_not_enabled` | 动作所属的模块没有安装 |

`reason` 到 `errCode` 的权威映射是服务端 [`server/profile.go`](https://github.com/dishikun2786-prog/Talkwire/blob/main/server/profile.go) 的 `errCodes` 表；单测 `TestEveryErrorReasonHasACode` 保证服务端发出的每个固定 `reason` 都在表里（规范第 4.3 节）。type 帧族的错误本来就用小写英文的 `reason`，没有 `errCode`。

### type 帧族的 `reason`

type 帧族应答失败时带 `ok: false` 和小写英文的 `reason`。规范为下列应答写明了取值：

| 应答 | 规范列出的 `reason` | 节 |
| --- | --- | --- |
| `forward_bundle_result` | `not_authenticated`、`bad_request`、`too_many_messages`、`not_member`、`source_not_found`、`forward_chain_too_deep`、`internal_error` | 9.6 |
| `forward_broadcast_result` | 每个目标一项；目标写成对方 uid 时得到 `topic_not_found`。整体失败时每个目标带同一个 `reason` | 9.6 |
| `e2e_publish_bundle_result` | 缺必需的公钥字段时 `bad_request` | 13.1 |
| `e2e_set_group_encrypted_result` | `not_owner`、`topic_not_found`、`bad_request`、`e2ee_disabled`、`group_e2ee_disabled` | 13.3 |
| `update_group_ai_settings_result` | `not_owner`、`topic_not_found`、`bad_request`、`internal_error` | 15.1 |

服务端源码里还有表中没列出的取值，例如 `e2e_publish_bundle_result` 的 `not_authenticated`、`e2ee_disabled`、`internal_error`（[`server/e2ee.go`](https://github.com/dishikun2786-prog/Talkwire/blob/main/server/e2ee.go)）。客户端遇到不认识的 `reason` 时按失败处理即可。

## 动作与服务端帧一览

客户端可发 51 个动作，服务端可发 56 个 action 名和 15 个 type 名，以两份 JSON Schema 为准（[`client-frames.schema.json`](https://github.com/dishikun2786-prog/Talkwire/blob/main/contract/protocol/schema/client-frames.schema.json)、[`server-frames.schema.json`](https://github.com/dishikun2786-prog/Talkwire/blob/main/contract/protocol/schema/server-frames.schema.json)）。下表按功能分组；“应答”一列写“同名”的，应答是与请求同名的 action 帧。

| 领域 | 客户端动作 | 应答 | 服务端推送 | 规范 |
| --- | --- | --- | --- | --- |
| 协议层 | 任意帧 | 帧不是合法 JSON 时回 `error`，不带 `id` | — | 4.2 |
| 连接 | `login` | 同名 | 被顶替的旧连接可能收到 401 的 `login`（`logged in from another device`） | 6 |
| 订阅 | `sub`、`leave` | 同名（`leave` 未登录或缺 `topic` 时无应答） | 单聊 `sub`、`get` 之后一帧对方的 `pres` | 7、9.4 |
| 消息 | `pub`、`get`、`del` | 同名 | `data`、`recalled` | 9.1–9.5 |
| 转发 | `forward_bundle`、`forward_broadcast` | type 帧 `forward_bundle_result`、`forward_broadcast_result`，按 `reqId` | 目标会话的 `data` | 9.6 |
| 同步 | `sync_summary`（仅 strict 档） | 同名 | — | 8.1 |
| 清空 | `del_topic`、`ack_topic_cleared`（无应答） | 同名 | `topic_cleared` | 11 |
| 在线与输入 | `pres`、`note`（无应答） | `pres` 快照（`what=snapshot`）；`note` 只在非成员发送时回 401 | `pres`、`info` | 12 |
| 群 | `create_grp`、`invite`、`kick`、`leave_grp`、`update_grp`、`set_member_role`、`transfer_owner`、`disband_grp`、`get_groups`、`get_members`、`ack_group_event`（无应答） | 同名 | `grp_sync`、`grp_disband` | 10 |
| 推送令牌 | `reg_token`、`unreg_token`（无应答） | — | — | 17 |
| 宿主触发 | — | — | `contact_sync`、`custom_update`、`red_packet_update`，type 帧 `call_ended` | 附录 B |
| 扫码入群（模块 `groupjoin`） | `group_qr_token`、`group_qr_resolve`、`group_join`、`group_join_requests_list`、`group_join_review`、`get_group_join_settings`、`update_group_join_settings` | 同名 | `group_join_request`、`group_join_reviewed`、`grp_sync` | 14 |
| 群 AI（模块 `groupai`） | `get_group_ai_settings`、`update_group_ai_settings` | type 帧 `group_ai_settings`、`update_group_ai_settings_result` | type 帧 `ai_typing`；`senderKind` 为 `ai_assistant` 的 `data` | 15 |
| 端到端加密（模块 `e2ee`）：密钥与群加密 | `e2e_publish_bundle`、`e2e_fetch_bundle`、`e2e_otpk_count`、`e2e_set_group_encrypted` | type 帧 `e2e_publish_bundle_result`、`e2e_bundle`、`e2e_otpk_count_result`、`e2e_set_group_encrypted_result` | `e2e_set_group_encrypted` | 13.1、13.3 |
| 端到端加密（模块 `e2ee`）：密文与会话 | `e2e_send`、`e2e_clear`、`e2e_terminate`、`e2e_group_clear` | 同名 | `e2e_deliver`、`e2e_cleared`、`e2e_terminated`、`e2e_group_cleared` | 13.2、13.4 |
| 通话（模块 `call`） | `call_invite`、`call_accept`、`call_reject`、`call_cancel`、`call_hangup`、`call_upgrade`、`call_upgrade_accept`、`call_upgrade_reject` | 同名 | type 帧 `call_ring`、`call_answered`、`call_ended`、`call_upgrade_request`、`call_upgraded`、`call_upgrade_declined` | 16 |

模块的说明见[音视频通话](/modules/call)、[扫码入群](/modules/group-join)和[端到端加密](/modules/e2ee)。没有安装的模块的动作得到 501（见上文）。
