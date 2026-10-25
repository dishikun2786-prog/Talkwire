# 协议 Schema 工具

| 文件 | 作用 |
| --- | --- |
| `gen.py` | 从字段表生成 `contract/protocol/schema/` 下的两份 JSON Schema（2020-12 草案）。改协议时只改这里，不要手改生成的文件 |
| `check.mjs` | 用 Schema 校验 `contract/protocol/transcripts/` 和 `transcripts-strict/`（strict 档有意偏离原版的场景，帧里保留 strict 档新增的字段）里的每一帧 |
| `negative-cases.json` | 场景里故意发出的非法请求（按场景名和步骤号）。这些请求必须**不通过**客户端 Schema |

## 用法

```
python -I tools/schema/gen.py            # 重新生成
python -I tools/schema/gen.py --check    # 生成结果与仓库不一致时退出码 1
node tools/schema/check.mjs
```

ajv 8 用 `host-sdk-node` 的开发依赖（先在那里 `npm install`）；`--ajv <目录>` 可以指向别的装有 ajv 的目录。

## 两份 Schema 的约定

- `client-frames.schema.json`：客户端发出的帧。允许未声明字段，因为服务端必须忽略它们。
- `server-frames.schema.json`：服务端发出的帧。**禁止**未声明字段，用来保证服务端不会发出规范里没写的字段。客户端运行时**不得**用它校验收到的帧，客户端必须忽略未知字段。

## 当前结果

全部 32 个场景（核心组 25 个、模块组 7 个）的 923 帧通过（2026-10-24）。加上 strict 档的 2 个场景共 949 帧通过（2026-10-25）。Phase 2 加了宿主卡片更新的帧（`custom_update`、`red_packet_update`）和核心组场景 `core/host-red-packet` 后，33 个场景加 strict 档的 6 个场景共 1114 帧通过（2026-10-25）；用补入 strict 字段之前的 Schema 校验，strict 场景报 16 处错误，说明这些字段确实被校验到了。红绿验证：在副本里加一个未声明字段、把一个 `seq` 改成字符串、删掉一个请求的必填字段，三处都被报出，退出码 1。
