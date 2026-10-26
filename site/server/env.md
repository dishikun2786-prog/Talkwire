---
title: "环境变量"
description: "Talkwire 服务端读取的全部环境变量，按主题列出名称、默认值与作用，以及旧名 IMKIT_* 的兼容规则。"
---

# 环境变量

服务端的启动配置都来自环境变量，下面按主题列出代码里读取的每一个（`server/` 下除测试以外的 `os.Getenv`、`getenv`、`lookupEnv`、`envInt`、`envDuration` 调用）。只有 `DB_PASSWORD` 和 `JWT_SECRET` 必填。

群人数上限、消息长度、推送文案这类运行期设置不在环境变量里，由宿主经 `runtime-config` 端点下发、每 60 秒刷新一次，见 [宿主接口契约](/host/)。

## 数据库 {#database}

这组变量拼成 lib/pq 的连接串（`server/config.go` 的 `DSN()`）。

| 变量 | 默认 | 作用 |
| --- | --- | --- |
| `DB_HOST` | `localhost` | 数据库主机 |
| `DB_PORT` | `5432` | 数据库端口；不是整数时忽略，用默认值 |
| `DB_USER` | `xiaotaozi` | 数据库用户 |
| `DB_PASSWORD` | 无，**必填** | 数据库口令；缺少时启动即退出 |
| `DB_NAME` | `xiaotaozi` | 库名 |
| `DB_SSLMODE` | `disable` | 原样传给连接串的 `sslmode` |

## 监听与宿主 {#host}

| 变量 | 默认 | 作用 |
| --- | --- | --- |
| `IM_PORT` | `6062` | 监听端口，地址是 `0.0.0.0`；不是整数时忽略，用默认值 |
| `TALKWIRE_HOST_MODE` | `http` | `http` 或 `standalone`，其他值启动失败。见 [宿主模式](/server/#host-mode) |
| `MAIN_API_BASE_URL` | `http://127.0.0.1:6001` | http 宿主的地址，只在 `http` 模式下使用 |
| `TALKWIRE_STANDALONE_CONFIG` | 不设 | standalone 宿主的 JSON 配置文件；不设时任何人可以互发消息、没有资料、用内置运行配置。设了而模式不是 `standalone` 时启动失败。见 [standalone 宿主](/server/#standalone) |
| `TALKWIRE_MODULES` | 宿主支持的全部模块 | 逗号分隔的模块名（`call`、`groupjoin`、`groupai`、`e2ee`）或 `none`。显式列出的模块宿主不支持、或名字未知时启动失败。见 [可选模块](/server/#modules) |

## 身份与协议 {#identity}

| 变量 | 默认 | 作用 |
| --- | --- | --- |
| `JWT_SECRET` | 无，**必填** | 与宿主共享的 HS256 密钥：验用户令牌（未设 JWKS 时）、验宿主发来的内部令牌、给发往宿主的请求签令牌；缺少时启动即退出 |
| `TALKWIRE_PROTOCOL_PROFILE` | `strict` | `strict` 或 `legacy`，其他值启动失败。见 [legacy 与 strict](/protocol/profiles) |
| `TALKWIRE_TOKEN_USER_ROLES` | `app_user` | strict 档允许登录的令牌 `role`，逗号分隔 |
| `TALKWIRE_ACCEPT_LEGACY_TOKENS` | 不设 | 只有值为 `1` 时生效：strict 档也接受非访问令牌，只用于迁移期，启动时打印警告 |
| `TALKWIRE_JWKS_URL` | 不设 | 用宿主公布的 JWKS 验用户令牌（RS256/ES256，按 `kid` 选钥，RSA 至少 2048 位）；只接受 https 或本机 http |

令牌的 claim 与两档的区别见 [签发 IM 令牌](/host/token)。

## 推送 {#push}

每家推送服务商各自由它的密钥文件启用，没配的那家在启动日志里写一行“not configured”。配置步骤见 [部署与运维](/server/operations#push)。

| 变量 | 默认 | 作用 |
| --- | --- | --- |
| `FCM_SERVICE_ACCOUNT_KEY_PATH` | 不设 | FCM 服务账号密钥文件（Firebase Admin SDK 的 JSON） |
| `FCM_SERVICE_ACCOUNT_PATH` | 不设 | 上一项的旧名；只在 `FCM_SERVICE_ACCOUNT_KEY_PATH` 没设时读 |
| `TALKWIRE_APNS_KEY_PATH` | 不设 | APNs 的 `.p8` 密钥文件；设了才启用 APNs |
| `TALKWIRE_APNS_KEY_ID` | 不设 | APNs 密钥 ID；启用 APNs 时必填，缺这一项或下面两项时 APNs 被停用并打日志 |
| `TALKWIRE_APNS_TEAM_ID` | 不设 | Apple 团队 ID；启用 APNs 时必填 |
| `TALKWIRE_APNS_TOPIC` | 不设 | App 的 bundle ID；启用 APNs 时必填 |
| `TALKWIRE_APNS_ENV` | `production` | `production` 或 `sandbox`，其他值启动失败 |
| `TALKWIRE_VAPID_KEY_PATH` | 不设 | Web Push 的 VAPID 私钥（P-256 PEM，可用 `cmd/vapidkey` 生成）；设了才启用 Web Push |
| `TALKWIRE_VAPID_SUBJECT` | 不设 | 给推送服务的联系方式，`mailto:` 或 `https:`；不设时 VAPID 令牌里不带 `sub` |
| `TALKWIRE_WEBPUSH_ALLOWED_HOSTS` | `fcm.googleapis.com`、`updates.push.services.mozilla.com`、`web.push.apple.com`、`*.notify.windows.com` | 浏览器订阅里的推送服务地址只许这些主机，逗号分隔；`*.域名` 匹配子域名，单独一个 `*` 放行任何主机 |
| `TALKWIRE_WEBPUSH_TTL_SEC` | `86400` | 推送服务保留未送达消息的秒数 |
| `TALKWIRE_PUSH_HTTP_TIMEOUT_SEC` | `10` | 每次推送请求（含换取访问令牌）的超时秒数；没设时读旧名 `IM_FCM_HTTP_TIMEOUT_SEC` |

## 连接与调优 {#tuning}

这组都是正整数，取值无效或不大于 0 时打一行日志并用默认值，不会让启动失败。

| 变量 | 默认 | 作用 |
| --- | --- | --- |
| `IM_MAX_CONNECTIONS` | `10000` | 同时接受的 WebSocket 连接数上限；超过时升级请求得到 HTTP 503 |
| `TALKWIRE_MAX_DEVICES_PER_USER` | `10` | 一个账号最多同时在线的设备数（协议规范第 6.2 节） |
| `IM_PONG_WAIT_SEC` | `60` | 读超时秒数，收到 pong 时重新计时；服务端每隔它的 0.45 倍（默认 27 秒）发一次 ping |
| `IM_WRITE_WAIT_SEC` | `10` | 单帧写超时秒数 |
| `IM_HUB_CHAN_BUF` | `100` | 连接登记与注销队列的长度；断网后大量重连时会被填满 |
| `IM_PRESENCE_CHAN_BUF` | `1024` | 在线状态队列的长度；大群同时上线时会被填满 |
| `IM_CALL_DISCONNECT_GRACE_SEC` | `10` | 通话一方断线后等多少秒再结束通话；只对 `call` 模块有意义 |

原版服务就有的项，默认值与原版写死的值相同；单测 `TestR9_DefaultsEqualOriginalValues` 逐项比对，新增的项列在测试里并写明理由（`server/README.md`）。

## 取值无效时

不同类型的变量对无效值的处理不同：

- **必填项缺失**（`DB_PASSWORD`、`JWT_SECRET`）：启动即退出。
- **枚举项取值不对**（`TALKWIRE_HOST_MODE`、`TALKWIRE_PROTOCOL_PROFILE`、`TALKWIRE_APNS_ENV`，以及 `TALKWIRE_MODULES` 里的未知模块名）：启动即退出。
- **组合或文件有误**：设了 `TALKWIRE_STANDALONE_CONFIG` 而模式不是 `standalone`、standalone 配置文件读不了或有未知字段、`TALKWIRE_JWKS_URL` 既不是 https 也不是本机 http：启动即退出。
- **开关**（`TALKWIRE_ACCEPT_LEGACY_TOKENS`）：只认 `1`，其他值当作没设。
- **端口**（`IM_PORT`、`DB_PORT`）：不是整数时不报错，用默认值。
- **调优项**（上一节的正整数，以及 `TALKWIRE_WEBPUSH_TTL_SEC`、`TALKWIRE_PUSH_HTTP_TIMEOUT_SEC`）：打一行日志，用默认值。

## 旧名 `IMKIT_*` {#old-names}

项目在 2026-10-26 由 ImKit 改名为 Talkwire，`IMKIT_*` 变量全部改成了 `TALKWIRE_*`。旧名在 1.0 之前照常生效，1.0 起只认新名（[`CHANGELOG.md`](https://github.com/dishikun2786-prog/Talkwire/blob/main/CHANGELOG.md)）。

规则写在 `server/config.go` 的 `lookupEnv` 里：

- 读 `TALKWIRE_X` 时，若这个名字没设，就读 `IMKIT_X`，并为每个旧名打一次警告：`WARNING: IMKIT_X is the old name of TALKWIRE_X; rename it, the old name stops working at 1.0`。
- 新旧两个名字都设了时用新名字。
- 只有 `TALKWIRE_` 开头的变量有这条回退。`DB_*`、`IM_*`、`JWT_SECRET`、`MAIN_API_BASE_URL`、`FCM_*` 不在这次改名范围内，没有 `IMKIT_` 形式的旧名。

另外两个旧名与这次改名无关，来自原版服务：`FCM_SERVICE_ACCOUNT_PATH`（新名 `FCM_SERVICE_ACCOUNT_KEY_PATH`）和 `IM_FCM_HTTP_TIMEOUT_SEC`（新名 `TALKWIRE_PUSH_HTTP_TIMEOUT_SEC`），两者都照常读取。

## 不属于服务端的变量

下面几个变量出现在仓库里，但服务端本身不读：

| 变量 | 谁读 |
| --- | --- |
| `TALKWIRE_TEST_DSN` | 服务端的数据库单测与 `tools/ci.sh`，指向一个可以随意改写的测试库；不设时数据库用例跳过 |
| `LOADTEST_JWT_SECRET` | 压测工具 `tools/loadtest`，见 [压测](/server/loadtest) |
| `TALKWIRE_MINGW` | 竞态检测脚本 `tools/race.sh`，在 Windows 上指向 MinGW-w64 GCC |
