---
title: "数据库与迁移"
description: "服务端怎样使用 PostgreSQL，按模块分组的迁移怎样在启动时执行，以及校验和、版本检查与咨询锁的规则。"
---

# 数据库与迁移

服务端的全部持久数据都在一个 PostgreSQL 库里，表结构由服务端在启动时自己维护：每次启动按顺序执行尚未执行过的迁移，执行过的记在 `talkwire_schema_migrations` 表里。不需要另外跑迁移命令，也不需要手工建表。

## 连接

服务端用 `github.com/lib/pq` 驱动，连接串由 `DB_HOST`、`DB_PORT`、`DB_USER`、`DB_PASSWORD`、`DB_NAME`、`DB_SSLMODE` 拼成，各项默认值见 [环境变量](/server/env#database)。启动时先 ping 一次，连不上就打印 `Failed to connect to database` 并退出。

连接池的参数写在 `server/store.go` 里，不能用环境变量调整：

| 参数 | 值 |
| --- | --- |
| 最大打开连接数 | 50 |
| 最大空闲连接数 | 20 |
| 连接最长存活 | 5 分钟 |

健康检查端点 `/api/health` 也会 ping 数据库，见 [部署与运维](/server/operations#health)。

## 迁移集

迁移按模块分组，每组叫一个迁移集：核心一组，自己有表的可选模块各一组。SQL 文件在 `server/migrations/` 下，用 `go:embed` 编进可执行文件。

| 迁移集 | 目录 | 迁移 | 建的表与列 |
| --- | --- | --- | --- |
| `core` | `migrations/core/` | `0001_baseline` | `im_users`、`im_messages`、`im_topics`、`im_topic_members`、`im_fcm_tokens`、`im_group_settings`、`im_pending_clears`、`im_pending_group_events`、`im_read_cursors` 及其索引 |
| | | `0002_push_provider` | 列 `im_fcm_tokens.provider`（`fcm`、`apns` 或 `webpush`，已有的行记为 `fcm`） |
| | | `0003_devices` | 表 `im_devices`、`im_pending_acks`，列 `im_fcm_tokens.device_id` |
| `groupjoin` | `migrations/groupjoin/` | `0001_baseline` | `im_group_invite_tokens`、`im_group_join_requests` |
| `e2ee` | `migrations/e2ee/` | `0001_baseline` | `im_e2e_identities`、`im_e2e_one_time_pre_keys`、`im_e2e_envelopes` |
| | | `0002_owner_device` | 列 `im_e2e_identities.owner_device` |

`call` 与 `groupai` 模块没有自己的表。`im_fcm_tokens` 保留了原版的名字，虽然它已经不只存 FCM 令牌。

执行顺序是先核心、再已安装的模块（`groupjoin` 在 `e2ee` 之前），因为模块的表引用核心的表。没安装的模块不提供迁移集，它的表不会被建；之后某次启动装上这个模块时，它的迁移在那次启动时执行。

### 文件规则

迁移文件由 `server/migrate/migrate.go` 加载，规则如下：

- 文件名是 `NNNN_name.sql`：四位版本号、下划线、小写字母数字与下划线组成的名字；目录里出现别的文件或子目录，加载即报错。
- 每组的版本号从 1 开始连续编号，不能有空缺。
- 一个文件可以有多条语句，整个文件与它的记录行在同一个事务里执行，失败时一起回滚。

## 记录表

已执行的迁移记在 `talkwire_schema_migrations` 里，每个迁移集各自编号，主键是（模块，版本）：

```sql
CREATE TABLE IF NOT EXISTS talkwire_schema_migrations (
	module     VARCHAR(64)  NOT NULL,
	version    INT          NOT NULL,
	name       VARCHAR(255) NOT NULL,
	checksum   CHAR(64)     NOT NULL,
	applied_at TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
	PRIMARY KEY (module, version)
)
```

`checksum` 是这个迁移 SQL 原文的 SHA-256（十六进制）。每次启动执行了哪些迁移，日志里各有一行 `applied migration <模块>/<NNNN_name>`；没有新迁移时重启不会重复执行任何一个。

## 启动时的检查

迁移器逐组处理迁移集：先检查这一组的版本，再按编号逐个核对或执行。遇到下面任一情况立即停止，服务端打印 `Failed to initialize tables` 及原因后退出；在此之前已经执行完的迁移保留。

### 校验和

已执行过的迁移，每次启动都会重新计算 SQL 的校验和，与记录表里的比较。不一致时报 `migrate: <模块>/<NNNN_name> was changed after it was applied`。

所以已发布的迁移文件不能再改；表结构要变，就在这一组里加一个新编号的文件。校验和按文件的原始字节计算，换行符不同也算改动；仓库用 `.gitattributes` 让所有文本文件以 LF 检出。

### 拒绝更新的表结构

库里某个迁移集记录的版本号，比当前可执行文件知道的还大时，迁移器报 `refusing to run against a newer schema` 并拒绝启动。这说明库已经被更新的版本迁移过。

由此推出：一个库被新版本迁移之后，不能再用旧版本的可执行文件对着它启动。检查只针对本次传入的迁移集，也就是核心与已安装的模块。

## 并发启动与咨询锁

多个进程连同一个库同时启动时，迁移由 PostgreSQL 的会话级咨询锁串行化。迁移器在一条专用连接上执行 `pg_advisory_lock`，锁的键是 `0x696d6b6974`（十进制 452807321972），迁移做完再释放。

`pg_advisory_lock` 会一直等到拿到锁为止，所以后启动的进程等先启动的迁移完，再按记录表跳过已执行的部分。这个键是 `imkit` 的 ASCII 编码，改名后没有变：改名前后的两个版本同时连同一个库时，仍然互相等待（`CHANGELOG.md`）。

## 接管改名前的记录表

在改名前（ImKit 时期）迁移过的库上启动时，迁移器先把旧记录表改名，已执行过的迁移不会再执行。这一步在拿到咨询锁之后、建记录表之前执行：

```sql
DO $$
BEGIN
	IF to_regclass('talkwire_schema_migrations') IS NULL AND to_regclass('imkit_schema_migrations') IS NOT NULL THEN
		ALTER TABLE imkit_schema_migrations RENAME TO talkwire_schema_migrations;
		ALTER INDEX IF EXISTS imkit_schema_migrations_pkey RENAME TO talkwire_schema_migrations_pkey;
	END IF;
END $$
```

只有新表不存在、旧表存在时才改名，所以对新库和已经接管过的库什么都不做。单测 `TestApplyTakesOverBookkeepingFromBeforeRename` 验证改名后只执行旧表里没有的迁移。

## 在原版服务建好的库上启动

每组的 `0001_baseline` 是原版服务（源提交 `33e23250`）建出的表结构，每条语句都是幂等的（`CREATE TABLE IF NOT EXISTS` 等）。所以服务端可以直接指向原版已经在用的库：基线只补缺的部分，不重建，之后的迁移再加上 Talkwire 新增的表和列。

相对原版新增的表是 `talkwire_schema_migrations`、`im_devices`、`im_pending_acks`，新增的列是 `im_fcm_tokens.provider`、`im_fcm_tokens.device_id`、`im_e2e_identities.owner_device`（`server/README.md`）。迁移原版项目的完整步骤见 [从现有 IM 系统迁移](/migrate/)。

一致性测试台的 `schema` 子命令验证这一点：空库上建出的表结构与原版建出的逐项相同（去掉新增项后，比较列、索引、约束、序列），重启不重复执行迁移，在原版库上启动后与空库上建出的完全相同。用法见 [`tools/conformance/README.md`](https://github.com/dishikun2786-prog/Talkwire/blob/main/tools/conformance/README.md)。

## 测试用的库

数据库单测需要环境变量 `TALKWIRE_TEST_DSN` 指向一个可以随意改写的库（libpq 的 `key=value` 格式）。用例在里面建临时 schema、用完删除；不设时这些用例跳过。一致性测试台每次运行会删除并重建 `-db` 指定库的 `public` schema，只能指向一次性测试库。详见 [测试与红线](/contributing/testing)。
