# 更新日志

## 未发布

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
