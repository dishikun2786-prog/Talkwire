---
title: "用其他语言实现"
description: "不用 Node 时怎样照着 OpenAPI 契约实现宿主：内部令牌、uid、必须与可选的端点、要通知 IM 的事，以及用契约测试和黄金对话验证。"
---

# 用其他语言实现

宿主契约与语言无关：两份 OpenAPI 3.1 文件加一份 uid 黄金向量，就是 IM 服务端对宿主的全部要求。任何能提供 HTTP 服务、能签 HS256 JWT、能算 SHA-256 的语言都可以实现宿主，最后用仓库里的一致性测试台检查它与真实 IM 服务端配合起来是否与参照实现逐帧一致。

本页按实现顺序列出要做的事，每个端点的细节见 [宿主接口契约](/host/)。权威文本是 [`contract/host-api/README.md`](https://github.com/dishikun2786-prog/Talkwire/blob/main/contract/host-api/README.md) 和两份 OpenAPI 文件；Node 的参考实现是 [Node 宿主 SDK](/host/node-sdk)，遇到契约没说清的地方可以对照它的源码。

## 1. 内部令牌

宿主与 IM 服务端互相调用时带同一种令牌：HS256 JWT，密钥是双方共享的 `JWT_SECRET`，claims 为 `{"role": "internal_service", "exp": <现在 + 60 秒>}`。

- **收**：宿主收到的每个请求都带 `Authorization: Bearer <令牌>`。验签时只认 HS256，再验 `role` 和过期，任何一项不通过都答 401 和 JSON `{"error": ...}`。
- **发**：宿主调 IM 的内部端点时自己签一个同样的令牌。IM 的 401 和 405 应答是纯文本，不是 JSON，解析错误时要兼顾这两种。

客户端登录 IM 用的用户令牌是另一回事，claims 与校验规则见 [签发 IM 令牌](/host/token)。

## 2. uid

IM 不认识宿主的用户 id，线上只用 uid，算法固定：

```
uid = "uid_" + base64url_nopad( SHA-256( UTF-8(appUserId) )[0:8] )
```

结果固定 15 个字符。把 [`uid-vectors.json`](https://github.com/dishikun2786-prog/Talkwire/blob/main/contract/host-api/uid-vectors.json) 读进你的单元测试，逐条核对，每一条都必须相同；向量里有空格、中文、带变音符号的字符、UUID 和一个很长的 id。服务端（`server/host/uids_test.go`）、Node 宿主 SDK（`host-sdk-node/test/uid-token.test.ts`）、Android 与 Web 客户端都用同一个文件做这件事。

宿主要能从 uid 查回用户（按 uid 批量查资料、扫码加好友、后台显示聊天记录），所以把 uid 和用户一起存下来。改动算法会让已存的 uid 全部失效。

## 3. 必须实现的端点

只有两个端点是核心功能必需的：

| 端点 | 超时 | 宿主答什么 | 失败时 IM |
| --- | --- | --- | --- |
| `GET /api/v1/internal/im/friendship?a=&b=` | 3 s | `{"friends": true}` 当且仅当两人的关系已被接受 | 拒绝需要这次检查的动作 |
| `GET /api/v1/internal/im/profile?viewer=&sender=` | 3 s | `{"displayName", "avatarUrl"}` | 通知用兜底名字、不带头像 |

`profile` 先给 **viewer 自己为 sender 设的备注**（只在两人是已接受的好友时），没有就给 sender 的显示名；不认识的 sender 答两个空串。备注是按方向存的私人数据，A 给 B 的备注只有 A 看得到。

::: warning 注意
备注方向是最容易写反的地方。用两个账号各给对方设一个不同的备注，分别核对推送里的名字。
:::

## 4. 可选与模块端点

其余端点要么是可选的，要么只有装了某个模块才会被调用：

| 端点 | 用到它的 | 超时 | 失败时 IM |
| --- | --- | --- | --- |
| `GET runtime-config` | 核心（可选） | 3 s | 沿用上一次的值；404 表示宿主没有这份文档，用内置默认值 |
| `GET e2ee-config` | 端到端加密模块 | 3 s | 沿用上一次的值 |
| `POST delete-media` | 核心（可选） | 3 s | 只记日志，文件留着 |
| `POST calls/create`、`answer`、`settle`、`upgrade` | 通话模块 | 3 s（`answer` 8 s） | 见契约里各自的 `onFailure` |
| `POST group-ai-qa` | 群 AI 模块 | 10 s | 当作超时，群里收到兜底文案 |

宿主对不提供的模块端点答 404，同时不要在 IM 上装这个模块（`TALKWIRE_MODULES`）。装了而宿主不答，每次使用都会失败。

运行配置每 60 秒刷新一次，按“零值结构”解码：缺的整数字段被修正为默认值，**缺的布尔字段就是 `false`**，所以 `fcmEnabled`、`readReceiptEnabled` 要显式给出。字段与默认值见 [宿主接口契约](/host/#runtime-config-与-e2ee-config)。

## 5. 要通知 IM 的事

有些事 IM 服务端自己看不到，宿主要主动调它的内部端点（默认端口 6062）：

- **好友关系被接受或解除时调 `contact-sync`**，这是必须的。IM 只在这时建好两人的单聊会话；不调的话，新好友之间收不到彼此的在线状态，第一次聊天要等一次好友校验。
- **宿主卡片状态变了**（红包被领、订单状态变了），或要让一个用户的各个设备立刻知道某件事（有人加你好友），调 `custom-broadcast`。帧只发给此刻在线的连接，不进离线队列，所以客户端打开卡片时还要向宿主拉一次当前状态。
- **通话模块里宿主自己结束通话**（余额用完、超时），调 `call-broadcast`。
- **后台查看、清空聊天记录**用 `admin/*` 四个端点。清空会永久删除消息和文件：在宿主这边要求超级管理员、输入确认语、写明理由，并用幂等键防止重复执行；审计只记元数据，不记消息内容。

## 6. 媒体

消息里的文件 URL 由宿主签发，IM 只存 URL，文件的读写、权限和删除都在宿主：

- 读取时带 `Content-Length`，不带的话部分客户端的缓存不落盘。
- 私密文件用 `Cache-Control: private, no-store`，不进 CDN，否则消息撤回后文件仍能访问。
- 文件名不可猜。
- `delete-media` 只在没有任何剩余消息引用这些 URL 时才会被调用，直接删除即可；不认识的 URL 忽略，删除不存在的文件不算错。
- 宿主自己删文件之前（例如用户放弃了一个上传后没发出去的文件），先调 `media-ref-count` 确认引用数为 0。

## 7. 数据

宿主要存三样东西：

- **资料**：包括 uid，以及唯一、不分大小写的 handle。
- **关系**：每对用户最多一条，记录谁先发起、状态、各自的备注、谁拉黑的。
- **后台审计**：只记元数据。

Node SDK 的表结构在 [`host-sdk-node/prisma/talkwire.sql`](https://github.com/dishikun2786-prog/Talkwire/blob/main/host-sdk-node/prisma/talkwire.sql)，可以照着建；那份 SQL 里 `id`（uuid）和 `updated_at` 由应用填写。

## 验证

验证分两层：契约测试检查每个请求和应答的形状，黄金对话检查宿主与真实 IM 服务端配合起来的行为。

### 契约测试

对两份 OpenAPI 各写一组测试，覆盖三件事：

1. 你的宿主对 `host.openapi.yaml` 每个操作的应答都能通过它的 Schema。
2. 你调 IM 时发出的请求符合 `im-internal.openapi.yaml`。
3. IM 回给你的应答也符合 `im-internal.openapi.yaml`。

Node 下的写法在 `host-sdk-node/test/`：`host-plugin.test.ts`、`goldens-contract.test.ts`、`im-client-contract.test.ts`，校验工具是同目录的 `openapi.ts`（用 ajv 的 2020-12 草案校验器和 yaml 解析器加载契约）。

`goldens-contract.test.ts` 用的数据值得借用：黄金对话（`contract/protocol/transcripts/` 与 `transcripts-strict/`）的每一步都记录了 IM 服务端对宿主的调用（`hostCalls`），是一份现成的真实请求样本。

### 黄金对话

一致性测试台用真实 WebSocket 驱动 IM 服务端，把收到的帧与录好的黄金对话逐帧比较，说明见 [`tools/conformance/README.md`](https://github.com/dishikun2786-prog/Talkwire/blob/main/tools/conformance/README.md)。加 `-host <URL>` 时它不起自带的假宿主，而是让被测 IM 服务端的 `MAIN_API_BASE_URL` 指向你的宿主，于是黄金对话检查的就是你的宿主。

为此宿主要实现一组**一致性控制接口**，挂在 `<URL>/__conformance/` 下。全是 `POST`、JSON 请求体，鉴权与 IM 调宿主相同（内部令牌），应答 200 即成功，其余状态码让场景失败：

| 路径 | 请求体 | 作用 |
| --- | --- | --- |
| `reset` | `{}` | 清空关系、资料、两份配置文档和调用记录 |
| `friends` | `{"a","b","friends":bool}` | `true`：两人成为已接受的好友；`false`：删除两人的关系 |
| `profile` | `{"user","displayName","avatarUrl"}` | 设置资料；`avatarUrl` 为空串表示没有头像 |
| `runtime-config`、`e2ee-config` | `{"document": 对象或 null}` | 设置配置文档；`null` 表示没有（宿主对该端点答 404） |
| `calls` | `{}` | 答 `{"calls":[…]}` 并清空：自上次以来 IM 对宿主的每次调用 |

`calls` 里每次调用形如 `{"method","path","query","body","status"}`：`query` 是排好序的字符串表（没有则省略），`body` 是请求体 JSON（没有则省略）。控制接口自身和两个配置端点的周期刷新不记录。

::: warning 注意
控制接口能任意改写关系和资料，只能在测试环境打开。
:::

通话和群 AI 模块的业务应答属于宿主自己，要比对就得给出与假宿主相同的值：

- 通话凭证：`channelId` 为 `fake-channel`，两个令牌为 `fake-rtc-caller`、`fake-rtc-callee`，`appId` 为 `fake-app`；`videoQuality` 接听时为 `sd`、升级时为 `hd`。
- 不是好友的两人发起通话：`ok=false`，`reason` 为 `not_friends`。
- 群助手：问题里含 `quota` 时答 `quota_exceeded`，其余答 `fake answer`。

测试台需要 Go 1.26.4 及以上和一个本机 PostgreSQL 测试库（默认 `127.0.0.1:55432`，用户 `postgres`，口令从 `PGPASSWORD` 或 pgpass 文件读取）。构建测试台和服务端：

```bash
cd server
go build -o bin/talkwire-server.exe .
```

```bash
cd tools/conformance
go build -o bin/conformance.exe ./cmd/conformance
```

启动你的宿主后，用 `check -host` 跑全部场景。下面的例子里，宿主是 Node SDK 的 [`host-sdk-node/conformance/host.ts`](https://github.com/dishikun2786-prog/Talkwire/blob/main/host-sdk-node/conformance/host.ts)，监听 17101 端口：

```bash
./bin/conformance.exe check -bin ../../server/bin/talkwire-server.exe -db talkwire_new -host http://127.0.0.1:17101   # 33/33；内存版 --store memory 同样 33/33
```

- **密钥**：测试台用自己的测试密钥（`tools/conformance/cmd/conformance/main.go` 里的常量 `testSecret`）签控制接口的令牌，也把它设为被测服务端的 `JWT_SECRET`，所以你的宿主要用同一个值。
- **数据库**：每次运行都会删除并重建 `-db` 指定库的 `public` schema，只能指向一次性测试库。端口与库用 `-pghost`、`-pgport`、`-pguser`、`-db` 修改。
- **档位**：测试台默认给被测服务端设 `TALKWIRE_PROTOCOL_PROFILE=legacy`；检查 strict 档时加 `-additions strict -overlay ../../contract/protocol/transcripts-strict -env TALKWIRE_PROTOCOL_PROFILE=strict`。
- **其他命令**：`record`、`repeat`、`schema` 同样接受 `-host`。

`host.ts` 从环境变量 `TALKWIRE_HOST_SECRET` 读测试密钥，用 `--port` 和 `--store memory` 或 `--store prisma` 启动，是一个完整的外部宿主样例。全部场景通过，说明真实的 IM 服务端与你的宿主配合起来，与参照实现逐帧一致。
