# 开发计划

权威版本在可编辑文档《ImKit 详细开发计划》：https://claude.ai/artifact/Eh89avD5BQyVK1Urfz7Lss

## 已定决策（2026-10-24）

- 核心许可证 Apache-2.0；E2EE 客户端模块独立发布，AGPLv3。
- v1 支持 Android、iOS、Web；协议规范按“照着就能实现”的标准写。
- v1 做多端同时在线（Phase 1 任务 1.12），不含 E2EE 多设备。
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
