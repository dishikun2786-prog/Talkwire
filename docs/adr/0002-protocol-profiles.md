# ADR-0002：两档协议（legacy 与 strict）

- 状态：已采纳
- 日期：2026-10-25
- 依据：协议规范第 0.3、18 节；`docs/plan.md` 的“已定决策（2026-10-25，G1 验收）”

## 背景

ADR-0001 规定协议 v1 冻结、只加不改，现网 APK 必须能不升级就连上 ImKit 服务端。可是原版协议有已知缺陷（规范第 18 节的 D1–D9），也接受不该接受的令牌（刷新、代理、管理员令牌都能登录）。要修，就会改变旧客户端看到的帧；不修，新项目一上来就继承这些缺陷。

## 决定

1. 服务端有两档：`legacy` 对外的帧与原版逐帧一致，`strict` 修正 D1–D5、D7–D9 并加上新增字段（`errCode`、`protocol`、`capabilities`、`vapidKey`、两种应答的 `id` 等，见规范第 0.3 节）。D6 两档都不改。
2. 默认 `strict`。迁移原版项目、要与原版客户端逐帧兼容时设 `IMKIT_PROTOCOL_PROFILE=legacy`；一致性测试台测原版对话时自动用 legacy。
3. 令牌：strict 只接受 `type=access`、`role` 在 `IMKIT_TOKEN_USER_ROLES`（默认 `app_user`）里的令牌（红线 R18）；legacy 照原版接受全部。迁移期的开关 `IMKIT_ACCEPT_LEGACY_TOKENS=1` 让 strict 也接受全部，默认关，开着时启动打印警告。
4. 新功能（多端在线、模块、推送服务商）两档都有，但只在客户端用到新字段时启用：不带 `device` 登录就和原版一样互相顶替。
5. strict 偏离原版的每一处都有录制的对话（`contract/protocol/transcripts-strict/`）和两档对照的单测；legacy 由原版的 33 个黄金对话把关。

## 后果

- 客户端要处理两档的差异：靠 `errCode` 判断失败原因，没有 `errCode`（legacy）时按原版的状态码；不解析 `reason` 的文字。
- 迁移顺序因此是“先换服务端（legacy）、再换客户端、最后切 strict”，见 `docs/migration-from-xiaotaozi.md`。
- 每修一个缺陷都要同时维护两档的行为和两份对话，代价落在服务端与测试台。
