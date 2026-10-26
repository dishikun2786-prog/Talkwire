---
title: "安装与配置"
description: "构建 Talkwire 服务端，用最少的环境变量启动它，并选定宿主模式、可选模块和协议档位。"
---

# 安装与配置

Talkwire 服务端是 `server/` 目录下的一个 Go 程序，构建出单个可执行文件 `talkwire-server`。它在一个端口上同时提供 WebSocket 与少量 HTTP 接口，数据只存 PostgreSQL，启动配置全部来自环境变量。

## 环境要求

构建需要 Go，运行需要一个 PostgreSQL 库；用默认的 http 宿主模式时，还要有宿主在运行。

| 项目 | 要求 | 出处 |
| --- | --- | --- |
| Go | 1.26.4 及以上（`server/go.mod` 写的是 `go 1.26.4`）；本机验证用的是 1.27.0 | [`server/go.mod`](https://github.com/dishikun2786-prog/Talkwire/blob/main/server/go.mod)、[`README.md`](https://github.com/dishikun2786-prog/Talkwire/blob/main/README.md) |
| PostgreSQL | CI 用 `postgres:17` 镜像，本机测试也用 17；仓库没有写最低版本 | [`.github/workflows/ci.yml`](https://github.com/dishikun2786-prog/Talkwire/blob/main/.github/workflows/ci.yml) |
| Go 依赖 | `github.com/gorilla/websocket`、`github.com/golang-jwt/jwt/v5`、`github.com/lib/pq` | `server/go.mod` |

数据库可以是空库：服务端启动时自己建表，见 [数据库与迁移](/server/database)。

## 构建

在 `server/` 目录里 `go build` 即可，`server/README.md` 给出的命令是：

```bash
cd server
go build -o bin/talkwire-server.exe .
```

Linux 与 macOS 上输出文件名不带 `.exe`，`tools/ci.sh` 按平台加这个后缀。迁移脚本用 `go:embed` 编进了可执行文件，部署时不需要另带 SQL 文件。

::: tip 提示
仓库的 `.gitattributes` 规定文本文件一律以 LF 检出（`README.md`“开发工具”一节）。没有这条时，Git for Windows 默认的 `core.autocrlf=true` 会把 Go 源码检出成 CRLF，gofmt 会报整份文件。
:::

构建之后的检查命令（`go vet`、单测、竞态检测、一致性测试）见 [`server/README.md`](https://github.com/dishikun2786-prog/Talkwire/blob/main/server/README.md#构建与验证) 与 [测试与红线](/contributing/testing)。

## 最少的环境变量

只有 `DB_PASSWORD` 和 `JWT_SECRET` 是必填的，缺任何一个，服务端打印原因后立即退出。其余变量都有默认值，完整列表见 [环境变量](/server/env)。

| 变量 | 默认 | 说明 |
| --- | --- | --- |
| `DB_PASSWORD` | 无，必填 | 数据库口令 |
| `JWT_SECRET` | 无，必填 | 与宿主共享的 HS256 密钥：服务端用它验用户令牌和宿主发来的内部令牌，也用它给发往宿主的请求签令牌 |
| `DB_HOST`、`DB_PORT` | `localhost`、`5432` | 数据库地址 |
| `DB_USER`、`DB_NAME` | `xiaotaozi`、`xiaotaozi` | 默认值是原版部署写死的值，用别的库时须显式设置 |
| `IM_PORT` | `6062` | 监听端口 |

`web/QUICKSTART.md` 里在本机启动服务端的命令如下，`<同上>` 指与示例宿主相同的 `JWT_SECRET`：

```bash
JWT_SECRET=<同上> DB_HOST=127.0.0.1 DB_USER=<用户> DB_PASSWORD=<密码> DB_NAME=<库名> server/bin/talkwire-server.exe
```

这条命令用默认的 http 宿主模式，宿主须在 `http://127.0.0.1:6001` 上运行。没有宿主时改用 standalone 模式，见下文。

## 启动时做什么

`main.go` 按固定顺序启动。读配置、连数据库、建立令牌校验、建立宿主、安装模块、执行迁移任何一步出错，进程都打印原因后退出；推送服务商加载失败只停用这一家并打日志，不影响启动。

1. 读环境变量并校验取值；
2. 连接数据库并 ping 一次；
3. 建立令牌校验（共享密钥，或 `TALKWIRE_JWKS_URL` 指向的密钥集）；
4. 定下协议档位；
5. 加载已配置的推送服务商；
6. 建立宿主（http 或 standalone）；
7. 安装可选模块；
8. 执行核心与已安装模块的数据库迁移；
9. 启动后台任务（运行配置刷新、离线队列清理等），开始在 `0.0.0.0:<IM_PORT>` 上接受连接。

迁移在开始接受连接之前完成，所以服务端一旦开始监听，表结构已经就绪。

## 宿主模式 {#host-mode}

`TALKWIRE_HOST_MODE` 决定服务端从哪里得到好友关系、资料和运行配置，取值 `http`（默认）或 `standalone`，其他值让启动失败。

| 模式 | 宿主是什么 | 能装的模块 |
| --- | --- | --- |
| `http` | 一个 HTTP 服务，地址是 `MAIN_API_BASE_URL`（默认 `http://127.0.0.1:6001`）。服务端向它查好友关系与资料、请求删除媒体、拉运行配置，模块的宿主调用也走它 | 全部四个 |
| `standalone` | 没有宿主进程，上述内容来自进程内的配置，可从 `TALKWIRE_STANDALONE_CONFIG` 指向的 JSON 文件读入 | 只有 `groupjoin`、`e2ee` |

http 宿主要实现的端点见 [宿主接口契约](/host/)。

### standalone 宿主 {#standalone}

standalone 宿主不需要任何宿主进程。不设 `TALKWIRE_STANDALONE_CONFIG` 时，任何两个用户都可以互发消息，没有资料，运行配置用内置默认值。设了 `TALKWIRE_STANDALONE_CONFIG` 而 `TALKWIRE_HOST_MODE` 不是 `standalone` 时，服务端拒绝启动。

配置文件的写法见 `server/host/standalone/standalone.go` 的包注释：

```json
{
  "relations": "whitelist",
  "friends": [["alice", "bob"]],
  "profiles": {"alice": {"displayName": "Alice", "avatarUrl": "https://..."}},
  "runtimeConfig": {"maxMembers": 200},
  "e2eeConfig": {"enabled": true}
}
```

| 字段 | 含义 |
| --- | --- |
| `relations` | `allow-all`（默认，任何人可以给任何人发消息）或 `whitelist`（只有 `friends` 里列出的用户对，顺序无关） |
| `friends` | 好友对，只在 `whitelist` 下可用；在 `allow-all` 下写了会被拒绝。一对里两个 id 不能相同也不能为空 |
| `profiles` | 每个用户的 `displayName`、`avatarUrl`。没写资料的用户显示服务端的通用文案 |
| `runtimeConfig` | 运行配置，字段与 http 宿主的 `runtime-config` 端点相同 |
| `e2eeConfig` | 端到端加密模块的运行配置，字段与 `e2ee-config` 端点相同 |

`friends` 和 `profiles` 里的 id 是宿主的用户 id，也就是用户令牌的 `sub`。文件里出现未知字段时启动失败，拼错的键不会悄悄退回 `allow-all`。standalone 宿主没有媒体存储，删除媒体的请求什么都不做。

::: warning 注意
`runtimeConfig`、`e2eeConfig` 一旦写了，没写的数值项取内置默认值，没写的布尔项却是 `false`（`contract/host-api/host.openapi.yaml` 对 `runtime-config` 的说明，`e2ee-config` 同规则）。所以写了 `runtimeConfig` 又要离线推送时，必须写 `"fcmEnabled": true`；写了 `e2eeConfig` 时 `enabled`、`groupEnabled` 都要写明。两段都不写时用内置默认值，推送开关与加密开关都是开的。
:::

仓库的压测用 standalone 宿主，一致性测试也有 standalone 一档：在这种模式下黄金对话 30/33 通过，失败的是依赖宿主的通话两个、群 AI 一个场景（`server/README.md`）。

## 可选模块 {#modules}

`TALKWIRE_MODULES` 选择安装哪些可选模块，值是逗号分隔的模块名，或 `none`。不设时安装宿主支持的全部模块。

| 模块 | 内容 | 要求宿主提供 | 自己的表 | 手册 |
| --- | --- | --- | --- | --- |
| `groupjoin` | 扫码入群 | 无 | 有 | [扫码入群](/modules/group-join) |
| `e2ee` | 端到端加密的密钥包与离线信封 | 无 | 有 | [端到端加密](/modules/e2ee) |
| `groupai` | 群助手：群消息转给宿主回答 | 回答群问题（`host.GroupAI`） | 无 | [群助手钩子](/modules/bot-hook) |
| `call` | 音视频通话信令 | 管理通话会话（`host.CallSessions`） | 无 | [音视频通话](/modules/call) |

模块按上表的顺序安装（`server/modules.go` 的 `allModules`）。安装规则如下：

- **不设 `TALKWIRE_MODULES`**：宿主缺少某个模块需要的能力时，跳过这个模块并打一行日志，standalone 宿主因此只装 `groupjoin` 和 `e2ee`。
- **显式列出**：宿主缺少列出模块需要的能力时，服务端拒绝启动；列出未知的模块名也拒绝启动。
- **没装的模块**：它的动作得到 501 `module not enabled: <名>`，它的表不会被建，它的后台任务不会运行。

strict 档的登录应答在能力列表里为每个已安装模块加一项 `module:<名>`。e2ee 模块没装、库里却有被标为加密的群时，启动日志会打一条警告：这些群的成员客户端仍然期待加密。

## 协议档位 {#profile}

`TALKWIRE_PROTOCOL_PROFILE` 选择协议档位，取值 `strict`（默认）或 `legacy`，其他值让启动失败。legacy 与原版服务逐帧一致，用于迁移原版项目；strict 修正已知缺陷并增加字段与动作。两档的完整区别见 [legacy 与 strict](/protocol/profiles)。

档位也决定接受哪些用户令牌：

| 设置 | 默认 | 作用 |
| --- | --- | --- |
| `TALKWIRE_TOKEN_USER_ROLES` | `app_user` | strict 档只接受 `type=access` 且 `role` 在这个逗号分隔列表里的令牌；legacy 档接受任何带 `sub` 的有效令牌 |
| `TALKWIRE_ACCEPT_LEGACY_TOKENS` | 不设 | 设为 `1` 时 strict 档也接受刷新令牌、管理员令牌等非访问令牌，只用于迁移期，启动时打印警告 |
| `TALKWIRE_JWKS_URL` | 不设 | 用宿主公布的 JWKS 验用户令牌（RS256/ES256，RSA 至少 2048 位），只接受 https 或本机 http；内部令牌仍用 `JWT_SECRET` 验 |

设了 `TALKWIRE_JWKS_URL` 时，服务端启动时读一次密钥集，读失败只打日志、不阻止启动，之后登录时会重试。令牌的格式见 [签发 IM 令牌](/host/token)。
