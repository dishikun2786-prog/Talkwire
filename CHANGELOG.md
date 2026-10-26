# 更新日志

## 未发布

### 云端测试服务 imtalk.openedskill.com（2026-10-26）

- 新增公开的测试服务，给第三方开发者不部署就能试用框架：IM 服务端（strict 协议档，装 groupjoin、e2ee、groupai、call 模块）、沙箱宿主与 Web 示例。使用说明在网站的“云端测试服务”一页（`site/guide/sandbox.md`）。
- 新增沙箱宿主 `examples/sample-host/src/sandbox.ts`（入口 `sandbox-main.ts`，`npm run sandbox`）：按空间隔离（`POST /sandbox/spaces` 建空间，`/dev/login` 要名字加空间码，同一空间的人自动互为联系人），没有管理员登录与管理接口，按网络地址限制建空间与登录的频率，每个空间最多 50 人，上传限单个大小、每人每小时次数与全站总量，`GET /sandbox/info` 给出清空时间与限额。7 个测试。
- Web 示例在沙箱上多出空间码、“新建空间”、邀请链接与测试服务提示，并在启动时用保存的名字和空间码重新登录；连开发宿主时不变。Android 示例加了 `-PtalkwireSandbox=<地址>` 构建参数，连沙箱时登录页同样要空间码。
- 新增 `deploy/imtalk/`：服务器一次性准备（`setup.sh`）、发布（`release.sh`，宿主用 esbuild 打成一个文件，服务器上不装 npm 依赖）、systemd 单元（只允许本机通信）、Caddy 站点，以及每天北京时间 04:00 清空数据库与上传文件的定时器。
- 新增 `tools/sandbox-check/check.mjs`：用真实的 Web 客户端从公网把测试服务走一遍（17 步：服务信息与网页、没有管理接口、空间隔离、WebSocket 登录与同步、单聊、跨空间发不进来、图片上传与下载比对、群聊、跨空间扫码入群并审批、撤回、已读、同一用户两台设备、通话信令全过程、双方清空、离线补拉与在线状态）。2026-10-26 部署后两次运行均 17/17 通过。
- `deploy/site/Caddyfile` 末尾导入 `/etc/caddy/sites/*.caddy`，同一台服务器上的其他站点各放一个文件。
- 测试服务的 TLS 改由 Caddy 自己完成，DNS 可以设为“仅 DNS”让浏览器直连：Cloudflare 区域开着客户端证书（mTLS）时会在握手里请求客户端证书，Chrome 的 WebSocket 一遇到就放弃，页面一直显示“网络已断开”。直连时 Caddy 用连接本身的地址覆盖 `CF-Connecting-IP`，限频不能被伪造；http 跳到 https。排查方法与临时诊断页 `deploy/imtalk/ws-diag.html` 写进 `deploy/imtalk/README.md`。

### 网站与开发手册（2026-10-26）

- 新增 `site/`：网站首页与开发手册（VitePress），覆盖接入、服务端、宿主、客户端、协议、可选模块与贡献，规范全文直接引用 `contract/protocol/spec/protocol-v1.md`。`tools/ci.sh site` 构建它，站内死链或指向不存在标题的锚点都让构建失败。
- 新增部署配置：`.github/workflows/site.yml` 在 `main` 上构建并经 SSH 部署到 talkwire.openedskill.com，服务器的准备步骤与 Caddy 配置在 `deploy/site/`。
- 新增 `CONTRIBUTING.md`、`CODE_OF_CONDUCT.md`、`SECURITY.md` 与 Issue、合并请求模板。
- 协议规范第 4.3 节的 `errCode` 表补上表头，原来渲染不成表格；内容没有变。

### 改名：ImKit → Talkwire（2026-10-26）

项目名、包名、模块名与配置名一并改为 Talkwire。线协议没有变化：协议帧里本来就不含项目名，legacy 档仍与原版客户端逐帧一致。

| 原名 | 新名 |
| --- | --- |
| npm 包 `@imkit/*` | `@talkwire/*` |
| Android 模块 `imkit-*`、包名 `io.imkit.*` | `talkwire-*`、`io.talkwire.*` |
| Go 模块 `example.com/imkit/...`、可执行文件 `imkit-server` | `github.com/dishikun2786-prog/Talkwire/...`、`talkwire-server` |
| `ImKitProvider`、`useImKit`、`imkitHostPlugin` 等带项目名的接口 | `TalkwireProvider`、`useTalkwire`、`talkwireHostPlugin` 等 |
| CSS 变量 `--imkit-*` | `--talkwire-*` |
| 环境变量 `IMKIT_*` | `TALKWIRE_*` |

已有部署升级时：

- **服务端环境变量**：没改名的 `IMKIT_*` 照常生效，启动时每个旧名记一条警告；同时设了新旧两个名字时用新名字。1.0 起只认 `TALKWIRE_*`。
- **服务端数据库**：迁移记录表 `imkit_schema_migrations` 在启动时自动改名为 `talkwire_schema_migrations`，已执行过的迁移不会再执行。迁移锁的键没有变，改名前后的两个版本同时连同一个库时仍会互相等待。
- **宿主数据库**（Node 宿主 SDK）：表名由 `imkit_*` 改为 `talkwire_*`。升级前执行一次 `host-sdk-node/prisma/rename-from-imkit.sql`，数据保留；Prisma 模型 `ImkitCallRecord` 改为 `TalkwireCallRecord`。
- **客户端本地数据**：Android 的消息库与加密库文件名、密钥别名、通知渠道，Web 的多标签频道名都换了新名字，旧的不迁移。升级后本地消息缓存从服务端重新同步；端到端加密需要重新发布密钥包。改名前没有公开发布过版本，受影响的只有开发中的安装。
- **深链与推送**：Android 深链 `imkit://` 改为 `talkwire://`；APNs 的本地化键 `IMKIT_E2E_MESSAGE` 改为 `TALKWIRE_E2E_MESSAGE`。
