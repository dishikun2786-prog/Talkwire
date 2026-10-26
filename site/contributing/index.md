---
title: "开发环境"
description: "参与开发 Talkwire 要装的工具链、仓库的目录布局，以及怎样用 tools/ci.sh 在本机跑 CI 的全部检查。"
---

# 开发环境

参与开发要装几套工具链，再用 `tools/ci.sh` 在本机跑一遍 CI 的检查。CI 跑的就是这个脚本：本机能过的，CI 上也能过。提交改动的规矩（先开 Issue、协议先改规范、合并请求怎么写）在仓库根目录的 [`CONTRIBUTING.md`](https://github.com/dishikun2786-prog/Talkwire/blob/main/CONTRIBUTING.md)。

## 工具链

只改一部分时，装那一部分要的工具就够了。版本取自仓库的 `README.md`、构建文件和 CI 配置。

| 工具 | 版本 | 用在 |
| --- | --- | --- |
| Go | 1.26.4 及以上（`go.mod` 的要求；本机验证用 1.27.0） | `server/`、`tools/conformance/`、`tools/loadtest/` |
| Node.js | 22 及以上（本机验证用 24.19.0，CI 用 24） | `host-sdk-node/`、`examples/`、`web/`、`site/`，以及 `tools/schema/check.mjs`、`tools/depcheck/` |
| JDK | 17 | `android/` |
| Android SDK | platform 34 | `android/`；路径写在 `android/local.properties` 的 `sdk.dir`（不进仓库） |
| Gradle | 8.7，由 wrapper 下载 | `android/`；AGP 8.4.2、Kotlin 1.9.24 |
| Python | 3（CI 用 3.12） | `tools/schema/gen.py`，由协议字段表生成 JSON Schema |
| PostgreSQL | CI 用 17 | 数据库测试与一致性测试，只用一次性的测试库 |
| staticcheck | 可选（CI 装 2026.2.2） | `tools/lint.sh`；没装时脚本说明并跳过这一项 |
| C 编译器 | — | 竞态检测 `tools/race.sh` 需要 cgo |
| Microsoft Edge | — | 本机的 Web 端到端测试默认用它，不另外下载浏览器 |

几点要注意：

- **换行一律 LF**（见 `.gitattributes`）。Git for Windows 默认 `core.autocrlf=true`，不加这条时 Go 源码检出后是 CRLF，哈希对不上冻结的原版源码，gofmt 也会报整份文件。
- **依赖版本钉死**：Node 项目的 lockfile 已提交，CI 上一律 `npm ci`。
- **Android 单测**跑在 Robolectric 4.12.2 上，第一次运行会下载约 144 MB 的 Android 运行时到 Maven 本地缓存，离线环境要先备好。
- **Windows 上的竞态检测**用私有的 MinGW-w64 GCC，只在这一条命令里打开 cgo、不改 PATH；位置可用 `TALKWIRE_MINGW` 指定。Linux 和 macOS 直接用系统编译器。

## 仓库布局

每个目录各有一份 README，写着它的命令与现状。

| 目录 | 内容 |
| --- | --- |
| `contract/protocol/` | 线协议：规范、JSON Schema、黄金对话（`transcripts/`，以及 strict 档的偏离场景 `transcripts-strict/`） |
| `contract/redlines/` | 红线对照表：`redlines.json` 与由它生成的 `matrix.md` |
| `contract/host-api/` | 宿主与 IM 服务端之间的 OpenAPI 3.1、uid 黄金向量 |
| `server/` | Go 实时服务端 |
| `host-sdk-node/` | Node 宿主 SDK `@talkwire/host-node` |
| `web/` | Web SDK 与可选模块（`web/packages/`）、示例（`web/sample/`）、端到端用例（`web/e2e/`） |
| `android/` | Android SDK、Compose 界面套件、可选模块与示例 App |
| `examples/` | `sample-host`（示例用的 Node 开发宿主）、`red-packet`（宿主自定义消息类型）、`artc`（阿里云 ARTC 3.0 通话） |
| `tools/` | `ci.sh` 全量检查；`conformance/` 一致性测试台与回放服务器；`depcheck/` 依赖方向与许可证；`schema/` 协议 Schema；`loadtest/` 压测；`lint.sh`、`race.sh`、`oldname.sh` |
| `docs/` | 计划与进度（`plan.md`）、架构决策记录（`adr/`）、可选模块手册、迁移说明 |
| `site/` | 本网站与开发手册（VitePress） |
| `deploy/site/` | 网站服务器的配置与部署说明 |
| `.github/` | CI 工作流、Issue 与合并请求模板 |

iOS SDK 还没开始（需要 macOS），仓库里没有对应的目录。

## 用 `tools/ci.sh` 跑检查

`tools/ci.sh` 是全部检查的唯一入口，分作业跑，每个作业对应 CI 的一个 job。`.github/workflows/ci.yml` 只负责装工具链、起一次性的 PostgreSQL，再调一次 `tools/ci.sh <作业>`。

```bash
bash tools/ci.sh                 # 全部作业
bash tools/ci.sh go web          # 只跑这两个
bash tools/ci.sh --list          # 列出每个作业的步骤
```

| 作业 | 检查什么 |
| --- | --- |
| `deps` | 依赖方向与许可证（`tools/depcheck`）、生成的 JSON Schema 是否最新、每个黄金帧能否通过 Schema、红线对照表是否最新、旧项目名只出现在该出现的地方 |
| `go` | gofmt、go vet、staticcheck；服务端、一致性测试台与压测工具的 Go 测试；服务端的竞态检测 |
| `conformance` | 用黄金对话检验服务端：legacy 档逐帧一致、strict 档、Node 宿主 SDK 当宿主、协议覆盖；本机构建了原版服务时还比较表结构 |
| `node` | Node 宿主 SDK 的构建、类型与测试；三个示例的类型与测试 |
| `web` | 生成的帧类型是否最新、各包与示例的类型、Vitest |
| `e2e` | 对示例全套（示例宿主、IM 服务端、网页示例）跑 Playwright |
| `android` | Android 单测、Lint、核心与可选模块的依赖图里没有 libsignal 与 RTC SDK、E2EE 模块里 libsignal 仍是钉死的版本 |
| `site` | 网站能构建；站内的死链接让构建失败 |

每一层测试是什么、红线怎么登记，见 [测试与红线](/contributing/testing)。

### 环境变量

数据库测试需要一次性的 PostgreSQL 库，库的连接经环境变量交给脚本。脚本不打印这些值，失败步骤只打印工具自己的输出。

| 变量 | 作用 |
| --- | --- |
| `TALKWIRE_TEST_DSN` | 一个一次性测试库，libpq 的 `key=value` 写法；测试会在里面删建 schema。它的 host、port、user、dbname 也交给一致性测试台 |
| `PGPASSWORD` | 一致性测试台的数据库口令；不设时从 pgpass 文件读 |
| `DATABASE_URL` | 另一个一次性库，给宿主 SDK 的 Prisma 测试用，表由 `prisma db push` 建 |
| `CONFORMANCE_ORACLE_DB` | 表结构比较时原版服务用的一次性库，默认 `talkwire_oracle`，必须与 `TALKWIRE_TEST_DSN` 的库不同 |
| `CONFORMANCE_HOST_PORT` | “Node 宿主 SDK 当宿主”这一步的宿主端口，默认 17101 |
| `E2E_CHANNEL` | 端到端测试用的浏览器；本机默认 Edge，设为空串时用 Playwright 自己的 Chromium（CI 这样设） |
| `CI` | 为 `true` 时缺数据库变量算失败而不是跳过，每个 Node 目录都用 `npm ci` |
| `CI_LOGS` | 把每一步的输出留在这个目录（CI 失败时上传） |

- 本机缺 `TALKWIRE_TEST_DSN` 或 `DATABASE_URL` 时，相关测试跳过，脚本会明说跳过了什么；CI 上一律算失败，不许悄悄跳过。
- `TALKWIRE_TEST_DSN` 的写法见服务端 README，例如 `host=127.0.0.1 port=55432 user=postgres dbname=talkwire_new sslmode=disable`，口令从 pgpass 读。
- **一致性测试每次都会删除并重建所指库的 `public` schema**，只能指向一次性的测试库。
- 表结构比较要先从源项目构建原版服务（`tools/conformance/oracle/build-oracle.sh`），源项目不在本仓库里。没有构建时这一步在任何地方都跳过，CI 也一样。

### 不经脚本、单独跑一部分

改一个子项目时，也可以直接用它自己的命令，各目录 README 里有完整的列表。几个常用的：

```bash
cd server
go build -o bin/talkwire-server.exe .
go vet ./...
```

```bash
npm install            # 在 web/ 下
npm run gen            # 由 JSON Schema 重新生成帧类型；gen:check 只检查是否最新
npm run typecheck      # 各包、示例与 e2e 的类型检查
npm test               # Vitest
npm run sample         # 示例，http://127.0.0.1:5173
```

```bash
cd tools/conformance && bash ../race.sh
cd server && bash ../tools/race.sh
```

Web 与 Android 的回放测试要先构建回放服务器（`cd tools/conformance && go build -o bin/replay.exe ./cmd/replay`），对真实服务端的测试要先构建服务端二进制；缺了这些的用例显示为跳过，不算失败。
