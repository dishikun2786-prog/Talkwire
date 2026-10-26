---
title: "视频压缩"
description: "Android 可选模块 talkwire-video-compress 在上传前把大于 50 MB 的视频重新编码，压缩失败或没变小时退回发原片。"
---

# 视频压缩

Android 的可选模块 `talkwire-video-compress` 在上传前把大视频重新编码成 MP4（H.264 视频加 AAC 音频）。它单独成模块，是为了不发大视频的 App 不必带上编码器：Media3 Transformer 会带进 ExoPlayer、特效管线和封装器。Web 端没有对应的包。

## 怎么启用

启用分两步：依赖这个模块，再把压缩器交给 `MediaSender`。示例 App 两步都做了：

```kotlin
implementation(project(":talkwire-video-compress"))
```

```kotlin
MediaSender(app, engine, MediaUploader(SampleConfig.HOST_URL, tokens, http), videoCompressor = Media3VideoCompressor(app)),
```

第一行摘自 `android/sample-app/build.gradle.kts`，第二行摘自 `android/sample-app/src/main/kotlin/io/talkwire/sample/ChatSession.kt`。模块依赖 `talkwire-android` 与 `androidx.media3:media3-transformer:1.4.1`，与 `talkwire-android` 用同一版 Media3（Media3 的各个产物版本必须一致）。

不给 `videoCompressor` 时 `MediaSender` 不压缩任何视频，超过上限的直接拒绝。`VideoCompressor` 是 `talkwire-android` 里的接口，也可以换成自己的实现。

## 什么时候压缩

压缩由 `MediaSender` 决定，压缩器只负责编码。相关参数：

| 参数 | 默认 | 作用 |
| --- | --- | --- |
| `maxBytes` | `50L * 1024 * 1024`（50 MB） | 能发的最大文件；Node 宿主 SDK 的媒体上传默认上限也是 50 MB |
| `videoCompressor` | `null` | 压缩器；不给就不压缩 |
| `compressVideosAboveBytes` | `50L * 1024 * 1024`（50 MB） | 大于它的视频先重新编码；参考部署对 50 MB 及以上的视频就是这样做的 |

选中一个视频后：

1. 门槛取 `compressVideosAboveBytes` 与 `maxBytes` 中较小的一个。不大于门槛的视频原样上传。
2. 系统选择器报不出文件大小时，先把视频读进临时文件，按实际大小判断。
3. 超过门槛的交给压缩器，输出文件名改为 `.mp4`、类型为 `video/mp4`。
4. 压缩成功、结果非空、比原片小、并且不超过 `maxBytes`：上传压缩结果。
5. 否则（压缩失败、没变小、仍然超限）：原片不超过 `maxBytes` 就发原片，超过就返回 `Result.TooLarge`。

首帧缩略图总是从原片取，取不到或上传失败都不影响发送。临时文件放在 App 的缓存目录里，用完即删。

## `Media3VideoCompressor`

`Media3VideoCompressor(context, videoBitrate)` 用 Media3 Transformer 重新编码。

- **分辨率不变**。`videoBitrate` 为 `null`（默认）时由编码器自己选码率，与参考部署相同；给定码率时结果的大小可以预估：视频部分约为 `码率 × 秒数 / 8` 字节。
- **不占主线程**：每次导出在自己的线程上跑，Transformer 只在建它的那个线程上操作。
- **失败与取消**：编码失败返回 `false`，并删掉没写完的输出；调用方取消时停下 Transformer，同样不留半截文件。

## 验证状态

压缩只在 JVM 上测过发送逻辑，真正的编码没有在真机上跑过。

- `MediaSender` 的各个分支（压缩、退回原片、拒绝）有单测。
- `Media3VideoCompressor` 只测了“读不了的文件返回 `false` 且不留文件”，真正的编码需要设备。
- 旋转（竖拍）视频编码后方向是否正确、首帧缩略图是否按旋转角调整宽高，都没拿真机拍的视频验证过。
