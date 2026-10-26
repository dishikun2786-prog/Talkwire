# 宿主契约

“宿主”是业务后端：它知道用户是谁、谁和谁是好友、每个人叫什么，并拥有媒体文件和一切业务（积分、红包、通话计费……）。IM 服务端只管连接、消息、群和推送。两者之间的全部接口在这里：

| 文件 | 内容 |
| --- | --- |
| `host.openapi.yaml` | 宿主实现、IM 服务端调用的端点 |
| `im-internal.openapi.yaml` | IM 服务端暴露、宿主调用的内部端点 |
| `uid-vectors.json` | 宿主用户 id → 线上 uid 的黄金向量 |

两份 OpenAPI 都是 3.1；每个操作的 `x-talkwire` 写明它是必须的、可选的还是只有某个模块用（`requirement`），IM 服务端等多久（`timeoutSeconds`），以及失败时 IM 怎么办（`onFailure`）。IM 服务端从不重试失败的调用。

Node 宿主可以直接用 `host-sdk-node/`。下面是用别的语言实现宿主时要做的事。

## 用其他语言实现宿主

### 1. 鉴权

两个方向用同一种令牌：HS256 JWT，密钥是双方共享的 `JWT_SECRET`，claims 为 `{"role": "internal_service", "exp": <现在 + 60 秒>}`。

- 宿主收到的每个请求都带 `Authorization: Bearer <令牌>`。验签（只认 HS256）、验 `role`、验过期，否则答 401 和 JSON `{"error": ...}`。
- 宿主调 IM 内部端点时自己签一个同样的令牌。IM 的 401、405 是纯文本，不是 JSON。
- 客户端登录 IM 用的是**用户令牌**，与此无关：宿主签发，HS256 时 claims 至少要有 `sub`（宿主用户 id）、`type: "access"`、`role`（strict 档只接受 `TALKWIRE_TOKEN_USER_ROLES` 里的角色，默认 `app_user`）；也可以让 IM 用宿主公布的 JWKS 验 RS256/ES256 令牌（`TALKWIRE_JWKS_URL`）。

### 2. uid

IM 不认识宿主的用户 id，线上只用 uid：`"uid_"` 加 `SHA-256(UTF-8 的宿主用户 id)` 前 8 个字节的 base64url（不补齐），共 15 个字符。用 `uid-vectors.json` 核对你的实现，每一条都必须相同；改算法会让已存的 uid 全部失效。宿主要能从 uid 查回用户（扫码加好友、按 uid 批量查资料、后台显示聊天记录），所以把 uid 和用户一起存下来。

### 3. 必须实现的端点

| 端点 | 超时 | 宿主答什么 | 失败时 IM |
| --- | --- | --- | --- |
| `GET /api/v1/internal/im/friendship?a=&b=` | 3 s | `{"friends": true}` 当且仅当两人的关系已被接受 | 拒绝需要这次检查的动作 |
| `GET /api/v1/internal/im/profile?viewer=&sender=` | 3 s | `{"displayName", "avatarUrl"}`，见下 | 通知用兜底名字、不带头像 |

`profile` 用于离线推送的标题：先给 **viewer 自己为 sender 设的备注**（只在两人是已接受的好友时），没有就给 sender 的显示名；不认识的 sender 答两个空串，IM 会改用运行配置里的 `pushText.sender`。备注是按方向存的私人数据：A 给 B 的备注只有 A 看得到。这是最容易写反的地方，务必用两个账号各设一个备注来测。

### 4. 可选与模块端点

| 端点 | 用到它的 | 超时 | 失败时 IM |
| --- | --- | --- | --- |
| `GET runtime-config` | 核心（可选） | 3 s | 沿用上一次的值；404 表示宿主没有这份文档，用内置默认值 |
| `GET e2ee-config` | 端到端加密模块 | 3 s | 沿用上一次的值 |
| `POST delete-media` | 核心（可选） | 3 s | 只记日志，文件留着 |
| `POST calls/create`、`answer`、`settle`、`upgrade` | 通话模块 | 3 s（`answer` 8 s） | 见契约里各自的 `onFailure` |
| `POST group-ai-qa` | 群 AI 模块 | 10 s | 当作超时，群里收到兜底文案 |

不提供某个模块，就别在 IM 上装它（`TALKWIRE_MODULES`）；装了而宿主不答，每次使用都会按 `onFailure` 失败。宿主对不提供的模块端点答 404。

`group-ai-qa` 是机器人钩子：群成员写“@ + 助手的名字”（运行配置 `groupAi.name`，默认“小桃子”）提问，IM 先在群里发 `ai_typing`，再问宿主；回答什么、扣不扣费、怎么扣，都由宿主决定，IM 把回答以助手身份发进群。宿主答不了时给 `ok=false` 和 `reason`，可以再带一个 `message` 作为自己的说法；不带就用运行配置 `groupAi` 里对应原因的兜底文案（默认是原版的中文措辞，`{name}` 换成助手名）。10 秒内没答，按超时处理。

运行配置每 60 秒刷新一次。注意解码规则：文档按“零值结构”解码，**缺的整数字段会被修正为默认值，缺的布尔字段就是 false**，所以 `fcmEnabled`、`readReceiptEnabled` 要显式给出；非 200 的应答一律保留上一次的值。字段与默认值见契约的 `RuntimeConfig`。

### 5. 宿主必须通知 IM 的事

- **好友关系被接受或解除时调 `contact-sync`**（必须）。IM 只在这时建好两人的单聊会话；不调的话，新好友之间收不到彼此的在线状态，第一次聊天要等一次好友校验。
- 宿主自定义的消息卡片状态变了（红包被领、订单状态变了），或要让一个用户的各个设备立刻知道某件事（有人加你好友），调 `custom-broadcast`。帧是 `custom_update`，只发给此刻在线的连接，不进离线队列，所以客户端打开卡片时还要向宿主拉一次当前状态。示例见 `examples/red-packet/`。
- 通话模块里宿主自己结束通话（余额用完、超时），调 `call-broadcast`。
- 后台查看、清空聊天记录用 `admin/*` 四个端点。清空会永久删除消息和文件：在宿主这边要求超级管理员、输入确认语、写明理由，并用幂等键防止重复执行；审计只记元数据，不记消息内容。

### 6. 媒体

消息里的文件 URL 由宿主签发，IM 只存 URL。建议：

- 读取时带 `Content-Length`（不带的话部分客户端的缓存不落盘）；私密文件 `Cache-Control: private, no-store`，不进 CDN，否则消息撤回后文件仍能访问；文件名不可猜。
- `delete-media` 只在没有任何剩余消息引用这些 URL 时才会被调用，直接删除即可；不认识的 URL 忽略，删除不存在的文件不算错。
- 宿主自己删文件（例如用户放弃了一个上传后没发出去的文件）之前，先调 `media-ref-count` 确认引用数为 0。

### 7. 数据

宿主要存三样东西：资料（含 uid 与唯一、不分大小写的 handle）、关系（每对用户最多一条，记录谁先发起、状态、各自的备注、谁拉黑的）、后台审计。Node SDK 的表结构在 `host-sdk-node/prisma/talkwire.sql`，可以照着建；那份 SQL 里 `id`（uuid）和 `updated_at` 由应用填写。

### 8. 验证你的宿主

1. **契约测试**：对两份 OpenAPI 各写一组测试——你的宿主对 `host.openapi.yaml` 每个操作的应答都要能通过它的 Schema；你调 IM 时发出的请求与收到的应答都要符合 `im-internal.openapi.yaml`。`host-sdk-node/test/` 里的 `host-plugin.test.ts`、`goldens-contract.test.ts`、`im-client-contract.test.ts` 是这三件事在 Node 下的写法。
2. **黄金对话**：在宿主上实现一致性控制接口（`/__conformance/reset`、`friends`、`profile`、`runtime-config`、`e2ee-config`、`calls`，说明见 `tools/conformance/README.md` 的“外部宿主”），然后用 `conformance check -host <你的宿主>` 跑全部场景。全部通过，说明真实的 IM 服务端和你的宿主配合起来，与原版逐帧一致。控制接口只在测试环境打开。
