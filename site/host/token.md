---
title: "签发 IM 令牌"
description: "客户端登录 IM 用的令牌由宿主签发：它的 claims、服务端的校验规则、JWKS、uid 的算法，以及客户端怎样刷新令牌。"
---

# 签发 IM 令牌

客户端登录 IM 时出示一个由宿主签发的 JWT，IM 服务端从中取出宿主用户 id。默认用与宿主共享的 `JWT_SECRET` 验 HS256 签名；宿主也可以公布 JWKS，让服务端用公钥验 RS256 或 ES256 令牌。这个令牌与宿主和 IM 之间的内部令牌是两回事，后者见 [宿主接口契约](/host/#鉴权-内部令牌)。

## 令牌内容

IM 服务端在 strict 档（默认）下只接受普通用户的访问令牌，令牌至少要有下面这些内容：

| 项 | 值 | 说明 |
| --- | --- | --- |
| 算法 | HS256 | 密钥与 IM 服务端的 `JWT_SECRET` 相同 |
| `sub` | 宿主用户 id | 必须有，缺了就拒绝；契约里宿主用户 id 最长 128 个字符 |
| `type` | `access` | strict 档要求 |
| `role` | `app_user` | strict 档要求在 `TALKWIRE_TOKEN_USER_ROLES` 里，默认只有 `app_user` |
| `exp` | 过期时刻 | 带了就校验，过期的令牌被拒绝 |

开发用宿主 [`examples/sample-host`](https://github.com/dishikun2786-prog/Talkwire/blob/main/examples/sample-host/src/app.ts) 是这样签的（Node，`jsonwebtoken`）：

```ts
/** What the IM server accepts at login: an access token of an ordinary user (red line R18). */
export function signAccessToken(secret: string, userId: string, ttlSeconds = 30 * 24 * 3600): string {
  return jwt.sign({ type: "access", role: "app_user" }, secret, { algorithm: "HS256", subject: userId, expiresIn: ttlSeconds });
}
```

宿主只应把访问令牌交给客户端。刷新令牌、管理员令牌不要交给客户端；管理员令牌要与用户令牌分开签（不同的密钥或不同的 `type`），sample-host 用 `secret + ":admin"` 作密钥、`type: "admin_access"` 签管理员令牌。

## 服务端怎样校验

登录时服务端先验签名和有效期，再按协议档位决定接受哪些令牌：

| 设置 | 接受的令牌 |
| --- | --- |
| strict 档（默认） | `type` 为 `access` 且 `role` 在 `TALKWIRE_TOKEN_USER_ROLES` 里的令牌 |
| strict 档且 `TALKWIRE_ACCEPT_LEGACY_TOKENS=1` | 任何带 `sub` 的有效令牌，启动时打印警告 |
| legacy 档（`TALKWIRE_PROTOCOL_PROFILE=legacy`） | 任何带 `sub` 的有效令牌，包括刷新令牌、管理员令牌和没有 `type` 的令牌 |

`TALKWIRE_TOKEN_USER_ROLES` 是逗号分隔的角色列表，只在 strict 档生效。`TALKWIRE_ACCEPT_LEGACY_TOKENS=1` 只用于迁移期，客户端都改为出示访问令牌后应去掉；两个档位的区别见 [legacy 与 strict](/protocol/profiles)，环境变量见 [环境变量](/server/env)。

登录失败的应答：

| 情况 | 应答 |
| --- | --- |
| 缺 `token` | 401 `token required` |
| 令牌无效、过期，或不符合档位要求 | 401 `invalid token` |

## 用 JWKS 验令牌

设了 `TALKWIRE_JWKS_URL` 时，服务端从这个地址读宿主公布的 JSON Web Key Set，用公钥验用户令牌。这样只有宿主的私钥能签出登录令牌，IM 服务端不持有任何能签发它的东西。

- **算法**：只接受 RS256 与 ES256。令牌头必须带 `kid`，服务端按它选公钥。
- **公钥**：每个 key 必须有 `kid`，`use` 为空或 `sig`；RSA 至少 2048 位；EC 只支持 P-256；`alg` 写了就必须与密钥类型对应。
- **地址**：必须是 https，只有本机地址（`localhost` 或回环 IP）可以用 http。
- **读取**：启动时读一次，失败只记日志，之后登录时会再读。缓存的公钥超过 10 分钟会在后台重读，期间照常使用旧公钥。
- **新 `kid`**：遇到没见过的 `kid` 时重读一次，最多每 30 秒一次，防止编造的 `kid` 把请求压到宿主上。每次读取超时 3 秒。

档位规则（`type`、`role`）对 JWKS 令牌同样适用。`JWT_SECRET` 仍然必须设置：内部令牌一直用它校验，服务端调宿主时也用它签名。

## uid

IM 不认识宿主用户 id，线上只用 uid。uid 由宿主用户 id 算出，算法固定：

```
uid = "uid_" + base64url_nopad( SHA-256( UTF-8(appUserId) )[0:8] )
```

结果固定 15 个字符。客户端可以在本地算出任何用户的 uid，但不能从 uid 反推宿主用户 id，所以宿主要把 uid 与用户一起存下来，用于按 uid 查资料、扫码加好友和后台显示聊天记录。

服务端与 Node 宿主 SDK 的实现：

::: code-group

<<< @/../server/host/uids.go [服务端]

<<< @/../host-sdk-node/src/uid.ts [宿主]

:::

Android 的 `ImIds.uid`（`talkwire-core`）和 Web 的 `uidOf`（`@talkwire/protocol`）算的是同一个值。每个实现都必须逐条复现 [`uid-vectors.json`](https://github.com/dishikun2786-prog/Talkwire/blob/main/contract/host-api/uid-vectors.json) 里的向量，节选如下：

| 宿主用户 id | uid |
| --- | --- |
| `alice` | `uid_K9gGyX8OAK8` |
| `bob` | `uid_gbY32PzSxto` |
| `0` | `uid_X-zrZv_Ibzg` |
| `张三` | `uid_HYQbwO6YMJw` |
| `00000000-0000-4000-8000-000000000000` | `uid_24BV4OAwfVo` |

::: warning 注意
改动算法会让已经存下的 uid 全部失效。服务端还把以 `usr` 开头的字符串当作用户 uid，那是早期系统遗留的格式，新实现不应再生成。
:::

## 客户端刷新令牌

两个客户端 SDK 在每次登录尝试前都向 App 提供的回调要令牌。回调的参数 `forceRefresh` 为 `true` 时，表示上一次登录因令牌无效被拒（401），回调不应再返回缓存的旧令牌；返回 `null` 表示用户未登录。

::: code-group

```ts [Web]
const tokenProvider = (forceRefresh?: boolean) => myAuth.accessToken(forceRefresh); // 可以返回 Promise；返回 null 表示未登录
```

```kotlin [Android]
val tokens = TokenProvider { forceRefresh -> myAuth.accessToken(forceRefresh) }
```

:::

两端的重连规则相同：

- 回调返回 `null`（Web 上空串也算）：停止连接，原因是 `no_credentials`（Android 为 `NO_CREDENTIALS`）。
- 回调抛异常：这次尝试算失败，按退避稍后重试。
- 连续被拒的次数达到 `maxAuthFailures`（默认 3）：停止连接，原因是 `auth_rejected`（Android 为 `AUTH_REJECTED`）。一次成功的登录会把计数清零。
- 已经登录后才收到的 401 `logged in from another device` 表示被顶替，不算令牌被拒，也不会触发刷新。

同一个回调也用于向宿主上传媒体：两端的 `MediaUploader` 用它取令牌放进 `Authorization: Bearer`，这时总是 `forceRefresh = false`。完整的接入写法见 [Web SDK](/client/web) 和 [Android SDK](/client/android)。
