---
title: "宿主接口契约"
description: "宿主与 IM 服务端之间的全部 HTTP 接口：服务端调宿主的端点、宿主调服务端的内部端点，以及两个方向共用的内部令牌。"
---

# 宿主接口契约

宿主是你的业务后端，IM 服务端靠它知道用户是谁、谁和谁是好友、每个人叫什么。两者之间只通过 HTTP 交谈，接口写在仓库的 [`contract/host-api/`](https://github.com/dishikun2786-prog/Talkwire/blob/main/contract/host-api/README.md) 里，共三个文件：

| 文件 | 内容 |
| --- | --- |
| [`host.openapi.yaml`](https://github.com/dishikun2786-prog/Talkwire/blob/main/contract/host-api/host.openapi.yaml) | 宿主实现、IM 服务端调用的端点 |
| [`im-internal.openapi.yaml`](https://github.com/dishikun2786-prog/Talkwire/blob/main/contract/host-api/im-internal.openapi.yaml) | IM 服务端暴露、宿主调用的内部端点 |
| [`uid-vectors.json`](https://github.com/dishikun2786-prog/Talkwire/blob/main/contract/host-api/uid-vectors.json) | 宿主用户 id 到线上 uid 的黄金向量，见 [签发 IM 令牌](/host/token#uid) |

两份 OpenAPI 都是 3.1 版。Node 宿主可以直接用 [Node 宿主 SDK](/host/node-sdk)；用别的语言实现时看 [用其他语言实现](/host/other-languages)。

## 宿主负责什么

IM 服务端只管连接、消息、群和推送，其余都归宿主：

- **身份**：宿主签发用户登录 IM 用的令牌，见 [签发 IM 令牌](/host/token)。
- **关系**：谁和谁是已接受的好友，以及各自给对方设的备注。
- **资料**：每个人的显示名和头像。
- **媒体**：消息里的文件 URL 由宿主签发，文件也存在宿主那里；IM 只存 URL。
- **业务**：积分、红包、通话计费等都在宿主，IM 只转发宿主给的结果。
- **运行配置**：群人数上限、推送文案等运行期设置由宿主下发。

宿主是一个 HTTP 服务时，IM 服务端设 `TALKWIRE_HOST_MODE=http`（默认），用 `MAIN_API_BASE_URL`（默认 `http://127.0.0.1:6001`）找到它。不需要宿主进程时可以用 `TALKWIRE_HOST_MODE=standalone`，好友、资料和运行配置改由 `TALKWIRE_STANDALONE_CONFIG` 指向的文件提供，详见 [环境变量](/server/env)。

## 鉴权：内部令牌

两个方向的调用都带同一种内部令牌：`Authorization: Bearer <令牌>`，令牌是用双方共享的 `JWT_SECRET` 签名的 HS256 JWT。用户登录 IM 用的是另一种令牌，与此无关。

| claim | 值 |
| --- | --- |
| `role` | `internal_service` |
| `exp` | 签发时刻加有效期。IM 服务端签 1 分钟的令牌；契约说参考宿主签 1 小时的，1 分钟已经够用 |

- **IM 调宿主**：宿主收到的每个请求都要验签（只认 HS256）、验 `role`、验过期，不通过就答 401 和 JSON `{"error": ...}`。
- **宿主调 IM**：宿主自己签一个同样的令牌。IM 拒绝时答 401，正文是纯文本 `unauthorized`；方法不对时答 405，正文也是纯文本；其余错误是 JSON `{"error": "..."}`。

::: warning 注意
IM 的内部端点与客户端的 `/ws/im` 在同一个端口（`IM_PORT`，默认 6062）上。不要让公网反向代理转发 `/api/v1/internal/` 下的路径；内部令牌校验只是第二道防线。
:::

## 契约里的 `x-talkwire`

每个操作的 `x-talkwire` 写明 IM 服务端对它的期待。IM 服务端从不重试失败的调用：超时、传输错误和 200 以外的任何状态都算失败，按 `onFailure` 处理。

| 键 | 含义 |
| --- | --- |
| `requirement` | `required`：核心功能需要；`optional`：没有也能运行；`module:<名>`：只有装了这个可选模块才会调用 |
| `timeoutSeconds` | IM 服务端等多久 |
| `onFailure` | 失败时 IM 服务端怎么办 |
| `cacheSeconds` | IM 服务端缓存应答多久（只有 `profile`） |
| `refreshSeconds` | IM 服务端多久重新拉一次（两份配置文档） |

200 的应答必须是 JSON，IM 不认识的字段会被忽略。宿主用户 id 是宿主自己选的不透明字符串，最长 128 个字符。

## 宿主实现的端点

这些端点由宿主实现，IM 服务端在 `MAIN_API_BASE_URL` 后面加路径调用，路径都以 `/api/v1/internal/im/` 开头：

| 端点 | 要求 | IM 何时调用 | 超时 | 失败时 IM |
| --- | --- | --- | --- | --- |
| `GET friendship?a=&b=` | required | 单聊、邀请入群、建群之前 | 3 s | 拒绝这个动作 |
| `GET profile?viewer=&sender=` | required | 给离线推送定标题和头像 | 3 s | 用兜底名字、不带头像 |
| `GET runtime-config` | optional | 启动时，之后每 60 秒 | 3 s | 沿用上一次的值 |
| `GET e2ee-config` | module:e2ee | 启动时，之后每 60 秒 | 3 s | 沿用上一次的值 |
| `POST delete-media` | optional | 撤回或清空之后，对不再被任何消息引用的文件 | 3 s | 只记日志，文件留着 |
| `POST calls/create` | module:call | 发起通话之前 | 3 s | 拒绝邀请（500） |
| `POST calls/answer` | module:call | 被叫接听时 | 8 s | 双方的通话以 `failed` 结束 |
| `POST calls/settle` | module:call | 通话进入 IM 判定的终态时 | 3 s | 只记日志，宿主要自己清扫 |
| `POST calls/upgrade` | module:call | 双方同意把语音升级为视频时 | 3 s | 拒绝升级（500），通话继续是语音 |
| `POST group-ai-qa` | module:groupai | 群成员 @ 群助手提问时 | 10 s | 当作超时，群里收到兜底文案 |

宿主不提供某个模块，就对该模块的端点答 404，并且不要在 IM 上装这个模块（`TALKWIRE_MODULES`）。装了而宿主不答，每次使用都会按 `onFailure` 失败。

### friendship

`friendship` 回答两个宿主用户之间是否有已接受的关系，应答是 `{"friends": true}` 或 `{"friends": false}`。关系是对称的：`(a, b)` 和 `(b, a)` 必须得到同一个答案。失败时，需要这次检查的动作得到错误帧 `cannot verify relationship` 或 `relationship check failed`。

### profile

`profile` 回答 viewer 看到的 sender 是什么样子，应答是 `{"displayName", "avatarUrl"}`，用作离线推送的标题和头像。`displayName` 先给 viewer 自己为 sender 设的备注，没有备注才给 sender 自己的显示名。

- 备注只在两人是已接受的好友时才给。备注是按方向存的私人数据：sender 给 viewer 设的备注绝不能返回。
- 宿主不认识 sender 时答 200 和两个空串，IM 改用运行配置里的 `pushText.sender`。
- `avatarUrl` 可以是绝对 URL 或相对宿主的路径；没有头像时给空串或 `null`。
- IM 服务端把同一对 viewer 与 sender 的应答缓存 300 秒。

::: warning 注意
备注的方向是最容易写反的地方。用两个账号各给对方设一个不同的备注，分别核对两边推送里的名字。
:::

### runtime-config 与 e2ee-config

两份配置文档在启动时拉一次，之后每 60 秒拉一次。非 200 的应答一律保留上一次的值，第一次成功之前用内置默认值；404 表示宿主没有这份文档。

运行配置的解码规则要特别注意：缺失或不是正数的整数字段保留内置默认值，但**缺失的布尔字段就是 `false`**，所以每个布尔字段都要显式给出。字段如下（全部可选）：

| 字段 | 含义 | 默认值 |
| --- | --- | --- |
| `maxMembers` | 群人数上限 | 500 |
| `maxAdmins` | 每个群的管理员数 | 10 |
| `nameMaxLen` | 群名长度（码点） | 30 |
| `messageMaxLen` | 消息长度（字节） | 20000 |
| `forwardBundleMax` | 合并转发的消息条数 | 50 |
| `forwardBroadcastMax` | 一次转发的目标数 | 20 |
| `historyPageSize` | 历史消息一页的最大条数 | 50 |
| `fcmEnabled` | 离线推送开关 | `true`，字段缺失时为 `false` |
| `fcmConcurrency` | 并行推送请求数 | 16 |
| `readReceiptEnabled` | 群已读游标 | `false` |
| `qrTtlSeconds` | 群二维码有效期 | 604800 |
| `publishRateLimitPer10s` | 一个用户每 10 秒能发的消息数 | 20 |
| `callRingTimeoutSeconds` | 振铃多久判为无人接听 | 45 |
| `callMaxDurationMinutes` | IM 服务端读取但不执行 | 60 |
| `pushText` | 离线推送文案：`image`、`video`、`audio`、`file`、`sender`、`assistant`、`joinRequest`（`{group}` 换成群名）、`joinApproved`、`joinRejected` | 空或缺失的条目保留默认值 |
| `groupAi` | 群助手的名字 `name`（默认“小桃子”）和答不了时的文案：`insufficientCredits`、`quotaExceeded`、`notConfigured`、`ownerInactive`、`rateLimited`、`busy` | 空或缺失的条目保留默认值；文案里的 `{name}` 换成助手名 |

端到端加密配置有三个字段：`enabled`、`groupEnabled`、`envelopeTtlHours`（至少 1）。内置默认值是 `enabled=true`、`groupEnabled=true`、`envelopeTtlHours=336`，见 [端到端加密](/modules/e2ee)。

### delete-media

`delete-media` 让宿主永久删除一批文件，请求体是 `{"urls": [...]}`，最多 500 个。IM 只在没有任何剩余消息引用这些 URL 时才调用它，宿主直接删除即可。

- 宿主只删自己签发的 URL 背后的文件，不认识的 URL 忽略。
- 删除不存在的文件不算错。
- 应答可以带 `{"deleted": <数量>}`，IM 不读应答体。

### 通话模块

通话模块的四个端点把是否允许通话、媒体凭证和计费交给宿主，详见 [音视频通话](/modules/call)。

| 端点 | 请求体 | 宿主答什么 |
| --- | --- | --- |
| `calls/create` | `callSessionId`、`callerAppUserId`、`calleeAppUserId`、`callType`（`voice` 或 `video`） | `{"ok", "reason"}`。IM 先生成 `callSessionId`、占住双方再问宿主；`ok=false` 时主叫收到带宿主 `reason` 的错误帧，`not_friends` 为 401，其余（例如 `insufficient_credits`）为 409 |
| `calls/answer` | `callSessionId`、`effectiveType` | `ok`、`reason`、`channelId`、`callerToken`、`calleeToken`、`videoQuality`、`denoiseEnabled`、`appId`。`effectiveType` 是此后的类型：被叫把视频邀请当语音接听时为 `voice`。两个令牌必须不同，各自编码自己的参与者；`appId` 对双方相同。`ok=false` 时通话以 `failed` 结束，被叫的错误帧带宿主的 `reason` |
| `calls/settle` | `callSessionId`、`reason`（`user_hangup`、`no_answer`、`rejected`、`cancelled`、`disconnected`、`failed`） | 任意 JSON 对象，IM 不读 |
| `calls/upgrade` | `callSessionId`、`toType`（固定为 `video`） | `{"ok", "reason", "videoQuality"}`。`ok=false` 时升级以 409 和宿主的 `reason` 被拒，通话继续是语音 |

### group-ai-qa

`group-ai-qa` 是群助手的钩子：群成员写“@”加助手的名字（运行配置 `groupAi.name`）提问，IM 先在群里发 `ai_typing`，再把问题交给宿主。回答什么、扣不扣费都由宿主决定，IM 把回答以助手身份发进群，详见 [群助手钩子](/modules/bot-hook)。

请求体有 `groupTopic`（以 `grp_` 开头）、`askerImUid`、`question`、`groupOwnerAppUserId`、`kbIds`、`docIds`。应答规则：

- 能回答时给 `{"ok": true, "answer": "..."}`。
- 业务上答不了时仍然答 200，给 `ok=false` 和 `reason`：`insufficient_credits`、`quota_exceeded`、`not_configured`、`owner_inactive`、`rate_limited`，其他原因一律用 `busy` 文案。
- 可以再带 `message` 作为宿主自己的说法，IM 把它发进群代替配置的文案；`ok=true` 时 `message` 被忽略。
- 200 以外的状态、超时和传输错误都按技术故障处理，群里收到 `busy` 文案。

## IM 服务端暴露的端点

这些端点由 IM 服务端在 `IM_PORT`（默认 6062）上提供，宿主调用：

| 端点 | 要求 | 作用 |
| --- | --- | --- |
| `POST /api/v1/internal/im/contact-sync` | required | 关系被接受或解除 |
| `POST /api/v1/internal/im/media-ref-count` | optional | 查一个媒体 URL 还被多少条消息引用 |
| `POST /api/v1/internal/im/topic-members-resolve` | optional | 按宿主用户 id 查群成员身份，按 uid 反查宿主用户 id |
| `POST /api/v1/internal/im/custom-broadcast` | optional | 把宿主卡片的新状态推给一个会话或一个用户 |
| `POST /api/v1/internal/im/red-packet-broadcast` | optional，已弃用 | 参考部署的旧写法，新宿主用 `custom-broadcast` |
| `POST /api/v1/internal/im/call-broadcast` | module:call | 把宿主决定的通话帧推给一个用户的所有设备 |
| `GET /api/v1/internal/admin/im/topics` | optional | 后台：一个用户的会话列表 |
| `GET /api/v1/internal/admin/im/topic` | optional | 后台：一个会话 |
| `GET /api/v1/internal/admin/im/messages` | optional | 后台：一个会话的消息 |
| `POST /api/v1/internal/admin/im/clear` | optional | 后台：为双方清空一个单聊 |

### contact-sync

宿主必须在关系被接受或解除时调用 `contact-sync`，请求体是 `{"appUserIdA", "appUserIdB", "event"}`，`event` 为 `accepted` 或 `removed`。应答是 `{"topic", "uidA", "uidB"}`，`topic` 是两人单聊的规范 topic（`p2p_…`）。

- `accepted`：建好两人的单聊会话，把两人设为成员，并告诉两个在线的用户。
- `removed`：把两人移出这个会话，历史记录保留。
- 两个用户在 IM 这边不存在时会先建出来。

不调用的后果是：新好友之间收不到彼此的在线状态，第一次聊天还要等一次好友校验。

### custom-broadcast

`custom-broadcast` 把宿主卡片的状态变化或宿主事件实时推给客户端，例如红包被领了、有人加你好友。请求体有 `kind`、`payload`，以及 `topic` 和 `targetUid` 二者之一（不能同时给）：

- 给 `topic`：订阅了这个会话的所有连接收到帧。
- 给 `targetUid`：这个用户的所有连接收到帧，帧里没有 `topic`；应答里的 `delivered` 为 `false` 表示该用户当时没有连接。

IM 原样转发，帧是 `{"action":"custom_update","topic":...,"what":<kind>,"extra":<payload>}`。`kind` 匹配 `^[a-z][a-z0-9_.-]{0,63}$`，`payload` 可以是除 `null` 外的任何 JSON 值，整个请求不超过 64 KiB（超出答 413）。

这个推送只发给此刻在线的连接，不进离线队列，所以客户端打开卡片时还要向宿主拉一次当前状态。完整的例子是 [`examples/red-packet/`](https://github.com/dishikun2786-prog/Talkwire/blob/main/examples/red-packet/README.md)，客户端怎么显示自定义卡片见 [自己的消息类型](/client/message-types)。

### call-broadcast

`call-broadcast` 用于通话模块里宿主自己做出的决定，例如通话中的余额更新、宿主结束的通话（`credit_exhausted`、`max_duration_reached`）。请求体是 `{"targetUid", "payload"}`，`payload` 必须已经是客户端认识的完整帧，IM 逐字节发出。

`payload` 的 `type` 为 `call_ended` 时，IM 也会释放这通通话。应答里的 `delivered` 为 `false` 表示该用户没有在线设备，不会排队。

### media-ref-count 与 topic-members-resolve

`media-ref-count` 回答一个 URL 还被多少条消息引用（`{"count"}`），已撤回的消息不计。转发的消息与原消息共用 URL，所以宿主自己删文件之前（例如用户放弃了一个没发出去的上传）要先确认引用数为 0。

`topic-members-resolve` 一次回答两类问题，请求体是 `topic` 加可选的 `checkAppUserIds`、`resolveUids`：

- `members`：`checkAppUserIds` 里每个宿主用户 id 是否是这个会话的成员。
- `resolved`：`resolveUids` 里每个 uid 背后的宿主用户 id（IM 从没见过这个 uid 时为 `null`）以及是否是成员。

### 后台端点

后台四个端点供宿主的管理后台查看和清空聊天记录，前端页面见 [后台页面](/modules/admin)。

| 端点 | 参数 | 应答 |
| --- | --- | --- |
| `topics` | `uid`、`limit`（1–100，默认 50） | 这个用户的会话，最近的在前 |
| `topic` | `topic` | 一个会话：成员 uid、最后一条消息、消息数、清空版本等 |
| `messages` | `topic`，可选 `beforeSeq`、`limit`（1–100，默认 50）、`type`、`keyword`、`fromMs`、`toMs` | 一页消息，新的在前，用 `beforeSeq` 往前翻；已撤回的消息不返回 |
| `clear` | `{"topic"}` | `deletedMessages`、`deletedMedia`、`clearVersion`、`clearBeforeSeq` |

`clear` 为两个人永久删除一个单聊的全部消息，请宿主删除不再被引用的媒体，并告诉双方（在线的立刻，离线的在下次登录时）；对群答 409。清空前要求操作者确认、记录审计都是宿主的事：宿主应要求超级管理员、输入确认语、写明理由，用幂等键防止重复执行，审计只记元数据、不记消息内容。
