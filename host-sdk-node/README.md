# @imkit/host-node

ImKit 的 Node 宿主 SDK：把宿主（业务后端）这一侧做成 Fastify 4 插件，加上存储接口和参考实现。契约见 `contract/host-api/`，用别的语言实现宿主的说明也在那里。

| 导出 | 作用 |
| --- | --- |
| `imkitHostPlugin` | IM 服务端调用的端点：好友关系、资料（带备注）、运行配置、端到端加密配置、删媒体、通话与群 AI 模块；校验内部令牌 |
| `imkitAppRoutes` | 给聊天 App 用的接口：我的资料、批量查资料、按 handle 搜索与扫码找人（带一次性加好友令牌）、联系人增删改查、按方向存的备注、增量同步 |
| `imkitMediaRoutes` | 媒体上传、读取（`Content-Length`、`private, no-store`）、删除自己未发出的上传（先查引用计数） |
| `imkitAdminRoutes` | 后台查看聊天记录；清空单聊需超级管理员、确认语、理由与幂等键，审计只记元数据。给了 `calls` 时还有通话记录列表（按用户、状态、结果、类型、时间筛选，游标分页）与单条详情 |
| `recordingCalls` | 包住宿主自己的通话处理器，把它接受的每通通话写进 `CallRecordStore`：振铃、接听、升级视频、结束原因与时长 |
| `ImClient` | 调 IM 服务端的内部端点：`contactSync`、`customBroadcast`、`callBroadcast`、`mediaRefCount`、`topicMembersResolve`、后台四个 |
| 存储 | 接口 `ProfileStore`、`ContactStore`、`AuditStore`、`CallRecordStore`、`ConfigSource`、`BlobStore`；内存版在主入口，Prisma 版在 `@imkit/host-node/prisma`，S3 兼容存储在 `@imkit/host-node/s3`，本地磁盘为 `LocalDiskBlobStore` |

业务功能（红包、转账、积分）不在 SDK 里；做法见 `examples/red-packet/`。

## 用法

```ts
import Fastify from "fastify";
import { PrismaClient } from "@prisma/client";
import { ImClient, imkitHostPlugin, imkitAppRoutes, imkitMediaRoutes, mediaDeleter, LocalDiskBlobStore } from "@imkit/host-node";
import { prismaStores } from "@imkit/host-node/prisma";

const prisma = new PrismaClient();
const { profiles, contacts, audit } = prismaStores(prisma);
const im = new ImClient({ baseUrl: "http://127.0.0.1:6062", secret: process.env.JWT_SECRET! });
const blobs = new LocalDiskBlobStore("/data/imkit-media");
const authenticate = async (req) => /* 你的登录态 → 宿主用户 id 或 null */ null;

const app = Fastify();
await app.register(imkitHostPlugin, { secret: process.env.JWT_SECRET!, contacts, profiles, deleteMedia: mediaDeleter(blobs) });
await app.register(imkitAppRoutes, { authenticate, profiles, contacts, im, addTokenSecret: process.env.ADD_TOKEN_SECRET! });
await app.register(imkitMediaRoutes, { authenticate, store: blobs, im });
```

- IM 服务端的 `MAIN_API_BASE_URL` 指向这个应用；`JWT_SECRET` 两边相同。
- Prisma：把 `prisma/imkit.prisma` 拷进你的 schema（多文件 schema 直接放进 schema 目录），再照常迁移。不用 Prisma 的宿主可以用 `prisma/imkit.sql` 建同样的表，自己实现存储接口。
- 联系人与资料变化时，`imkitAppRoutes` 默认经 `custom-broadcast` 把事件推给用户的各个设备（`what` 为 `contact.request`、`contact.accepted`、`contact.removed`、`contact.alias`、`profile.updated`），并在关系被接受或解除时调 `contact-sync`。可以用 `notify` 换成你自己的推送。
- 加好友默认要求令牌（`addTokenMode: "require"`）：令牌只能由搜索或扫码得到，绑定双方、两分钟有效。从不带令牌的旧客户端迁移时先用 `"observe"`。

后台与通话记录（前端页面见 `web/packages/admin`，`@imkit/admin`）：

```ts
import { imkitAdminRoutes, recordingCalls } from "@imkit/host-node";

const { calls: callRecords } = prismaStores(prisma); // 模型里有 ImkitCallRecord 时才有
await app.register(imkitHostPlugin, { /* 同上 */ calls: recordingCalls(myCallHandlers, callRecords!) });
await app.register(imkitAdminRoutes, { authenticate: adminAuth, im, audit, profiles, calls: callRecords });
```

- `adminAuth` 返回 `{ adminId, role: "admin" | "superadmin" }` 或 null。读记录任何管理员都可以；清空要超级管理员。管理员的令牌与用户的令牌要分开签（不同密钥或不同 `type`），IM 服务端只认普通用户的访问令牌（红线 R18）。
- 只记录 `create` 接受了的通话；被拒的邀请不是通话。记录只有元数据：双方、请求的与最终的类型、各步时间、结束原因、时长（接听到结束，未接听为 0）；不存频道号和 RTC 凭证。每一步都只往前走，IM 服务端重试同一请求、或者结束两次，都不会改坏记录。
- 记录写失败不影响通话（`onError`，默认 `console.error`）；`settle` 里计费抛错时，结束照样记下。

## 开发

```bash
npm install
npm run typecheck
npm test                                   # 需要数据库的用例在缺环境时跳过
npm run build                              # 输出到 dist/
```

- Prisma 用例需要 `DATABASE_URL` 指向一个一次性库，并先 `npx prisma db push --schema prisma`（本机用 `127.0.0.1:55432` 的 `imkit_host`）；还会核对 `prisma/imkit.sql` 与模型一致（改了模型要 `npm run prisma:sql > prisma/imkit.sql`）。
- 与真实 IM 服务端的契约测试需要 `IMKIT_TEST_DSN`（IM 服务端用的库）和构建好的服务端（`server/bin/imkit-server[.exe]`，或用 `IMKIT_SERVER_BIN` 指定）。
- `conformance/host.ts` 是给一致性测试台用的宿主：用本 SDK 扮演宿主跑全部黄金对话，见 `tools/conformance/README.md` 的“外部宿主”。

## 验证结果（2026-10-25）

- 单元与集成测试 56 个全部通过：存储（内存版与 Prisma 版跑同一套，含双账号备注方向、并发互加好友、幂等键、通话记录各步只进不退与游标分页）、宿主插件、应用接口、媒体、后台（含通话记录接口）、通话记录包装器、uid 向量与令牌。
- 契约测试双向通过：宿主插件的每个应答符合 `host.openapi.yaml`，且契约里每个操作都被调到；黄金对话里录到的每次 IM→宿主调用都符合 `host.openapi.yaml`；`ImClient` 对真实 IM 服务端调遍 `im-internal.openapi.yaml` 的每个操作，请求与应答都符合契约。两处契约测试做过变异检查。
- 用本 SDK 替换假宿主后，黄金对话 33/33 全部通过：内存版与 Prisma 版（连测试库），legacy 与 strict 两档。

## 已知问题

- `npm audit` 报运行时依赖 zod 3 的一条高危公告（校验超大的受限集合时可能占用大量内存），修复只在 zod 4。zod 4 是大版本变化，按“大版本与原版一致”的决定暂未升级；缓解是每个插件给请求体设了上限（宿主插件 256 KiB，其余 16 KiB），能进入校验的数据量有界。开发依赖（Prisma CLI 的 `deepmerge-ts`、minio 的 `decode-uri-component`）另有公告，minio 是可选的对等依赖，由宿主自己安装其版本。
- 媒体读取不支持 Range 请求（与原版相同）。
