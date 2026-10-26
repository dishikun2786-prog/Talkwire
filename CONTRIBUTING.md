# 参与贡献

欢迎提 Issue、补用例、修缺陷、实现别的语言的宿主。这份说明讲提交改动前要做的事；开发环境的完整说明在开发手册的[开发环境](https://talkwire.openedskill.com/contributing/)一页（源文件 `site/contributing/`）。

## 开始之前

- 改动较大（新功能、协议变化、新的可选模块）时，先开一个 Issue 说明想法，免得做完才发现方向不合。
- 安全问题不要开公开 Issue，按 [SECURITY.md](SECURITY.md) 私下报告。
- 参与即表示同意遵守[行为准则](CODE_OF_CONDUCT.md)。

## 跑检查

CI 跑的就是 `tools/ci.sh`，本机能过，CI 上也能过：

```bash
tools/ci.sh --list          # 有哪些作业、每个作业做什么
tools/ci.sh go web          # 只跑相关的作业
tools/ci.sh                 # 全部
```

数据库相关的测试需要两个一次性的 PostgreSQL 库：`TALKWIRE_TEST_DSN`（libpq 的 `key=value` 形式）与 `DATABASE_URL`（宿主 SDK 的 Prisma 测试用）。本机没设时这些测试跳过并说明原因；CI 上没设算失败。

## 改动的规矩

- **协议先改规范**：改线协议时，先改 `contract/protocol/spec/protocol-v1.md`，再录或更新黄金对话（`contract/protocol/transcripts/`、`transcripts-strict/`）与 JSON Schema。legacy 档必须与原版逐帧一致；strict 档的偏离要写进规范第 0.3 节与第 18 节。
- **红线有用例**：修一类事故（丢消息、串号、越权……）时，按红线编号命名用例（服务端 `TestR<n>_…`，宿主、Android、Web `R<n>_…` 或 `C<n>_…`），并在 `contract/redlines/redlines.json` 登记。不在表里的红线用例会被 `deps` 作业报出来。
- **依赖只往下走**：核心不依赖可选模块，不链接 libsignal 与 RTC SDK；新增的包或模块要在 `tools/depcheck/check.mjs` 里归类。
- **许可证**：核心以 Apache-2.0 发布；端到端加密的 Android 模块 `android/talkwire-e2ee` 是 AGPLv3，其他代码不能依赖它。新增依赖必须是宽松许可证。
- **不提交密钥**：`.env`、`pgpass.conf`、keystore、`google-services.json` 已在 `.gitignore` 里。测试用的密钥写成测试常量，不要用真实环境的值。
- **文档跟着改**：用户看得到的行为变了，同步改开发手册（`site/`）与 `CHANGELOG.md` 的“未发布”一节。

## 提交与合并请求

- 一个合并请求只做一件事。提交说明写清改了什么、为什么；可以加 `feat(范围):`、`fix(范围):` 这样的前缀。
- 合并请求的描述里写明跑过哪些 `tools/ci.sh` 作业、结果如何；没法在本机跑的（比如需要真机的 Android 检查）照实写。

## 许可

按 Apache-2.0 第 5 条，你提交的贡献以项目的许可证发布：核心是 Apache-2.0，`android/talkwire-e2ee` 是 AGPLv3。不需要另外签署协议。
