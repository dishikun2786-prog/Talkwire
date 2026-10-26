# depcheck

依赖方向与许可证检查（计划 9.2），不依赖任何第三方包：

```bash
node tools/depcheck/check.mjs            # 在仓库根目录或任何地方运行
node tools/depcheck/check.mjs <目录>     # 检查另一份检出
```

检查三件事，任何一条不满足就列出来并以 1 退出：

1. **核心不链接 libsignal 和 RTC SDK**：Go 服务端（`go.mod`、`go.sum` 与 import）、Node 宿主 SDK（import 与全部运行时依赖）、Web 核心包（`protocol`、`client`、`storage-idb`、`react`）、Android 核心模块（`talkwire-core`、`talkwire-android`、`talkwire-storage-room`、`talkwire-ui`、`talkwire-push-fcm`），既看声明的依赖，也看源码里的 import。
2. **依赖只往下走**：Web 核心分层（`protocol` 不依赖任何 `@talkwire` 包；`client` 只依赖 `protocol`；`storage-idb`、`react` 只依赖前两个）；模块包（`call`、`group-join`、`admin`）只依赖核心，不互相依赖；RTC 适配器包（`call-dingrtc`）只依赖核心与它适配的模块，RTC SDK 只能是对等依赖（由应用安装）；源码 import 的 `@talkwire` 包必须在 `package.json` 里声明。Android 核心模块只依赖核心模块；Android 模块（`talkwire-group-join`、`talkwire-call`、`talkwire-video-compress`）只依赖核心，RTC 适配器（`talkwire-call-dingrtc`）只依赖核心与 `talkwire-call`，SDK 只能在它里面出现。`web/packages` 与 `android/` 下新增的包或模块必须在脚本里归类，否则报错。
3. **许可证**：我们自己的包是 Apache-2.0，唯一的例外是 E2EE 客户端模块 `android/talkwire-e2ee`：它可以依赖 libsignal，目录里的 `LICENSE` 必须是 AGPLv3 全文，每个 Kotlin/Java 源文件都要带 `SPDX-License-Identifier: AGPL-3.0-only`；其他模块的源文件都不许带这一行，也不许依赖它；Node 宿主 SDK 的运行时依赖（一直往下）都是宽松许可证（MIT、ISC、Apache-2.0、BSD、0BSD 等；`A OR B` 有一个宽松即可）。

看不到的：Android 解析后的传递依赖与 Go、Android 依赖的许可证。`tools/ci.sh` 的 android 作业另外检查解析结果：核心与 Apache-2.0 模块的依赖图里没有 libsignal 与 RTC SDK，E2EE 模块解析出的 libsignal 仍是钉死的 0.73.0。

做过变异检查：在一份注入了违规的目录树上（核心依赖 libsignal、核心反向依赖模块、模块互相依赖、没声明的 import、MIT 许可证、未归类的包、深层的 AGPL 依赖、`go.mod` 与 Go import、Gradle 依赖与 Kotlin import）逐条报出。

加入 E2EE 模块后又做了一次变异检查：核心依赖 libsignal、E2EE 源文件缺 SPDX 行、核心源文件带 AGPL 的 SPDX 行、`LICENSE` 不是 AGPLv3、核心依赖 E2EE 模块，五处都报出。
