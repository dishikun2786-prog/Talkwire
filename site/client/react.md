---
title: "React 界面套件"
description: "@talkwire/react 提供会话列表、聊天页、会话信息页、选人与通讯录，宿主只实现 ImUiHost 一个对象。"
---

# React 界面套件

`@talkwire/react` 是建在 [Web SDK](/client/web) 的 `ChatEngine` 之上的一整套聊天界面，宿主只需提供一个 `ImUiHost` 对象：人的名字与头像、联系人、媒体。支持 React 18 或 19，文案自带英文、简体中文、越南文。

## 挂载

组件放在 `TalkwireProvider` 里，外层元素带 `talkwire-root` 类，`TalkwireStyles` 把套件的样式表放进页面。下面摘自 `web/QUICKSTART.md`：

```tsx
<div className="talkwire-root">
  <TalkwireStyles />
  <TalkwireProvider engine={engine} host={host} locale="zh">
    <ConversationList onOpen={setOpen} /> {/* 顶上带搜索框；searchable={false} 去掉 */}
    {open && <ChatScreen conversation={open} onBack={() => setOpen(null)} onOpenInfo={() => setInfo(open)} />}
    {info && <ConversationInfo conversation={info} onBack={() => setInfo(null)} onLeft={() => { setInfo(null); setOpen(null); }} />}
  </TalkwireProvider>
</div>
```

`engine`、`host` 的建立见 [Web SDK](/client/web#装起来)。界面只是 `ChatEngine` 的视图：消息收发、补拉、已读、多标签都在引擎里，换掉界面不影响它们。

## TalkwireProvider

| 属性 | 说明 |
| --- | --- |
| `engine` | `ChatEngine` |
| `host` | `ImUiHost`，见下一节 |
| `locale` | BCP 47 语言标签，选内置文案表（`en`、`zh`、`vi`）；不给时用英文 |
| `strings` | 逐条替换文案，`Partial<ImStrings>` |
| `types` | 消息类型注册表，不给时只有内置类型；见[自己的消息类型](/client/message-types) |

## ImUiHost

框架只认 uid，名字、头像、谁是好友都归宿主。`ImUiHost` 是 Android 套件同名接口在 Web 上的对应物。用 Node 宿主 SDK（`@talkwire/host-node` 的 `talkwireAppRoutes`）时资料与联系人两个接口是现成的，见 [Node 宿主 SDK](/host/node-sdk)：

```ts
import { Signal } from "@talkwire/client";
import type { ImProfile, ImUiHost } from "@talkwire/react";

const get = (path: string) => fetch(path, { headers: { authorization: `Bearer ${token}` } }).then((r) => r.json());
const toProfile = (p: { uid: string; displayName: string; avatarUrl: string | null }): ImProfile => ({ uid: p.uid, name: p.displayName || p.uid, avatarUrl: p.avatarUrl ?? undefined });

const contacts = new Signal<ImProfile[]>([]);
const host: ImUiHost = {
  contacts, // 发起单聊、建群时列出
  profiles: async (uids) => (await get(`/api/v1/chat/profiles?uids=${encodeURIComponent(uids.join(","))}`)).items.map(toProfile),
  resolveMediaUrl: (url) => (url ? new URL(url, hostBaseUrl).toString() : undefined),
};
```

| 成员 | 必须 | 说明 |
| --- | --- | --- |
| `profiles(uids)` | 是 | 返回 `{ uid, name, avatarUrl? }` 数组。同一时刻的请求合并成一次并缓存；宿主没给名字的人显示缩短的 uid |
| `contacts` | 否 | 联系人信号，发起单聊、建群、加人时列出 |
| `refreshContacts()` | 否 | 重新拉联系人；选人页与通讯录打开时调用 |
| `media` | 否 | `MediaSender`，发图片、视频、语音；不给时不显示附件与语音按钮 |
| `resolveMediaUrl(url)` | 否 | 把存下的媒体地址变成可加载的地址；默认原样 |
| `onOpenProfile(uid)` | 否 | 点了某人的头像；默认什么都不做，群成员之间不能互看资料 |

## 组件

| 组件 | 内容 |
| --- | --- |
| `ConversationList` | 会话列表。右键或长按：置顶、免打扰、删除（只删本机）；顶上的搜索框按会话名与本机存的文字消息找 |
| `ChatScreen` | 聊天页：气泡、输入栏（表情、按住说话）、消息操作、群里每条自己的消息显示谁已读 |
| `ConversationInfo` | 会话信息页：群成员与角色、按权限给出的成员操作、加人、改群名、置顶、免打扰、清空、退群与解散 |
| `PickPeople` | 选人：建群，或往群里加人 |
| `ContactList` | 通讯录，点人即建立并打开单聊 |
| `ConnectionBanner` | 没登录上时显示“连接中”或离线提示 |
| `MessageBubble` | 单独一个气泡，用于预览与测试 |
| `HoldToTalk` | 按住说话按钮：按下录音、松开发送、上滑取消，在一次指针手势内完成 |
| `Avatar` | 头像，可带在线圆点 |
| `TalkwireStyles` | 把样式表放进页面一次 |

### ConversationList

| 属性 | 说明 |
| --- | --- |
| `onOpen(conversation)` | 点了一个会话或一条搜索结果 |
| `activeConversation` | 旁边正在显示的会话，高亮 |
| `filter` | 显示哪些会话；默认不显示已离开且没有消息的群、没有消息也没有草稿的单聊 |
| `searchable` | 搜索框，默认 `true` |

搜索结果分“聊天”（按会话名）与“聊天记录”两段，后者来自 `engine.searchMessages`，停止输入 250 毫秒后才查。点结果打开那个会话，不跳到那条消息。

### ChatScreen

| 属性 | 说明 |
| --- | --- |
| `conversation` | 会话编号：单聊是对方 uid，群是 `grp_…` |
| `onBack` | 返回 |
| `headerActions` | 页头右侧的额外按钮 |
| `onOpenInfo` | 打开会话信息页；不给时页头没有“⋯”按钮 |

右键或长按气泡打开菜单，给什么由消息类型的 `actions` 决定，别人的消息不给撤回、重发、删除：

- 回复：把被引用的消息写进 `extra.replyTo`（`seq`、`from`、截到 120 字的 `text`），与 Android 同一格式，两端互相认得。
- 转发：转发一条走 `forward_broadcast`，可选多个会话；多选后转发走 `forward_bundle`，一个会话，按原顺序。单聊的目标用 `p2p_…` topic，不用对方 uid（F22）。
- 撤回：撤回的消息在本地留一条墓碑，显示“你撤回了一条消息”或“某某撤回了一条消息”，不计未读。

### ConversationInfo 与 PickPeople

下面摘自 `web/README.md`：

```tsx
import { ConversationInfo, PickPeople } from "@talkwire/react";

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

| `ConversationInfo` 属性 | 说明 |
| --- | --- |
| `conversation` | 会话编号 |
| `onBack`、`onLeft` | 返回；退群或解散之后 |
| `onAddMembers` | 加人；不给时打开套件自己的选人页 |
| `groupExtras(topic, role)` | 可选模块放进群信息页的内容，位于成员之后、聊天设置之前；`role` 是本人在群里的角色 |
| `maxAdmins` | 群管理员上限，默认 10，应与服务端的 `group_chat.max_admins` 一致 |

成员行只对有权限的人可点，判断与服务端的检查一致（`memberPermissions`）：群主可以设为或取消管理员、转让群主、移出；管理员只能移出普通成员。服务端仍是最终裁决，拒绝时提示，管理员满员（409）单独提示。普通成员之间不能互看资料，成员行不可点。

`PickPeople` 列出 `host.contacts`，打开时调 `host.refreshContacts()`；已在群里的人显示但不能勾选。它的属性是 `topic`（不给时建群）、`onDone(conversation)`、`onCancel`。

## Hooks

在 `TalkwireProvider` 里面写自己的组件时可以用这些：

| Hook | 返回 |
| --- | --- |
| `useTalkwire()` | `engine`、`host`、`strings`、`locale`、`types`、`profiles` |
| `useSignal(signal)` | 订阅任意信号的当前值 |
| `useConnectionState()` | `engine.client.state` |
| `useConversations()`、`useConversation(id)` | 会话列表；一个会话 |
| `useMessages(id)` | 一个会话的消息 |
| `usePresence(uid)` | `online`、`offline` 或 `unknown` |
| `useTyping(id)` | 此刻在这个会话里输入的人 |
| `useProfile(uid)` | 一个人的资料；宿主回答前是缩短的 uid |
| `useContacts()` | 宿主的联系人，挂载时调 `refreshContacts` |

## 主题

外观全部是 `.talkwire-root` 上的 CSS 变量，在任何祖先元素上设这些变量就能改，例如 `.my-app { --talkwire-accent: #e91e63; --talkwire-radius: 6px; }`（`web/packages/react/src/theme.ts` 开头的注释）。

| 变量 | 浅色默认 | 用途 |
| --- | --- | --- |
| `--talkwire-font` | 系统字体栈 | 字体 |
| `--talkwire-bg` | `#f3f4f6` | 页面背景 |
| `--talkwire-surface` | `#ffffff` | 列表、页头等表面 |
| `--talkwire-text` | `#111827` | 正文 |
| `--talkwire-text-muted` | `#6b7280` | 次要文字 |
| `--talkwire-border` | `#e5e7eb` | 分隔线 |
| `--talkwire-accent` | `#2563eb` | 强调色 |
| `--talkwire-on-accent` | `#ffffff` | 强调色上的文字 |
| `--talkwire-bubble-mine` | `#2563eb` | 自己的气泡 |
| `--talkwire-bubble-mine-text` | `#ffffff` | 自己气泡里的文字 |
| `--talkwire-bubble-other` | `#ffffff` | 别人的气泡 |
| `--talkwire-bubble-other-text` | `#111827` | 别人气泡里的文字 |
| `--talkwire-danger` | `#dc2626` | 未读角标、危险操作 |
| `--talkwire-online` | `#16a34a` | 在线圆点 |
| `--talkwire-radius` | `14px` | 圆角 |

深色跟随系统：`prefers-color-scheme: dark` 时生效，除非根元素带 `data-theme="light"`；根元素带 `data-theme="dark"` 时强制深色。样式表本身是导出的字符串 `talkwireCss`，`TalkwireStyles` 只是把它放进一个 `<style>`。

## 文案与语言

`locale` 按语言部分选内置表：`zh` 开头用简体中文，`vi` 开头用越南文，其他一律英文。`strings` 在选好的表上逐条替换，也可以给一整张表。占位符写作 `{0}`、`{1}`，由 `format` 填入。

内置表以 `en`、`zh`、`vi` 导出（合在 `builtInStrings` 里），`stringsFor(locale)` 返回某个语言标签对应的表。可选模块的组件按 Provider 的 `locale` 选自己的文案。

## 可选模块与后台

服务端装了哪个模块（`TALKWIRE_MODULES`），前端才装对应的包，不装不影响核心包。模块的组件都放在 `TalkwireProvider` 里：

- 扫码入群：`@talkwire/group-join/react`，见[扫码入群](/modules/group-join)。
- 音视频通话：`@talkwire/call/react`，见[音视频通话](/modules/call)。
- 后台页面：`@talkwire/admin`，嵌进宿主自己的管理后台，见[后台页面](/modules/admin)。

## 示例

`web/sample/` 是可运行的示例 App（Vite），连 `examples/sample-host` 与服务端：

```bash
npm run sample
```

在两个浏览器（或一个普通窗口、一个无痕窗口）里用不同的名字登录，就能互相发消息、建群；右上角显示当前标签的连接方式。完整步骤见[快速开始](/guide/quickstart)。
