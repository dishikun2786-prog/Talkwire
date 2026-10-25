# 开发计划

权威版本在可编辑文档《ImKit 详细开发计划》：https://claude.ai/artifact/Eh89avD5BQyVK1Urfz7Lss

## Phase 1 结果（2026-10-25）

1.1–1.13 全部完成，各任务的内容与验证结果见 `server/README.md`。1.13 是计划外加的：strict 档修正 D1、D2、D5、D7、D8（原计划 1.7–1.9 没有覆盖它们，2026-10-25 定为另排任务）。legacy 档 32/32 逐帧一致，strict 档 32/32，表结构比较 5 项通过，竞态检测三轮无报告，staticcheck 无报告。

**闸门 G1 已通过（2026-10-25）**。验收前待定的 6 项按建议定下（见下节），随后装了 staticcheck（2026.2.2）并修掉它报出的 6 处；其中一处是原版的缺陷：`IM_HUB_CHAN_BUF`、`IM_PRESENCE_CHAN_BUF` 读进来了却没被使用（F62），已接上，默认值不变。

## Phase 2 结果（2026-10-25）

2.1–2.8 全部完成：`contract/host-api/` 的两份 OpenAPI 3.1（宿主实现的端点、IM 暴露的内部端点，逐个标明必须/可选/模块、超时与失败时的行为）与 uid 黄金向量（Go、TypeScript 共用，Kotlin、Swift 以后照用）；`host-sdk-node/`（宿主插件、App 接口、媒体、后台、IM 客户端，内存与 Prisma 两种存储，S3 与本地磁盘两种文件存储）；服务端新增通用的宿主卡片更新接口 `custom-broadcast`，`red-packet-broadcast` 保留为兼容形式；红包卡做成 `examples/red-packet/`。

验收三项均达成：用 Node SDK（Prisma 连测试库 `imkit_host`）替换假宿主后，黄金对话 33/33 全绿（legacy、strict 两档，内存版同样通过）；OpenAPI 契约测试双向通过（宿主应答、黄金对话里录到的 IM→宿主请求、对真实 IM 服务端的内部端点调用，三者都符合契约且覆盖每个操作）；“用其他语言实现宿主”的说明在 `contract/host-api/README.md`。为此一致性测试台加了外部宿主模式（`-host`，宿主实现一组控制接口即可被黄金对话检查），并新录核心组场景 `core/host-red-packet`。

过程中发现并修正的契约缺口：通话与群 AI 端点没写 400 和“不提供该模块”的 404。G1 收尾时另修了一个原版的并发缺陷 F63（登录中途断开的连接被注册成在线）。

## Phase 3 结果（2026-10-25）

Android 核心 SDK（无 UI）完成，**M1 达成**。四个模块：`imkit-core`（纯 JVM：`ImClient`、`ChatEngine`、存储接口、在线三态、通知决策、推送令牌注册）、`imkit-storage-room`（Room 新库 v1，可选的加密粉碎，源项目 v18 旧库导入器）、`imkit-android`（通知渲染、媒体缓存、图片压缩、AndroidKeyStore 包裹密钥）、`imkit-push-fcm`。各任务的结果与测试方法见 `android/README.md`。

验收：`M1RealServerTest` 对独立运行的服务端（strict、legacy 两档，各连续 3 次）跑通“登录 → 单聊文本 → 确认与幂等 → 历史分页 → 离线后补拉 → 断线重连 → 撤回 → 清空 → 已读 → 在线状态”；C1–C5、C9 都有用例，回放测试用黄金对话扮演服务端并注入断线、丢应答、重复帧；`imkit-core` 只依赖协程与 OkHttp。对照表里 Android 的 20 个计划用例写了 13 个，其余 7 个属于 Phase 4 与 E2EE 模块。红线工具现在也扫描 Android 的 Kotlin 测试。3.10 里“迁移源项目单测”一项：约 1.5k 行逐个文件看过，适用于核心 SDK、又没被现有用例覆盖的移植成新用例，其余（时间格式、气泡、联系人与头像）属于 UI 或宿主，清单在 `android/README.md`。

过程中修的客户端问题：`stop()` 后立刻 `start()` 再等待登录，会读到上一轮的“已停止”而立即返回（`start()` 现在同步进入连接中）；带 id 的登录应答在已登录时被吞掉，发它的请求只能超时（现在先按 id 关联）。

未验证与未做：没有真机验证（AndroidKeyStore、通知响铃与来电时序、FCM 收发）；视频压缩与缩略图未做（3.9 只做了缓存与图片压缩）；旧库导入没拿真实用户的旧库验证；本地消息搜索未做。minSdk 用 26（源项目的值），计划 3.1 写的是 27，待定。

## Phase 4 结果（2026-10-25）

Compose 界面套件 `imkit-ui` 与示例完成，**M2 达成**（真机一台）。4.1 组件（会话列表、单聊群聊、通讯录与选人、群信息与成员管理、消息操作、按住说话、表情与附件面板、已读名单、前台横幅）；4.2 `ImTheme` 与英文、简体中文、越南文文案，弹层与对话框统一包一层以跟随 App 语言；4.3 `MessageTypeRegistry`，气泡与预览按 `contentType` 选；4.4 宿主只实现 `ImUiHost` 一个接口，深链接 `imkit://`；4.5 `sample-app` 配 Node 开发宿主 `examples/sample-host`，接入说明 `android/QUICKSTART.md`；4.6 红包卡片 `examples/red-packet/android` 只靠注册接入。依赖与源项目一致（Compose BOM 2023.10.01）。

验收：sample-app 在一台小米（Android 15）上跑通登录、单聊、输入中与在线、双向已读、前台横幅与提示音、后台通知与点开、按住说话与上滑取消、建群、红包收发与实时更新、撤回、换账号，逐项与依据在 `android/DEVICE_CHECKS.md`。红包卡片不改 SDK 即显示。`m2/SampleStackTest` 用两个用户对真实服务端与示例宿主跑通同一条链路（不需要手机）。Android 单测 165 个、示例 2 个，全部通过；对照表里 Android 的计划用例写了 16/20，剩下的两个真机项是人工检查（结果记在 DEVICE_CHECKS.md），另两个属于 E2EE。

真机上发现并修了 5 个问题，都先补了能复现的用例：会话打开期间到达的消息不算已读；“新消息”分隔线标在打开后才到的消息上；换账号或新设备后单聊回不来（协议没有列出单聊的调用，现在按宿主联系人找回，`ImUiConfig.chatsFromContacts`）；录音浮层文字重复、按钮压住计时器；示例的退出按钮盖住麦克风。

未验证与未做：相册、拍摄、文件入口没在真机上点（选择器会显示私人照片）；来电时序、FCM、多设备同账号未在真机验证；视频压缩未做（首帧缩略图已做）；新设备上单聊历史全算未读（服务端只存群聊已读位置，要改服务端）；撤回不留提示行。

## Phase 5 结果（2026-10-25）

Web SDK 完成，在 `web/`（npm 工作区，说明见 `web/README.md`）。5.1 `@imkit/protocol`：帧类型由 JSON Schema 生成（`npm run gen:check` 防漂移），黄金对话逐帧过类型检查；5.2 `@imkit/client` 的 `ImClient`（世代号、抖动退避、登录确认前不算已连接、按 id 关联、旧服务端按 action 关联）与在线三态；5.3 同一账号的多个标签页共用一条连接：优先 SharedWorker，起不来时退回 BroadcastChannel 选主（有 Web Locks 用它，否则心跳），一个标签的发送与已读像第二台设备那样同步给其他标签；5.4 `@imkit/storage-idb`，表与 Room 对应，可选按会话的不可导出 AES-GCM 密钥加密，删会话即粉碎；5.5 `ChatEngine` 按协议规格重写同步顺序；5.6 前台横幅与系统通知的决策、Web Push 订阅与显示；5.7 媒体上传与 MediaRecorder 录音（优先 Android 能播的容器）；5.8 回放与真实服务端用例；5.9 `@imkit/react`：CSS 变量主题，英文、简体中文、越南文文案可替换，消息类型注册表与 Android 同名；5.10 示例 `web/sample`（Vite，代理示例宿主与 IM 服务端）与 Playwright 端到端用例。

验收：Vitest 54 个全部通过，其中回放 9 个，另有对真实服务端的 M1 链路（strict、legacy 两档）。Playwright 在本机 Edge 上跑通三条链路：两人实时互发、建群并对话、同一账号两个标签页加第二台设备同时收到，连续 3 次通过。对照表里 Web 的计划用例 12/12 已写：新登记了 `C1_IgnoresDuplicatedFrames`、`C9_UnknownRosterMeansMember` 两个 Web 用例，红线工具现在也扫描 Web 的测试。

过程中修的问题：SharedWorker 起不来时（脚本加载失败，或浏览器拦截了它的网络请求），标签页一直停在“连接中”，现在退回选主并重连；示例建群后标题一直显示群号，`ChatEngine.createGroup` 现在建群后立即按群名列出，再刷新群列表（Android 由界面层在建群后调用 `refreshGroups()`）。

**M3 达成**（真机一台：小米，Android 15）。Web 示例与 Android 示例连同一套开发环境（strict 档），实时互发：中文正文、在线状态、双向已读都对。同一账号在手机和网页上同时在线：对方发来的消息两边即时收到；一边发出的，另一边显示为自己发的，对方也收到。对方由一个直连 IM 服务端的小脚本扮演。

真机上发现并修了一个 Android 的问题：会话页顶部的“对方正在输入…”出现后一直不消失（M2 只核对了它会出现）。`ConversationScreen` 直接读引擎的输入状态表，没有按 `typingIn()` 的时限过滤。现在改用 `typingIn()`，显示期间每秒复查一次，超时即消失；补了界面用例（旧代码下失败），真机上复核过：对方停止输入约 10 秒后，标题变回“在线”。引擎那张表仍然不清理过期项，只多占一点内存，留给 `imkit-core`。

另外 Web Push 只做了订阅与显示，没有接推送服务；只在 Edge（Chromium）上跑过，Firefox、Safari 未测。

## Phase 7 进展（2026-10-25）

7.2 通话服务端：响铃与断线宽限两种计时器改为可注入时钟，补齐红线 R16 的两个用例（`TestR16_DisconnectGraceEndsCall`、`TestR16_TimersStoppedOnEveryPath`），按真实时长检查而不必真的等。写用例时发现原版的一处遗漏：通话以别的方式结束时（挂断、拒接、取消、响铃超时、宿主结束、对方宽限先到），另一方还挂着的断线宽限计时器没有停，要等到点才发现无事可做；到点时它结束的是“这个用户此刻的通话”，而不是它守着的那通。现在每条结束路径都经 `removeCall` 停掉属于这通电话的计时器，计时器到点时也只结束它守着的那通。

7.5 机器人钩子：群 AI 问答成为通用接口。回答什么、怎么计费由宿主决定，IM 以助手身份把回答发进群；仍是 10 秒超时，答不了时发兜底文案。助手的名字（“@”后面那个，默认“小桃子”）和各种兜底文案放进运行配置 `groupAi`，默认值与原版逐字相同（黄金对话两档 33/33 不变）；宿主答 `ok=false` 时可以带 `message` 用自己的说法。推送标题里助手的名字默认跟随 `groupAi.name`。宿主契约、Node 宿主 SDK 已同步。Web 端：`ai_typing` 显示为助手“正在输入”，最长 15 秒，回复到达即消失（顺带：任何人的消息到达都结束他自己的“正在输入”）；React 套件里助手没有被宿主命名时叫“助手”（`strings.assistant`）。Android 端的 `ai_typing` 等另一个会话改完 `imkit-core` 再做。

未做：7.1 E2EE 客户端模块、7.3 通话客户端、7.4 ARTC 适配示例、7.6 扫码入群的客户端页面。

## 已定决策（2026-10-25，G1 验收）

- 默认档位 strict；迁移原版项目、要与原版客户端逐帧兼容时设 `IMKIT_PROTOCOL_PROFILE=legacy`。一致性测试台测原版对话时自动用 legacy。
- strict 档的白名单：`-additions strict`（`errCode`、`protocol`、`capabilities`、`vapidKey` 与两种应答的 `id`）和 `contract/protocol/transcripts-strict/` 的六个场景（D3、D4 各一个，1.13 加的 D1/D2、D5、D7、D8 四个）。
- staticcheck 纳入 `tools/lint.sh`（`go install honnef.co/go/tools/cmd/staticcheck@latest`，本机 2026.2.2）。
- R12 对 APNs 的例外：只发数据载荷的 APNs 推送是静默的后台推送，iOS 会限流、不保证送达，所以 APNs 载荷另带一个只由数据字段组成的 alert；理由写在 `server/push/r12_test.go`。
- D1–D5、D7–D9 只在 strict 档修正，legacy 保持原版行为。1.13 的取舍：D5 对补投的入群申请、审核结果帧也用当前群名；D7 发请求的连接只订阅、不再收 `grp_sync`；D8 对另一个群的申请保持与原版相同的应答（已处理 409、待处理 400）。
- 多端在线：字段名 `device`；不带它等于设备 `""`、照旧互相顶替；E2EE 只属于发布密钥的设备，客户端开始带 `device` 后要重新发布密钥包；`answered_elsewhere`；推送令牌按设备、每个服务商一个，不带设备的令牌在设备 `""` 没连上时也推；每个账号最多 10 台设备；不在通话里的设备操作通话得到 404。
- Phase 2 起的 Node 包在 imcms 里用 npm 安装依赖，版本钉死并提交 lockfile；大版本与原版一致。
- Node 宿主 SDK 的 Prisma 测试与一致性宿主用本机测试库 `imkit_host`（与 IM 服务端的 `imkit_new` 分开，因为一致性测试台每个场景都会清空 `imkit_new`）。
- 待定：zod 3 有一条只在 zod 4 修复的高危公告（见 `host-sdk-node/README.md`），升级是大版本变化，暂按上一条不升、用请求体上限缓解。

## 已定决策（2026-10-24）

- 核心许可证 Apache-2.0；E2EE 客户端模块独立发布，AGPLv3。
- v1 支持 Android、iOS、Web；协议规范按“照着就能实现”的标准写。
- v1 做多端同时在线（Phase 1 任务 1.12，已完成），不含 E2EE 多设备。
- 清空语义按现网冻结：全员物理删除。
- 测试库：本机 PostgreSQL 17（127.0.0.1:55432）上的 `imkit_oracle`、`imkit_new`。
- 命名暂用占位符：`github.com/<org>/imkit`、`io.imkit`、`ImKit`、`@imkit/*`。Go 模块在代码里写作 `example.com/imkit/...`。
- 安装工具链、`git init`、任何对外发布前先征得同意。`git init` 已于 2026-10-24 获准执行（默认分支 `main`），提交仍须征得同意。
- 竞态检测工具链（0.10）：本机 WinLibs GCC 16.2.0（MinGW-w64，UCRT），解压在 `F:\Program Files\tools\winlibs`，不进 PATH，只由 `tools/race.sh` 临时启用。Linux CI 等 `git init` 后再定。

## 阶段与闸门

| 阶段 | 内容 | 粗估（人日） | 闸门 |
| --- | --- | --- | --- |
| P0 | 安全网与协议规范 | 8–12 | G0：原版服务对同一套脚本连跑 3 次一致 |
| P1 | Go 服务端核心（含多端在线） | 26–38 | G1：与原版逐帧一致 |
| P2 | 宿主契约与 Node 宿主 SDK | 8–12 | 已完成（2026-10-25） |
| P3–4 | Android SDK 与 Compose UI | 26–38 | M1 无 UI 跑通、M2 sample-app |
| P5 | Web SDK | 20–29 | M3 与 Android 互通 |
| P6 | iOS SDK | 24–34 | M4 三端互发 |
| P7 | 可选模块 | 38–55 | |
| P8–9 | 后台组件、文档、CI、压测、发布 | 11–17 | |
