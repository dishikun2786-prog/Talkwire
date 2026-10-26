---
title: "群助手钩子"
description: "群成员“@助手”提问时，IM 服务端把问题交给宿主，宿主决定回答与计费，IM 以助手身份把回答或兜底文案发进群。"
---

# 群助手钩子

群成员“@助手”提问时，IM 服务端把问题交给宿主，宿主决定回答什么、怎么计费，IM 以助手的身份把回答发进群。这是 `groupai` 模块：原版写死在核心里的群 AI 问答改成了这个通用钩子，助手的名字与兜底文案进了运行配置，默认值与原版逐字相同（ADR-0003）。线协议在规范第 15 节，见 [规范全文](/protocol/spec)。

## 装上与拿掉

钩子的逻辑在服务端，回答在宿主，客户端不用装任何包。

| 位置 | 装上 | 拿掉 |
| --- | --- | --- |
| 服务端 | `TALKWIRE_MODULES` 列出 `groupai`；不设这个变量时装宿主支持的全部模块 | 从列表里去掉；群助手的设置动作得到 501 `module not enabled: groupai` |
| 宿主（Node SDK） | 给 `talkwireHostPlugin` 的 `groupAi` 选项 | 不给；`group-ai-qa` 端点答 404 |
| 客户端 | 核心包已处理 `ai_typing`（Web 的 `@talkwire/client`、Android 的 `talkwire-core`） | — |

standalone 宿主（`TALKWIRE_HOST_MODE=standalone`）不支持这个模块。

## 一次提问怎么走

提问本身是一条普通的群消息，钩子在它发出之后才开始工作。

1. 群消息照常发出：发送者收到 `pub` 应答，其他成员收到 `data`。
2. 应答之后，服务端检查正文里有没有提到助手（见下文“触发词”）。只有经 `pub` 发出的群消息会被检查；转发与助手自己的回答不经过这一步，不会再次触发。
3. 读这个群的设置。群主没打开 `aiQaEnabled` 的群到此为止。
4. 冷却：同一个群在 `aiCooldownSec`（默认 5 秒）内的后续提问被静默忽略，没有“正在输入”，也没有回答。
5. 群里所有在线成员（含提问者）收到 `{"type":"ai_typing","topic":"grp_…"}`。
6. 服务端查出群主的宿主用户 id，`POST .../group-ai-qa` 问宿主，最多等 10 秒。
7. 宿主答了：回答以普通群消息写入，所有在线成员收到 `data`，其中 `from` 为 `assistant_xiaotaozi`、`senderKind` 为 `ai_assistant`、`contentType` 为 `text`；`get` 的历史里也有这条。宿主没答成：服务端以同样的身份发一条兜底文案。

整个问答在一个独立的协程里跑，回答再慢也不会挡住群里其他消息的广播。

## 宿主要做的事

宿主实现一个处理器：拿到问题，回答，计费。Node 宿主 SDK 的定义：

```ts
export interface GroupAiRequest {
  groupTopic: string;
  askerImUid: string;
  question: string;
  groupOwnerAppUserId: string;
  kbIds: string[];
  docIds: string[];
}

/**
 * The group assistant's host side (the bot hook): the host decides the answer
 * and what it costs, and the IM server posts it in the group as the
 * assistant. On a business failure answer ok=false with a reason; `message`,
 * when given, is posted instead of the IM server's configured notice for the
 * reason (runtime config `groupAi`). The IM server waits 10 seconds, then
 * posts the "busy" notice.
 */
export type GroupAiHandler = (req: GroupAiRequest) => Promise<{ ok: boolean; answer?: string; reason?: string; message?: string }>;
```

| 字段 | 含义 |
| --- | --- |
| `groupTopic` | 提问所在的群 |
| `askerImUid` | 提问者的 IM uid |
| `question` | 那条群消息的全文，包括“@助手” |
| `groupOwnerAppUserId` | 群主的宿主用户 id；IM 服务端每次按当前的群主重新查，不信任客户端给的值 |
| `kbIds`、`docIds` | 群设置里的 `aiKbIds`、`aiDocIds`，含义由宿主解释（参考部署用来指定知识库与文档） |

- **回答**：`{ ok: true, answer }`，`answer` 原样作为助手的消息发进群。
- **不回答**：`{ ok: false, reason, message? }`。给了 `message` 时群里看到的就是它；没给时用运行配置里对应 `reason` 的兜底文案。
- **计费**：谁付钱、每天能问几次，都由宿主决定并执行。IM 服务端不检查每日次数，只执行冷却间隔。

## 兜底文案

宿主没给 `message`、超时或出错时，服务端按 `reason` 选一条兜底文案。文案在运行配置的 `groupAi` 里，宿主经 `ConfigSource.runtimeConfig` 下发；某一项为空或缺失时用默认值。

| `reason` | `groupAi` 里的键 | 默认文案 |
| --- | --- | --- |
| `insufficient_credits` | `insufficientCredits` | 群主积分不足，{name}暂时无法为本群解答问题 |
| `quota_exceeded` | `quotaExceeded` | 本群今日的{name}问答次数已用完，请明天再试 |
| `not_configured` | `notConfigured` | 群主还没有为本群配置可用的知识库，请联系群主在群设置中勾选 |
| `owner_inactive` | `ownerInactive` | {name}暂时无法为本群解答问题 |
| `rate_limited` | `rateLimited` | 问得有点快，请稍后再试 |
| 其他原因、超时、宿主出错 | `busy` | {name}正在整理资料，请稍后再问 |

- 文案里的 `{name}` 换成助手的名字。
- 10 秒内没有应答或连不上宿主算 `timeout`；宿主答了非 200（包括没给 `groupAi` 时的 404）算 `internal_error`。两者都落到 `busy`。

## 触发词

触发词是“@”加助手的名字，名字在运行配置 `groupAi.name`，默认“小桃子”。

- 只对群消息生效。正文里任意位置出现“@名字”，后面紧跟空白（含全角空格）、中英文标点 `，。！？：；、,.!?:;`、另一个 `@` 或正文结尾时触发。所以“@小桃子，帮我查一下”“@小桃子@张三”会触发，“@小桃子的粉丝群”不会。
- 宿主下发的名字会去掉开头的“@”和两端空白；只剩这些、没有名字时保留默认值，否则每条消息都会被当成提问。
- 改名字不改助手的 uid：回答的 `from` 仍是 `assistant_xiaotaozi`。
- 离线推送里助手的名字跟随 `groupAi.name`，除非宿主另外设了 `pushText.assistant`。

运行配置的完整字段见 [宿主接口契约](/host/#runtime-config-与-e2ee-config)。

## 群的设置

每个群有自己的一份设置，没设置过的群用默认值。

| 动作 | 谁能调 | 内容 |
| --- | --- | --- |
| `get_group_ai_settings` | 成员 | 返回 `aiQaEnabled`、`aiKbIds`、`aiDocIds`、`aiDailyQuota`、`aiCooldownSec`，字段总是全部出现 |
| `update_group_ai_settings` | 群主 | 只能改 `aiQaEnabled`、`aiKbIds`、`aiDocIds` |

- 默认值：`aiQaEnabled` 为 false，两个列表为空，`aiDailyQuota` 50，`aiCooldownSec` 5。后两项是表的列默认值，协议里没有修改它们的动作。
- 非成员、未登录或出错时，`get_group_ai_settings` 不报错，返回默认值，与真实的默认设置无法区分（F47）。
- 服务端把设置缓存 60 秒；经 `update_group_ai_settings` 修改时立即失效，群解散时也清掉。

## 助手“正在输入”

两端的核心引擎收到 `ai_typing` 时，在那个群的“正在输入”里显示助手，回答一到即消失。

- 只认本人所在的群：本地没有这个群的会话时忽略。
- 最长显示 15 秒：Web 是 `ChatEngine` 配置的 `aiTypingTtlMs`，Android 是 `EngineConfig.aiTypingTtlMs`，默认都是 15000 毫秒。
- 界面上助手的名字：Web 的 React 套件在宿主没给这个 uid 起名时用文案表里的 `assistant`（简体中文为“助手”）；Android 显示为“助手”。

## 验证状态

服务端与宿主有自动化测试，客户端的“正在输入”只在测试里验证过。

- **服务端**：黄金对话 `module/group-ai`；`server/groupai_test.go`。standalone 模式下这个场景必然失败，因为它依赖宿主。
- **宿主**：`host-sdk-node` 的宿主插件测试覆盖 `group-ai-qa` 端点，不给 `groupAi` 时答 404。
- **客户端**：Web 与 Android 的“正在输入”只在单测与界面测试里验证过。
