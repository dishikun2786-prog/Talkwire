---
title: "部署与运维"
description: "服务端对外暴露哪些路径、怎样放到反向代理后面、怎样检查健康与读日志、怎样配置三家推送，以及连接与多端的上限。"
---

# 部署与运维

服务端是单个进程，在 `0.0.0.0:<IM_PORT>`（默认 6062）上以明文 HTTP 同时提供 WebSocket、健康检查和宿主用的内部接口。标为“通用做法”的条目是一般的 WebSocket 部署常识，仓库里没有专门说明。

## 端点 {#endpoints}

所有路径都在同一个端口上（`server/main.go`），按用途分为下面几类。

| 路径 | 谁用 | 鉴权 |
| --- | --- | --- |
| `/ws/im`、`/ws` | 客户端，升级为 WebSocket，两者等价 | 升级后发 `login` 帧，带用户令牌 |
| `/api/health` | 健康检查 | 无 |
| `/api/v1/im/user/register`、`/api/v1/im/user/topics` | 用户令牌访问的 HTTP 接口：建立 IM 用户、列出会话与群（随原版服务一起搬来） | `Authorization: Bearer <用户令牌>` |
| `/api/v1/internal/im/*`、`/api/v1/internal/admin/im/*` | 宿主 | `role=internal_service` 的内部令牌 |

内部接口不要经公网反向代理转发（[`im-internal.openapi.yaml`](https://github.com/dishikun2786-prog/Talkwire/blob/main/contract/host-api/im-internal.openapi.yaml)）；内部令牌校验只是第二道防线。宿主调用的接口与令牌格式见 [宿主接口契约](/host/)。

HTTP 服务器设了 5 秒的请求头读取超时和 120 秒的空闲超时，没有设整体读写超时，因为同一个服务器还承载 WebSocket 升级，一条 WebSocket 连接会持续整个会话（`server/main.go`）。

## 放在反向代理后面 {#reverse-proxy}

服务端自己不做 TLS（`main.go` 调的是 `ListenAndServe`），所以客户端从公网用 `wss://` 连接时，TLS 要由前面的反向代理终止（通用做法）。仓库里没有给 IM 服务端用的反向代理配置。

代理要把客户端的 WebSocket 升级请求转发到 `/ws/im`。下面几条是通用的 WebSocket 代理要求，服务端代码里能看到它们为什么重要：

- **转发升级**：代理要以 HTTP/1.1 转发，并保留 `Upgrade: websocket` 与 `Connection: Upgrade` 请求头（通用做法）。服务端发现 `Upgrade` 头不是 `websocket` 时会自己补上这两个头再升级，并打日志 `Adding missing WebSocket upgrade headers`；这只修补请求头，代理本身仍须支持协议切换。
- **空闲超时**：服务端每隔 `IM_PONG_WAIT_SEC` 的 0.45 倍（默认 27 秒）发一次 ping，读超时是 `IM_PONG_WAIT_SEC`（默认 60 秒），收到 pong 时重新计时。代理的空闲超时要长于 ping 间隔，否则空闲连接会被代理先断开（通用做法）。
- **单帧大小**：服务端单帧上限 256 KB（`client.go` 的 `maxMessageSize`），超过时直接断开。代理若限制 WebSocket 消息大小，不应低于这个值（通用做法）。
- **来源地址**：服务端日志里的客户端地址取自 `RemoteAddr`，不读 `X-Forwarded-For` 之类的头，放在代理后面时记录的是代理的地址。

服务端不校验 `Origin`，鉴权在升级之后的 `login` 帧里完成（协议规范第 2 节）。未登录的连接没有登录时限，只要回 pong 就一直保持（规范第 2 节，F38）；升级之前唯一的限制是连接数上限，见 [连接与多端上限](#limits)。

## 健康检查 {#health}

`/api/health` 在 2 秒内 ping 一次数据库：成功时返回 200，失败时返回 503。

```json
{"status":"ok","service":"im-server"}
```

```json
{"status":"error","service":"im-server","error":"database unreachable"}
```

这个端点只反映数据库是否可达，不检查宿主、推送服务商或运行配置的状态（`server/handler.go` 的 `HealthCheck`）。

## 日志 {#logging}

服务端用 Go 标准库的 `log` 写日志，每行以 `[IM] ` 开头，带日期、时间和源文件行号；输出到标准错误，这是 `log` 包的默认去处。没有日志级别或格式的配置项。

启动日志说明了实际生效的配置，排查部署问题时先看它：

| 日志 | 内容 |
| --- | --- |
| `Config: port=…, db=…` | 端口与数据库地址（不含口令） |
| `Identity: …` | 用共享密钥还是密钥集验令牌，是否只接受访问令牌 |
| `Protocol profile: …` | 协议档位 |
| `Push: …` | 每家推送服务商：已启用、未配置或加载失败的原因 |
| `Host: …` | 宿主模式与地址 |
| `Modules: […]` | 实际安装的模块；跳过的模块另有一行原因 |
| `[Store] applied migration …` | 本次执行的迁移 |

运行中值得留意的几类日志：

- `WARNING: IMKIT_… is the old name of …`：还在用改名前的环境变量名，见 [环境变量](/server/env#old-names)。
- `WARNING: TALKWIRE_ACCEPT_LEGACY_TOKENS=1 …`：strict 档正在接受非访问令牌。
- `[WS] connection cap reached`：连接数到了 `IM_MAX_CONNECTIONS`。
- `[Client] Send buffer full for …`：某个客户端读得太慢，连接被断开。
- `Login refused: … already connected from … devices`：某个账号的设备数到了上限。

## 运行配置 {#runtime-config}

群人数上限、消息长度、发送频率、推送文案等运行期设置由宿主下发，不在环境变量里。服务端启动时拉一次，之后每 60 秒拉一次；拉取失败时保留上一次的值，从未拉到时用内置默认值（`server/config_refresh.go`）。字段与默认值见 [宿主接口契约](/host/)；standalone 宿主从配置文件读这些值，见 [standalone 宿主](/server/#standalone)。

## 推送 {#push}

离线推送由服务端直接发给推送服务商，支持 FCM（Android）、APNs（iOS）和 Web Push（浏览器）三家。每家由自己的密钥文件启用、互不依赖：某一家加载失败只停用这一家并打日志，其余照常工作；一家都没配时离线推送关闭。

| 服务商 | 必需的配置 | 说明 |
| --- | --- | --- |
| FCM | `FCM_SERVICE_ACCOUNT_KEY_PATH`：Firebase Admin SDK 的服务账号 JSON | 走 FCM HTTP v1 接口，发 data-only 消息，数据字段与原版相同 |
| APNs | `TALKWIRE_APNS_KEY_PATH`（`.p8`）、`TALKWIRE_APNS_KEY_ID`、`TALKWIRE_APNS_TEAM_ID`、`TALKWIRE_APNS_TOPIC`（bundle ID） | 基于令牌的 HTTP/2 连接；`TALKWIRE_APNS_ENV=sandbox` 改用沙箱环境 |
| Web Push | `TALKWIRE_VAPID_KEY_PATH`，可选 `TALKWIRE_VAPID_SUBJECT` | 按 RFC 8291 加密、RFC 8292 签名；推送服务地址只许 `TALKWIRE_WEBPUSH_ALLOWED_HOSTS` 里的主机 |

三家共用 `TALKWIRE_PUSH_HTTP_TIMEOUT_SEC`（默认 10 秒）作为每次请求的超时。全部变量见 [环境变量](/server/env#push)。

### 生成 VAPID 密钥

Web Push 的 VAPID 密钥用 `server/cmd/vapidkey` 生成：

```bash
cd server
go run ./cmd/vapidkey -out vapid.pem
```

私钥以 PKCS #8 PEM 写到 `-out` 指定的文件，权限只给属主；文件已存在时不覆盖。命令把公钥打印到标准输出，浏览器订阅时用它（`PushManager.subscribe` 的 `applicationServerKey`）。把 `TALKWIRE_VAPID_KEY_PATH` 指向这个文件后，服务端启动时也会在日志里打印公钥，strict 档的登录应答还会带上 `vapidKey`。

### 推送的运行开关

推送的总开关和并发数也在宿主下发的运行配置里，改它们不需要重启：

| 字段 | 默认 | 作用 |
| --- | --- | --- |
| `fcmEnabled` | `true` | 所有推送通道的总开关（名字是历史遗留） |
| `fcmConcurrency` | `16` | 一条消息给多个离线成员推送时的并发数 |
| `pushText` | 原版的中文文案 | 媒体占位、入群申请等通知文案 |

端到端加密的消息推送时既不带发送者也不带预览，APNs 与 Web Push 上只给本地化键 `TALKWIRE_E2E_MESSAGE`，由 App 显示自己的文案（`server/push/push.go`）。

客户端用 `reg_token` 登记推送令牌，并说明属于哪一家；strict 档的登录应答在能力列表里为每家已启用的服务商加一项 `push:<服务商>`。客户端一侧的细节见 [多端与推送](/protocol/devices-push)。

## 连接与多端上限 {#limits}

服务端对连接数、设备数和每个连接的发送队列都有上限，到达时的行为如下：

| 上限 | 默认 | 到达时 |
| --- | --- | --- |
| `IM_MAX_CONNECTIONS` | 10000 | 新的升级请求得到 HTTP 503 `server busy`，在升级之前就拒绝 |
| `TALKWIRE_MAX_DEVICES_PER_USER` | 10 | 新设备的 `login` 得到 409 `too many devices`（strict 档 `errCode` 为 `too_many_devices`） |
| 每连接发送队列 | 256 帧（写在代码里） | 断开这个连接，迫使客户端重连后重新同步 |

设备上限只对新设备生效：已在线的设备再次登录时替换它原来的连接，不占新名额。登录不带 `device` 的客户端算作设备 `""`。上限在登录时检查，几台新设备同时登录时可能超出上限，超出的数量不超过同时登录的台数（`server/config.go` 的 `maxDevicesPerUser`）。多端同时在线的协议规则见 [多端与推送](/protocol/devices-push)。

发送队列满时断开，而不是丢掉这一帧：丢帧会在接收方的消息序号里留下永久空洞，断开则让客户端重连后补拉（`client.go` 的 `enqueue`）。压测里被这样断开的连接算作 closed early，见 [压测](/server/loadtest)。

## 停止与重启 {#restart}

`main.go` 没有处理终止信号，也没有排空连接的过程：进程停止时，所有 WebSocket 连接随之断开。客户端须把断开视为正常事件，重连后按 [同步与补拉](/protocol/sync) 重新同步（规范第 2 节）。

连接表和订阅索引都在进程内存里（`server/hub.go`、`server/subscriptions.go`），重启后由客户端重新登录建立。仓库里没有在多个服务端实例之间转发消息的机制，也没有描述多实例部署。

重启是安全的：迁移只执行没执行过的部分，见 [数据库与迁移](/server/database)。
