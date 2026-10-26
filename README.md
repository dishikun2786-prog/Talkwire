# Talkwire

Talkwire 是从小桃子自研即时聊天系统提炼出的 IM 框架：一个 Go 实时服务端、一个宿主（业务后端）接入 SDK，以及 Android、iOS、Web 三端客户端 SDK。业务项目只需实现少量宿主接口（身份、好友关系、资料、推送、媒体），就能得到单聊、群聊、媒体消息、已读、撤回、在线状态与离线推送。

网站与开发手册：<https://talkwire.openedskill.com>，源文件在 `site/`。

> 项目原名 ImKit，2026-10-26 改名为 Talkwire：Go 模块 `github.com/dishikun2786-prog/Talkwire/...`、Android 包名 `io.talkwire`、iOS 模块名 `Talkwire`、npm 包名 `@talkwire/*`。改名对已有部署的影响见 `CHANGELOG.md`。

## 当前状态

- **协议规范**（Phase 0）：规范、JSON Schema、黄金对话完成，规范草稿待审阅。
- **服务端**（Phase 1）：完成，闸门 G1 通过。legacy 档与原版逐帧一致（33 个黄金对话），默认 strict 档修正协议缺陷 D1–D5、D7–D9；多端同时在线、四个可选模块、FCM/APNs/Web Push。
- **宿主契约与 Node 宿主 SDK**（Phase 2）：完成。用 SDK 替换假宿主后黄金对话 33/33，契约测试双向通过。
- **Android**（Phase 3、4）：核心 SDK、Room 存储、通知与 FCM、Compose 界面套件、示例 App 完成，M1、M2 达成（真机一台）。
- **Web**（Phase 5）：协议类型、客户端与同步引擎、IndexedDB 存储、React 套件、示例与端到端用例完成，M3 达成（Web 与 Android 真机互发、同一账号多端在线）。
- **iOS**（Phase 6）：未开始，需要 macOS。
- **模块与业务**（Phase 7）：通话服务端计时器、机器人钩子（群 AI 改成宿主回答）完成；Web 端的通话与扫码入群完成；ARTC 适配示例的 Web 端与宿主一侧完成（`@talkwire/call-dingrtc`、`examples/artc`）。Android 端的扫码入群、通话、ARTC 适配器、群助手“正在输入”、视频压缩与 E2EE 客户端模块（`talkwire-e2ee`，AGPLv3）完成；扫码入群与通话信令已在一台真机上与网页示例互通（`android/DEVICE_CHECKS.md`），视频压缩、E2EE 与 ARTC 适配器没有真机验证；iOS 端未做。
- **后台**（Phase 8）：通话记录存储与后台接口（宿主 SDK）、可嵌入的后台页面 `@talkwire/admin` 完成。
- **文档、CI、压测**（Phase 9）：接入与迁移文档、ADR 完成；网站与开发手册（`site/`，VitePress）写好，部署到 talkwire.openedskill.com 的配置在 `deploy/site/`；全量检查脚本 `tools/ci.sh` 与 GitHub Actions 配置写好（还没在 GitHub 上跑过）；压测工具 `tools/loadtest` 在本机试跑过，正式压测待在 Linux 上做。未发布。

各部分的结果见各目录的 README，完整计划与进度见 `docs/plan.md`。

## 目录

| 目录 | 内容 | 状态 |
| --- | --- | --- |
| `contract/protocol/` | 线协议：规范、JSON Schema、黄金对话（含 strict 档的偏离场景） | 草稿，已补入 Phase 1 |
| `contract/redlines/` | 红线对照表：每条红线对应的已录用例与计划用例 | 建设中 |
| `contract/host-api/` | 宿主 ↔ IM 的 OpenAPI 3.1、uid 黄金向量、用其他语言实现宿主的说明 | Phase 2 完成 |
| `tools/conformance/` | 原版服务（参照）、假宿主、一致性测试台、客户端回放服务器 | 建设中 |
| `tools/` 其他 | `ci.sh` 全量检查、`depcheck/` 依赖方向与许可证、`loadtest/` 压测、`schema/` 协议 Schema、`lint.sh`、`race.sh` | 已有 |
| `docs/` | 计划、架构决策记录（`adr/`）、可选模块手册（`modules.md`）、从小桃子迁移（`migration-from-xiaotaozi.md`）、清单 | 建设中 |
| `server/` | Go 服务端 | Phase 1 完成，G1 通过 |
| `host-sdk-node/` | Node 宿主 SDK | Phase 2 完成 |
| `android/` | Android SDK：核心、Room 存储、通知与媒体、FCM、Compose 界面套件、示例 App；可选模块 `talkwire-group-join`、`talkwire-call`、`talkwire-video-compress`、`talkwire-e2ee`（AGPLv3）；通话的 ARTC 适配器 `talkwire-call-dingrtc`。快速接入：`android/QUICKSTART.md` | Phase 3 完成（M1）；Phase 4 完成（M2，真机一台）；Phase 7 的 Android 部分完成（未上真机） |
| `web/` | Web SDK：协议类型、客户端、IndexedDB 存储、React 套件；可选模块 `@talkwire/call`、`@talkwire/group-join`；通话的 ARTC 适配器 `@talkwire/call-dingrtc`；后台页面 `@talkwire/admin`；示例与端到端用例。快速接入：`web/QUICKSTART.md` | Phase 5 完成（M3）；Phase 7、8 的 Web 部分完成 |
| `ios/` | iOS SDK | Phase 6，未开始 |
| `site/` | 网站与开发手册（VitePress）：首页、接入、服务端、宿主、客户端、协议、可选模块、贡献；`tools/ci.sh site` 构建并检查死链 | 已有，未部署 |
| `deploy/site/` | 网站的 Caddy 配置与服务器准备步骤；部署由 `.github/workflows/site.yml` 完成 | 已有 |
| `examples/` | 业务示例：红包卡（宿主自定义消息类型，含 Android 卡片）；`sample-host`（示例 App 用的 Node 开发宿主：开发登录、演示通话、通话记录与后台）；`artc`（用阿里云 ARTC 3.0 打通话：宿主签频道令牌，各端适配器） | 已有 |

## 开发工具

- Go 1.26.4 及以上（本机验证用的是 1.27.0）。
- Node.js 22 及以上（`host-sdk-node/`、`examples/`、`site/`、`tools/schema/check.mjs`；本机验证用的是 24.19.0）。依赖版本钉死，lockfile 已提交。
- JDK 17 与 Android SDK（platform 34），用于 `android/`；Gradle 8.7 由 wrapper 下载。见 `android/README.md`。
- 文本文件一律 LF（见 `.gitattributes`）。Git for Windows 默认 `core.autocrlf=true`，不加这条，Go 源码检出后是 CRLF，哈希对不上冻结的源码，gofmt 也会报整份文件。
- 全量检查：`bash tools/ci.sh`（或指定作业，如 `bash tools/ci.sh go web`；`--list` 列出全部步骤）。CI 跑的就是它，数据库用例需要的环境变量见脚本开头。
- 压测：`tools/loadtest`，见 `tools/loadtest/README.md`。
- 依赖方向与许可证：`node tools/depcheck/check.mjs`（核心不链接 libsignal 与 RTC SDK、模块只依赖核心、许可证），见 `tools/depcheck/README.md`。
- 竞态检测用 `tools/race.sh`，参数原样传给 `go test -race`（默认 `./...`）：

  ```bash
  cd tools/conformance && bash ../race.sh
  cd server && bash ../tools/race.sh
  ```

  竞态检测需要 cgo 和 C 编译器。Windows 上用私有的 MinGW-w64 GCC（WinLibs GCC 16.2.0，默认在 `F:\Program Files\tools\winlibs\mingw64`，可用 `TALKWIRE_MINGW` 指向别处），只在这一条命令里打开 cgo，不改系统或用户 PATH：gcc 一旦出现在 PATH 上，本机其他 Go 构建都会悄悄改用 cgo。脚本里还绕过了 Windows 上的两个问题（GCC 装在带空格的目录下链接失败；地址随机化让 ThreadSanitizer 启动失败），原因写在脚本开头。Linux 和 macOS 直接用系统编译器。

## 许可证

核心以 Apache-2.0 发布（见 `LICENSE`）。链接 libsignal 的 E2EE 客户端模块单独发布并采用 AGPLv3（目前是 Android 的 `android/talkwire-e2ee`，目录里有它自己的 `LICENSE`）；它不会出现在核心产物的依赖树里，`tools/depcheck` 与 `tools/ci.sh` 检查这一点。
