---
title: "发布流程"
description: "Talkwire 的版本号与更新日志、哪些产物已经发布哪些还没有，以及网站的构建与计划中的自动部署。"
---

# 发布流程

Talkwire 还没有发布过任何版本：没有 npm 包、没有 Maven 产物、没有 Git 标签。本页写的是现在的约定和已经准备好的部分。按 `docs/plan.md` 里的已定决策，任何对外发布之前都要先征得同意。

## 版本号与更新日志

改动记在 [`CHANGELOG.md`](https://github.com/dishikun2786-prog/Talkwire/blob/main/CHANGELOG.md) 里；还没有发布过版本，所以目前只有“未发布”一节。

- **记什么**：用户看得到的行为变了，就同步改 `CHANGELOG.md` 的“未发布”一节和开发手册（`site/`），这是 [`CONTRIBUTING.md`](https://github.com/dishikun2786-prog/Talkwire/blob/main/CONTRIBUTING.md) 的要求。
- **怎么写**：现有的一条（项目改名）可以照着写：标题带日期，先说改了什么、线协议有没有变，再列新旧名字的对照表，最后逐项写已有部署升级时服务端环境变量、服务端数据库、宿主数据库、客户端本地数据、深链与推送各要怎样。
- **当前版本**：Node 宿主 SDK `@talkwire/host-node` 与 `web/packages/` 下的各个包都是 `0.1.0`；网站包 `@talkwire/site` 是 `0.0.0`，不发布（`private`）。
- **已定的兼容承诺**：改名前的服务端环境变量名照常生效、启动时每个记一条警告，1.0 起只认 `TALKWIRE_*`。

## 发布了什么、还没发布什么

| 产物 | 现状 |
| --- | --- |
| npm 包（`@talkwire/host-node`、`@talkwire/protocol`、`@talkwire/client`、`@talkwire/storage-idb`、`@talkwire/react`、`@talkwire/call`、`@talkwire/call-dingrtc`、`@talkwire/group-join`、`@talkwire/admin`） | 没发布。各包的 `package.json` 已经写好 `exports` 与 `files`；`web/packages/` 下的包在仓库里是 `web/` 的工作区包，示例经 `file:` 依赖引用 `host-sdk-node` 的构建产物 |
| Android 模块（`talkwire-*`，包名 `io.talkwire`） | 没发布；模块的构建文件里还没有 Maven 发布配置 |
| E2EE 模块 `talkwire-e2ee` | 没发布。计划单独发布，许可证 AGPLv3，见 [端到端加密](/modules/e2ee) |
| 服务端 | 没有发布的二进制，从源码构建（`cd server && go build -o bin/talkwire-server.exe .`） |
| Go 模块 | 没有标签。模块路径见下 |
| iOS | 未开始 |

Go 模块路径（各自一个 `go.mod`，要求 Go 1.26.4）：

| 目录 | 模块路径 |
| --- | --- |
| `server/` | `github.com/dishikun2786-prog/Talkwire/server` |
| `tools/conformance/` | `github.com/dishikun2786-prog/Talkwire/tools/conformance` |
| `tools/loadtest/` | `github.com/dishikun2786-prog/Talkwire/tools/loadtest` |

仓库根目录的 `README.md` 写明：仓库还没有远程，CI 配置还没在 GitHub 上跑过。

## 发布前要过的检查

发布前在干净的检出上跑一遍全部检查，CI 跑的是同一套：

```bash
bash tools/ci.sh
```

本机要提供 `TALKWIRE_TEST_DSN` 与 `DATABASE_URL`，否则数据库测试会跳过；CI 上缺了算失败。各作业的内容见 [开发环境](/contributing/#用-tools-ci-sh-跑检查)，各层测试见 [测试与红线](/contributing/testing)。

## 网站

这个网站与开发手册用 VitePress 1.6.4 写在 `site/` 里，页面是 Markdown，侧边栏在 `site/.vitepress/config.mts`。

```bash
cd site
npm install
npm run dev        # 本机预览
npm run build      # 构建
npm run preview    # 预览构建结果
```

- **构建检查**：`tools/ci.sh` 的 `site` 作业构建网站，站内的死链接让构建失败；CI 工作流 `.github/workflows/ci.yml` 里有对应的 `site` job。
- **最后更新时间**：页面显示的“最后更新”取自 Git 历史，所以 CI 检出时要拿全部历史（`fetch-depth: 0`）。
- **不提交的东西**：构建缓存 `site/.vitepress/cache/` 与构建输出都在 `.gitignore` 里。

### 部署（计划中）

计划由 GitHub Actions 把网站部署到 talkwire.openedskill.com。工作流 `.github/workflows/site.yml` 已经写好，但仓库还没有远程，它还没有运行过。

1. 每次推送到 `main`（或在 Actions 页面手动运行）时，用 `npm ci` 安装、`npm run build` 构建。
2. 经 SSH 把构建结果传到服务器上的一个新目录 `releases/<时间>-<提交>`。
3. 把 `current` 指向这个新目录，切换是一次改名，访问者看不到半截的站点；之后只保留最近 5 个目录。

服务器与 GitHub 上各要做一次准备，写在 [`deploy/site/README.md`](https://github.com/dishikun2786-prog/Talkwire/blob/main/deploy/site/README.md)：服务器上装 Caddy 与 rsync、建一个只放网站的账号、放好 `deploy/site/Caddyfile`；GitHub 上建三个 secret：`SITE_SSH_KEY`、`SITE_HOST`、`SITE_KNOWN_HOSTS`。三个 secret 没有全部设好时，工作流照常构建，跳过部署这一步并在运行摘要里说明原因，不报错。

回滚：把服务器上的 `current` 指回上一个目录即可，命令见 `deploy/site/README.md`。
