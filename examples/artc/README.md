# 示例：用阿里云 ARTC 3.0（DingRTC）打通话

通话模块（协议第 16 节）只管信令：谁打给谁、接没接、何时挂断。媒体走宿主自己的 RTC 服务，IM 服务端不碰音视频。本示例给出接阿里云 ARTC 3.0（DingRTC）要的两半：

- **宿主一侧**（本目录，Node）：`artcCalls()` 实现 `@imkit/host-node` 的 `CallHandlers`。每通电话开一个频道，接通时给主叫、被叫各签一个令牌；谁能打给谁、通话怎么计费仍由宿主决定。
- **客户端一侧**：各端的 RTC 适配器，把通话模块的媒体接口落到 DingRTC SDK 上。Web 版是 `web/packages/call-dingrtc`。

两半都不进核心：核心与通话模块的依赖树里没有 DingRTC，`tools/depcheck` 会检查这一点。适配器只把 SDK 写成 peer 依赖，由应用自己安装。

## 宿主一侧

```ts
import { imkitHostPlugin, recordingCalls } from "@imkit/host-node";
import { artcCalls, friendsOnly } from "./calls.js";

const calls = artcCalls({
  appId: process.env.ARTC_APP_ID!,
  appKey: process.env.ARTC_APP_KEY!,     // 只在宿主，不下发
  canCall: friendsOnly(contacts),        // 默认谁都能打
  videoQuality: () => config.videoQuality, // "sd" | "hd"
  onSettle: (call, reason) => bill(call, reason), // 每通电话恰好一次
});
await app.register(imkitHostPlugin, { ..., calls: recordingCalls(calls, callRecords) });
```

- **频道**：每通电话一个，`imkit-` 加 24 位随机十六进制。
- **令牌**：主叫、被叫各一个，签给各自的 RTC 用户 id。IM 服务端只把各自那份交给各自，客户端不转发给对方。令牌最长 24 小时（格式本身的上限），`tokenSeconds` 可以设得更短。
- **RTC 用户 id**：默认是用户的 IM uid（`imUid(用户id)`），这也是客户端适配器默认的入会身份，两边必须一致。宿主想用自己的用户 id 时，两边都要改：这里传 `rtcUserId`，客户端适配器传 `userId`。
- **通话记录**：用 `recordingCalls` 包一层，后台的通话记录页才有数据。记录里只有元数据，没有频道号和令牌。
- **示例的取舍**：`artcCalls` 把进行中的通话放在内存里。多进程部署时，要么把这张表换成共享存储，要么保证同一通电话的四个回调落在同一个进程。

### 令牌格式

`src/app-token.ts` 生成 ARTC 3.0 的 AppToken（版本 `000`）。它从参考部署移植过来，在那里与阿里云官方 Node.js 示例（github.com/aliyun/AliRTCSample，`Server/nodejs`）逐字节比对过；`test/app-token.test.ts` 里的两组黄金向量就是那份示例的输出。几处容易写错、写错了也不报错的地方：

- 外层两个缓冲区补零到容量（从 256 起翻倍），补的零也参与签名和压缩；内层的 service 和 options 不补零。
- options 默认是“有、零条”（`bool(true) + int32(0)`），不是“没有”。
- 签名密钥是两级 HMAC：先用签发时间作键对 appKey 做 HMAC，再用盐作键对结果做 HMAC。
- 压缩是 zlib，不是 gzip。

`readAppToken` 把令牌解开，供测试和排查用，不校验签名。

## 客户端一侧

| 端 | 适配器 | SDK | 状态 |
| --- | --- | --- | --- |
| Web | `@imkit/call-dingrtc`（`web/packages/call-dingrtc`） | npm `dingrtc`，按 3.9.37 编写 | 可用，有单元测试（假 SDK） |
| Android | `imkit-call-dingrtc`（`android/imkit-call-dingrtc`），依赖 `com.ding.rtc:dingrtc-full` | 3.0.0 | 可用，有单元测试（SDK 替身）；渲染视图沿用参考部署在真机上验证过的写法 |
| iOS | CocoaPods `DingRTC_iOS` | — | 待 iOS 端开工（需要 Mac） |

Web：

```tsx
import DingRTC from "dingrtc";
import { DingRtcMediaEngine } from "@imkit/call-dingrtc";
import { CallOverlay, CallProvider } from "@imkit/call/react";

const media = new DingRtcMediaEngine(DingRTC);
<CallProvider media={media}>
  <CallOverlay />
</CallProvider>
```

- 接通时用宿主给的频道、appId 和自己那份令牌入会；先监听再入会，频道里已经在推流的人也会被订阅。
- 音频订阅一次混流（`mcu`）；视频按人订阅。清晰度按宿主给的 `videoQuality`：`sd` 为 640×480，`hd` 为 1280×720，15 帧。
- 入会途中挂断：之后建出来的麦克风和摄像头会立即关掉，不会留着亮。
- 适配器只依赖 SDK 的这几处接口（`createClient`、`createMicrophoneAudioTrack`、`createCameraVideoTrack`、客户端的 `join`、`publish`、`subscribe` 与两个事件），见 `src/index.ts` 的类型；换 SDK 版本时先对这些。

Android：

```kotlin
val media = DingRtcMediaEngine(context)          // io.imkit.call.dingrtc
val calls = CallManager(client, me, media, scope) // io.imkit.call
calls.start()
// 界面：CallScreen(calls)、CallButtons(calls, peerUid, onRefused)
```

- SDK 在阿里云 Maven 仓库（`https://maven.aliyun.com/repository/public`）。`android/settings.gradle.kts` 用 `exclusiveContent` 把这个仓库限定为 `com.ding.rtc` 一个组，应用自己的工程照此配置。
- 入会后显式发布麦克风（3.0 入会不自动发布）；视频按宿主的 `videoQuality` 设编码尺寸，本地预览在通话界面要本地画面时才“先绑定、再启动”，否则先启动的预览接不上画面。
- 渲染视图用 `SafeRtcSurfaceView`：SDK 自带的 `RTCSurfaceView` 在绑定前被测量会空指针崩溃（参考部署里“视频通话接听即闪退”）。远端画面等 SDK 报告对方视频可用后才创建、绑定好再放进布局。
- 3.0 没有音频降噪接口，`denoiseEnabled` 在 Android 上不起作用。
- SDK 的调用都收在 `RealDingRtc` 里，引擎逻辑对着 `DingRtc` 接口写，单元测试用替身；换 SDK 版本时先核对 `RealDingRtc`。

## 运行

示例通过 `file:` 依赖引用 `host-sdk-node` 的构建产物，先构建 SDK：

```bash
cd host-sdk-node && npm install && npm run build
cd ../examples/artc && npm install
npm run typecheck
npm test
```

测试不连阿里云：令牌对黄金向量，处理器对令牌里写的频道、用户、过期时间和签名。真机上的音视频（入会、出声、出画、升级视频、弱网）没有验证过，接入时需要用真实的 appId 和 appKey 在两台设备上跑一遍。
