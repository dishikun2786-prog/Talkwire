---
title: "测试与红线"
description: "Talkwire 的各层测试（单测、真实服务端、黄金对话、Schema、端到端、Android 依赖图），以及红线对照表与旧项目名检查。"
---

# 测试与红线

Talkwire 的测试分几层：单测管函数，真实服务端测试管整条链路，黄金对话管线协议逐帧不走样。另有两张表约束改动：红线对照表要求每条“出过事故的规矩”都有用例，旧项目名检查不让改名前的名字回到代码里。全部检查都经 `tools/ci.sh` 跑，环境与命令见 [开发环境](/contributing/)。

## 各层测试

| 层 | 测什么 | 在哪里 | 需要什么 |
| --- | --- | --- | --- |
| 单测 | 函数与组件 | 各子项目自己的测试 | 本机工具链 |
| 真实服务端 | 客户端或宿主对一个真实的 IM 服务端跑整条流程 | Web、Android、宿主 SDK、服务端进程内 | 服务端二进制，`TALKWIRE_TEST_DSN` |
| 一致性 | 服务端发出的每一帧与黄金对话一致 | `tools/conformance` | 服务端二进制，`TALKWIRE_TEST_DSN` |
| 回放 | 客户端发出的每一帧与黄金对话一致，并经得起故障 | Web、Android | 回放服务器 |
| Schema | 黄金对话里的每一帧符合协议的 JSON Schema | `tools/schema` | Node、Python |
| 端到端 | 浏览器里点一遍示例全套 | `web/e2e` | 服务端二进制，`TALKWIRE_TEST_DSN`，浏览器 |
| Android 静态检查 | Lint、解析后的依赖图 | `android/` | JDK、Android SDK |

缺数据库、缺二进制或缺回放服务器的用例在本机显示为跳过，不算失败；CI 上缺数据库算失败。

### 单测

每个子项目用自己的测试框架。

- **服务端**（Go）：`go test ./...`。数据库用例在 `TALKWIRE_TEST_DSN` 指向的库里建临时 schema、用完删除，不设时跳过。多端在线、并发类用例在进程内起真实的服务端和 WebSocket（`server/live_test.go`）。另有 `tools/lint.sh`（gofmt、go vet、staticcheck）与竞态检测 `tools/race.sh`。
- **宿主 SDK**（node:test）：存储的内存版与 Prisma 版跑同一套用例，Prisma 用例需要 `DATABASE_URL`，并核对 `prisma/talkwire.sql` 与模型一致。
- **Web**（Vitest）：`npm test`，React 组件用 jsdom。
- **Android**（JUnit，Android 模块跑在 Robolectric 4.12.2 上）：`talkwire-core` 是纯 JVM 模块，其余模块跑 debug 的单测。`talkwire-core` 的测试夹具提供存储契约 `ChatStoreContract` 与内存服务器，其他模块共用。

### 对真实服务端的测试

这一层把客户端或宿主接到一个真正在跑的 IM 服务端上。

- **Web**：M1 链路的用例需要 `server/bin/talkwire-server(.exe)` 与 `TALKWIRE_TEST_DSN`。口令只经环境变量交给服务端，不进日志。
- **Android**：`M1RealServerTest` 以独立模式启动服务端，strict、legacy 两档各跑一遍“登录、单聊、确认与幂等、历史分页、离线补拉、断线重连、撤回、清空、已读、在线状态”。`SampleStackTest` 另起示例宿主和宿主模式的服务端，走一遍示例 App 的全链路，还需要 Node 与 `examples/sample-host` 的依赖。
- **宿主 SDK**：契约测试双向检查。宿主插件的每个应答符合 `host.openapi.yaml`，且契约里每个操作都被调到；`ImClient` 对真实的 IM 服务端调遍 `im-internal.openapi.yaml` 的每个操作。

### 一致性测试：两个档位的黄金对话

黄金对话是对原版服务录下来的场景：每一步发了什么、每条连接收到了什么。`tools/conformance` 用它们检验 Talkwire 服务端：

| 检查 | 做法 |
| --- | --- |
| legacy 档 | 与原版逐帧一致 |
| strict 档 | 比较前去掉 strict 档新增的字段（`errCode`、`protocol`、`capabilities`、`vapidKey`、`readSeq`，以及两种应答的 `id`），去掉之后仍须逐帧一致；有意偏离原版的六个场景改用 `contract/protocol/transcripts-strict/` 里另录的版本 |
| Node 宿主 SDK 当宿主 | 用 `host-sdk-node/conformance/host.ts` 代替测试台的假宿主，跑全部黄金对话 |
| 覆盖 | 协议 Schema 里的每个帧名都要有黄金用例，不启动服务端 |
| 表结构 | 新服务端在空库和原版库上建出的表与原版相同（去掉 Talkwire 新增的表和列）；需要先从源项目构建原版服务，本仓库里没有源项目 |

比较时每一步按连接比较收到的帧的多重集合，不比较到达顺序；用户 id、topic、时间戳、在线版本号先换成占位符。多端在线、`sync_summary` 这类原版没有的能力没有黄金对话，由服务端的单测覆盖。规则详见规范第 19 节（[规范全文](/protocol/spec)），档位见 [legacy 与 strict](/protocol/profiles)。

::: warning 注意
一致性测试每个场景都会删除并重建所指库的 `public` schema，只能指向一次性的测试库。
:::

### 回放：用黄金对话测客户端

回放服务器反过来，拿同一份黄金对话扮演服务端去测客户端，不需要数据库，也不需要真服务端。客户端多发、少发或发错一帧都会失败。

```bash
cd tools/conformance && go build -o bin/replay.exe ./cmd/replay
```

它还能注入故障：延迟、每帧发两次、整步不发、断线并等客户端重连登录。Web 与 Android 都用它跑核心场景和可选模块的场景（如 Android 的 `ReplayScenarioTest`、`GroupJoinReplayTest`、`CallReplayTest`、`E2eeSignalingReplayTest`）。回放只校验客户端发了什么；收到帧之后做了什么，由客户端自己的测试断言。

### Schema 校验

协议的 JSON Schema 由 `tools/schema/gen.py` 从字段表生成，`check.mjs` 用它校验黄金对话里的每一帧。

```bash
python -I tools/schema/gen.py            # 重新生成
python -I tools/schema/gen.py --check    # 生成结果与仓库不一致时退出码 1
node tools/schema/check.mjs
```

- `client-frames.schema.json`：客户端发出的帧，允许未声明的字段，因为服务端必须忽略它们。
- `server-frames.schema.json`：服务端发出的帧，**禁止**未声明的字段，保证服务端不会发出规范里没写的字段。客户端运行时不得用它校验收到的帧。
- 场景里故意发出的非法请求登记在 `negative-cases.json`，它们必须**不通过**客户端 Schema。

改协议时只改 `gen.py` 的字段表，不要手改生成的文件。

### Playwright 端到端

`web/e2e` 对真实的示例全套跑浏览器：示例宿主、IM 服务端（宿主模式、strict 档）和网页示例，由全局准备自己启动。用例有四组：

| 文件 | 内容 |
| --- | --- |
| `chat.spec.ts` | 单聊实时收发；建群、拉人、群聊；同一账号两个标签页加第二台设备；引用回复与转发；撤回留痕与本地搜索 |
| `call.spec.ts` | 呼叫、接听、切视频、挂断，再拒接一通 |
| `group-join.spec.ts` | 凭链接入群，再走一遍需要审核的入群 |
| `admin.spec.ts` | 通话进入通话记录；读聊天记录并为双方清空 |

- 端口默认 16001、16062、15173，可用 `E2E_HOST_PORT`、`E2E_IM_PORT`、`E2E_SAMPLE_PORT` 改，不会碰已经在 6001、6062、5173 上跑的开发环境。
- 默认用本机的 Microsoft Edge，`E2E_CHANNEL=chrome` 改用 Chrome，`E2E_HEADED=1` 显示窗口。子进程日志在 `web/e2e/.stack-logs/`。

### Android：Lint 与依赖图

`android` 作业在单测之外还查三件事：

- **Lint**：`./gradlew lintDebug`。错误让构建失败，警告（例如有更新的依赖版本）只报告。
- **依赖图里没有 libsignal 与 RTC SDK**：`talkwire-ui`（包含核心）、`talkwire-group-join`、`talkwire-call`、`talkwire-video-compress` 解析后的 `releaseRuntimeClasspath` 里不得出现 libsignal 或 DingRTC。`tools/depcheck` 只看声明的依赖，这一步看的是含传递依赖的解析结果。
- **libsignal 没被升级**：`talkwire-e2ee` 解析出的 `libsignal-android` 与 `libsignal-client` 必须正好是 0.73.0。

真机才能看的项目（按住说话的手感、提示音、来电时序）不是测试函数，结果记在 [`android/DEVICE_CHECKS.md`](https://github.com/dishikun2786-prog/Talkwire/blob/main/android/DEVICE_CHECKS.md)，写明设备、做了什么、怎么判断、没做什么。

### 依赖方向与许可证

`node tools/depcheck/check.mjs` 检查三件事：核心不链接 libsignal 与 RTC SDK；依赖只往下走（模块只依赖核心、互不依赖，RTC 适配器只多依赖它适配的模块）；许可证（我们的包是 Apache-2.0，唯一的例外 `android/talkwire-e2ee` 必须是 AGPLv3 并在每个源文件带 SPDX 标记）。`web/packages` 与 `android/` 下新增的包或模块必须在脚本里归类，否则报错。

## 红线

红线是一组必须由用例钉住的规矩，针对丢消息、串号、越权这类事故：对照表把每条红线对应到已录的黄金对话步骤和计划中的自动化用例。一共 32 条：服务端与宿主 22 条（R1–R22），客户端 10 条（C1–C10）。修这类问题时按红线编号命名用例并登记，是 `CONTRIBUTING.md` 的要求。

| 文件 | 作用 |
| --- | --- |
| [`contract/redlines/redlines.json`](https://github.com/dishikun2786-prog/Talkwire/blob/main/contract/redlines/redlines.json) | 真相源：每条红线对应已录的黄金对话步骤和计划中的用例 |
| [`contract/redlines/matrix.md`](https://github.com/dishikun2786-prog/Talkwire/blob/main/contract/redlines/matrix.md) | 由 JSON 生成的对照表，不要手改 |

在 `tools/conformance` 下运行：

```bash
go run ./cmd/redlines check   # 校验，并确认 matrix.md 是最新的
go run ./cmd/redlines write   # 改完 JSON 后重新生成 matrix.md
```

### 红线用例的命名

用例名带红线编号，`redlines` 扫描代码，把写好的用例在对照表里标为“已写”：

| 位置 | 命名 | 扫描范围 |
| --- | --- | --- |
| 服务端 | `TestR<n>_…` | `server/` 下 `*_test.go` 里的测试函数 |
| 宿主 | `R<n>_…` | `host-sdk-node/` 的 node:test 用例 |
| Android | `R<n>_…` 或 `C<n>_…` | 各模块 `src/test` 下的 Kotlin 测试函数 |
| Web | `R<n>_…` 或 `C<n>_…` | `web/` 下 `*.test.ts(x)`、`*.spec.ts` 里的 `it(…)`、`test(…)` |

例如服务端的 `TestR16_CallStateCASUnderRace`、Web 的 `it("C3_ResumeFromContiguousEnd", …)`。加了用例要重新 `write`。

### `check` 查什么

- 按红线命名的测试必须出现在对照表里，登记为这条红线在那个平台上的用例；起了红线名却没登记的会被报出来。
- 编号连续，R 在前、C 在后；计划用例的任务号存在、平台属于这条红线、名字带红线编号。
- 每条红线的每个平台至少有一个用例；服务端红线没有黄金用例时必须写明原因；原版违反的红线必须有 strict 档的用例。
- 黄金用例按“步号:动作”引用步骤；场景改动后，被引用的步骤不存在或动作变了会被报出来，引用不会悄悄漂移。
- 场景的 `redlines` 标签与对照表双向一致。

`go test ./redlines/` 对每条规则各做一次变异，确认它真能报出问题。

## 旧项目名检查

项目原来的名字只允许出现在还要读取改名前写下的东西的地方：旧环境变量名、旧的迁移记录表、宿主表的改名脚本，以及解释它们的说明。`tools/oldname.sh` 用 `git grep` 不分大小写地查已提交的文件，在别处出现、或有文件路径还带着旧名时失败。

```bash
bash tools/oldname.sh
```

允许出现旧名的文件列在脚本开头的 `allowed` 里，新的地方确实需要它时加进这个列表。`site/` 不在列表里，所以网站页面写到改名时不能写出旧名。改名的来龙去脉见 [`CHANGELOG.md`](https://github.com/dishikun2786-prog/Talkwire/blob/main/CHANGELOG.md)。
