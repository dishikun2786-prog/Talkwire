# 开发计划

权威版本在可编辑文档《ImKit 详细开发计划》：https://claude.ai/artifact/Eh89avD5BQyVK1Urfz7Lss

## Phase 1 结果（2026-10-25）

1.1–1.13 全部完成，各任务的内容与验证结果见 `server/README.md`。1.13 是计划外加的：strict 档修正 D1、D2、D5、D7、D8（原计划 1.7–1.9 没有覆盖它们，2026-10-25 定为另排任务）。legacy 档 32/32 逐帧一致，strict 档 32/32，表结构比较 5 项通过，竞态检测三轮无报告，staticcheck 无报告。

**闸门 G1 已通过（2026-10-25）**。验收前待定的 6 项按建议定下（见下节），随后装了 staticcheck（2026.2.2）并修掉它报出的 6 处；其中一处是原版的缺陷：`IM_HUB_CHAN_BUF`、`IM_PRESENCE_CHAN_BUF` 读进来了却没被使用（F62），已接上，默认值不变。

## 已定决策（2026-10-25，G1 验收）

- 默认档位 strict；迁移原版项目、要与原版客户端逐帧兼容时设 `IMKIT_PROTOCOL_PROFILE=legacy`。一致性测试台测原版对话时自动用 legacy。
- strict 档的白名单：`-additions strict`（`errCode`、`protocol`、`capabilities`、`vapidKey` 与两种应答的 `id`）和 `contract/protocol/transcripts-strict/` 的六个场景（D3、D4 各一个，1.13 加的 D1/D2、D5、D7、D8 四个）。
- staticcheck 纳入 `tools/lint.sh`（`go install honnef.co/go/tools/cmd/staticcheck@latest`，本机 2026.2.2）。
- R12 对 APNs 的例外：只发数据载荷的 APNs 推送是静默的后台推送，iOS 会限流、不保证送达，所以 APNs 载荷另带一个只由数据字段组成的 alert；理由写在 `server/push/r12_test.go`。
- D1–D5、D7–D9 只在 strict 档修正，legacy 保持原版行为。1.13 的取舍：D5 对补投的入群申请、审核结果帧也用当前群名；D7 发请求的连接只订阅、不再收 `grp_sync`；D8 对另一个群的申请保持与原版相同的应答（已处理 409、待处理 400）。
- 多端在线：字段名 `device`；不带它等于设备 `""`、照旧互相顶替；E2EE 只属于发布密钥的设备，客户端开始带 `device` 后要重新发布密钥包；`answered_elsewhere`；推送令牌按设备、每个服务商一个，不带设备的令牌在设备 `""` 没连上时也推；每个账号最多 10 台设备；不在通话里的设备操作通话得到 404。
- Phase 2 起的 Node 包在 imcms 里用 npm 安装依赖，版本钉死并提交 lockfile；大版本与原版一致。

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
| P2 | 宿主契约与 Node 宿主 SDK | 8–12 | |
| P3–4 | Android SDK 与 Compose UI | 26–38 | M1 无 UI 跑通、M2 sample-app |
| P5 | Web SDK | 20–29 | M3 与 Android 互通 |
| P6 | iOS SDK | 24–34 | M4 三端互发 |
| P7 | 可选模块 | 38–55 | |
| P8–9 | 后台组件、文档、CI、压测、发布 | 11–17 | |
