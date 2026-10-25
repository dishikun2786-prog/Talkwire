# ImKit Web SDK

浏览器端的 ImKit：协议类型、连接与同步、IndexedDB 存储、React 界面组件，外加一个可运行的示例和端到端用例。行为与 Android SDK 一致，依据是 `contract/protocol/spec/protocol-v1.md` 与 `contract/redlines/`。许可证 Apache-2.0。

## 包

| 包 | 内容 |
| --- | --- |
| `@imkit/protocol` | 由 `contract/protocol/schema` 生成的帧类型（`src/generated/`，不要手改），uid 与会话编号的计算，帧的解析与编码 |
| `@imkit/client` | `ImClient`（连接、登录、请求与推送）、`ChatEngine`（同步、本地会话与消息、发送与重发、已读、在线、群）、多标签共用连接、媒体上传与录音、通知决策与 Web Push、内存版存储 |
| `@imkit/storage-idb` | `IdbChatStore`：IndexedDB 存储，可选加密 |
| `@imkit/react` | 会话列表（右键或长按：置顶、免打扰、删除；顶上的搜索按会话名与本机存的文字消息找）、聊天页、气泡、输入栏（表情、按住说话）；消息操作（复制、保存、回复引用、转发、多选合并转发、撤回，撤回后留“某某撤回了一条消息”）；群里每条自己的消息显示谁已读，点开是名单；通讯录（`ContactList`）；会话信息页（群成员与角色、按权限给出的成员操作、加人、改群名、置顶、免打扰、清空、退群与解散）与选人（建群、加人）；主题、文案、消息类型注册表 |
| `@imkit/group-join` | 可选模块：扫码入群（协议第 14 节）。`GroupJoinClient` 一个动作一个方法；`/react` 里有群二维码卡片、入群页、入群申请列表、审核开关 |
| `@imkit/admin` | 后台页面，嵌进宿主自己的管理后台：聊天记录（查一个用户的会话与消息，为双方清空单聊）、通话记录与详情。对接 `@imkit/host-node` 的 `imkitAdminRoutes`，只依赖 React |
| `@imkit/call` | 可选模块：音视频通话（协议第 16 节）。`CallSignaling` 一个动作一个方法；`CallManager` 管本标签页的通话状态，驱动一个 `CallMediaEngine`（RTC 适配器）；`/react` 里有标签页内来电与通话界面、单聊页的通话按钮。来电铃声与回铃音由 `ToneRinger` 用 Web Audio 合成（不带音频文件），`CallProvider` 默认启用，`ringer={null}` 关掉。`manager.trace()` 保留最近 100 条记录（收到哪些推送、来电为什么没响、标签页之间的消息、状态变化，只有通话 id 与阶段，不含姓名与凭据），`CallProvider` 的 `log` 可把每条接到调试日志 |
| `@imkit/call-dingrtc` | 通话的 RTC 适配器：阿里云 ARTC 3.0（DingRTC）Web SDK 上的 `CallMediaEngine`。SDK（npm `dingrtc`）是 peer 依赖，由应用安装后传入；宿主一侧的签令牌见 `examples/artc` |
| `sample/` | 示例 App（Vite），连 `examples/sample-host` 与 `imkit-server` |
| `e2e/` | Playwright 端到端用例，对真实服务端跑示例 |

## 命令

```bash
npm install            # 在 web/ 下
npm run gen            # 由 JSON Schema 重新生成帧类型；gen:check 只检查是否最新
npm run typecheck      # 各包、示例与 e2e 的类型检查
npm test               # Vitest
npm run sample         # 示例，http://127.0.0.1:5173
npm run e2e            # Playwright（见下文）
```

## 接入

```ts
import { ChatEngine, ImClient, MediaSender, MediaUploader, createTabTransport } from "@imkit/client";
import { IdbChatStore } from "@imkit/storage-idb";
import { ChatScreen, ConversationList, ImKitProvider } from "@imkit/react";

// 同一账号的所有标签页共用一条连接（见下文）
const tabs = createTabTransport({
  name: userId,
  sharedWorker: () => new SharedWorker(new URL("./imkit-worker.ts", import.meta.url), { type: "module", name: "imkit" }),
});
const client = new ImClient({ endpoints: ["wss://im.example.com/ws/im"], tokenProvider: () => accessToken, transport: tabs.transport, deviceName });
const store = await IdbChatStore.open({ name: "imkit", encrypt: true });
const engine = new ChatEngine(client, store, { appUserId: userId });
await engine.open();
client.start();

// 宿主只实现 ImUiHost：资料（昵称、头像）、媒体地址，可选的媒体发送
const host = {
  profiles: async (uids: string[]) => fetchProfiles(uids), // [{ uid, name, avatarUrl? }]
  resolveMediaUrl: (url?: string) => url && new URL(url, hostBaseUrl).toString(),
  media: new MediaSender(engine, new MediaUploader(hostBaseUrl, () => accessToken)),
};

<ImKitProvider engine={engine} host={host} locale="zh">
  <ConversationList onOpen={setOpen} />
  {open && <ChatScreen conversation={open} />}
</ImKitProvider>
```

Worker 文件只有一行：`serveSharedWorker(self as unknown as SharedWorkerGlobalScope)`。用 Vite 时 Worker 的选项必须是常量，可改用 `import Worker from "./imkit-worker.ts?sharedworker"`，示例就是这样做的（`sample/src/stack.ts`）。

单聊不在协议里列出，按宿主的联系人建立（`engine.ensureConversation(uid, name)`）；群由 `engine.createGroup(name, members)` 创建，建好立即按群名列出。

会话信息页与选人：

```tsx
import { ConversationInfo, PickPeople } from "@imkit/react";

// 聊天页头出现“⋯”按钮，打开会话信息页（不给 onOpenInfo 就没有这个按钮）
<ChatScreen conversation={open} onOpenInfo={() => setInfo(open)} />
<ConversationInfo
  conversation={open}
  onBack={() => setInfo(null)}
  onLeft={() => setOpen(null)}                              // 退群或解散之后
  groupExtras={(topic, role) => <GroupQrCard topic={topic} canRefresh={role !== "member"} linkFor={linkFor} />}
  maxAdmins={10}                                            // 服务端的 group_chat.max_admins
/>
<PickPeople onDone={openChat} />                            // 建群；带 topic 时是往群里加人
```

成员行只对有权限的人可点（群主：设为或取消管理员、转让群主、移出；管理员：移出普通成员），与服务端的检查一致（`memberPermissions`），服务端仍是最终裁决：拒绝时提示，管理员满员（409）单独提示。普通成员之间不能互看资料，成员行不可点。选人列出 `host.contacts`，打开时调 `host.refreshContacts()`；已在群里的人显示但不能勾选。

### 可选模块

服务端装了哪个模块（`IMKIT_MODULES`），前端才装对应的包；不装不影响核心包。两个模块的组件都放在 `<ImKitProvider>` 里，文案跟随它的 `locale`（英文、简体中文、越南文，可逐条替换）。

```tsx
import { GroupJoinProvider, GroupQrCard, JoinGroupScreen, JoinRequestsPanel } from "@imkit/group-join/react";
import { CallButtons, CallOverlay, CallProvider } from "@imkit/call/react";

<ImKitProvider engine={engine} host={host} locale="zh">
  <GroupJoinProvider>
    <CallProvider media={rtcAdapter}>
      {/* 群里：<GroupQrCard topic={topic} canRefresh={isManager} linkFor={(t) => `${appUrl}#/join/${t}`} renderQr={drawQr} /> */}
      {/* 打开入群链接：<JoinGroupScreen qrToken={token} onOpen={openChat} /> */}
      {/* 单聊页头：<CallButtons peer={uid} /> */}
      <CallOverlay />
    </CallProvider>
  </GroupJoinProvider>
</ImKitProvider>
```

- **扫码入群**：二维码里放什么由应用决定（`linkFor`，通常是自己的入群页链接）；套件不带二维码编码器，`renderQr` 交给应用自己的库画。legacy 档的服务端不会给入群者订阅群（D7），`JoinGroupScreen` 入群后会订阅并刷新群列表；只用 `GroupJoinClient` 时调 `afterJoined(engine, topic)`。审核失败时按 strict 档的 `errCode` 区分“群已满”（申请留着）与“已被处理”，不解析 `reason`。服务端同意申请后只通知申请人（F45）：`JoinRequestsPanel` 同意后调 `engine.noteGroupChanged(topic)`，本设备的成员列表随之刷新；只用 `GroupJoinClient` 审核时自己调它。申请人的 `JoinGroupScreen` 停在“申请已发送”时会跟着审核结果变成已入群或被拒绝。
- **通话**：媒体不经过 IM 服务端，走宿主的 RTC 服务；`CallMediaEngine` 是 RTC 适配器要实现的接口（加入频道、开摄像头、静音、离开、挂视频画面）。用阿里云 ARTC 时直接用 `new DingRtcMediaEngine(DingRTC)`（`@imkit/call-dingrtc`），其他 RTC 服务照它的样子写一个。每一方只拿到自己的 `rtcToken`，客户端不把它发给对方。同一账号的多个标签页：来电在所有标签页响铃，在哪个标签页接听或拒绝，其他标签页就停止响铃；一个标签页在通话中时，其他标签页不响铃也不能发起（在通话中的标签页每 30 秒告知一次，关掉的标签页 90 秒后不再算数）。未知的结束原因按“通话已结束”显示。
- **React StrictMode**：开发模式下效果会先卸载一次再挂上；两个 Provider 都在效果里重新开始监听，模块客户端的 `dispose()` 之后可以再 `start()`。严格模式还会把 `useMemo` 的工厂调两次并丢掉一个结果，所以 `CallProvider` 用 `autoStart: false` 建 `CallManager`、只在效果里 `start()`，丢掉的那个从不监听；它自己开的标签页频道在 `dispose()` 时关闭、`start()` 时重开。

### 后台页面

```tsx
import { AdminStyles, CallRecords, ChatRecords, ImAdminApi, ImAdminProvider, imAdminPages } from "@imkit/admin";

const api = new ImAdminApi({ headers: () => ({ authorization: `Bearer ${adminToken}` }) }); // 默认 /api/v1/admin/im
<ImAdminProvider api={api} role={me.role} locale="zh">
  <AdminStyles />
  <ChatRecords />
</ImAdminProvider>
```

- 页面也给成路由表 `imAdminPages`（聊天记录、通话记录、通话详情），路径相对宿主挂载的位置，配合任何路由库；没有路由库时用 `matchImAdminPage(path)`。用法见 `src/pages.tsx` 开头的注释和示例的 `sample/src/Admin.tsx`。
- `role` 只决定显示什么，每个请求服务端都会再查。清空只给超级管理员、只对单聊：要填理由，并逐字输入服务端校验的确认语 `CLEAR <会话>`。一次清空用一个幂等键：没收到应答时重试沿用同一个键，不会清两次；服务端明确报了失败才换新键。
- 消息里的文件链接来自用户，只有 http(s) 和同站路径会做成链接，`javascript:`、`data:` 只显示文字。

## 要点

- **多标签**：有 SharedWorker 时由它持有唯一的 WebSocket；它起不来（脚本加载失败，或浏览器拦截了它的网络请求），标签页改用 BroadcastChannel 选出一个标签持有连接（有 Web Locks 用它，否则心跳），已有的连接按断线处理并重连。一个标签发出的消息与已读，其他标签像第二台设备那样收到。`tabs.role` 是当前方式：`worker`、`leader`、`follower`、`solo`。
- **存储**：表与 Android 的 Room 库对应。`encrypt: true` 时每个会话一把不可导出的 AES-GCM 密钥加密正文、扩展字段与草稿，删会话即删密钥。换账号时 `engine.open()` 清空另一个账号留下的数据（C4）。
- **同步**：登录后对每个会话从本地连续段末尾补拉（C3）。服务端宣告了 `sync-summary`（strict 档的 ImKit 服务端）时，先发一次 `sync_summary`，只对有新消息、被清空或群里他人已读位置变了的会话发 `get`，其余只按摘要推进本人的已读位置；`synced` 事件的 `pulled` 是真正拉了的会话数。补拉时服务端跳过的 seq 一律记为已撤回（协议第 8.2 节），包括本机从没收到过的，否则每次重连都会从那里重拉。群里他人的已读时刻只记在内存里，所以页面刷新后开着已读回执的群会各多拉一次。
- **发送**：回执超时但连接还在，消息保持“发送中”，不自动重发（C2）；连接断了才在下次同步后重发。媒体按文件地址对账，文本按内容对账。
- **气泡**：按 `contentType` 选，从不看正文里的文字（C7）。自定义类型用 `new MessageTypeRegistry.Builder().register(type).build()` 传给 `ImKitProvider`，名字与 Android 的注册表一致。
- **消息操作**：右键或长按气泡打开菜单；每种类型的 `actions` 决定给什么，别人的消息不给撤回、重发、删除。回复把被引用的消息写进 `extra.replyTo`（`seq`、`from`、截到 120 字的 `text`），与 Android 同一格式，两端互相认得。转发一条走 `forward_broadcast`（可选多个会话），多选后转发走 `forward_bundle`（一个会话，按原顺序）；单聊的目标用 p2p 话题，不用对方 uid（F22）。撤回的消息在本地变成一条墓碑（发送人与时间，没有内容，`contentType` 为 `application/vnd.imkit.recalled`，从不发出），显示“你撤回了一条消息”，不计未读；本机没收过的消息被撤回时只记 seq。
- **搜索**：`engine.searchMessages(q)` 读本机每个会话最近 500 条，只搜文字消息，按时间倒序；正文加密存放时经存储解密后再比对。`ConversationList` 默认带搜索框（`searchable={false}` 去掉），结果分“聊天”（会话名）与“聊天记录”两段，点结果打开会话，不跳到那条消息。
- **响铃**：浏览器在页面被点过、按过键之前不让出声，所以一个没碰过的标签页来电时只有界面没有声音；手机浏览器上同时振动（`navigator.vibrate`）。
- **主题与文案**：CSS 变量 `--imkit-*`，跟随系统深色；`strings` 可整体替换。

## 测试

- **单测与组件**：`npm test`。React 组件用 jsdom。
- **回放**：用黄金对话扮演服务端，并注入断线、丢应答、重复帧。需要先构建回放服务器：`cd tools/conformance && go build -o bin/replay.exe ./cmd/replay`，没有它时这些用例跳过。
- **真实服务端（M1 链路）**：需要 `server/bin/imkit-server(.exe)` 和环境变量 `IMKIT_TEST_DSN`（一个可以随便写的 PostgreSQL 测试库，libpq 的 key=value 写法），否则跳过。密码只经环境变量交给服务端，不会出现在日志里。
- **端到端**：`npm run e2e`，同样需要 `IMKIT_TEST_DSN` 和服务端二进制。全局准备会自己起示例宿主、IM 服务端（host 模式、strict 档）与示例的 Vite，端口 16001、16062、15173（可用 `E2E_HOST_PORT`、`E2E_IM_PORT`、`E2E_SAMPLE_PORT` 改），不会碰已经在 6001、6062、5173 上跑的开发环境。默认用本机的 Microsoft Edge（`channel: "msedge"`，不下载浏览器），`E2E_CHANNEL=chrome` 改用 Chrome，`E2E_HEADED=1` 显示窗口。子进程日志在 `e2e/.stack-logs/`。

对照表里 Web 的计划用例（`contract/redlines/matrix.md`）以红线编号起名，如 `it("C3_ResumeFromContiguousEnd", …)`；`tools/conformance` 下 `go run ./cmd/redlines check` 会扫描它们。

## 示例

```bash
# 1. 示例宿主（开发登录、联系人、媒体），127.0.0.1:6001；2. IM 服务端，6062。见 android/QUICKSTART.md
# 3. 示例
npm run sample
```

在不同的浏览器（或无痕窗口）里用不同的名字登录，就能互相发消息；登录时会和之前登录过的人互加联系人。右上角显示当前标签的连接方式。聊天页头的“⋯”打开会话信息页：群成员与角色、成员操作、加人、改群名、置顶与免打扰、清空、退群或解散；群的信息页里还有群二维码与入群审核，链接在另一个浏览器里打开就能加入；单聊页头可以发起语音、视频通话，示例宿主给的是演示用的凭证，只有信令，没有声音和画面（`NoMediaEngine`）。页头的“管理后台”（`#/admin`）是后台页面：示例宿主的 `/dev/admin-login` 用名字和角色登录，能看到每通通话的记录，超级管理员能为双方清空单聊。

## 未做

通话的 RTC 适配器 `@imkit/call-dingrtc` 只用模拟的 SDK 测过，没有接真实的 ARTC 服务打过一通有声音画面的电话（需要 ARTC 账号）。Web Push 只做了订阅与显示，没有接推送服务；只在 Edge（Chromium）上跑过，Firefox、Safari 未测。与 Android 示例在真机上互发、同一账号手机与网页同时在线，已在一台小米（Android 15）上验收，见 `docs/plan.md` 的 Phase 5 结果；2026-10-26 又与 Android 示例互通了撤回、引用回复、双向语音通话（信令与铃声）和扫码入群（含审批），见 `android/DEVICE_CHECKS.md`。

已知问题：网页有一次没有对手机的回拨响铃，之后三次都正常，没能复现；现在服务端的邀请日志写明振铃推给了哪些设备，`CallManager.trace()` 记下收到的推送与来电被丢弃的原因（网页示例开发模式下打到控制台，`imkitCallTrace()` 可看表格），再遇到时能看出停在哪一步，见 `android/DEVICE_CHECKS.md`。同一次互通发现的“同意入群申请后成员数不刷新”“严格模式多出的 `CallManager` 一直收推送”“审批后入群页不变”已修复并在真机上复核。
