# ImKit Web SDK

浏览器端的 ImKit：协议类型、连接与同步、IndexedDB 存储、React 界面组件，外加一个可运行的示例和端到端用例。行为与 Android SDK 一致，依据是 `contract/protocol/spec/protocol-v1.md` 与 `contract/redlines/`。许可证 Apache-2.0。

## 包

| 包 | 内容 |
| --- | --- |
| `@imkit/protocol` | 由 `contract/protocol/schema` 生成的帧类型（`src/generated/`，不要手改），uid 与会话编号的计算，帧的解析与编码 |
| `@imkit/client` | `ImClient`（连接、登录、请求与推送）、`ChatEngine`（同步、本地会话与消息、发送与重发、已读、在线、群）、多标签共用连接、媒体上传与录音、通知决策与 Web Push、内存版存储 |
| `@imkit/storage-idb` | `IdbChatStore`：IndexedDB 存储，可选加密 |
| `@imkit/react` | 会话列表、聊天页、气泡、输入栏与按住说话；主题、文案、消息类型注册表 |
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

## 要点

- **多标签**：有 SharedWorker 时由它持有唯一的 WebSocket；它起不来（脚本加载失败，或浏览器拦截了它的网络请求），标签页改用 BroadcastChannel 选出一个标签持有连接（有 Web Locks 用它，否则心跳），已有的连接按断线处理并重连。一个标签发出的消息与已读，其他标签像第二台设备那样收到。`tabs.role` 是当前方式：`worker`、`leader`、`follower`、`solo`。
- **存储**：表与 Android 的 Room 库对应。`encrypt: true` 时每个会话一把不可导出的 AES-GCM 密钥加密正文、扩展字段与草稿，删会话即删密钥。换账号时 `engine.open()` 清空另一个账号留下的数据（C4）。
- **发送**：回执超时但连接还在，消息保持“发送中”，不自动重发（C2）；连接断了才在下次同步后重发。媒体按文件地址对账，文本按内容对账。
- **气泡**：按 `contentType` 选，从不看正文里的文字（C7）。自定义类型用 `new MessageTypeRegistry.Builder().register(type).build()` 传给 `ImKitProvider`，名字与 Android 的注册表一致。
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

在不同的浏览器（或无痕窗口）里用不同的名字登录，就能互相发消息；登录时会和之前登录过的人互加联系人。右上角显示当前标签的连接方式。

## 未做

Web Push 只做了订阅与显示，没有接推送服务；只在 Edge（Chromium）上跑过，Firefox、Safari 未测。与 Android 示例在真机上互发、同一账号手机与网页同时在线，已在一台小米（Android 15）上验收，见 `docs/plan.md` 的 Phase 5 结果。
