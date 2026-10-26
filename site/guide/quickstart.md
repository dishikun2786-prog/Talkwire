---
title: "快速开始"
description: "在一台开发机上把 PostgreSQL、IM 服务端、开发宿主、Web 示例和 Android 示例连成一套，用两个账号互发消息。"
---

# 快速开始

本页在一台开发机上跑起整套 Talkwire：PostgreSQL、IM 服务端、开发宿主 `examples/sample-host`、Web 示例 `web/sample`，以及可选的 Android 示例 `android/sample-app`。跑通后用两个名字登录，就能互发消息、建群、扫码入群、打演示通话。

Talkwire 还没有发布到 npm 和 Maven，下面的命令都在仓库里运行。

::: warning 注意
开发宿主的 `/dev/login` 凭名字就能登录任何人，默认只监听 127.0.0.1，只能在本机开发用。
:::

## 准备

| 工具 | 版本 | 用于 |
| --- | --- | --- |
| Go | 1.26.4 及以上 | 构建服务端 |
| Node.js | 22 及以上（宿主 SDK 声明的最低版本是 20） | 宿主 SDK、开发宿主、Web 示例 |
| PostgreSQL | 一个空库；CI 用的是 PostgreSQL 17 | 服务端的数据，服务端启动时自己在库里建表 |
| JDK 17、Android SDK（platform 34）、一台打开了 USB 调试的手机 | — | 只有跑 Android 示例时需要 |

整套跑起来是三个进程，开发宿主和 IM 服务端必须用**同一个** `JWT_SECRET`：宿主用它签发登录令牌，服务端用它验证。

| 进程 | 地址 |
| --- | --- |
| 开发宿主 | `http://127.0.0.1:6001` |
| IM 服务端 | 端口 6062，WebSocket 在 `/ws/im` |
| Web 示例（Vite） | `http://127.0.0.1:5173` |

## 1. 构建服务端

```bash
cd server
go build -o bin/talkwire-server.exe .
```

Linux 和 macOS 上输出文件用 `bin/talkwire-server`，不带 `.exe`；后面的命令同样去掉 `.exe`。

## 2. 启动开发宿主

开发宿主经 `file:` 依赖引用 Node 宿主 SDK，而 SDK 的入口在构建产物 `dist/` 里，所以先构建 SDK：

```bash
cd host-sdk-node
npm install
npm run build
```

再启动开发宿主（另开一个终端，从仓库根目录起）：

```bash
cd examples/sample-host
npm install
JWT_SECRET=<至少 32 个字符> npm start
```

Windows PowerShell 里写成 `$env:JWT_SECRET="…"; npm start`。`JWT_SECRET` 不足 32 个字符时开发宿主拒绝启动。

| 环境变量 | 默认 | 作用 |
| --- | --- | --- |
| `JWT_SECRET` | 无，必填 | 与 IM 服务端共享的密钥，至少 32 个字符 |
| `HOST` | `127.0.0.1` | 监听地址 |
| `PORT` | `6001` | 监听端口 |
| `IM_BASE_URL` | `http://127.0.0.1:6062` | IM 服务端的地址，开发宿主经它调 IM 的内部接口 |
| `MEDIA_DIR` | `data/media`（相对启动目录） | 上传的媒体文件存在这里 |

开发宿主用 `@talkwire/host-node` 提供 IM 服务端要调的宿主接口、App 用的资料与联系人接口、媒体上传，另有红包示例、只有信令的演示通话和后台接口。它把用户和联系人存在内存里，重启后每个人都要重新登录。它的入口 `examples/sample-host/src/main.ts`：

<<< @/../examples/sample-host/src/main.ts

## 3. 启动 IM 服务端

另开一个终端，在仓库根目录：

```bash
JWT_SECRET=<同上> DB_HOST=127.0.0.1 DB_PORT=5432 DB_USER=<用户> DB_PASSWORD=<密码> DB_NAME=<库名> server/bin/talkwire-server.exe
```

- `DB_PASSWORD` 和 `JWT_SECRET` 没有默认值，缺任何一个服务端启动即退出。
- 其余用默认值就能和开发宿主连上：宿主模式 `http`、宿主地址 `http://127.0.0.1:6001`、协议档位 `strict`、端口 6062、装宿主支持的全部模块。
- 服务端监听 `0.0.0.0`，启动时在空库里建表，日志里出现 `IM Server listening on 0.0.0.0:6062` 即就绪。

::: tip 提示
服务端拼数据库连接串时不给值加引号，口令里有空格会连不上，本地开发时换一个不含空格的口令。
:::

健康检查端点会顺带检查数据库：

```bash
curl http://127.0.0.1:6062/api/health
```

正常时返回 `{"status":"ok","service":"im-server"}`；连不上数据库时返回 503。完整的配置项见[环境变量](/server/env)。

## 4. 打开 Web 示例

再开一个终端：

```bash
cd web
npm install
npm run sample
```

打开 `http://127.0.0.1:5173`。示例的 Vite 开发服务器把 `/dev`、`/api`、`/media` 转给开发宿主、把 `/ws` 转给 IM 服务端，页面只和一个源打交道；它直接引用 `web/packages/` 下各包的源码，不需要先构建。地址可以用 `SAMPLE_HOST_URL`、`SAMPLE_IM_URL`、`SAMPLE_PORT` 改。

在两个浏览器（或一个普通窗口、一个无痕窗口）里用不同的名字登录，名字是 2–20 个字母、数字或下划线。后登录的人自动和之前登录过的人互为好友。然后可以试：

- 互发消息、建群；聊天页头的“⋯”打开会话信息页，管理群成员与角色、改群名、清空、退群或解散。
- 群信息页里有群二维码与入群审核，把入群链接拿到另一个浏览器里打开就能加入。
- 单聊页头发起语音或视频通话。开发宿主给的是演示凭证，只有信令，没有声音和画面。
- 页头的“管理后台”（`#/admin`）是后台页面：用名字和角色（`admin` 或 `superadmin`）登录，能看到每通通话的记录；超级管理员能为双方清空单聊。

## 5. 装上 Android 示例（可选）

Android 示例与 Web 示例连同一个开发宿主和 IM 服务端，手机上的用户可以和网页上的用户互发消息。构建前在 `android/local.properties` 里写好 Android SDK 的路径 `sdk.dir`。

手机经 USB 访问电脑上的端口：

```bash
adb reverse tcp:6001 tcp:6001
adb reverse tcp:6062 tcp:6062
adb reverse tcp:5173 tcp:5173   # 可选：示例的群二维码链接指向 Web 示例的入群页
```

装上示例（包名 `io.talkwire.sample`，不影响手机上的其他 App）：

```bash
cd android
./gradlew :sample-app:installDebug
```

- 只有一台手机时，先登录一个名字，点左下角“退出”，再用另一个名字登录；切换账号时本地缓存整个清掉，不会串号。
- 会话列表右上角“＋ → 发起聊天”能看到之前登录过的人。
- 示例没有配置 Firebase 项目，所以没有离线推送；通话只有信令（`NoMediaEngine`）。
- 扫码入群：在 App 里粘贴入群链接，或打开 `talkwiresample://join/<令牌>`。

## 不开浏览器和手机也能跑一遍

两套自动化用例会自己起开发宿主和 IM 服务端，把同样的流程走一遍。它们都需要构建好的服务端二进制、第 2 步构建好的宿主 SDK，以及环境变量 `TALKWIRE_TEST_DSN`：一个可以随便写的 PostgreSQL 测试库，写成 libpq 的 `key=value` 形式。

- Web：在 `web/` 下 `npm run e2e`。它用自己的端口 16001、16062、15173，不碰已经在跑的开发环境；默认用本机的 Microsoft Edge，`E2E_CHANNEL=chrome` 改用 Chrome。走完单聊、群聊、多端、扫码入群、通话、后台。
- Android：`talkwire-core` 的 `SampleStackTest`（在 `android/` 下 `./gradlew :talkwire-core:test`），两个用户走一遍登录、单聊、语音、建群和红包。

::: warning 注意
`TALKWIRE_TEST_DSN` 只能指向一次性的测试库：测试会在里面建临时 schema、写入数据。
:::

## 示例里的接入代码

两个示例登录之后建起的对象就是接入方要写的全部代码：令牌、地址、本地存储、宿主适配（`ImUiHost`）和通知。

::: code-group

<<< @/../web/sample/src/stack.ts [Web]

<<< @/../android/sample-app/src/main/kotlin/io/talkwire/sample/ChatSession.kt [Android]

:::

## 下一步

- [安装与配置](/server/)、[环境变量](/server/env)：在服务器上部署 IM 服务端。
- [签发 IM 令牌](/host/token)、[Node 宿主 SDK](/host/node-sdk)：把示例宿主换成你自己的业务后端。
- [Web SDK](/client/web)、[Android SDK](/client/android)：在自己的 App 里接入客户端。
- [音视频通话](/modules/call)、[扫码入群](/modules/group-join)：装可选模块。
