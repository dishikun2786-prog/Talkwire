# 5 分钟接入 ImKit（Web）

接入方只写三样东西：**令牌**、**联系人与资料**、**通知**。消息收发、离线补拉、已读、多标签共用一条连接、本地加密存储、聊天界面都由 ImKit 提供。完整的可运行例子是 `sample/`（下文的代码都摘自 `sample/src/stack.ts` 与 `App.tsx`）。

## 1. 依赖

```bash
npm install @imkit/client @imkit/storage-idb @imkit/react   # 只要逻辑、自己画界面时不装 @imkit/react
```

包名是占位符（`@imkit/*`），在本仓库里是 `web/packages/` 下的工作区包。React 18 或 19。

## 2. 令牌

IM 服务端只认宿主签发的访问令牌（HS256，`sub` 为宿主用户 id，`type: "access"`，`role: "app_user"`，密钥与服务端的 `JWT_SECRET` 相同；也可以用 `IMKIT_JWKS_URL` 换成宿主的公钥）。页面用自己的登录拿到它：

```ts
const tokenProvider = (forceRefresh?: boolean) => myAuth.accessToken(forceRefresh); // 可以返回 Promise；返回 null 表示未登录
```

令牌被拒时 ImKit 会带 `forceRefresh = true` 再要一次。

## 3. 联系人与资料：实现 `ImUiHost`

框架只认 uid，名字、头像、谁是好友都归宿主。用 Node 宿主 SDK（`@imkit/host-node` 的 `imkitAppRoutes`）时这两个接口现成：

```ts
import { Signal } from "@imkit/client";
import type { ImProfile, ImUiHost } from "@imkit/react";

const get = (path: string) => fetch(path, { headers: { authorization: `Bearer ${token}` } }).then((r) => r.json());
const toProfile = (p: { uid: string; displayName: string; avatarUrl: string | null }): ImProfile => ({ uid: p.uid, name: p.displayName || p.uid, avatarUrl: p.avatarUrl ?? undefined });

const contacts = new Signal<ImProfile[]>([]);
const host: ImUiHost = {
  contacts, // 发起单聊、建群时列出
  profiles: async (uids) => (await get(`/api/v1/chat/profiles?uids=${encodeURIComponent(uids.join(","))}`)).items.map(toProfile),
  resolveMediaUrl: (url) => (url ? new URL(url, hostBaseUrl).toString() : undefined),
};
```

单聊不在协议里列出，按宿主的联系人建立：拉到联系人后对每个调 `engine.ensureConversation(uid, name)`；对方加你为好友时客户端会收到 `contactSync` 事件，再拉一次。可选：`media`（发图片、视频、语音；不给时不显示附件与语音按钮）、`refreshContacts`（选人页打开时重新拉联系人）、`onOpenProfile`（默认什么都不做：群成员之间不能互看资料）。

## 4. 装起来

同一账号的所有标签页共用一条连接，放在一个 SharedWorker 里。Worker 文件只有一行：

```ts
// imkit-worker.ts
import { serveSharedWorker } from "@imkit/client";
serveSharedWorker(self as unknown as SharedWorkerGlobalScope);
```

```tsx
import { ChatEngine, ImClient, MediaSender, MediaUploader, createTabTransport } from "@imkit/client";
import { IdbChatStore } from "@imkit/storage-idb";
import { ChatScreen, ConversationList, ImKitProvider, ImKitStyles } from "@imkit/react";
import ImkitWorker from "./imkit-worker.ts?sharedworker"; // Vite；别的打包器见 README

const tabs = createTabTransport({ name: userId, sharedWorker: () => new ImkitWorker({ name: "imkit:" + userId }) });
const client = new ImClient({ endpoints: ["wss://im.example.com/ws/im"], tokenProvider, transport: tabs.transport, deviceName });
const store = await IdbChatStore.open({ name: "imkit", encrypt: true });
const engine = new ChatEngine(client, store, { appUserId: userId });
await engine.open();   // 换了账号时先清掉上一个账号的本地数据
client.start();
host.media = new MediaSender(engine, new MediaUploader(hostBaseUrl, tokenProvider));

<div className="imkit-root">
  <ImKitStyles />
  <ImKitProvider engine={engine} host={host} locale="zh">
    <ConversationList onOpen={setOpen} /> {/* 顶上带搜索框；searchable={false} 去掉 */}
    {open && <ChatScreen conversation={open} onBack={() => setOpen(null)} onOpenInfo={() => setInfo(open)} />}
    {info && <ConversationInfo conversation={info} onBack={() => setInfo(null)} onLeft={() => { setInfo(null); setOpen(null); }} />}
  </ImKitProvider>
</div>
```

会话信息页（`ConversationInfo`）管群成员与角色、加人、改群名、置顶、免打扰、清空、退群与解散；建群与加人用 `PickPeople`；通讯录用 `ContactList`（点人即建立并打开单聊）。可选模块的群内容（二维码、入群审核）经 `groupExtras` 放进群的信息页。

- `deviceName` 是这个浏览器的设备名（存在 localStorage 里），同一账号的手机和网页因此能同时在线。
- 退出登录：`engine.close()`、`tabs.close()`，再清掉自己的会话。
- 颜色、圆角用 CSS 变量 `--imkit-*`，跟随系统深色；文案自带英文、简体中文、越南文，`strings` 可整体替换。

## 5. 通知

```ts
import { BrowserNotifier } from "@imkit/client";

const notifier = new BrowserNotifier(engine, {
  openConversation: () => currentConversation,           // 正在看的会话不提醒
  render: (m, c, groupName) => ({ title: groupName ?? c?.title ?? "", body: m.content }),
  onBanner: (m, r) => showMyBanner(r),                    // 页面可见但不在这个会话：页内横幅
  onOpen: (c) => openConversation(c),                     // 点了系统通知
  playSound: () => myDing.play(),                         // 提示音由页面放
});
notifier.start();
void BrowserNotifier.requestPermission();
```

页面关着时的离线推送用 Web Push：服务端配好 VAPID 密钥（`IMKIT_VAPID_KEY_PATH`）后登录应答带 `vapidKey`，页面注册一个 Service Worker，再 `enableWebPush(client, registration)`；Service Worker 里收到推送用 `showPushNotification` 显示。

## 6. 自己的消息类型与可选模块

业务卡片（红包、转账、订单）不进 SDK：注册一个消息类型，`new MessageTypeRegistry.Builder().register(type).build()` 传给 `ImKitProvider` 的 `types`。类型按 `contentType`（和 `extra`）认，不看 `content` 的文字。例子见 `examples/red-packet/`。

服务端装了哪个模块，前端才装对应的包：扫码入群 `@imkit/group-join`、通话 `@imkit/call`、后台页面 `@imkit/admin`。见 `README.md` 的“可选模块”“后台页面”和 `docs/modules.md`。

## 在本机跑示例

需要：构建好的服务端（`server/bin/imkit-server.exe`）、一个空的 PostgreSQL 库（服务端启动时自己建表）、Node 20+。两个进程必须用**同一个** `JWT_SECRET`。

```bash
# 1. 示例宿主（开发登录、联系人、媒体、演示通话、后台），127.0.0.1:6001
cd examples/sample-host && npm install && JWT_SECRET=<至少 32 个字符> npm start
```

```bash
# 2. IM 服务端（另开一个终端），6062
JWT_SECRET=<同上> DB_HOST=127.0.0.1 DB_USER=<用户> DB_PASSWORD=<密码> DB_NAME=<库名> server/bin/imkit-server.exe
```

```bash
# 3. 示例（Vite 代理宿主和 IM 的 WebSocket），http://127.0.0.1:5173
cd web && npm install && npm run sample
```

在两个浏览器（或一个普通窗口、一个无痕窗口）里用不同的名字登录，就能互相发消息、建群、扫码入群、打演示通话；页头的“管理后台”是后台页面。`/dev/login` 和 `/dev/admin-login` 凭名字就能登录，只监听 127.0.0.1，只能在本机开发用。

不开浏览器也能跑一遍：`npm run e2e` 自己起示例宿主、IM 服务端和示例，用本机 Edge 走完单聊、群聊、多端、扫码入群、通话、后台（需要 `IMKIT_TEST_DSN` 指向一个测试库）。
