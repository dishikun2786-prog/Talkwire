# depcheck

依赖方向与许可证检查（计划 9.2），不依赖任何第三方包：

```bash
node tools/depcheck/check.mjs            # 在仓库根目录或任何地方运行
node tools/depcheck/check.mjs <目录>     # 检查另一份检出
```

检查三件事，任何一条不满足就列出来并以 1 退出：

1. **核心不链接 libsignal 和 RTC SDK**：Go 服务端（`go.mod`、`go.sum` 与 import）、Node 宿主 SDK（import 与全部运行时依赖）、Web 核心包（`protocol`、`client`、`storage-idb`、`react`）、Android 核心模块（`imkit-core`、`imkit-android`、`imkit-storage-room`、`imkit-ui`、`imkit-push-fcm`），既看声明的依赖，也看源码里的 import。
2. **依赖只往下走**：Web 核心分层（`protocol` 不依赖任何 `@imkit` 包；`client` 只依赖 `protocol`；`storage-idb`、`react` 只依赖前两个）；模块包（`call`、`group-join`、`admin`）只依赖核心，不互相依赖；RTC 适配器包（`call-dingrtc`）只依赖核心与它适配的模块，RTC SDK 只能是对等依赖（由应用安装）；源码 import 的 `@imkit` 包必须在 `package.json` 里声明。Android 核心模块只依赖核心模块。`web/packages` 下新增的包必须在脚本里归类，否则报错。
3. **许可证**：我们自己的包是 Apache-2.0；Node 宿主 SDK 的运行时依赖（一直往下）都是宽松许可证（MIT、ISC、Apache-2.0、BSD、0BSD 等；`A OR B` 有一个宽松即可）。

看不到的：Android 解析后的传递依赖与 Go、Android 依赖的许可证。CI 要再跑 `./gradlew :imkit-ui:dependencies` 检查解析结果。

做过变异检查：在一份注入了违规的目录树上（核心依赖 libsignal、核心反向依赖模块、模块互相依赖、没声明的 import、MIT 许可证、未归类的包、深层的 AGPL 依赖、`go.mod` 与 Go import、Gradle 依赖与 Kotlin import）逐条报出。

E2EE 客户端模块（AGPLv3）加进来时，把它归为模块并放宽它自己的许可证检查；核心对它的依赖仍然会报错。
