# server

ImKit 的 Go 实时服务端。

来源是小桃子原版 IM 服务（源提交 `33e23250` 的 `server/im-go`）。Phase 1 先把它原样搬进来（1.1），再一步步改造（1.2–1.13），每一步都重新通过一致性测试：legacy 档与原版的黄金对话**逐帧一致**，有意的偏离只放在 strict 档（见协议规范第 0.3 节）。默认档位是 strict，闸门 G1 已于 2026-10-25 通过。模块名 `example.com/imkit/server` 是占位符，定名后替换。

## 现状（2026-10-25）

| 任务 | 内容 |
| --- | --- |
| 1.1 | 原样搬入，只改模块名 |
| 1.2 | 按职责拆文件（见下文“代码结构”），之前先单独做了一步 gofmt |
| 1.3 | 宿主适配层（`host/`）：HTTP 宿主（原版行为）与不需要宿主进程的 standalone 宿主 |
| 1.4 | 推送：FCM 之外加 APNs 和 Web Push |
| 1.5 | 模块注册表：通话、扫码入群、群 AI、端到端加密成为可选模块 |
| 1.6 | 数据库迁移（`migrations/`）：空库建表、采用原版已建的库、重启不重复执行，加表结构比较 |
| 1.7 | strict 档：应答补 `id`、登录应答带协议版本与能力、错误帧带 `errCode` |
| 1.8 | 安全：strict 档只接受访问令牌（R18）、在线状态查询只返回共享会话的用户（R22）；可用 JWKS 验令牌 |
| 1.9 | 已知缺口：撤回视频时一并回收缩略图；离线推送的文案可配置 |
| 1.10 | 工程化：`go.mod` 整理、lint 脚本、按红线编号的单测、D9（strict 档） |
| 1.11 | 并发：修 F58、F60、F61，竞态压力用例 |
| 1.12 | 多端同时在线：登录带 `device`，不同设备同时在线（协议规范第 6.2 节） |
| 1.13 | strict 档修正 D1、D2（`leave_grp` 校验群与成员）、D5（补投带当前群名）、D7（扫码入群通知成员、订阅入群者）、D8（审核先检查后写入）；计划外加的任务 |

验证结果：legacy 档 32/32 逐帧一致；strict 档（去掉新增字段并用 `transcripts-strict/` 覆盖六个有意偏离的场景）32/32；standalone 29/32（依赖宿主的通话两个、群 AI 一个场景在这种模式下必然失败）；表结构比较 5 项全部通过；竞态检测连跑三轮无报告；单测 139 个（含子测试，数据库用例需要 `IMKIT_TEST_DSN`）。

Phase 2 之后（加入 `custom-broadcast` 与核心组场景 `core/host-red-packet`，2026-10-25 在最终代码上重跑）：legacy 33/33，strict 33/33，standalone 30/33（同样是那三个依赖宿主的场景），表结构比较 5 项通过，竞态检测三轮无报告，staticcheck 无报告，单测 146 个全部通过、无跳过。

## 代码结构

| 文件 | 内容 |
| --- | --- |
| `main.go`、`config.go`、`config_refresh.go` | 启动、环境变量、从宿主刷新运行配置 |
| `hub.go`、`session.go`、`client.go`、`handler.go`、`custombroadcast.go` | 连接表与动作分发；登录、注册、注销；单个连接的读写；HTTP 与内部接口（宿主卡片更新单独一个文件） |
| `devices.go`、`store_devices.go` | 多端在线：按设备查连接、发给一个用户的所有设备、离开的设备该补投什么 |
| `subscriptions.go`、`presence.go` | 订阅索引与按会话广播；在线状态 |
| `publish.go`、`forward.go`、`clear.go`、`access.go` | 消息收发与历史；转发；撤回与清空；成员与好友校验、单聊地址解析 |
| `group.go`、`offline.go` | 群管理；离线补投（清空、群事件）与回执 |
| `push.go`、`push/` | 离线推送：选目标与文案；FCM、APNs、Web Push 三个服务商 |
| `profile.go`、`protocol.go` | 协议档位与 strict 档的新增；帧的结构。strict 档对缺陷的修正写在各自的处理函数旁（如 `mayLeaveGroup`、`reviewGroupJoinStrict`） |
| `modules.go`、`call.go`、`groupjoin.go`、`groupai.go`、`e2ee.go` | 模块注册表与四个可选模块 |
| `store*.go` | 数据库访问，按领域分文件 |
| `host/` | 宿主接口与实现：`httpadapter`（原版的 HTTP 宿主）、`standalone`、`hmacjwt`、`jwks` |
| `migrations/`、`migrate/` | 迁移脚本（`core/`、`e2ee/`、`groupjoin/` 各自编号）与执行它们的迁移器，启动时执行 |
| `cmd/vapidkey` | 生成 Web Push 用的 VAPID 密钥 |

## 配置

必填：`DB_PASSWORD`、`JWT_SECRET`（与宿主共享的 HS256 密钥；用 JWKS 验用户令牌时，内部接口仍用它）。缺任何一个启动即退出。

| 变量 | 默认 | 作用 |
| --- | --- | --- |
| `IM_PORT` | 6062 | 监听端口 |
| `DB_HOST`、`DB_PORT`、`DB_USER`、`DB_NAME`、`DB_SSLMODE` | localhost、5432、xiaotaozi、xiaotaozi、disable | 数据库 |
| `IMKIT_HOST_MODE` | `http` | `http`：宿主是一个 HTTP 服务（`MAIN_API_BASE_URL`，默认 `http://127.0.0.1:6001`）；`standalone`：没有宿主进程，好友、资料、运行配置来自 `IMKIT_STANDALONE_CONFIG` 指向的文件 |
| `IMKIT_MODULES` | 宿主支持的全部模块 | 逗号分隔的模块名（`call`、`groupjoin`、`groupai`、`e2ee`），或 `none`。没装的模块的动作得到 501 `module not enabled: <名>`。standalone 宿主只支持 `groupjoin`、`e2ee` |
| `IMKIT_PROTOCOL_PROFILE` | `strict` | `strict` 或 `legacy`（协议规范第 0.3 节）；迁移原版项目时设 `legacy` |
| `IMKIT_TOKEN_USER_ROLES` | `app_user` | strict 档允许登录的令牌 `role`，逗号分隔 |
| `IMKIT_ACCEPT_LEGACY_TOKENS` | 不设 | 设为 `1` 时 strict 档也接受非访问令牌，只用于迁移期（启动时打印警告） |
| `IMKIT_JWKS_URL` | 不设 | 用宿主公布的 JWKS 验用户令牌（RS256/ES256，按 `kid` 选钥，RSA 至少 2048 位）；只接受 https 或本机 http |
| `IMKIT_MAX_DEVICES_PER_USER` | 10 | 一个账号最多同时在线的设备数（协议规范第 6.2 节） |
| `FCM_SERVICE_ACCOUNT_KEY_PATH` | 不设 | FCM 服务账号密钥文件；旧名 `FCM_SERVICE_ACCOUNT_PATH` 也读 |
| `IMKIT_APNS_KEY_PATH`、`_KEY_ID`、`_TEAM_ID`、`_TOPIC`、`_ENV` | `_ENV`=production | APNs（`.p8` 密钥）；`_ENV` 可设 sandbox |
| `IMKIT_VAPID_KEY_PATH`、`IMKIT_VAPID_SUBJECT` | 不设 | Web Push 的 VAPID 密钥与联系方式；配置后 strict 登录应答带 `vapidKey` |
| `IMKIT_WEBPUSH_ALLOWED_HOSTS` | 主流浏览器的推送服务 | 订阅里的推送服务地址只许这些主机 |
| `IMKIT_WEBPUSH_TTL_SEC` | 86400 | 推送服务保留消息的时长 |
| `IMKIT_PUSH_HTTP_TIMEOUT_SEC` | 10 | 每次推送请求的超时；旧名 `IM_FCM_HTTP_TIMEOUT_SEC` 也读 |
| `IM_MAX_CONNECTIONS` | 10000 | 同时接受的 WebSocket 连接数上限 |
| `IM_PONG_WAIT_SEC`、`IM_WRITE_WAIT_SEC` | 60、10 | 心跳超时（发 ping 的间隔是它的 0.45 倍）、写超时 |
| `IM_HUB_CHAN_BUF`、`IM_PRESENCE_CHAN_BUF` | 100、1024 | 内部队列长度 |
| `IM_CALL_DISCONNECT_GRACE_SEC` | 10 | 通话一方断线后等多久结束通话 |

默认值与原版写死的值相同（单测 `TestR9_DefaultsEqualOriginalValues` 逐项比对，新增的配置项列在测试里并写明理由）。离线推送的文案等运行配置由宿主下发，不在环境变量里。

## 数据库

启动时按顺序执行 `migrations/` 里尚未执行过的脚本，记录在 `imkit_schema_migrations`。在原版服务已经建好的库上启动时，基线脚本只补缺的部分，不重建。相对原版新增的表：`imkit_schema_migrations`、`im_devices`、`im_pending_acks`；新增的列：`im_fcm_tokens.provider`、`im_fcm_tokens.device_id`、`im_e2e_identities.owner_device`。`im_fcm_tokens` 保留原名，虽然它已经不只存 FCM 令牌。

## 宿主接口

服务端调用宿主的端点、以及暴露给宿主的内部端点，契约都在 `contract/host-api/`（OpenAPI 3.1），Node 宿主 SDK 在 `host-sdk-node/`。uid 的算法由 `contract/host-api/uid-vectors.json` 钉住，`host/uids_test.go` 逐条核对。

## 构建与验证

```bash
cd server
go build -o bin/imkit-server.exe .
go vet ./...
IMKIT_TEST_DSN="host=127.0.0.1 port=55432 user=postgres dbname=imkit_new sslmode=disable" go test ./...   # 口令从 pgpass 读
bash ../tools/race.sh                # 竞态检测（同样需要 IMKIT_TEST_DSN 才会跑数据库用例）
bash ../tools/lint.sh                # gofmt、go vet、staticcheck（没装时跳过并说明）

cd ../tools/conformance              # 三种一致性检查与表结构比较，见该目录 README
./bin/conformance.exe check -bin ../../server/bin/imkit-server.exe -db imkit_new
```

数据库用例在 `IMKIT_TEST_DSN` 指向的库里建临时 schema，用完删除；不设时跳过。多端、并发类用例在进程内起真实的服务端和 WebSocket（`live_test.go`）。

## 与原版的差异

legacy 档对外的帧与原版逐帧一致，下面这些是帧以外的差异或新增的可选能力：

- 修了原版的并发缺陷：振铃计时器的数据竞争（F58）、被顶替连接的迟到帧抢走新连接的订阅（F60）、同一新用户并发首次登录失败（F61）。
- 撤回视频时缩略图也请求宿主删除（原版只删主文件）。
- 推送多了 APNs 与 Web Push，`reg_token` 可带 `provider`；文案可由宿主配置。
- 模块可以不装；standalone 模式不需要宿主进程。
- 多端同时在线：只在客户端登录带 `device` 时启用，不带时与原版相同。
- 队列长度配置 `IM_HUB_CHAN_BUF`、`IM_PRESENCE_CHAN_BUF` 真正生效（原版读了却没用，F62）。
- 登录中途断开的连接不再被注册成在线（原版偶发让用户永久显示在线，F63）。
- 默认档位是 strict；要与原版逐帧一致须设 `IMKIT_PROTOCOL_PROFILE=legacy`。
- 新增内部接口 `POST /api/v1/internal/im/custom-broadcast`（Phase 2）：宿主把自定义消息卡片的新状态推给一个会话的在线连接或一个用户的全部设备，帧为 `custom_update`（协议规范附录 B）。它是原版 `red-packet-broadcast` 的通用形式，后者保留。两档都有；契约见 `contract/host-api/im-internal.openapi.yaml`，测试 `custombroadcast_test.go`。

strict 档在此之外修正协议规范第 18 节除 D6 以外的全部缺陷（D1–D5、D7–D9），并加上新增字段。修正逐项有两档对照的单测（`strict_defects_test.go`、`d9_test.go`）。

## 已知问题

- 测试里会打印 `settleCallAsync: recovered panic ... nil pointer dereference`：部分单测的 Hub 没有配宿主，异步结算在 recover 里吞掉了这个 panic，不影响结果。
- `store_users.go` 里有一行没有对象的注释（`// GetUserTopics returns …`，函数在 `store_topics.go`），原版就有，未改。
