# 一致性测试台

用真实 WebSocket 驱动 IM 服务端，把每个连接收到的帧归一化后录成“黄金对话”，再拿它比对别的服务端实现。参照实现是小桃子原版服务（源提交 `33e23250`）。

## 组成

| 路径 | 作用 |
| --- | --- |
| `oracle/build-oracle.sh` | 用 `git archive` 从源提交导出原版服务并编译到 `oracle/bin/`，源工作区不被触碰 |
| `oracle/SOURCE.sha256` | 导出源码的哈希，等于 Git 中存储的字节（LF）；`server/` 也用它核对是否原样 |
| `oracle/baseline-schema.sql` | 原版服务在空库上建出的表结构，`0001_baseline` 的依据 |
| `fakehost/` | 可脚本化的假宿主：好友关系、资料、配置、删媒体；记录 IM 对宿主的每次调用。`Control` 接口统一驱动它和外部宿主（`-host`，见下文） |
| `harness/` | 场景执行、归一化、比对 |
| `cmd/conformance/` | 命令行：`record`、`check`、`repeat`、`coverage`、`schema` |
| `replay/`、`cmd/replay/` | 客户端回放服务器：按一份黄金对话扮演服务端，用来测客户端 SDK；可注入断线、延迟、重复、丢弃 |
| `redlines/`、`cmd/redlines/` | 红线对照表的校验与生成：`contract/redlines/redlines.json` → `matrix.md` |
| `legacy-probes/` | Phase 0 开工前用来核实行为的一次性 Node 探针，已被本工具取代，仅作留档 |

场景在 `contract/protocol/scenarios/`，黄金对话在 `contract/protocol/transcripts/`。

## 前提

- Go 1.26.4 及以上
- 本机 PostgreSQL 测试库（默认 `127.0.0.1:55432`，用户 `postgres`）。口令从 `PGPASSWORD` 或 pgpass 文件读取，不会打印
- **每次运行都会删除并重建 `-db` 指定库的 `public` schema**，只能指向一次性测试库（默认 `talkwire_oracle`）

## 用法

```bash
cd tools/conformance
bash oracle/build-oracle.sh
go build -o bin/conformance.exe ./cmd/conformance
./bin/conformance.exe record                     # 对原版服务录制全部场景
./bin/conformance.exe repeat -n 3                # G0：每个场景连跑 3 次，结果必须一致
./bin/conformance.exe check -bin <新服务端> -db talkwire_new   # 用黄金对话检查新服务端
./bin/conformance.exe coverage                   # 协议里每个帧名是否都有黄金用例（不启动服务端）
./bin/conformance.exe schema -bin <新服务端> -db talkwire_new   # 新服务端建的表结构与原版比较
```

常用参数：`-only <名称片段>` 只跑部分场景；`-port`、`-hostport` 改端口；`-logs` 改服务端日志目录；`-host <URL>` 用外部宿主代替假宿主（见下文“外部宿主”）。

`check` 的其他参数：

| 参数 | 作用 |
| --- | --- |
| `-env KEY=VALUE` | 给被测服务端加环境变量，可重复，例如 `-env TALKWIRE_MODULES=none`、`-env TALKWIRE_PROTOCOL_PROFILE=strict` |
| `-additions strict` | 比较前从服务端的帧里去掉 strict 档新增的字段（顶层的 `errCode`、`protocol`、`capabilities`、`vapidKey`、`readSeq`，以及成功 `login` 与 `pres` 快照应答里的 `id`），并按场景报出去掉了多少处。去掉之后仍须逐帧一致，证明 strict 档只是在原版之上加字段 |
| `-overlay <目录>` | 目录里有同名黄金对话的场景改用它比较，其余照旧用 `-golden`。strict 档有意偏离原版的场景放在 `contract/protocol/transcripts-strict/`（六个，见协议规范第 19 节） |
| `-standalone` | 不起宿主进程（`TALKWIRE_HOST_MODE=standalone`），好友、资料、运行配置由场景写成配置文件交给服务端；不比较对宿主的调用。依赖宿主的场景（通话、群 AI）在这种模式下必然失败 |

Talkwire 服务端的三种检查：

```bash
./bin/conformance.exe check -bin ../../server/bin/talkwire-server.exe -db talkwire_new          # legacy：33/33（测试台给被测服务端设 TALKWIRE_PROTOCOL_PROFILE=legacy，-env 可改）
./bin/conformance.exe check -bin ../../server/bin/talkwire-server.exe -db talkwire_new   -additions strict -overlay ../../contract/protocol/transcripts-strict -env TALKWIRE_PROTOCOL_PROFILE=strict   # strict：33/33
./bin/conformance.exe check -bin ../../server/bin/talkwire-server.exe -db talkwire_new -standalone   # 30/33（通话两个、群 AI 一个需要宿主）
```

`schema` 依次比较四种情形，任何一项不同即失败：空库上新服务端建的表结构与原版建的（去掉 Talkwire 新增的表和列后逐项相同：列、索引、约束、序列）；重启后表结构不变、不再重复执行迁移；在原版建好的库上启动新服务端后，与原版相同（同样去掉新增），且与空库上建的完全相同。Talkwire 新增的表：`talkwire_schema_migrations`（迁移记录）、`im_devices`、`im_pending_acks`（多端在线）；新增的列：`im_fcm_tokens.provider`（推送服务商）、`im_fcm_tokens.device_id`、`im_e2e_identities.owner_device`（多端在线）。新增项列在 `cmd/conformance/main.go` 的 `schemaAddedTables`、`schemaAddedColumns` 里，加表加列时要同步。`-oracle`、`-oracle-db` 指定原版服务和它用的一次性库（默认 `talkwire_oracle`，必须与 `-db` 不同）。

多端同时在线是原版没有的能力，没有黄金对话；它由 `server/multidevice_test.go` 覆盖。同理，只有 Talkwire 服务端才有的帧（宿主卡片更新 `custom_update`，strict 档的会话变更摘要 `sync_summary` 的请求与应答）登记在 `harness/additions.go` 的 `AddedFrames` 里，写明由哪个服务端测试覆盖；`coverage` 把它们列为 `TEST` 而不是缺失。

`coverage` 以 `contract/protocol/schema/` 定义的帧名为准（客户端 51 个 action、服务端 71 个帧名），逐个统计黄金对话里用到它的步骤和帧，列出出处；有帧名没有任何黄金用例、或黄金对话用了 Schema 里没有的帧名时退出码为 1。故意发非法请求的步骤（`../schema/negative-cases.json`）不计入。同样的检查以单元测试形式放在 `harness/coverage_test.go`，导出了原版源码时还会核对源码里的 69 个 `Action*` 和 9 个 `Type*` 常量都在 Schema 里。

竞态检测：`bash ../race.sh`（在本目录下跑整个模块的 `go test -race`，Windows 上需要的 C 编译器与绕过办法见根目录 README 的"开发工具"）。

## 外部宿主（`-host`）

`-host <URL>` 不起假宿主：被测服务端的 `MAIN_API_BASE_URL` 指向这个 URL，场景对宿主的设置（好友关系、资料、运行配置）经它的**一致性控制接口**下发，它记录的 IM→宿主调用拿来与黄金对话比较。这样黄金对话可以检查任何语言写的真实宿主，而不只是服务端。`record`、`repeat`、`schema` 同样接受 `-host`。

控制接口挂在 `<URL>/__conformance/` 下，全是 `POST`、JSON 请求体，鉴权与 IM 调宿主相同（`Authorization: Bearer` 内部令牌，用测试台的密钥签名）。应答 200 即成功，其余状态码让场景失败：

| 路径 | 请求体 | 作用 |
| --- | --- | --- |
| `reset` | `{}` | 清空关系、资料、两份配置文档和调用记录 |
| `friends` | `{"a","b","friends":bool}` | `true`：两人成为已接受的好友；`false`：删除两人的关系 |
| `profile` | `{"user","displayName","avatarUrl"}` | 设置资料；`avatarUrl` 为空串表示没有头像 |
| `runtime-config`、`e2ee-config` | `{"document": 对象或 null}` | 设置配置文档；`null` 表示没有（宿主对该端点应答 404） |
| `calls` | `{}` | 应答 `{"calls":[…]}` 并清空：自上次以来 IM 对宿主的每次调用，形如 `{"method","path","query","body","status"}`，`query` 是排好序的字符串表（没有则省略），`body` 是请求体 JSON（没有则省略）；控制接口自身和两个配置端点的周期刷新不记录 |

通话和群 AI 模块的业务应答属于宿主自己，外部宿主要给出与假宿主相同的值才能比对（通话凭证 `fake-channel`、`fake-rtc-caller`、`fake-rtc-callee`、`fake-app`，`videoQuality` 接听 `sd`、升级 `hd`；未成为好友的通话 `not_friends`；含 `quota` 的提问 `quota_exceeded`，其余答 `fake answer`）。Node 宿主 SDK 的 `host-sdk-node/conformance/host.ts` 就是这样一个外部宿主：

```bash
cd host-sdk-node
TALKWIRE_HOST_SECRET=talkwire-conformance-secret-not-for-production-0123456789 node --import tsx conformance/host.ts --port 17101 --store prisma &
cd ../tools/conformance
./bin/conformance.exe check -bin ../../server/bin/talkwire-server.exe -db talkwire_new -host http://127.0.0.1:17101   # 33/33；内存版 --store memory 同样 33/33
```

## 每个场景怎么跑

1. 删除并重建测试库的 `public` schema；
2. 按场景脚本设置假宿主或外部宿主（好友关系、资料、运行配置）；
3. 启动被测服务端，`MAIN_API_BASE_URL` 指向宿主；
4. 逐步执行：每步发出请求、等应答（按 `id`；原版的登录和在线快照应答不带 `id`，只对这两种按 `action` 匹配），再等一个静置窗口收集所有连接上的推送；
5. 归一化：用户 id、uid、单聊主题替换成占位符；时间类字段写成 `<time>`，在线版本号写成 `<rev>`；群 id 按首次出现编号；每个连接的帧排序后比较；
6. 停止服务端。

## 场景里能写什么

见 `harness/scenario.go` 中 `Step` 的说明。值得注意的两项：

- `capture`：从应答里取值存为变量，后续步骤用 `${名称}` 引用；
- `optional`：原版服务里“有时送达、有时不送达”的帧（已确认的竞态）。匹配的帧仍记录在黄金对话的 `optional` 里，但不参与比对。每处都要在 `note` 里写明原因。

## 客户端回放服务器（测客户端用）

`check` 拿黄金对话测服务端；回放服务器反过来，拿同一份黄金对话扮演服务端去测客户端，不需要数据库，也不需要真服务端。

```bash
go build -o bin/replay.exe ./cmd/replay
./bin/replay.exe -scenario core/p2p -bindings          # 打印占位符的具体值（各用户 uid、单聊主题、群 id……）
./bin/replay.exe -scenario core/p2p -user A            # 在 ws://127.0.0.1:17062/ 等客户端以 A 的身份接入
./bin/replay.exe -scenario core/p2p -user A -faults '[{"kind":"disconnect","step":9}]'
```

客户端连上任意路径并登录（令牌不校验）。回放结束后打印 `PASS`（退出码 0）或 `FAIL` 加出错的步骤（退出码 1）；参数错误退出码 2。`-report` 把报告写成 JSON。

### 它怎么扮演服务端

- **主体连接**：被测客户端扮演场景里的一个用户（`-user`，即该用户登录过的全部连接），或指定的若干连接（`-conns`）。其他用户不存在，他们的动作只体现为主体收到的帧。
- **逐步推进**：每一步若轮到主体动作，就等它做——`connect` 等一条新连接，`close` 等它关闭，`login`/`send` 等一个请求并与场景比对（只比场景里写了的字段，多出的字段不管），`raw` 收到任何一帧即可。然后把参照服务端在这一步发给主体的帧原样发出，最后按 `closed` 关掉服务端主动关闭的连接。
- **客户端自己挑的值**：`id`、`reqId`、`clientMsgId`、`clientCallId` 由客户端决定，回放按"录制值 → 客户端实际值"一一对应，发出的帧里换成客户端的值。客户端没带 `id` 的请求，应答里也没有 `id`。同一个录制值必须始终对应同一个客户端值；`clientMsgId`、`clientCallId` 也不能两个录制值共用一个客户端值，`id`、`reqId` 可以复用（应答后客户端重用是正常的）。
- **占位符**：`${uid:X}` 等按协议规范的算法算出真实值；群 id、捕获的通话 id、扫码令牌换成形状正确的确定性值（同一场景每次回放都一样）；`<time>` 发成当前毫秒时间，`<rev>` 发成递增计数。
- **一步之内的顺序**：黄金对话按多重集记录，不保存同一步里帧的先后。回放默认把对主体请求的应答放在最前（`-order first`，与参照服务端大多数处理函数的实际顺序一致），`-order last` 放在最后，用来检查客户端是否依赖顺序。
- **偶发帧**：`optional` 里的帧默认也发出，`-skip-optional` 不发。目前的黄金对话里还没有录到偶发帧（`core/login` 声明了，但录制时竞态没有发生）。

### 故障注入

`-faults` 接受 JSON 数组（或 `@文件`），每项 `{"kind","step","conn","ms"}`：

| kind | 效果 | `step: 0` |
| --- | --- | --- |
| `delay` | 等 `ms` 毫秒再发这一步的帧 | 每一步 |
| `duplicate` | 这一步的每一帧发两次 | 每一步 |
| `drop` | 这一步的帧一帧都不发 | 不允许 |
| `disconnect` | 这一步开始前关掉 `conn`（省略则关掉全部主体连接），等客户端重连并**先登录**；登录应答是合成的 `{"action":"login","code":200,"uid":…}`，与参照服务端成功登录的形状一致（不带 `id`） | 不允许 |

断线重连后、下一个脚本请求到来前，客户端主动发的 `sub` 会按参照服务端的形状应答（`code 200`，带回请求的 `id` 和 `topic`）。**单聊订阅时参照服务端还会推一帧在线快照，这一帧不会合成**；`sub` 以外的补偿请求（比如补拉历史的 `get`）没有脚本可依，会让回放失败，`-allow-extra` 可以改为跳过这类不在脚本里的请求（记入报告、不应答、不影响 id 对应关系），继续等脚本里的下一个请求——但客户端等不到它的应答。

### 局限（如实写明）

- 回放是"照本宣科"，不是服务端模拟器：客户端只要偏离脚本（多发、少发、换了顺序发请求）就判失败，不会算出一个新的合理应答。
- 只校验客户端发了什么，不校验客户端收到帧之后做了什么（界面、存储、去重）——那要被测客户端自己的测试来断言，回放提供的是可重复的输入。
- 断线注入只模拟了"连接被关、重连登录"；参照服务端在断线时给其他用户推的在线状态变化、断线宽限期内的通话处理都不在主体的帧里，也就不会出现。

### 自测

`go test ./replay/` 用一个照脚本走的 Go 客户端回放全部黄金对话：每个场景按每个登录过的用户各回放一次（33 个场景共 88 次），再让一个客户端扮演全部连接各回放一次（33 次）；另有断线（含重连后补订阅、没登录就发请求）、重复（单步与每一步）、丢弃、延迟、应答顺序、偶发帧、发错请求、`-allow-extra` 跳过多余请求、比对失败不留下对应关系、错误参数、对应关系一一性等用例。另用一个 Node 客户端（只读借用 roubao `server/node_modules/ws`）驱动命令行跑通过 `core/p2p`，确认外部语言的客户端也能接入。变异检查：去掉 id 回写、把 `<time>` 发成字符串、不发偶发帧，三处都会被测试抓到。

## 红线对照表

`contract/redlines/redlines.json` 把计划里的每条红线（R1–R22 服务端与宿主，C1–C10 客户端）对应到已录的黄金对话步骤和计划中的自动化用例；`contract/redlines/matrix.md` 由它生成，不要手改。服务端、宿主、Android 与 Web 已经写好的用例在表里标“已写”：生成时扫描 `server/` 下 `*_test.go` 里的测试函数（`-tests`，默认 `../../server`）、`host-sdk-node/` 的 node:test 用例（`-host`，默认 `../../host-sdk-node`）、`android/` 各模块 `src/test` 下以红线编号开头的 Kotlin 测试函数（`-android`，默认 `../../android`），以及 `web/` 下的 Vitest 与 Playwright 用例（`-web`，默认 `../../web`），所以加了用例要重新 `write`。iOS 还没有代码，不扫描。

```bash
go run ./cmd/redlines check   # 校验，并确认 matrix.md 是最新的
go run ./cmd/redlines write   # 改完 JSON 后重新生成 matrix.md
```

黄金用例按 `"步号:动作"` 引用步骤（发送步骤写请求的 `action`，其他步骤写步骤类型，如 `"7:raw"`）。场景改动后，被引用的步骤若不存在或动作变了，`check` 会报出来，引用不会悄悄漂移。

`check` 还要求：

- 编号连续，R 在前、C 在后；
- 场景的 `redlines` 标签与对照表双向一致：标了某条红线的场景必须被它引用，被引用的场景必须标了它；
- 计划用例的任务号在 `tasks` 里，平台属于这条红线，用例名带红线编号（如 `TestR3_…`、`C1_…`）；
- 每条红线的每个平台至少有一个用例；服务端红线没有黄金用例时必须写明原因（`noGolden`）；参照服务违反的红线必须有 strict 档用例；
- 按红线命名的测试必须出现在对照表里，登记为该红线在那个平台上的用例：`server/` 里的 `TestR<n>_…`，`host-sdk-node/` 里的 `R<n>_…`，`android/` 与 `web/` 里的 `R<n>_…`、`C<n>_…`。

`go test ./redlines/` 对每条规则各做一次变异，确认它真能报出问题。
