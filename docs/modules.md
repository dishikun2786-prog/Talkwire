# 可选模块手册

ImKit 的核心是单聊、群聊、媒体消息、已读、撤回、在线状态与离线推送。其余能力都是可选模块：服务端按配置装，宿主按需实现对应的接口，客户端按需装对应的包。每个模块都能单独装上、单独拿掉，核心产物的依赖树里没有 libsignal 和任何 RTC SDK。

## 怎么装、怎么拿掉

| 位置 | 装上 | 拿掉 |
| --- | --- | --- |
| 服务端 | `IMKIT_MODULES` 列出模块名（`call`、`groupjoin`、`groupai`、`e2ee`）；不设时装宿主支持的全部模块 | 从列表里去掉，或 `IMKIT_MODULES=none`。没装的模块的动作得到 501 `module not enabled: <名>` |
| 宿主（Node SDK） | 给 `imkitHostPlugin` 对应的选项（`calls`、`groupAi`、`config`） | 不给；对应端点答 404 |
| 客户端 | 装对应的包（Web 是 `@imkit/<模块>`） | 不装；核心包不引用模块包 |

- 显式列出的模块，宿主缺对应能力时服务端启动即报错；不设 `IMKIT_MODULES` 时缺能力的模块跳过并打一行日志。standalone 宿主（`IMKIT_HOST_MODE=standalone`）只支持 `groupjoin` 与 `e2ee`。
- 模块有自己的迁移脚本（`server/migrations/<模块>/`），装上时建表；拿掉模块不删表，数据留着，再装上时接着用。
- 核心客户端对模块的推送帧只转交、不处理，没装模块包时这些帧没有人接；遇到不认识的消息类型时显示 `content` 里的兜底文字。

## 音视频通话（`call`）

信令走 IM，媒体走宿主自己的 RTC 服务。谁能打给谁、频道和凭证、计费、什么时候强制结束，都由宿主决定。

| 位置 | 内容 |
| --- | --- |
| 协议 | 第 16 节：`call_invite`、`call_accept`、`call_reject`、`call_cancel`、`call_hangup`、`call_upgrade*`；推送 `call_ring`、`call_answered`、`call_ended` 等 |
| 宿主 | `CallHandlers`：`create`（能不能打）、`answer`（频道与双方各自的令牌）、`settle`（结束原因，在这里计费）、`upgrade`（语音转视频）。宿主要提前结束通话（余额不足、到了时长上限）时用 `ImClient.callBroadcast` 给双方各推一个 `call_ended`，IM 收到后释放这通通话。`recordingCalls` 把通话记成后台能查的记录 |
| Web | `@imkit/call`：`CallManager` 驱动 RTC 适配器接口 `CallMediaEngine`；`/react` 有来电与通话界面、单聊页头的通话按钮 |
| Android | 待做（7.3）：通话引擎接口与界面；原项目踩过的坑（未绑定的渲染视图不能被测量、前台服务不能自动重启、先发全屏通知再开麦克风服务、每条结束路径都撤掉来电通知）要原样带上 |
| iOS | 待做（Phase 6） |
| RTC 适配 | 待做（7.4）：阿里云 ARTC 的三端适配器放在 `examples/`，Android 另发一个只按 Maven 坐标依赖 dingrtc 的可选 AAR |

要点：每一方只拿到自己的 RTC 令牌，IM 不会把一方的令牌交给另一方，客户端也不转发；被叫可能先于主叫拿到“对方已接听”以外的推送，客户端要缓存早到的帧（协议 F49）；一方断线后等 `IM_CALL_DISCONNECT_GRACE_SEC`（默认 10 秒）再结束通话。

## 扫码入群（`groupjoin`）

群二维码令牌、凭二维码入群、入群审核。只在 IM 里完成，宿主不需要实现任何接口。

| 位置 | 内容 |
| --- | --- |
| 协议 | 第 14 节：`group_qr_token`、`group_qr_resolve`、`group_join`、`group_join_requests_list`、`group_join_review`、`get_group_join_settings`、`update_group_join_settings` |
| Web | `@imkit/group-join`：`GroupJoinClient` 一个动作一个方法；`/react` 有群二维码卡片、入群页、入群申请列表、审核开关 |
| Android | 待做（7.6） |

要点：二维码里放什么由应用决定（通常是自己的入群页链接，令牌在链接里），套件不带二维码编码器。legacy 档的服务端不给入群者订阅群（D7），客户端入群后要自己订阅并刷新群列表；审核失败用 strict 档的 `errCode` 区分“群已满”和“已被处理”，不解析 `reason`。

## 群里的机器人（`groupai`）

群成员“@助手”提问时，IM 把问题交给宿主，宿主决定回答什么、怎么计费，IM 以助手的身份把回答发进群。

| 位置 | 内容 |
| --- | --- |
| 宿主 | `GroupAiHandler`（`POST .../group-ai-qa`）：答 `{ ok: true, answer }`；答不了时 `{ ok: false, reason, message? }`，`message` 是给群里看的说法 |
| 运行配置 | `groupAi`：助手的名字（“@”后面那个，默认“小桃子”）、各种兜底文案；宿主经 `ConfigSource.runtimeConfig` 下发 |
| 客户端 | 收到 `ai_typing` 时显示助手“正在输入”（Web 已做，最长 15 秒，回复到达即消失；Android 待做） |

要点：宿主 10 秒内不答，IM 发兜底文案。每个群有自己的开关、每日次数与冷却间隔，由群主设置。

## 端到端加密（`e2ee`）

服务端只存转不透明的密文，不链接 libsignal，仍是 Apache-2.0。客户端模块链接 libsignal，单独发布，许可证 AGPLv3。

| 位置 | 内容 |
| --- | --- |
| 服务端 | 密钥包的发布与领取、密文的存转；密钥包只属于发布它的设备 |
| 宿主 | `ConfigSource.e2eeConfig`（`enabled`、`groupEnabled`、密文保留时长）；不设时答 404，服务端用默认值 |
| 客户端 | 待做（7.1）：Android 模块钉死 libsignal 版本，失败分类按红线 C10 |

要点：v1 不做 E2EE 多设备；客户端开始带 `device` 登录后要重新发布密钥包。

## 不是模块、但宿主常用的扩展点

- **自定义消息与卡片**：业务卡片（红包、转账、订单）用宿主自己的 `contentType` 与 `extra`，客户端注册对应的消息类型；卡片状态变化时宿主调 `ImClient.customBroadcast` 推 `custom_update`。例子：`examples/red-packet/`。
- **后台**：宿主注册 `imkitAdminRoutes`（聊天记录、清空单聊；给了 `calls` 时还有通话记录），前端嵌 `@imkit/admin` 的页面。清空要超级管理员、确认语、理由与幂等键，审计只记元数据。
- **推送**：FCM、APNs、Web Push 都在核心里，配置了才启用（见 `server/README.md` 的“配置”）。

## 验证

- 服务端：`modules_test.go` 覆盖未知模块名、宿主缺能力、模块动作互不重叠、未装模块的动作答 501。
- 宿主：`host-sdk-node` 的宿主插件测试覆盖通话与群机器人端点，不给选项时答 404；契约测试覆盖 `host.openapi.yaml` 的每个操作。
- Web：每个模块包有自己的单测、组件测试与黄金对话回放；端到端用例在示例里走一遍扫码入群、通话与后台。
