# ImKit

ImKit 是从小桃子自研即时聊天系统提炼出的 IM 框架：一个 Go 实时服务端、一个宿主（业务后端）接入 SDK，以及 Android、iOS、Web 三端客户端 SDK。业务项目只需实现少量宿主接口（身份、好友关系、资料、推送、媒体），就能得到单聊、群聊、媒体消息、已读、撤回、在线状态与离线推送。

> 名称、Go 模块路径、Android 包名（`io.imkit`）、iOS 模块名（`ImKit`）与 npm 包名（`@imkit/*`）都是占位符，定名后统一替换。Go 模块在代码里暂写作 `example.com/imkit/...`（计划里的 `github.com/<org>/imkit` 不是合法的 Go 路径）。

## 当前状态

**Phase 0（安全网与协议规范）** 的任务已完成，验收还差协议规范草稿的审阅。**Phase 1（Go 服务端核心）** 的 1.1–1.13 已全部完成（2026-10-25）：拆文件、宿主适配、推送三家、可选模块、迁移、strict 档、安全修复、并发修复、多端同时在线，以及 strict 档补修的 D1、D2、D5、D7、D8；legacy 档与原版逐帧一致，默认档位为 strict，闸门 G1 已通过。**Phase 2（宿主契约与 Node 宿主 SDK）** 已完成（2026-10-25）：宿主双向的 OpenAPI 契约、uid 黄金向量、Node 宿主 SDK、宿主卡片更新接口 `custom-broadcast` 与红包示例；用 Node SDK（Prisma 连测试库）替换假宿主后黄金对话 33/33 全绿，契约测试双向通过，“用其他语言实现宿主”的说明在 `contract/host-api/README.md`。**Phase 3（Android 核心 SDK，无 UI）** 已完成，M1 达成：JVM 集成测试对独立运行的服务端（strict、legacy 两档）跑通登录到在线状态的整条流程，C1–C5、C9 各有用例，`imkit-core` 不依赖 Compose 和 Room；真机未验证，视频压缩与缩略图未做。各任务的结果见 `server/README.md`、`host-sdk-node/README.md`、`android/README.md`，完整计划见 `docs/plan.md`。

## 目录

| 目录 | 内容 | 状态 |
| --- | --- | --- |
| `contract/protocol/` | 线协议：规范、JSON Schema、黄金对话（含 strict 档的偏离场景） | 草稿，已补入 Phase 1 |
| `contract/redlines/` | 红线对照表：每条红线对应的已录用例与计划用例 | 建设中 |
| `contract/host-api/` | 宿主 ↔ IM 的 OpenAPI 3.1、uid 黄金向量、用其他语言实现宿主的说明 | Phase 2 完成 |
| `tools/conformance/` | 原版服务（参照）、假宿主、一致性测试台、客户端回放服务器 | 建设中 |
| `docs/` | 计划、架构决策记录、协议规范、清单 | 建设中 |
| `server/` | Go 服务端 | Phase 1 完成，G1 通过 |
| `host-sdk-node/` | Node 宿主 SDK | Phase 2 完成 |
| `android/` | Android SDK：核心、Room 存储、通知与媒体、FCM、Compose 界面套件、示例 App | Phase 3 完成（M1）；Phase 4 完成（M2，真机一台） |
| `web/`、`ios/` | Web、iOS SDK | Phase 5–6 |
| `examples/` | 业务示例：红包卡（宿主自定义消息类型，含 Android 卡片）；`sample-host`（示例 App 用的 Node 开发宿主） | 已有 |
| `admin-kit/` | 后台组件 | Phase 8 |

## 开发工具

- Go 1.26.4 及以上（本机验证用的是 1.27.0）。
- Node.js 22 及以上（`host-sdk-node/`、`examples/`、`tools/schema/check.mjs`；本机验证用的是 24.19.0）。依赖版本钉死，lockfile 已提交。
- JDK 17 与 Android SDK（platform 34），用于 `android/`；Gradle 8.7 由 wrapper 下载。见 `android/README.md`。
- 文本文件一律 LF（见 `.gitattributes`）。Git for Windows 默认 `core.autocrlf=true`，不加这条，Go 源码检出后是 CRLF，哈希对不上冻结的源码，gofmt 也会报整份文件。
- 竞态检测用 `tools/race.sh`，参数原样传给 `go test -race`（默认 `./...`）：

  ```bash
  cd tools/conformance && bash ../race.sh
  cd server && bash ../tools/race.sh
  ```

  竞态检测需要 cgo 和 C 编译器。Windows 上用私有的 MinGW-w64 GCC（WinLibs GCC 16.2.0，默认在 `F:\Program Files\tools\winlibs\mingw64`，可用 `IMKIT_MINGW` 指向别处），只在这一条命令里打开 cgo，不改系统或用户 PATH：gcc 一旦出现在 PATH 上，本机其他 Go 构建都会悄悄改用 cgo。脚本里还绕过了 Windows 上的两个问题（GCC 装在带空格的目录下链接失败；地址随机化让 ThreadSanitizer 启动失败），原因写在脚本开头。Linux 和 macOS 直接用系统编译器。

## 许可证

核心以 Apache-2.0 发布（见 `LICENSE`）。链接 libsignal 的 E2EE 客户端模块将单独发布并采用 AGPLv3；它们不会出现在核心产物的依赖树里。
