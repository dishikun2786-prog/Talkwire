---
title: "云端测试服务"
description: "imtalk.openedskill.com：不用自己部署，直接用 Web 示例、Android 示例或自己的客户端连上一套完整的 Talkwire 测试。按空间隔离，每天清空。"
---

# 云端测试服务

[imtalk.openedskill.com](https://imtalk.openedskill.com) 是一套公开的 Talkwire，给第三方开发者试用和测试框架：IM 服务端、开发宿主和 Web 示例都已经跑好，不用先在本机装 PostgreSQL、构建服务端。

::: warning 公开的测试服务
任何人都能用，没有任何保密保证。不要发真实的个人信息、账号或文件。所有数据每天北京时间 04:00 清空。
:::

## 地址

| 用途 | 地址 |
| --- | --- |
| Web 示例 | `https://imtalk.openedskill.com` |
| WebSocket（IM 服务端） | `wss://imtalk.openedskill.com/ws/im` |
| 宿主接口（登录、联系人、资料、上传） | `https://imtalk.openedskill.com` |

服务端用 `strict` 协议档（见[协议档](/protocol/profiles)）。宿主就是仓库里的开发宿主 `examples/sample-host`，加上公网需要的空间隔离与限额（`examples/sample-host/src/sandbox.ts`）。

## 空间与登录

测试服务按**空间**隔离：先建一个空间，拿到 8 位空间码；用名字加空间码登录。登录同一个空间的人自动互为联系人，不同空间互相看不见，也不能互发消息。这样同时测试的开发者互不打扰。

群二维码是例外：二维码是有意分享出去的，拿到它的人不论在哪个空间都能申请入群（与正式部署一样，扫码入群不需要好友关系）。

```bash
# 建一个空间
curl -X POST https://imtalk.openedskill.com/sandbox/spaces
# {"space":"k7m3qx2a"}

# 用名字加空间码登录，拿到访问令牌
curl -X POST https://imtalk.openedskill.com/dev/login \
  -H 'content-type: application/json' \
  -d '{"name":"ann","space":"k7m3qx2a"}'
# {"userId":"sbx-k7m3qx2a-ann","uid":"uid_…","name":"ann","space":"k7m3qx2a","accessToken":"…"}
```

- 名字是 2 到 20 位字母、数字或下划线，不分大小写；同一个名字在不同空间是不同的用户。
- `accessToken` 24 小时有效，客户端的令牌提供者直接返回它（见 [Web SDK](/client/web) 与 [Android SDK](/client/android)）。没有刷新接口，过期了重新登录即可。
- 空间码不存在时登录返回 404：多半是每天的清空把它清掉了，再建一个。
- 测试服务没有管理员登录，也没有管理后台接口。

## 用现成的示例试

**Web 示例**：打开 [imtalk.openedskill.com](https://imtalk.openedskill.com)，点“新建空间”，填名字登录。右上角的“邀请链接”带着空间码，在另一个浏览器（或无痕窗口）打开它、换个名字登录，两边就互为联系人，可以单聊、建群、扫码入群、发图片和文件、撤回、打演示通话。

**Android 示例**：在 `android/` 下用沙箱地址构建，登录页会多出空间码和“新建空间”：

```bash
./gradlew :sample-app:installDebug -PtalkwireSandbox=https://imtalk.openedskill.com
```

不加这个参数时，Android 示例照旧连本机的开发宿主（`adb reverse`，见[快速开始](/guide/quickstart)）。手机和浏览器登录同一个空间，就能互相发消息。

## 用自己的客户端连

把 WebSocket 地址换成测试服务的，令牌用上面登录拿到的 `accessToken`：

```ts
const client = new ImClient({
  endpoints: ["wss://imtalk.openedskill.com/ws/im"],
  tokenProvider: () => accessToken,
  deviceName: "my-test-device",
});
// 上传走宿主：MediaUploader 的地址填 https://imtalk.openedskill.com
const uploader = new MediaUploader("https://imtalk.openedskill.com", () => accessToken);
```

Android 同理：`ImClientConfig(endpoints = listOf("wss://imtalk.openedskill.com/ws/im"), tokenProvider = …)`，`MediaUploader` 的宿主地址填 `https://imtalk.openedskill.com`。联系人列表用宿主的 `GET /api/v1/chat/contacts`，资料用 `GET /api/v1/chat/profiles`，与开发宿主一样（见[宿主接口](/host/)）。

## 能测什么

服务端装了 `groupjoin`、`e2ee`、`groupai`、`call` 四个模块（见[可选模块](/modules/group-join)）。“检查覆盖”一栏表示[端到端检查](#端到端检查)每次都从公网验证这一项。

| 功能 | 测试服务上 | 检查覆盖 |
| --- | --- | --- |
| 登录、首次同步、离线补拉、在线状态 | 可用 | 是 |
| 单聊、群聊 | 可用 | 是 |
| 扫码入群与入群审批 | 可用 | 是 |
| 图片、视频、语音、文件 | 可用，单个文件最大 10 MB；都走同一个上传接口 | 图片 |
| 撤回、已读、清空聊天 | 可用 | 是 |
| 同一用户多台设备同时在线 | 可用，每人最多 10 台 | 是 |
| 通话 | 只有信令：邀请、响铃、接听、挂断都走真实的服务端，但凭据是演示用的，没有音视频服务，接通后没有声音和画面 | 信令全过程 |
| 红包（宿主自定义消息类型的示例） | 可用 | 否 |
| 端到端加密、群 AI 问答 | 模块装了，测试服务没有专门配置，检查不覆盖 | 否 |
| 推送（FCM、APNs、Web Push） | 没有配置；应用在后台时收不到推送 | — |
| 管理后台 | 没有 | — |

## 限额

| 项目 | 上限 |
| --- | --- |
| 同一网络地址新建空间 | 每小时 10 个 |
| 同一网络地址登录 | 每分钟 30 次 |
| 每个空间的用户 | 50 人；已在空间里的人随时可以再登录 |
| 单个上传文件 | 10 MB |
| 每个用户上传 | 每小时 60 次 |
| 全站上传文件总量 | 2 GB，满了要等下一次清空 |
| 全站空间数 | 2000 个 |

超过时接口返回 429（太频繁）、409（空间满）、413（文件太大）、503 或 507（全站满），响应体的 `error` 字段说明原因。

## 每天清空

每天北京时间 04:00，测试服务清空数据库和上传的文件，宿主重启：所有空间、用户、联系人、消息和群都不复存在。之后旧的令牌不能再用，空间码也失效，重新建空间、重新登录即可。Web 示例和沙箱构建的 Android 示例在启动时会自动用保存的名字和空间码重新登录，空间已被清掉时回到登录页。

`GET /sandbox/info` 返回清空时间和当前限额，客户端可以拿它显示提示：

```json
{"sandbox":true,"resetTime":"04:00 Asia/Shanghai","docsUrl":"https://talkwire.openedskill.com/guide/sandbox","limits":{"usersPerSpace":50,"maxUploadBytes":10485760,"uploadsPerHour":60}}
```

## 端到端检查

仓库里的 `tools/sandbox-check/check.mjs` 用真实的 Web 客户端（`@talkwire/client`、`@talkwire/call` 的构建产物）从公网把测试服务走一遍：查看服务信息与网页、确认没有管理接口、建两个空间登录三个用户、空间隔离、WebSocket 登录与首次同步、单聊、另一个空间发不进来、上传图片发出去再下载比对、群聊、另一个空间扫码入群并由群主审批、撤回、已读、同一用户两台设备、通话信令全过程、双方清空聊天、离线补拉与在线状态，共 17 步。

```bash
cd web && npm ci && npm run build && cd ..
node tools/sandbox-check/check.mjs                       # 默认检查 https://imtalk.openedskill.com
node tools/sandbox-check/check.mjs --base https://你的地址
```

每一步都经过公网地址，所以全部通过说明 DNS、Caddy、宿主和 IM 服务端整条路都通。它用 Node 的 WebSocket，不会被 TLS 客户端证书请求挡住；浏览器里的 WebSocket 另用部署目录里的诊断页 `deploy/imtalk/ws-diag.html` 检查。它建的空间和用户留在自己的空间里，第二天清空。

## 自己部署一套

测试服务的部署脚本、systemd 单元、Caddy 配置与每日清空的定时器都在仓库的 [`deploy/imtalk/`](https://github.com/dishikun2786-prog/Talkwire/tree/main/deploy/imtalk)，可以照着在自己的服务器上搭一套给团队用。
