---
title: "音视频通话"
description: "通话模块只做信令、振铃与计时，声音和画面走宿主自己的 RTC 服务，凭证与计费由宿主决定。"
---

# 音视频通话

通话模块（`call`）只管信令：谁打给谁、接没接、什么时候结束。声音和画面走宿主自己的 RTC 服务，不经过 IM 服务端；谁能打给谁、RTC 频道与凭证、怎么计费、什么时候强制结束，都由宿主决定。线协议在规范第 16 节，见 [规范全文](/protocol/spec)。

## 装上与拿掉

通话模块在服务端、宿主和客户端三处各装一份，任何一处都能单独拿掉。

| 位置 | 装上 | 拿掉 |
| --- | --- | --- |
| 服务端 | `TALKWIRE_MODULES` 列出 `call`；不设这个变量时装宿主支持的全部模块 | 从列表里去掉；通话动作得到 501 `module not enabled: call` |
| 宿主（Node SDK） | 给 `talkwireHostPlugin` 的 `calls` 选项 | 不给；`calls/*` 端点答 404 |
| Web | `@talkwire/call`，外加一个 RTC 适配器 | 不装；核心包不引用它 |
| Android | `talkwire-call`，外加一个 RTC 适配器 | 不装；核心模块不引用它 |

- standalone 宿主（`TALKWIRE_HOST_MODE=standalone`）不支持通话，只支持 `groupjoin` 与 `e2ee`。
- 通话模块没有自己的表：进行中的通话只在 IM 服务端的内存里，通话记录存在宿主，见 [后台页面](/modules/admin#通话记录)。

## 服务端：信令与振铃

IM 服务端管实时的部分：对方在不在线、谁先接听或挂断、断线怎么办。每次需要业务决定时，它同步问一次宿主。

1. 主叫发 `call_invite`，`topic` 是对方的 uid，`callType` 是 `voice` 或 `video`。对方一台在线设备都没有时立即回 409 `offline`，不询问宿主。
2. 每个 uid 同一时刻只能在一通电话里，对方或自己已在通话中时回 409 `busy`。
3. 服务端生成通话号 `callSessionId`，调宿主的 `create`。宿主以 `not_friends` 拒绝时回 401，以其他原因拒绝时回 409，`reason` 原样交给主叫。
4. 被叫的每台在线设备收到 `call_ring`，带宿主按被叫视角解析的 `callerName`、`callerAvatar` 和 `ringTimeoutSeconds`。被叫**先**收到振铃，主叫**后**收到邀请的应答（F49），客户端要缓存早到的推送。
5. 被叫 `call_accept` 时，服务端调宿主的 `answer`，拿到频道、`appId` 和双方各自的令牌。被叫在应答里拿到自己的 `rtcToken`，主叫在 `call_answered` 推送里拿到自己的；被叫的其他设备收到 `call_ended`，`reason` 为 `answered_elsewhere`。
6. 任何一种结束（挂断、拒接、取消、无人接听、断线、接听失败）都推 `call_ended`，再在后台调宿主的 `settle` 报告结束原因。

通话状态的每一次改变都是一次“比较后替换”，振铃计时器在每条结束路径上显式停掉（红线 R16）。所以接听、拒接、取消与断线同时到达时只有一个生效；IM 服务端结束的每通电话，宿主的 `settle` 只被调一次。

### 计时器

通话的计时都在服务端，客户端不需要自己判断超时。

| 计时 | 默认 | 从哪里来 | 到时怎样 |
| --- | --- | --- | --- |
| 振铃超时 | 45 秒 | 运行配置 `callRingTimeoutSeconds`，宿主经 `ConfigSource.runtimeConfig` 下发 | 双方收到 `call_ended`，`reason` 为 `no_answer`。邀请那一刻读一次，改配置不影响正在响铃的电话 |
| 断线宽限 | 10 秒 | 环境变量 `IM_CALL_DISCONNECT_GRACE_SEC` | 通话所在的设备断线后等这么久，仍没连回来就给对方推 `call_ended`，`reason` 为 `disconnected` |
| 通话时长上限 | 60 分钟 | 运行配置 `callMaxDurationMinutes` | IM 服务端只读取、不执行；由宿主计时并结束通话，见下文 |

断线宽限是为手机系统在后台把连接断掉、几秒后重连的情况留的：连回来就取消计时，通话照常进行。只看通话所在的那台设备，同一账号的其他设备上下线不影响通话。

### 服务端调宿主的超时

IM 服务端调宿主的通话端点都是同步的，超时后按失败处理。

| 端点 | 超时 | 失败时 |
| --- | --- | --- |
| `calls/create` | 3 秒 | 主叫得到 500，通话不成立，被叫不会响铃 |
| `calls/answer` | 8 秒 | 双方收到 `call_ended`，`reason` 为 `failed` |
| `calls/upgrade` | 3 秒 | 同意升级的一方得到 500，通话保持语音 |
| `calls/settle` | 3 秒 | 只打日志，不重试 |

## 宿主要做的事

宿主实现 `CallHandlers` 的四个方法，钱和规则都在这里。Node 宿主 SDK 的定义：

```ts
export interface CallHandlers {
  create(req: { callSessionId: string; callerAppUserId: string; calleeAppUserId: string; callType: "voice" | "video" }): Promise<{ ok: boolean; reason?: string }>;
  answer(req: { callSessionId: string; effectiveType: "voice" | "video" }): Promise<{
    ok: boolean;
    reason?: string;
    channelId?: string;
    callerToken?: string;
    calleeToken?: string;
    videoQuality?: string;
    denoiseEnabled?: boolean;
    appId?: string;
  }>;
  settle(req: { callSessionId: string; reason: string }): Promise<void>;
  upgrade(req: { callSessionId: string; toType: "video" }): Promise<{ ok: boolean; reason?: string; videoQuality?: string }>;
}
```

| 方法 | 什么时候调 | 宿主做什么 |
| --- | --- | --- |
| `create` | 发起时 | 决定能不能打（是不是好友、余额够不够）；拒绝时给 `reason` |
| `answer` | 被叫接听时；被叫选“以语音接听”视频邀请时 `effectiveType` 是 `voice` | 开频道，给主叫、被叫各签一个令牌 |
| `settle` | 通话结束时；`reason` 是 `user_hangup`、`no_answer`、`rejected`、`cancelled`、`disconnected` 或 `failed` | 在这里计费 |
| `upgrade` | 双方同意语音转视频时 | 确认升级，给出 `videoQuality` |

- **令牌各归各的**：`callerToken` 与 `calleeToken` 必须不同，每个只编码自己那一方，互换会让一方以另一方的身份入会。IM 服务端只把各自那份交给各自，客户端**禁止**把自己的令牌发给对方。
- **宿主提前结束通话**：余额不足、到了时长上限时，宿主用 `ImClient.callBroadcast(targetUid, payload)` 给双方各推一个完整的 `call_ended` 帧（`reason` 如 `credit_exhausted`、`max_duration_reached`）。帧原样发给该用户的每台设备；类型是 `call_ended` 时 IM 服务端同时释放这通电话，不再另推、也不再调 `settle`。用户没有在线设备时返回 `false`，不排队。
- **通话记录**：用 `recordingCalls` 包住自己的处理器，后台的通话记录页才有数据，见 [后台页面](/modules/admin#通话记录)。

客户端**必须**容忍不认识的结束原因：宿主可以用自己的原因结束通话。

## RTC 适配器的边界

客户端的通话模块不知道任何 RTC SDK，媒体都交给一个 RTC 适配器：实现 `CallMediaEngine` 接口的对象。核心与通话模块的依赖树里没有 RTC SDK，`tools/depcheck` 与 CI 的 Android 依赖图检查都看这一点。

适配器拿到的入会参数是 `MediaJoin`：本人的 IM uid、通话类型、服务端交给本方的凭证（`channelId`、`rtcToken`、`appId`、`videoQuality`、`denoiseEnabled`）。两端的接口：

| 方法 | Web | Android | 作用 |
| --- | --- | --- | --- |
| `join` | 有 | 有 | 入会；入不了时抛错，通话管理器随即结束通话 |
| `setVideo` | 有 | 有 | 开关摄像头；通话升级为视频时打开 |
| `setMuted` | 有 | 有 | 静音 |
| `leave` | 有 | 有 | 离开；任何时候都要能调，`join` 失败或没完成时也一样，Android 上还要关掉摄像头和麦克风 |
| `attachLocalVideo`、`attachRemoteVideo` | 可选 | — | 把本方、对方画面挂到一个元素上 |
| `setSpeakerOn`、`switchCamera` | — | 可选 | 扬声器与听筒、前后摄像头 |
| `remoteVideo`、`createLocalVideoView`、`createRemoteVideoView` | — | 可选 | 对方画面可用时给出 RTC 用户 id；返回的视图已经绑定好，界面拿到后才放进布局 |
| `failures` | — | 可选 | 通话中途媒体彻底断了，通话管理器随即结束通话 |

两端都自带 `NoMediaEngine`：只有信令、不入会，给测试和演示用。示例 App 与网页示例用的就是它，所以示例里的通话没有声音和画面。

## Web：`@talkwire/call`

`@talkwire/call` 是信令与状态，`@talkwire/call/react` 是界面。依赖 `@talkwire/client`，React 与 `@talkwire/react` 是可选的对等依赖。

- `CallSignaling`：一个协议动作一个方法。
- `CallManager`：本标签页的通话状态（空闲、呼出、来电、通话中、已结束），驱动一个 `CallMediaEngine`；早到的推送保留 10 秒，等服务端给出通话号。
- `/react`：`CallProvider`（`media`、`channel`、`strings`、`ringer`、`log`）、标签页内的来电与通话界面 `CallOverlay`、单聊页头的 `CallButtons`。文案跟随 `TalkwireProvider` 的 `locale`，有英文、简体中文、越南文。
- `ToneRinger`：用 Web Audio 合成来电铃声与回铃音，不带音频文件。`CallProvider` 默认启用，`ringer={null}` 关掉。浏览器在页面被点过、按过键之前不让出声，一个没碰过的标签页来电时只有界面没有声音。

同一账号开着多个标签页时：

- 来电在所有标签页响铃，在哪个标签页接听或拒绝，其他标签页就停止响铃。
- 一个标签页在通话中时，其他标签页不响铃也不能发起。在通话中的标签页每 30 秒告知一次，关掉的标签页 90 秒后不再算数。

`manager.trace()` 保留最近 100 条记录：收到哪些推送、来电为什么被丢弃、标签页之间的消息、状态变化。记录里只有通话 id 与阶段，没有姓名和凭据；`CallProvider` 的 `log` 可以把每条接到调试日志。React 严格模式下 `CallProvider` 用 `autoStart: false` 建 `CallManager`，只在效果里 `start()`，被丢弃的那个实例从不监听。

## Android：`talkwire-call`

`talkwire-call` 是 Compose 模块，依赖 `talkwire-ui`。它的界面经 `ImUiHost.topBarExtras` 放进单聊页头，SDK 核心不依赖它。

- `CallManager(client, me, media, scope)`：通话状态机，驱动 `CallMediaEngine`。
- `CallSystem(context, calls, notifications, scope, incomingNotice, ongoingNotice)`：在 `calls.start()` 之后启动，负责来电全屏通知、来电铃声与回铃音、通话中的前台服务。
- `CallScreen(calls)`：盖在聊天界面之上的通话界面；`CallButtons(calls, peer, onRefused)`：单聊页头的语音、视频按钮。
- `SystemCallRinger`：`CallSystem` 的默认铃声。来电时循环播放手机的铃声并振动，跟随情景模式；呼出时放回铃音（425 Hz，响 1 秒停 4 秒）。`ringer = null` 关掉，也可以传自己的 `CallRinger`。

模块带上了原项目踩过的坑的修法：

- 前台服务被系统重建时自己停掉，不自动重启：通话状态在内存里，进程死了通话就没了，留着只会剩一条清不掉的通知。
- 前台服务的类型按启动那一刻已授予的权限选：Android 14 拒绝缺权限的类型，而视频来电振铃时还没有摄像头权限。
- 每条结束路径都撤掉来电通知。

App 要做的：清单里加 `VIBRATE`（振动）与 `USE_FULL_SCREEN_INTENT`；来电要在锁屏上亮屏显示时，响铃和通话期间对 Activity 调 `setShowWhenLocked(true)`、`setTurnScreenOn(true)`。

## 示例：阿里云 ARTC 3.0

[`examples/artc`](https://github.com/dishikun2786-prog/Talkwire/blob/main/examples/artc/README.md) 给出接阿里云 ARTC 3.0（DingRTC）要的两半：宿主一侧的 `CallHandlers`，以及各端的 RTC 适配器。两半都不进核心。

### 宿主一侧

`artcCalls()` 实现 `CallHandlers`：每通电话开一个频道，接通时给主叫、被叫各签一个 ARTC 3.0 AppToken。谁能打、怎么计费仍由宿主决定。

```ts
import { talkwireHostPlugin, recordingCalls } from "@talkwire/host-node";
import { artcCalls, friendsOnly } from "./calls.js";

const calls = artcCalls({
  appId: process.env.ARTC_APP_ID!,
  appKey: process.env.ARTC_APP_KEY!,     // 只在宿主，不下发
  canCall: friendsOnly(contacts),        // 默认谁都能打
  videoQuality: () => config.videoQuality, // "sd" | "hd"
  onSettle: (call, reason) => bill(call, reason), // 每通电话恰好一次
});
await app.register(talkwireHostPlugin, { ..., calls: recordingCalls(calls, callRecords) });
```

| 项目 | 做法 |
| --- | --- |
| 频道 | 每通电话一个，`talkwire-` 加 24 位随机十六进制 |
| 令牌 | 主叫、被叫各一个，签给各自的 RTC 用户 id；最长 24 小时（格式本身的上限），`tokenSeconds` 可以设得更短 |
| RTC 用户 id | 默认是用户的 IM uid，也是客户端适配器默认的入会身份，两边必须一致。要用宿主自己的用户 id 时两边都改：这里传 `rtcUserId`，适配器传 `userId` |
| 进行中的通话 | 示例放在内存里。多进程部署时要换成共享存储，或保证同一通电话的四个回调落在同一个进程 |

令牌生成从参考部署移植，用阿里云官方 Node.js 示例的两组黄金向量逐字节核对。测试不连阿里云：

```bash
cd host-sdk-node && npm install && npm run build
cd ../examples/artc && npm install
npm run typecheck
npm test
```

### 客户端一侧

::: code-group

```tsx [Web]
import DingRTC from "dingrtc";
import { DingRtcMediaEngine } from "@talkwire/call-dingrtc";
import { CallOverlay, CallProvider } from "@talkwire/call/react";

const media = new DingRtcMediaEngine(DingRTC);
<CallProvider media={media}>
  <CallOverlay />
</CallProvider>
```

```kotlin [Android]
val media = DingRtcMediaEngine(context)          // io.talkwire.call.dingrtc
val calls = CallManager(client, me, media, scope) // io.talkwire.call
calls.start()
// 界面：CallScreen(calls)、CallButtons(calls, peerUid, onRefused)
```

:::

| | Web：`@talkwire/call-dingrtc` | Android：`talkwire-call-dingrtc` |
| --- | --- | --- |
| SDK | npm `dingrtc`，对等依赖 `^3.9.37`，由应用安装后传入 | `com.ding.rtc:dingrtc-full:3.0.0`，在阿里云 Maven 仓库；`android/settings.gradle.kts` 用 `exclusiveContent` 把该仓库限定为 `com.ding.rtc` 一个组，应用照此配置 |
| 入会 | 先监听再入会，频道里已经在推流的人也会被订阅；入会途中挂断时，之后建出的麦克风和摄像头立即关掉 | 入会后显式发布麦克风（3.0 入会不自动发布） |
| 画面 | 音频订阅一次混流，视频按人订阅；`sd` 为 640×480，`hd` 为 1280×720，15 帧 | 视频按 `videoQuality` 设编码尺寸；远端画面等 SDK 报告对方视频可用后才创建、绑定好再放进布局 |
| 其他 | 只依赖 SDK 的少数接口（`createClient`、`createMicrophoneAudioTrack`、`createCameraVideoTrack`、`join`、`publish`、`subscribe` 与两个事件），换 SDK 版本时先对这些 | 渲染视图用 `SafeRtcSurfaceView`：SDK 自带的 `RTCSurfaceView` 在绑定前被测量会空指针崩溃。3.0 没有音频降噪接口，`denoiseEnabled` 不起作用。SDK 调用都收在 `RealDingRtc` 里 |

## 验证状态

通话的信令有自动化测试，真实的音视频从没验证过。

- **服务端**：黄金对话 `module/call`、`module/call-timeout`；红线 R16 的三个单测覆盖并发下的状态替换、断线宽限与计时器。规范注明断线宽限期与宿主主动结束两种情况没有录成黄金对话。
- **Web**：单测、组件测试与黄金对话回放；端到端用例 `web/e2e/call.spec.ts` 走一遍呼叫、接听、切视频、挂断，再拒接一通，媒体是 `NoMediaEngine`。`@talkwire/call-dingrtc` 只用模拟的 SDK 测过，没有接真实的 ARTC 服务打过一通有声音画面的电话。
- **Android**：单测、界面测试与 `CallReplayTest` 回放；`talkwire-call-dingrtc` 只用 SDK 替身做过单测。[`android/DEVICE_CHECKS.md`](https://github.com/dishikun2786-prog/Talkwire/blob/main/android/DEVICE_CHECKS.md) 记了 2026-10-26 在一台小米（Android 15）上与网页示例互通语音通话的信令与铃声（媒体仍是 `NoMediaEngine`）；视频、锁屏时的全屏来电、真实 RTC 媒体与 ARTC 适配器都没有在真机上验证。
- **`examples/artc`**：令牌对黄金向量，处理器对令牌里写的频道、用户、过期时间和签名；入会、出声、出画、升级视频、弱网都没有验证，接入时需要用真实的 appId 和 appKey 在两台设备上跑一遍。
