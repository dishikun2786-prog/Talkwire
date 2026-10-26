---
title: "Node 宿主 SDK"
description: "用 @talkwire/host-node 在 Node 与 Fastify 上实现宿主：四组路由插件、存储接口与参考实现、加好友令牌、媒体、后台与通话记录。"
---

# Node 宿主 SDK

`@talkwire/host-node` 把宿主一侧做成 Fastify 4 插件，加上存储接口和它们的参考实现。它实现了 [宿主接口契约](/host/) 里宿主该做的全部事情，你只需要接上自己的登录态和数据库。源码在仓库的 [`host-sdk-node/`](https://github.com/dishikun2786-prog/Talkwire/blob/main/host-sdk-node/README.md)。

红包、转账、积分等业务功能不在 SDK 里，做法见 [`examples/red-packet/`](https://github.com/dishikun2786-prog/Talkwire/blob/main/examples/red-packet/README.md)。

## 安装

SDK 是 ESM 包，需要 Node 20 或更高版本。包的入口指向 `dist/`，而 `dist/` 不进仓库，所以先在 `host-sdk-node/` 里构建：

```bash
npm install
npm run build                              # 输出到 dist/
```

仓库里的开发用宿主 `examples/sample-host` 以 `"@talkwire/host-node": "file:../../host-sdk-node"` 引用它。依赖与入口：

| 项 | 说明 |
| --- | --- |
| `fastify` 4.29.1 | 对等依赖，必需 |
| `@prisma/client` 6.19.3 | 对等依赖，可选；用 Prisma 存储时才需要 |
| `minio` 8.0.7 | 对等依赖，可选；用 S3 兼容存储时才需要 |
| `@talkwire/host-node` | 主入口：插件、`ImClient`、存储接口、内存版存储、本地磁盘存储 |
| `@talkwire/host-node/prisma` | Prisma 版存储 |
| `@talkwire/host-node/s3` | S3 兼容存储 `S3BlobStore` |

## 导出一览

主入口导出四组路由插件、一个客户端和若干工具：

| 导出 | 作用 |
| --- | --- |
| `talkwireHostPlugin` | IM 服务端调用的端点：好友关系、资料（带备注）、运行配置、端到端加密配置、删媒体、通话与群 AI 模块；校验内部令牌 |
| `talkwireAppRoutes` | 给聊天 App 用的接口：我的资料、批量查资料、按 handle 搜索与扫码找人（带一次性加好友令牌）、联系人增删改查、按方向存的备注、增量同步 |
| `talkwireMediaRoutes` | 媒体上传、读取、删除自己未发出的上传 |
| `talkwireAdminRoutes` | 后台查看与清空聊天记录；给了通话记录存储时还有通话记录列表与详情 |
| `recordingCalls` | 包住宿主自己的通话处理器，把它接受的每通通话写进 `CallRecordStore` |
| `ImClient` | 调 IM 服务端的内部端点 |
| `imUid`、`isUid` | 宿主用户 id 到 uid 的算法，见 [uid](/host/token#uid) |
| `signInternalToken`、`verifyInternalToken`、`bearerToken` | 签发与校验内部令牌，取 `Authorization: Bearer` 里的令牌 |
| `issueAddToken`、`checkAddToken`、`issueContactQr`、`checkContactQr` | 加好友令牌与联系人二维码 |
| `addContact`、`respondToContact`、`setAlias`、`removeContact` 等 | 联系人状态变更的业务规则，`talkwireAppRoutes` 用的就是它们 |
| `MemoryProfileStore`、`MemoryContactStore`、`MemoryAuditStore`、`MemoryCallRecordStore`、`staticConfig` | 内存版存储与固定配置，用于测试和演示 |
| `LocalDiskBlobStore`、`mediaDeleter` | 本地磁盘存储媒体；把存储接到宿主插件的 `deleteMedia` 上 |

## 最小接入

下面这段把三个插件装进一个 Fastify 应用，摘自 SDK 的 README：

```ts
import Fastify from "fastify";
import { PrismaClient } from "@prisma/client";
import { ImClient, talkwireHostPlugin, talkwireAppRoutes, talkwireMediaRoutes, mediaDeleter, LocalDiskBlobStore } from "@talkwire/host-node";
import { prismaStores } from "@talkwire/host-node/prisma";

const prisma = new PrismaClient();
const { profiles, contacts, audit } = prismaStores(prisma);
const im = new ImClient({ baseUrl: "http://127.0.0.1:6062", secret: process.env.JWT_SECRET! });
const blobs = new LocalDiskBlobStore("/data/talkwire-media");
const authenticate = async (req) => /* 你的登录态 → 宿主用户 id 或 null */ null;

const app = Fastify();
await app.register(talkwireHostPlugin, { secret: process.env.JWT_SECRET!, contacts, profiles, deleteMedia: mediaDeleter(blobs) });
await app.register(talkwireAppRoutes, { authenticate, profiles, contacts, im, addTokenSecret: process.env.ADD_TOKEN_SECRET! });
await app.register(talkwireMediaRoutes, { authenticate, store: blobs, im });
```

- IM 服务端的 `MAIN_API_BASE_URL` 指向这个应用，两边的 `JWT_SECRET` 相同。
- `authenticate` 由你实现：校验 App 发来的登录态，返回宿主用户 id，未登录时返回 `null`。App 发来的通常就是 [签发 IM 令牌](/host/token) 里的那个访问令牌，sample-host 就是这样验的。
- `addTokenSecret` 用一个只有宿主知道的密钥。

## talkwireHostPlugin

`talkwireHostPlugin` 注册 IM 服务端调用的全部端点（契约 `host.openapi.yaml`），每个请求都校验内部令牌，不通过答 401。选项：

| 选项 | 说明 |
| --- | --- |
| `secret` | 与 IM 服务端共享的密钥（它的 `JWT_SECRET`） |
| `contacts`、`profiles` | 联系人与资料存储 |
| `deleteMedia` | 删除 URL 背后的文件，返回删了几个；不给就什么都不删 |
| `config` | `ConfigSource`，提供两份配置文档；不给时两个配置端点都答 404，IM 用内置默认值 |
| `calls` | 通话模块的宿主侧 `CallHandlers`；不给时 `calls/*` 答 404 |
| `groupAi` | 群助手处理函数；不给时 `group-ai-qa` 答 404 |
| `basePath` | 端点挂在哪里，默认 `/api/v1/internal/im`；IM 调用 `MAIN_API_BASE_URL` 加这个路径 |

各端点的行为与契约一致：

- `friendship`：两人的关系状态为 `accepted` 时答 `true`。
- `profile`：只有两人是已接受的好友时才给 viewer 自己设的备注，否则给 sender 的显示名；不认识的 sender 答空串，让 IM 用自己的兜底名字。
- 两个配置端点：`ConfigSource` 返回 `null` 时答 404。`staticConfig({ runtime, e2ee })` 用固定的文档造一个 `ConfigSource`。
- `calls/settle`：调完处理器答 `{"ok": true}`。

`CallHandlers` 有四个方法，`create`、`answer`、`settle`、`upgrade`，参数与应答对应契约里的四个通话端点。`groupAi` 处理函数收到群里的问题，答 `{ ok, answer, reason, message }`；IM 等 10 秒，超时就发 `busy` 文案。请求体上限是 256 KiB，最大的合法请求是 500 个 URL 的 `delete-media`。

## talkwireAppRoutes

`talkwireAppRoutes` 是聊天 App 直接调用的宿主接口，IM 服务端从不调用它们。它们用 `authenticate` 识别用户，默认挂在 `/api/v1/chat` 下：

| 方法与路径 | 作用 |
| --- | --- |
| `GET /me` | 我的资料；第一次访问时建出资料 |
| `PATCH /me` | 改 `handle`、`displayName`、`avatarUrl` |
| `GET /me/qr` | 我的联系人二维码内容 |
| `GET /profiles?uids=` | 按 uid 批量查名字与头像，最多 200 个 |
| `POST /contacts/search` | 按 handle 找人，返回资料和加好友令牌 |
| `POST /contacts/qr-resolve` | 扫码找人，返回资料和加好友令牌 |
| `POST /contacts` | 加好友，带 `contactId` 与 `addToken` |
| `GET /contacts` | 联系人列表，可按 `status`、`direction`、`since` 筛选 |
| `PATCH /contacts/:id` | 接受、拒绝、拉黑，或设置自己的备注 |
| `DELETE /contacts/:id` | 删除关系，重复删除也算成功 |

选项：

| 选项 | 说明 |
| --- | --- |
| `authenticate` | 返回已登录用户的宿主用户 id，未登录返回 `null` |
| `profiles`、`contacts` | 资料与联系人存储 |
| `im` | `ImClient`，用来调 `contact-sync`，以及在没有 `notify` 时推送事件 |
| `addTokenSecret` | 加好友令牌和二维码签名用的密钥 |
| `addTokenMode` | `"require"`（默认）、`"observe"` 或 `"off"` |
| `onAddTokenMissing` | `"observe"` 下遇到没有有效令牌的请求时调用 |
| `notify` | 把事件送到一个用户的各个设备；默认经 IM 的 `custom-broadcast` |
| `isUserActive` | 一个用户能否被找到和添加，默认都能 |
| `initialProfile` | 用户的初始资料；默认 handle 由 id 派生（`defaultHandle`），没有名字 |
| `handleChange` | 用户改 handle 的次数：`"once"`（默认）、`"never"`、`"always"` |
| `reservedDisplayNames` | 谁都不能用的显示名，不分大小写 |
| `basePath` | 默认 `/api/v1/chat` |
| `onError` | 后台工作（同步 IM、推送事件）出错时调用，默认写 Fastify 日志 |

联系人与资料变化时，插件默认经 `custom-broadcast` 把事件推给用户的各个设备，`what` 为 `contact.request`、`contact.accepted`、`contact.removed`、`contact.alias` 或 `profile.updated`。关系被接受或解除时，插件还会调 `contact-sync`。

### 加好友令牌与二维码

加好友默认要求令牌（`addTokenMode: "require"`），令牌只能通过搜索或扫码得到。令牌绑定双方、两分钟有效，不带或无效的请求得到 403 `add_token_required`。

令牌不记状态，两分钟内对同一对用户可以再用；第二次请求会遇到已经存在的请求，得到 409。从不带令牌的旧客户端迁移时，先用 `"observe"`：请求照常处理，缺令牌的情况经 `onAddTokenMissing` 报告。

联系人二维码里放 `GET /me/qr` 给的签名串（`c1.<uid>.<签名>`），扫码后把它作为 `qr` 交给 `POST /contacts/qr-resolve`，签名不对得到 400 `invalid_qr`。uid 不是秘密，群里每个成员都看得到别人的，所以 `"require"` 下只给 `uid` 会被拒（400 `qr_required`），否则群成员不用对方出示二维码就能互加。`"observe"` 下裸 uid 照旧能解析，并经 `onAddTokenMissing` 报告，供迁移期已经印出去的旧码使用。

### 增量同步

`GET /contacts` 的应答带 `syncCursor`，下次把它作为 `since` 传回，只拿这之后变化过的关系。游标在读取之前就定下，所以读取期间变化的行要么在这次的应答里，要么在游标之后，不会丢。

## talkwireMediaRoutes

`talkwireMediaRoutes` 负责聊天媒体的上传、读取和删除：

| 方法与路径 | 作用 |
| --- | --- |
| `POST /api/v1/chat/upload` | 上传一个文件（multipart），应答 `{url, name, mime, size}` |
| `GET /media/:name` | 读取文件 |
| `DELETE /api/v1/chat/upload/:name` | 删除自己上传后没发出去的文件 |

- **命名**：文件名由 SDK 生成，形如 `m-<上传者标记>-<上传时间>-<32 位随机十六进制>.<扩展名>`，不可猜。
- **读取**：带 `Content-Length`、`Cache-Control: private, no-store`、`X-Content-Type-Options: nosniff`；图片、视频、音频 `inline`，其余 `attachment`。不支持 Range 请求。
- **删除**：只能删自己上传、30 分钟内的文件，删之前先经 `media-ref-count` 确认没有消息引用它。被引用答 409，查不了答 503，没给 `im` 答 501。

| 选项 | 默认值 |
| --- | --- |
| `store` | 必填，`BlobStore` |
| `authenticate` | 必填 |
| `im` | 不给就不能删除上传 |
| `types` | `DEFAULT_MEDIA_TYPES`：常见图片、视频、音频与 pdf、doc、docx、txt、zip |
| `maxBytes` | 50 MB |
| `servePath` | `/media` |
| `uploadPath` | `/api/v1/chat/upload` |
| `serveRequiresAuth` | `false`，文件名本身不可猜 |
| `deleteWindowMs` | 30 分钟 |

`mediaDeleter(store, servePath)` 是宿主插件的 `deleteMedia`：只删这组路由签发的 URL，忽略其他 URL。存储有两种参考实现：本地磁盘 `LocalDiskBlobStore`，以及 S3 兼容存储 `S3BlobStore`（从 `@talkwire/host-node/s3` 导入）。

::: warning 注意
存储桶要保持私有，文件只经宿主自己的路由提供，不进公开桶或 CDN。否则消息撤回或清空之后，文件仍然能被访问到。
:::

## 存储

SDK 通过下面这些接口读写数据，宿主可以在自己的数据库上实现它们，也可以用参考实现：

| 接口 | 存什么 | 参考实现 |
| --- | --- | --- |
| `ProfileStore` | 资料：uid、handle（唯一，不分大小写）、显示名、头像 | 内存版、Prisma 版 |
| `ContactStore` | 关系：每对用户最多一条，记录谁先发起、状态、各自的备注、谁拉黑 | 内存版、Prisma 版 |
| `AuditStore` | 后台操作审计，只有元数据 | 内存版、Prisma 版 |
| `CallRecordStore` | 通话记录，只有元数据 | 内存版、Prisma 版 |
| `ConfigSource` | 两份配置文档 | `staticConfig` |
| `BlobStore` | 媒体文件 | 本地磁盘、S3 兼容 |

### Prisma

用 Prisma 时，把 [`prisma/talkwire.prisma`](https://github.com/dishikun2786-prog/Talkwire/blob/main/host-sdk-node/prisma/talkwire.prisma) 拷进你的 schema（多文件 schema 直接放进 schema 目录），再照常迁移。模型不引用宿主自己的用户表，可以和任何用户模型放在一起；每个字段都映射到明确的列名。

| 模型 | 表 |
| --- | --- |
| `TalkwireProfile` | `talkwire_profiles` |
| `TalkwireContact` | `talkwire_contacts` |
| `TalkwireAdminAudit` | `talkwire_admin_audit` |
| `TalkwireCallRecord` | `talkwire_call_records` |

`prismaStores(prisma, options)` 返回 `profiles`、`contacts`、`audit`，生成的客户端里有 `TalkwireCallRecord` 模型时还有 `calls`。`PrismaContactStore` 在一个持有 PostgreSQL 咨询锁的事务里处理同一对用户的变更，所以多个进程之间也是串行的；`options.transactionTimeoutMs` 是这个事务能持续多久，默认 10 秒。

### 不用 Prisma

不用 Prisma 的宿主可以用 [`prisma/talkwire.sql`](https://github.com/dishikun2786-prog/Talkwire/blob/main/host-sdk-node/prisma/talkwire.sql) 建同样的表，再自己实现存储接口。这份 SQL 里 `id`（uuid）和 `updated_at` 由应用填写。

### 改名之前建的库

项目改名为 Talkwire 之前就建过表的宿主，在部署改名后的 SDK 之前执行一次 [`prisma/rename-from-imkit.sql`](https://github.com/dishikun2786-prog/Talkwire/blob/main/host-sdk-node/prisma/rename-from-imkit.sql)。它把 `imkit_*` 表和索引改成 `talkwire_*`，数据保留；已经改过名或从没建过的表不受影响。

这个文件只在仓库里，不随包发布。Prisma 用户还要把模型 `ImkitCallRecord` 改名为 `TalkwireCallRecord`（重新拷一次 `talkwire.prisma`），客户端属性随之变成 `prisma.talkwireCallRecord`。

## 后台与通话记录

`talkwireAdminRoutes` 给宿主的管理员查看与清空聊天记录，前端页面是 `web/packages/admin`（`@talkwire/admin`），见 [后台页面](/modules/admin)。接入方式摘自 SDK 的 README：

```ts
import { talkwireAdminRoutes, recordingCalls } from "@talkwire/host-node";

const { calls: callRecords } = prismaStores(prisma); // 模型里有 TalkwireCallRecord 时才有
await app.register(talkwireHostPlugin, { /* 同上 */ calls: recordingCalls(myCallHandlers, callRecords!) });
await app.register(talkwireAdminRoutes, { authenticate: adminAuth, im, audit, profiles, calls: callRecords });
```

`adminAuth` 返回 `{ adminId, role: "admin" | "superadmin" }`，不是管理员时返回 `null`。管理员的令牌要与用户的令牌分开签（不同的密钥或不同的 `type`），因为 IM 服务端只认普通用户的访问令牌。路由默认挂在 `/api/v1/admin/im` 下：

| 方法与路径 | 作用 | 谁能用 |
| --- | --- | --- |
| `GET /users/:userId/topics` | 一个用户的会话 | 任何管理员 |
| `GET /topics/:topic` | 一个会话 | 任何管理员 |
| `GET /topics/:topic/messages` | 一个会话的消息，可按 `beforeSeq`、`limit`、`type`、`keyword`、`fromMs`、`toMs` 筛选 | 任何管理员 |
| `POST /topics/:topic/clear` | 为双方清空一个单聊 | 超级管理员 |
| `GET /calls` | 通话记录，可按用户、状态、结果、类型、时间筛选，游标分页 | 任何管理员；给了 `calls` 才有 |
| `GET /calls/:callSessionId` | 一条通话记录 | 任何管理员；给了 `calls` 才有 |

IM 只认 uid，这组路由会用 `ProfileStore` 把 uid 换成宿主用户 id 和显示名再交给前端。

### 清空单聊

清空会永久删除消息和文件，请求体要带四样东西：

- `confirm`：确认语，必须正好是 `CLEAR <topic>`（`clearConfirmation(topic)` 生成）。
- `reason`：理由，1–500 个字符。
- `idempotencyKey`：幂等键，8–128 个字符。用同一个键再请求一次，只回答第一次的结果，不会清空第二次。
- `targetUserId`：可选，管理员当时在看的用户，记进审计。

审计只记元数据：谁、哪个会话、为什么、删了多少，从不记消息内容。

### 通话记录

`recordingCalls(handlers, store, options)` 包住你自己的 `CallHandlers`，把每通被 `create` 接受的通话写进 `CallRecordStore`。被拒的邀请不算通话，不记录。

- 记录只有元数据：双方、请求的与最终的类型、各步时间、结束原因、时长（从接听到结束，未接听为 0）；不存频道号和 RTC 凭证。
- 每一步都只往前走：IM 服务端重试同一请求或结束两次，都不会改坏记录。
- 记录写失败不影响通话，交给 `options.onError`，默认 `console.error`；`settle` 里计费抛错时，结束照样记下。

## ImClient

`ImClient` 调 IM 服务端的内部端点（契约 `im-internal.openapi.yaml`），每个请求自己签一个 60 秒有效的内部令牌。选项是 `baseUrl`（例如 `http://127.0.0.1:6062`）、`secret`、`timeoutMs`（默认 5 秒）和测试用的 `fetch`。

| 方法 | 对应端点 |
| --- | --- |
| `contactSync(a, b, event)` | `contact-sync` |
| `mediaRefCount(url)` | `media-ref-count` |
| `topicMembersResolve(topic, checkAppUserIds, resolveUids)` | `topic-members-resolve` |
| `customBroadcast(target, kind, payload)` | `custom-broadcast`；`target` 是 `{ topic }` 或 `{ targetUid }` |
| `callBroadcast(targetUid, payload)` | `call-broadcast`，返回 `delivered` |
| `redPacketBroadcast(topic, payload)` | `red-packet-broadcast`，已弃用 |
| `adminTopics`、`adminTopic`、`adminMessages`、`adminClear` | 后台四个端点 |

非 2xx 的应答或不是 JSON 的应答体会抛 `ImClientError`，其中 `status` 是状态码，`body` 是原始应答体。

## 测试

SDK 自己的测试在 `host-sdk-node/` 下运行：

```bash
npm install
npm run typecheck
npm test                                   # 需要数据库的用例在缺环境时跳过
npm run build                              # 输出到 dist/
```

- **Prisma 用例**：需要 `DATABASE_URL` 指向一个一次性库，并先执行 `npx prisma db push --schema prisma`。它们还会核对 `prisma/talkwire.sql` 与模型一致，改了模型要执行 `npm run prisma:sql > prisma/talkwire.sql`。
- **与真实 IM 服务端的契约测试**：需要 `TALKWIRE_TEST_DSN`（IM 服务端用的库）和构建好的服务端（`server/bin/talkwire-server[.exe]`，或用 `TALKWIRE_SERVER_BIN` 指定）。缺任何一个就跳过。
- **黄金对话**：`conformance/host.ts` 用本 SDK 扮演宿主，让一致性测试台跑全部黄金对话，见 [用其他语言实现](/host/other-languages#黄金对话)。

已知问题：`npm audit` 对运行时依赖 zod 3 报一条高危公告（校验超大的受限集合时可能占用大量内存），修复只在 zod 4，暂未升级。缓解是每个插件都给请求体设了上限：宿主插件 256 KiB，其余 16 KiB。
