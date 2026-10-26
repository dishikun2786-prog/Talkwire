<script setup lang="ts">
import { computed, nextTick, onMounted, ref } from "vue";
import { withBase } from "vitepress";
import { PLATFORMS, readPlatform, savePlatform, type Platform } from "../platform";

const repo = "https://github.com/dishikun2786-prog/Talkwire";
// The public test service (deploy/imtalk, site/guide/sandbox.md).
const sandbox = "https://imtalk.openedskill.com";

// ---- hero: a login and a reconnect sync, as frames on the wire -----------
// Shapes follow the spec (sections 6.1, 8.1, 8.2); long values are elided.
const frames = [
  { out: true, text: '{"id":"1","action":"login","token":"eyJ…","device":"web-a1"}' },
  { out: false, text: '{"id":"1","action":"login","code":200,"uid":"uid_mF3xQ…","protocol":1,"capabilities":["errcode","reply-id","multi-device","sync-summary"]}' },
  { out: true, text: '{"id":"2","action":"sync_summary"}' },
  { out: false, text: '{"id":"2","action":"sync_summary","code":200,"topics":[{"topic":"grp_k8sQ…","lastSeq":43,"isGroup":true}]}' },
  { out: true, text: '{"id":"3","action":"get","topic":"grp_k8sQ…","afterSeq":40}' },
  { out: false, text: '{"id":"3","action":"get","code":200,"messages":[{"seq":41,…},{"seq":43,…}]}', note: "42 已撤回：记下，不再重拉" },
  { out: false, text: '{"action":"data","topic":"grp_k8sQ…","from":"uid_Q2b7…","seq":44,"content":"今晚八点发版"}' },
];

const stats = [
  { value: "33 个黄金对话", body: "服务端的每次改动都与参照实现逐帧比对，一帧不同就不合入。" },
  { value: "两个协议档位", body: "legacy 与原版客户端逐帧兼容；strict 修正已知缺陷并加上错误码。" },
  { value: "32 条红线", body: "丢消息、串号、越权这类事故，每条都对应到用例。" },
  { value: "Apache-2.0", body: "核心宽松许可；端到端加密模块采用 AGPLv3，单独发布。" },
];

const features = [
  { title: "单聊与群聊", body: "群主、管理员、成员三种角色；邀请、移除、转让与解散。角色以服务端名册为准，拉不到时按普通成员处理。" },
  { title: "图片、视频、语音、文件", body: "客户端传到你的存储，消息里只带地址。气泡按内容类型选，不看正文里的文字。" },
  { title: "撤回与清空", body: "撤回留下一条墓碑，看得到谁在何时撤回；清空对所有成员生效，离线的成员上线后照样生效。" },
  { title: "已读与正在输入", body: "单聊已读位置、可关闭的群已读回执、正在输入。换一台新设备，看过的消息不会重新算成未读。" },
  { title: "多端同时在线", body: "一个账号默认最多 10 台设备同时在线。在一台设备上发的消息，在其他设备上像收到的一样出现。" },
  { title: "离线推送", body: "FCM、APNs 与 Web Push，按设备登记令牌；通知文案由宿主决定。" },
  { title: "在线状态三态", body: "在线、离线、未知，记录会过期。拿不准的时候显示“未知”，不冒充“离线”。" },
  { title: "浏览器多标签共用连接", body: "同一账号的多个标签页共用一条 WebSocket；持有连接的标签关掉，其他标签自动接管。" },
  { title: "可嵌入的后台页面", body: "聊天记录、通话记录与通话详情；清空只给超级管理员，要写理由并逐字输入确认语。" },
];

const rail = [
  { seq: 40, kind: "have" },
  { seq: 41, kind: "have" },
  { seq: 42, kind: "recalled" },
  { seq: 43, kind: "have" },
  { seq: 44, kind: "new" },
];

// ---- quick start tabs ------------------------------------------------------
const esc = (s: string) => s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
const cm = (s: string) => `<span class="c">${esc(s)}</span>`;
const kw = (s: string) => `<span class="k">${esc(s)}</span>`;
const st = (s: string) => `<span class="s">${esc(s)}</span>`;
const t = esc;

const code: Record<Platform, string> = {
  Web: [
    cm("# 1. 安装"),
    t("npm install @talkwire/client @talkwire/storage-idb @talkwire/react"),
    "",
    cm("// 2. 连接与同步"),
    `${kw("import")} { ImClient, ChatEngine } ${kw("from")} ${st('"@talkwire/client"')};`,
    `${kw("import")} { IdbChatStore } ${kw("from")} ${st('"@talkwire/storage-idb"')};`,
    "",
    `${kw("const")} client = ${kw("new")} ImClient({`,
    `  endpoints: [${st('"wss://im.example.com/ws/im"')}],`,
    `  tokenProvider,   ${cm("// 返回你的后端签发的访问令牌")}`,
    `  deviceName,      ${cm("// 本浏览器的设备名，存在 localStorage")}`,
    "});",
    `${kw("const")} store = ${kw("await")} IdbChatStore.open({ name: ${st('"talkwire"')}, encrypt: ${kw("true")} });`,
    `${kw("const")} engine = ${kw("new")} ChatEngine(client, store, { appUserId: userId });`,
    `${kw("await")} engine.open();`,
    "client.start();",
  ].join("\n"),
  Android: [
    cm("// 1. 依赖：Maven 坐标还没发布，先以源码模块引入"),
    "dependencies {",
    `    implementation(project(${st('":talkwire-ui"')}))`,
    `    implementation(project(${st('":talkwire-storage-room"')}))`,
    "}",
    "",
    cm("// 2. 连接与同步"),
    `${kw("val")} tokens = TokenProvider { forceRefresh -&gt; myAuth.accessToken(forceRefresh) }`,
    `${kw("val")} client = ImClient(ImClientConfig(`,
    `    endpoints = listOf(${st('"wss://im.example.com/ws/im"')}),`,
    "    tokenProvider = tokens,",
    `    deviceName = ${st('"android-$installId"')},`,
    "), scope)",
    `${kw("val")} store = RoomChatStore.open(context, keys = AndroidKeystoreKeyWrapper())`,
    `${kw("val")} engine = ChatEngine(client, store, myUserId, scope, EngineConfig())`,
    "scope.launch { engine.start() }",
  ].join("\n"),
  服务端: [
    cm("# 需要 Go 1.26.4 以上与 PostgreSQL"),
    t("git clone https://github.com/dishikun2786-prog/Talkwire"),
    `${kw("cd")} Talkwire/server &amp;&amp; go build -o bin/talkwire-server .`,
    "",
    cm("# 必填：数据库密码，以及与宿主共享的签名密钥"),
    "DB_PASSWORD=… JWT_SECRET=… MAIN_API_BASE_URL=https://api.example.com \\",
    "  ./bin/talkwire-server",
  ].join("\n"),
  宿主: [
    cm("// Node 宿主 SDK 是一组 Fastify 插件；别的语言照 OpenAPI 契约实现"),
    `${kw("import")} { ImClient, talkwireHostPlugin, talkwireAppRoutes } ${kw("from")} ${st('"@talkwire/host-node"')};`,
    `${kw("import")} { prismaStores } ${kw("from")} ${st('"@talkwire/host-node/prisma"')};`,
    "",
    `${kw("const")} { profiles, contacts } = prismaStores(prisma);`,
    `${kw("const")} secret = process.env.JWT_SECRET!;   ${cm("// 与 IM 服务端相同")}`,
    `${kw("const")} im = ${kw("new")} ImClient({ baseUrl: ${st('"http://127.0.0.1:6062"')}, secret });`,
    "",
    `${kw("await")} app.register(talkwireHostPlugin, { secret, contacts, profiles });`,
    `${kw("await")} app.register(talkwireAppRoutes, {`,
    "  authenticate, profiles, contacts, im,",
    "  addTokenSecret: process.env.ADD_TOKEN_SECRET!,",
    "});",
  ].join("\n"),
};

const tabIds: Record<Platform, string> = { Web: "web", Android: "android", 服务端: "server", 宿主: "host" };
const platform = ref<Platform>("Web");
const tabRefs = ref<HTMLButtonElement[]>([]);
const codeEl = ref<HTMLElement | null>(null);
const copyLabel = ref("复制");
let copyTimer: ReturnType<typeof setTimeout> | undefined;

onMounted(() => {
  const p = readPlatform();
  if (p) platform.value = p;
});

function pick(p: Platform, focus = false) {
  platform.value = p;
  savePlatform(p);
  copyLabel.value = "复制";
  if (focus) nextTick(() => tabRefs.value[PLATFORMS.indexOf(p)]?.focus());
}

function onTabKey(e: KeyboardEvent, i: number) {
  const n = PLATFORMS.length;
  const to = { ArrowRight: (i + 1) % n, ArrowLeft: (i - 1 + n) % n, Home: 0, End: n - 1 }[e.key];
  if (to === undefined) return;
  e.preventDefault();
  pick(PLATFORMS[to], true);
}

async function copy() {
  const el = codeEl.value;
  if (!el) return;
  let ok = false;
  try {
    await navigator.clipboard.writeText(el.textContent ?? "");
    ok = true;
  } catch {
    // no clipboard permission (embedded browsers, http): select the text instead
    const sel = window.getSelection();
    sel?.selectAllChildren(el);
    try {
      ok = document.execCommand("copy");
    } catch {
      ok = false;
    }
    if (ok) sel?.removeAllRanges();
  }
  copyLabel.value = ok ? "已复制" : "已选中，按 Ctrl+C 复制";
  clearTimeout(copyTimer);
  copyTimer = setTimeout(() => (copyLabel.value = "复制"), 2500);
}

const currentCode = computed(() => code[platform.value]);

const modules = [
  { title: "音视频通话", link: "/modules/call", body: "信令、振铃与断线计时在服务端；媒体走 RTC 适配器，自带阿里云 ARTC 3.0 的适配与宿主签令牌的示例。" },
  { title: "扫码入群", link: "/modules/group-join", body: "群二维码、入群页、入群申请与审核；群满时申请留着，不会被吞掉。" },
  { title: "群助手钩子", link: "/modules/bot-hook", body: "群里有人 @ 助手时，问题交给你的后端回答，计费也由你决定；答不上来时发兜底文案。" },
  { title: "端到端加密", link: "/modules/e2ee", badge: "AGPLv3", body: "基于 libsignal 的单聊与群发送密钥，服务端只转发信封。许可证不同，单独发布。" },
  { title: "视频压缩", link: "/modules/video-compress", body: "Android 上超过 50 MB 的视频先重新编码；失败或没变小时，放得下就发原片。" },
  { title: "自定义消息卡片", link: "/client/message-types", body: "红包、订单这类业务卡片用你自己的内容类型；状态变了由宿主推一次更新。示例里有一张红包卡。" },
];

const status = [
  { part: "Go 服务端", state: "可用", ok: true, check: "黄金对话两档全过、竞态检测；正式压测待在 Linux 上做" },
  { part: "Node 宿主 SDK", state: "可用", ok: true, check: "接入后黄金对话 33/33，契约测试双向通过" },
  { part: "Android SDK 与界面套件", state: "可用", ok: true, check: "单测与真实服务端用例；一台真机上验证了聊天、通话信令、扫码入群与网页互通；FCM、视频压缩、E2EE 未上真机" },
  { part: "Web SDK 与 React 套件", state: "可用", ok: true, check: "单测、端到端用例、与 Android 真机互发" },
  { part: "云端测试服务", state: "已上线", ok: true, check: "imtalk.openedskill.com；每次发布后从公网跑端到端检查 17 步" },
  { part: "iOS SDK", state: "规划中", ok: false, check: "—" },
];
</script>

<template>
  <div class="tw">
    <!-- Hero -->
    <section class="hero dark-band" aria-labelledby="tw-hero-title">
      <div class="wrap hero-grid">
        <div class="hero-copy">
          <p class="eyebrow on-dark"><span class="dot" aria-hidden="true" />开源 IM 框架 · 自己部署 · Apache-2.0</p>
          <h1 id="tw-hero-title" class="display">自己部署的即时通讯，每一帧都经得起比对</h1>
          <p class="lead">
            Talkwire 由一个 Go 实时服务端、Android 与 Web 客户端 SDK 和一份线协议组成。你的业务后端只实现身份、好友、资料、推送和媒体几个接口，就能得到单聊、群聊、媒体消息、已读回执、撤回、多端在线与离线推送。
          </p>
          <div class="actions">
            <a class="btn btn-accent" href="#start">开始接入
              <svg width="16" height="16" viewBox="0 0 24 24" aria-hidden="true"><path d="M5 12 H19 M13 6 L19 12 L13 18" /></svg>
            </a>
            <a class="btn btn-ghost" :href="sandbox" target="_blank" rel="noopener">在线试用
              <svg width="14" height="14" viewBox="0 0 24 24" aria-hidden="true"><path d="M14 5 H19 V10 M19 5 L11 13 M17 14 V19 H5 V7 H10" /></svg>
            </a>
            <a class="btn btn-ghost" :href="withBase('/guide/')">阅读开发手册</a>
          </div>
          <a class="repo mono" :href="repo">github.com/dishikun2786-prog/Talkwire</a>
        </div>

        <figure class="wire" aria-label="一次登录与重连补拉在线上的帧">
          <div class="wire-head mono">
            <span>wss://im.example.com/ws/im</span>
            <span class="live"><span class="live-dot" aria-hidden="true" />已连接</span>
          </div>
          <ol class="wire-body mono">
            <li v-for="(f, i) in frames" :key="i" class="frame" :style="{ animationDelay: 0.2 + i * 0.7 + 's' }">
              <span :class="f.out ? 'arrow-out' : 'arrow-in'" :aria-label="f.out ? '客户端发出' : '服务端发来'">{{ f.out ? "→" : "←" }}</span>
              <span>{{ f.text }}<span v-if="f.note" class="note"> // {{ f.note }}</span></span>
            </li>
          </ol>
        </figure>
      </div>
    </section>

    <!-- Proof -->
    <section class="proof" aria-label="可以验证的事实">
      <div class="wrap proof-grid">
        <div v-for="s in stats" :key="s.value" class="stat">
          <div class="stat-value display">{{ s.value }}</div>
          <div class="muted">{{ s.body }}</div>
        </div>
      </div>
    </section>

    <!-- Architecture -->
    <section id="protocol" class="band alt" aria-labelledby="tw-arch">
      <div class="wrap stack-48">
        <header class="head">
          <p class="eyebrow">架构</p>
          <h2 id="tw-arch" class="display">一个服务端，两端 SDK，一组由你实现的宿主接口</h2>
          <p class="sub">用户、好友和资料留在你自己的业务库里。Talkwire 服务端只管连接、消息与离线队列，需要业务数据时回调你的后端；你的后端也可以经内部接口推一张卡片、结束一通电话。</p>
        </header>
        <div class="diagram-box">
          <svg class="diagram" viewBox="0 0 980 480" role="img" aria-labelledby="tw-arch-desc">
            <desc id="tw-arch-desc">Android 与 Web 客户端经 WebSocket 连到 Talkwire 服务端，iOS 规划中；服务端经宿主接口调用你的业务后端，业务后端经内部接口调用服务端；服务端的数据存在 PostgreSQL。</desc>
            <defs>
              <marker id="tw-head" viewBox="0 0 10 10" refX="8" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
                <path d="M0 0 L10 5 L0 10 Z" class="head-fill" />
              </marker>
            </defs>
            <text x="40" y="40" class="lbl mono">客户端 SDK</text>
            <text x="370" y="40" class="lbl mono">Talkwire 服务端</text>
            <text x="730" y="40" class="lbl mono">宿主 · 你的业务后端</text>

            <rect x="40" y="70" width="220" height="68" rx="10" class="box" />
            <text x="60" y="98" class="strong">Android</text>
            <text x="60" y="122" class="small">Kotlin · Compose · Room</text>
            <rect x="40" y="170" width="220" height="68" rx="10" class="box" />
            <text x="60" y="198" class="strong">Web</text>
            <text x="60" y="222" class="small">TypeScript · React · IndexedDB</text>
            <rect x="40" y="270" width="220" height="68" rx="10" class="box planned" />
            <text x="60" y="298" class="strong dim">iOS</text>
            <text x="60" y="322" class="small dim">规划中</text>

            <rect x="370" y="70" width="260" height="268" rx="10" class="box core" />
            <text x="394" y="110" class="strong on-core big">Talkwire 服务端</text>
            <text x="394" y="146" class="on-core">Go · 线协议 v1</text>
            <text x="394" y="176" class="on-core">legacy / strict 两档</text>
            <text x="394" y="206" class="on-core">多端在线 · 离线队列</text>
            <text x="394" y="236" class="on-core">FCM / APNs / Web Push</text>

            <rect x="370" y="396" width="260" height="56" rx="10" class="box" />
            <text x="394" y="430" class="strong">PostgreSQL</text>

            <rect x="730" y="70" width="210" height="268" rx="10" class="box" />
            <text x="754" y="110" class="strong big">宿主</text>
            <text x="754" y="146">身份与令牌</text>
            <text x="754" y="176">好友与资料</text>
            <text x="754" y="206">推送文案与媒体</text>
            <text x="754" y="236">通话鉴权与计费</text>

            <path d="M264 104 H362" class="edge" marker-end="url(#tw-head)" />
            <path d="M264 204 H362" class="edge" marker-end="url(#tw-head)" />
            <path d="M264 304 H362" class="edge dashed" marker-end="url(#tw-head)" />
            <text x="313" y="94" class="small" text-anchor="middle">WebSocket</text>
            <path d="M634 160 H722" class="edge" marker-end="url(#tw-head)" />
            <text x="680" y="150" class="small" text-anchor="middle">宿主接口</text>
            <path d="M722 250 H634" class="edge" marker-end="url(#tw-head)" />
            <text x="680" y="240" class="small" text-anchor="middle">内部接口</text>
            <path d="M500 342 V388" class="edge" marker-end="url(#tw-head)" />
          </svg>
        </div>
        <ol class="flow" aria-label="组件与连接">
          <li class="node">
            <span class="node-label mono">客户端 SDK</span>
            <strong>Android · Web</strong>
            <span class="muted">iOS 规划中</span>
          </li>
          <li class="link" aria-label="经 WebSocket 连到服务端"><span aria-hidden="true">↓</span> WebSocket</li>
          <li class="node core">
            <span class="node-label mono">Talkwire 服务端</span>
            <strong>Go · 线协议 v1</strong>
            <span>legacy / strict 两档 · 多端在线 · 离线队列 · FCM / APNs / Web Push · 数据存 PostgreSQL</span>
          </li>
          <li class="link" aria-label="服务端经宿主接口调用宿主，宿主经内部接口调用服务端"><span aria-hidden="true">↓</span> 宿主接口　<span aria-hidden="true">↑</span> 内部接口</li>
          <li class="node">
            <span class="node-label mono">宿主 · 你的业务后端</span>
            <strong>身份与令牌 · 好友与资料</strong>
            <span class="muted">推送文案与媒体 · 通话鉴权与计费</span>
          </li>
        </ol>
        <p class="more"><a :href="withBase('/guide/architecture')">架构与概念</a><span aria-hidden="true"> · </span><a :href="withBase('/host/')">宿主接口契约</a><span aria-hidden="true"> · </span><a :href="withBase('/protocol/')">协议概述</a></p>
      </div>
    </section>

    <!-- Features -->
    <section class="band" aria-labelledby="tw-features">
      <div class="wrap stack-48">
        <header class="head">
          <p class="eyebrow">能力</p>
          <h2 id="tw-features" class="display">聊天应用要的，核心里都有</h2>
        </header>
        <ul class="cards cards-320">
          <li v-for="f in features" :key="f.title" class="card">
            <h3>{{ f.title }}</h3>
            <p>{{ f.body }}</p>
          </li>
        </ul>
      </div>
    </section>

    <!-- Reliability -->
    <section class="band alt" aria-labelledby="tw-rel">
      <div class="wrap stack-48">
        <header class="head">
          <p class="eyebrow">可靠性</p>
          <h2 id="tw-rel" class="display">不丢、不乱、不重</h2>
          <p class="sub">这三件事在小桃子的线上系统里都出过事故。Talkwire 把每一次的修法写进协议规范，再用用例钉住。</p>
        </header>
        <ul class="cards cards-340">
          <li class="card raised">
            <h3>发送：同一条只存一次</h3>
            <p>每条消息带 clientMsgId。回执丢了就保持“发送中”，重连后用同一个 id 再发，服务端认出来，只存一次。</p>
            <p class="ref mono">红线 C2</p>
          </li>
          <li class="card raised">
            <h3>补拉：从连续段末尾开始</h3>
            <div class="rail mono" role="img" aria-label="本地消息序号：40、41 已有，42 已撤回只记序号，43 已有，44 刚到">
              <span v-for="r in rail" :key="r.seq" :class="['seq', r.kind]">{{ r.seq }}</span>
            </div>
            <p>漏掉的从缺口补；离线时被撤回、本机没收过的 42 也记下，不会每次重连都从那里重拉。strict 档一次请求就知道哪些会话变了。</p>
            <p class="ref mono">红线 C3 · 协议第 8.1、8.2 节</p>
          </li>
          <li class="card raised">
            <h3>迁移：旧客户端不用改</h3>
            <p>legacy 档与原版服务逐帧一致，已经发出去的客户端照常工作；新客户端换到 strict 档，拿到错误码、请求关联和 8 处缺陷修正。</p>
            <p class="ref mono">协议第 0.3、18 节</p>
          </li>
        </ul>
      </div>
    </section>

    <!-- Quick start -->
    <section id="start" class="band" aria-labelledby="tw-start">
      <div class="wrap stack-40">
        <header class="head">
          <p class="eyebrow">快速开始</p>
          <h2 id="tw-start" class="display">接入只有四步</h2>
          <p class="sub">起服务端，实现宿主接口，再把客户端 SDK 接进 App。下面是每一端的最小代码。</p>
        </header>
        <div class="tabs-box">
          <div class="tablist" role="tablist" aria-label="选择平台">
            <button
              v-for="(p, i) in PLATFORMS"
              :key="p"
              :ref="(el) => { if (el) tabRefs[i] = el as HTMLButtonElement }"
              type="button"
              role="tab"
              :id="`tw-tab-${tabIds[p]}`"
              :aria-selected="platform === p"
              aria-controls="tw-code"
              :tabindex="platform === p ? 0 : -1"
              :class="['tab', { on: platform === p }]"
              @click="pick(p)"
              @keydown="onTabKey($event, i)"
            >{{ p }}</button>
          </div>
          <div id="tw-code" class="code mono" role="tabpanel" :aria-labelledby="`tw-tab-${tabIds[platform]}`">
            <button type="button" class="copy" @click="copy">{{ copyLabel }}</button>
            <span class="sr-only" aria-live="polite">{{ copyLabel === "复制" ? "" : copyLabel }}</span>
            <pre tabindex="0"><code ref="codeEl" v-html="currentCode" /></pre>
          </div>
        </div>
        <div class="after">
          <a class="btn btn-ink" :href="withBase('/guide/quickstart')">完整的接入步骤</a>
          <span class="muted">npm 包与 Maven 坐标还没有发布，现在从源码接入；包名在首个公开版本时确定，以手册为准。</span>
        </div>
        <div id="try" class="card raised try" role="group" aria-labelledby="tw-try">
          <div class="try-copy">
            <h3 id="tw-try">先不想搭环境？用云端测试服务</h3>
            <p>imtalk.openedskill.com 上跑着一整套 Talkwire。建一个空间、用两个名字登录，就能互发消息、建群、扫码入群、发图片、撤回、打演示通话；Android 示例和你自己的客户端也能直接连上去。按空间隔离，数据每天北京时间 04:00 清空。</p>
            <p class="mono try-endpoint">wss://imtalk.openedskill.com/ws/im</p>
          </div>
          <div class="try-actions">
            <a class="btn btn-ink" :href="sandbox" target="_blank" rel="noopener">打开在线示例
              <svg width="14" height="14" viewBox="0 0 24 24" aria-hidden="true"><path d="M14 5 H19 V10 M19 5 L11 13 M17 14 V19 H5 V7 H10" /></svg>
            </a>
            <p class="more"><a :href="withBase('/guide/sandbox')">测试服务说明</a></p>
          </div>
        </div>
      </div>
    </section>

    <!-- Modules -->
    <section id="modules" class="band alt" aria-labelledby="tw-mod">
      <div class="wrap stack-48">
        <header class="head">
          <p class="eyebrow">可选模块</p>
          <h2 id="tw-mod" class="display">用得上才装，核心不依赖它们</h2>
          <p class="sub">每个模块单独发布、单独开关。核心的依赖树里不会出现 RTC SDK 或 libsignal，构建时有检查。</p>
        </header>
        <ul class="cards cards-340">
          <li v-for="m in modules" :key="m.title" class="card raised linked">
            <h3>
              <a :href="withBase(m.link)">{{ m.title }}</a>
              <span v-if="m.badge" class="badge mono">{{ m.badge }}</span>
              <span class="go" aria-hidden="true">→</span>
            </h3>
            <p>{{ m.body }}</p>
          </li>
        </ul>
      </div>
    </section>

    <!-- Status -->
    <section id="status" class="band" aria-labelledby="tw-status">
      <div class="wrap stack-40">
        <header class="head">
          <p class="eyebrow">现状</p>
          <h2 id="tw-status" class="display">做到哪里，就写到哪里</h2>
          <p class="sub">首个公开版本之前，接口仍可能调整。每一端验证到什么程度，手册里逐项写明。</p>
        </header>
        <div class="table-box">
          <table>
            <thead>
              <tr><th scope="col">组成部分</th><th scope="col">状态</th><th scope="col">验证</th></tr>
            </thead>
            <tbody>
              <tr v-for="r in status" :key="r.part">
                <td>{{ r.part }}</td>
                <td><span class="state"><span :class="['state-dot', { planned: !r.ok }]" aria-hidden="true" />{{ r.state }}</span></td>
                <td class="muted">{{ r.check }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </div>
    </section>

    <!-- CTA + footer -->
    <section class="dark-band cta" aria-labelledby="tw-cta">
      <div class="wrap cta-grid">
        <div class="stack-16">
          <h2 id="tw-cta" class="display">代码、协议、用例，全部公开</h2>
          <p class="lead small-lead">核心以 Apache-2.0 发布，可以商用、可以改。欢迎提 Issue、补用例、实现别的语言的宿主。</p>
        </div>
        <div class="actions">
          <a class="btn btn-accent" href="#start">开始接入</a>
          <a class="btn btn-ghost" :href="sandbox" target="_blank" rel="noopener">在线试用</a>
          <a class="btn btn-ghost" :href="`${repo}/blob/main/CONTRIBUTING.md`">贡献指南</a>
        </div>
      </div>
      <footer class="foot">
        <div class="wrap foot-grid">
          <div class="stack-10">
            <div class="display foot-name">Talkwire</div>
            <div class="on-dark-3">从小桃子的即时通讯系统提炼出的开源框架。</div>
          </div>
          <nav class="stack-10" aria-label="文档">
            <div class="on-dark-3">文档</div>
            <a :href="withBase('/guide/')">开发手册</a>
            <a :href="withBase('/protocol/spec')">协议规范</a>
            <a :href="withBase('/modules/call')">可选模块</a>
          </nav>
          <nav class="stack-10" aria-label="社区">
            <div class="on-dark-3">社区</div>
            <a :href="repo">GitHub</a>
            <a :href="`${repo}/issues`">提交 Issue</a>
            <a :href="`${repo}/blob/main/CHANGELOG.md`">更新日志</a>
          </nav>
          <div class="stack-10">
            <div class="on-dark-3">许可</div>
            <span>核心 Apache-2.0</span>
            <span>端到端加密模块 AGPLv3</span>
            <span class="on-dark-3">© 2026 Talkwire 贡献者</span>
          </div>
        </div>
      </footer>
    </section>
  </div>
</template>

<style scoped>
.tw {
  --ink: var(--tw-ink);
  --fg: var(--vp-c-text-1);
  --fg-2: var(--vp-c-text-2);
  --line: var(--vp-c-divider);
  --surface: var(--vp-c-bg);
  --surface-alt: var(--vp-c-bg-alt);
  color: var(--fg);
  font-size: 16px;
  line-height: 1.6;
}
.wrap {
  max-width: 1200px;
  margin: 0 auto;
  padding: 0 24px;
}
.display {
  font-family: var(--tw-font-display);
  font-weight: 800;
}
.mono {
  font-family: var(--vp-font-family-mono);
}
.muted {
  color: var(--fg-2);
}
.sr-only {
  position: absolute;
  width: 1px;
  height: 1px;
  overflow: hidden;
  clip: rect(0 0 0 0);
  white-space: nowrap;
}
.stack-48 { display: flex; flex-direction: column; gap: 48px; }
.stack-40 { display: flex; flex-direction: column; gap: 40px; }
.stack-16 { display: flex; flex-direction: column; gap: 16px; }
.stack-10 { display: flex; flex-direction: column; gap: 10px; }

/* Dark bands: hero and closing call to action */
.dark-band {
  background-color: var(--tw-ink);
  color: var(--tw-on-dark);
}
.dark-band a {
  color: var(--tw-on-dark);
}
.on-dark-3 {
  color: var(--tw-on-dark-3);
}
.hero {
  background-image:
    linear-gradient(rgba(124, 156, 255, 0.06) 1px, transparent 1px),
    linear-gradient(90deg, rgba(124, 156, 255, 0.06) 1px, transparent 1px);
  background-size: 48px 48px;
}
.hero-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(min(480px, 100%), 1fr));
  gap: 56px;
  align-items: center;
  padding-top: 88px;
  padding-bottom: 104px;
}
.hero-copy {
  display: flex;
  flex-direction: column;
  gap: 28px;
}
.eyebrow {
  margin: 0;
  display: flex;
  align-items: center;
  gap: 10px;
  font-size: 13px;
  font-weight: 500;
  letter-spacing: 0.04em;
  color: var(--fg-2);
}
.eyebrow.on-dark {
  color: var(--tw-on-dark-3);
}
.dot {
  width: 8px;
  height: 8px;
  border-radius: 4px;
  background: var(--tw-accent);
}
h1.display {
  margin: 0;
  font-size: clamp(40px, 5.4vw, 68px);
  line-height: 1.08;
  letter-spacing: -0.02em;
  text-wrap: balance;
}
.lead {
  margin: 0;
  font-size: 18px;
  line-height: 1.75;
  color: var(--tw-on-dark-2);
  max-width: 560px;
  text-wrap: pretty;
}
.small-lead {
  font-size: 17px;
}
.actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 12px;
}
.btn {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  height: 48px;
  padding: 0 22px;
  border-radius: 8px;
  font-weight: 500;
  text-decoration: none;
  transition: background-color 0.15s, border-color 0.15s;
}
.btn svg {
  fill: none;
  stroke: currentColor;
  stroke-width: 2.2;
  stroke-linecap: round;
  stroke-linejoin: round;
}
.dark-band .btn-accent,
.btn-accent {
  background: var(--tw-accent);
  color: var(--tw-ink);
  font-weight: 600;
}
.btn-accent:hover {
  background: #e09608;
}
.btn-ghost {
  border: 1px solid #3a4766;
}
.btn-ghost:hover {
  border-color: #5a6888;
}
.btn-ink {
  height: 44px;
  padding: 0 18px;
  background: var(--tw-ink);
  color: #fff;
}
.dark .btn-ink {
  background: var(--tw-accent);
  color: var(--tw-ink);
}
.btn:focus-visible,
.tab:focus-visible,
.copy:focus-visible,
pre:focus-visible,
.tw a:focus-visible {
  outline: 2px solid var(--tw-accent);
  outline-offset: 2px;
}
.repo {
  font-size: 13px;
  color: var(--tw-on-dark-3) !important;
  text-decoration: none;
  width: fit-content;
}
.repo:hover {
  color: var(--tw-on-dark) !important;
}

/* The wire */
.wire {
  margin: 0;
  background: var(--tw-panel);
  border: 1px solid var(--tw-border-dark);
  border-radius: 12px;
  overflow: hidden;
  box-shadow: 0 24px 64px rgba(0, 0, 0, 0.35);
  min-width: 0;
}
.wire-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 12px 16px;
  border-bottom: 1px solid var(--tw-border-dark);
  font-size: 12px;
  color: var(--tw-on-dark-3);
}
.live {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}
.live-dot {
  width: 6px;
  height: 6px;
  border-radius: 3px;
  background: #3dd6a3;
}
.wire-body {
  list-style: none;
  margin: 0;
  padding: 20px 20px 24px;
  display: flex;
  flex-direction: column;
  gap: 12px;
  font-size: 13px;
  line-height: 1.65;
  color: var(--tw-on-dark);
  word-break: break-all;
}
.frame {
  display: flex;
  gap: 10px;
  opacity: 0;
  animation: tw-in 0.45s ease forwards;
}
.arrow-out {
  color: var(--tw-blue);
}
.arrow-in {
  color: var(--tw-accent);
}
.note {
  color: #8a95a8;
}
@keyframes tw-in {
  from { opacity: 0; transform: translateY(4px); }
  to { opacity: 1; transform: none; }
}
@media (prefers-reduced-motion: reduce) {
  .frame {
    opacity: 1;
    animation: none;
  }
}

/* Proof strip */
.proof {
  border-bottom: 1px solid var(--line);
}
.proof-grid {
  display: grid;
  grid-template-columns: 1fr;
  gap: 32px;
  padding-top: 40px;
  padding-bottom: 40px;
}
@media (min-width: 560px) {
  .proof-grid { grid-template-columns: repeat(2, 1fr); }
}
@media (min-width: 960px) {
  .proof-grid { grid-template-columns: repeat(4, 1fr); }
}
.stat {
  display: flex;
  flex-direction: column;
  gap: 6px;
  font-size: 15px;
}
.stat-value {
  font-size: 32px;
  line-height: 1.2;
  color: var(--fg);
}

/* Bands */
.band {
  padding: 104px 0;
}
.band.alt {
  background: var(--surface-alt);
  border-top: 1px solid var(--line);
  border-bottom: 1px solid var(--line);
}
.band + .band.alt,
.band.alt + .band {
  border-top: none;
}
.head {
  display: flex;
  flex-direction: column;
  gap: 16px;
  max-width: 760px;
}
h2.display {
  margin: 0;
  font-size: clamp(30px, 3.4vw, 44px);
  line-height: 1.15;
  letter-spacing: -0.01em;
  text-wrap: balance;
  border: none;
  padding: 0;
}
.sub {
  margin: 0;
  font-size: 17px;
  color: var(--fg-2);
  text-wrap: pretty;
}
.more {
  margin: 0;
  font-size: 15px;
}
.more a {
  color: var(--vp-c-brand-1);
  text-decoration: underline;
  text-underline-offset: 3px;
}

/* Architecture diagram */
.diagram-box {
  overflow-x: auto;
  background: var(--surface);
  border: 1px solid var(--line);
  border-radius: 12px;
}
.diagram {
  display: block;
  width: 100%;
  min-width: 760px;
  height: auto;
  font-family: var(--vp-font-family-base);
  font-size: 15px;
  fill: var(--fg);
}
.diagram .lbl {
  font-size: 13px;
  fill: var(--fg-2);
}
.diagram .small {
  font-size: 13px;
  fill: var(--fg-2);
}
.diagram .strong {
  font-weight: 600;
}
.diagram .big {
  font-size: 17px;
}
.diagram .dim {
  fill: var(--fg-2);
}
.diagram .box {
  fill: var(--surface);
  stroke: #c9d0dc;
}
.dark .diagram .box {
  stroke: #3a4766;
}
.diagram .box.planned {
  fill: var(--surface-alt);
  stroke: #a3adbf;
  stroke-dasharray: 5 4;
}
.diagram .box.core {
  fill: var(--tw-ink);
  stroke: var(--tw-ink);
}
.dark .diagram .box.core {
  fill: #18213a;
  stroke: #3a4766;
}
.diagram .on-core {
  fill: var(--tw-on-dark);
}
.diagram .edge {
  fill: none;
  stroke: #586274;
  stroke-width: 2;
}
.dark .diagram .edge {
  stroke: #a3adbf;
}
.diagram .edge.dashed {
  stroke: #8a95a8;
  stroke-dasharray: 6 6;
}
.diagram .head-fill {
  fill: #586274;
}
.dark .diagram .head-fill {
  fill: #a3adbf;
}

/* Narrow screens get the same picture as a vertical flow instead of a wide SVG. */
.flow {
  display: none;
  list-style: none;
  margin: 0;
  padding: 0;
  flex-direction: column;
  align-items: stretch;
  gap: 8px;
}
.flow .node {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 16px 18px;
  background: var(--surface);
  border: 1px solid var(--line);
  border-radius: 10px;
  font-size: 15px;
}
.flow .node.core {
  background: var(--tw-ink);
  border-color: var(--tw-ink);
  color: var(--tw-on-dark);
}
.dark .flow .node.core {
  background: #18213a;
  border-color: #3a4766;
}
.flow .node-label {
  font-size: 12px;
  color: var(--fg-2);
}
.flow .node.core .node-label {
  color: var(--tw-on-dark-3);
}
.flow .link {
  align-self: center;
  font-size: 13px;
  color: var(--fg-2);
}
@media (max-width: 640px) {
  .diagram-box {
    display: none;
  }
  .flow {
    display: flex;
  }
}

/* Cards */
.cards {
  list-style: none;
  margin: 0;
  padding: 0;
  display: grid;
  gap: 16px;
}
.cards-320 {
  grid-template-columns: repeat(auto-fit, minmax(min(320px, 100%), 1fr));
}
.cards-340 {
  grid-template-columns: repeat(auto-fit, minmax(min(340px, 100%), 1fr));
}
.card {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 28px;
  border: 1px solid var(--line);
  border-radius: 12px;
}
.card.raised {
  background: var(--surface);
}
.card h3 {
  margin: 0;
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 18px;
  font-weight: 600;
  line-height: 1.4;
}
.card p {
  margin: 0;
  font-size: 15px;
  color: var(--fg-2);
}
.card .ref {
  font-size: 13px;
  padding-top: 2px;
}
.card.linked {
  position: relative;
  transition: border-color 0.15s;
}
.card.linked:hover {
  border-color: var(--tw-accent);
}
.card.linked h3 a {
  color: var(--fg);
  text-decoration: none;
}
.card.linked h3 a::after {
  content: "";
  position: absolute;
  inset: 0;
  border-radius: 12px;
}
.go {
  margin-left: auto;
  color: var(--fg-2);
  font-weight: 400;
  transition: transform 0.15s, color 0.15s;
}
.card.linked:hover .go {
  transform: translateX(3px);
  color: var(--fg);
}
.badge {
  font-size: 12px;
  font-weight: 400;
  padding: 2px 8px;
  border: 1px solid #a3adbf;
  border-radius: 999px;
  color: var(--fg-2);
}

/* Sequence rail */
.rail {
  display: flex;
  gap: 6px;
  font-size: 13px;
}
.seq {
  width: 44px;
  height: 36px;
  display: flex;
  align-items: center;
  justify-content: center;
  border: 1px solid #a3adbf;
  border-radius: 6px;
}
.seq.recalled {
  border-style: dashed;
  color: var(--fg-2);
}
.seq.new {
  border-color: var(--tw-ink);
  background: var(--tw-ink);
  color: #fff;
}
.dark .seq.new {
  border-color: var(--tw-accent);
  background: var(--tw-accent);
  color: var(--tw-ink);
}

/* Quick start */
.tabs-box {
  display: flex;
  flex-direction: column;
  border: 1px solid var(--line);
  border-radius: 12px;
  overflow: hidden;
}
.tablist {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
  padding: 8px;
  background: var(--surface-alt);
  border-bottom: 1px solid var(--line);
}
.tab {
  height: 40px;
  padding: 0 16px;
  border-radius: 8px;
  border: 1px solid transparent;
  font-size: 15px;
  color: var(--fg-2);
  cursor: pointer;
}
.tab:hover {
  color: var(--fg);
}
.tab.on {
  border-color: var(--tw-ink);
  background: var(--tw-ink);
  color: #fff;
  font-weight: 500;
}
.dark .tab.on {
  border-color: var(--tw-accent);
  background: var(--tw-accent);
  color: var(--tw-ink);
}
.code {
  position: relative;
  font-variant-ligatures: none;
  background: var(--tw-panel);
  color: var(--tw-on-dark);
  font-size: 14px;
  line-height: 1.75;
}
.code pre {
  margin: 0;
  padding: 24px 28px;
  overflow-x: auto;
}
.code :deep(.c) { color: #8a95a8; }
.code :deep(.k) { color: var(--tw-blue); }
.code :deep(.s) { color: #f2c46b; }
.copy {
  position: absolute;
  right: 12px;
  top: 12px;
  height: 32px;
  padding: 0 12px;
  border-radius: 6px;
  border: 1px solid #2a3550;
  background: var(--tw-ink-2);
  color: var(--tw-on-dark-2);
  font-family: var(--vp-font-family-base);
  font-size: 13px;
  cursor: pointer;
}
.copy:hover {
  color: var(--tw-on-dark);
}
.after {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 12px;
  font-size: 15px;
}

/* The public test service, under the quick start */
.card.try {
  flex-direction: row;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 24px;
  border-left: 3px solid var(--tw-accent);
}
.try-copy {
  display: flex;
  flex-direction: column;
  gap: 10px;
  flex: 1 1 520px;
  max-width: 760px;
}
.card .try-endpoint {
  font-size: 13px;
  overflow-wrap: anywhere;
}
.try-actions {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 12px;
}

/* Status table */
.table-box {
  overflow-x: auto;
  border: 1px solid var(--line);
  border-radius: 12px;
}
table {
  width: 100%;
  min-width: 640px;
  border-collapse: collapse;
  font-size: 15px;
  display: table;
  margin: 0;
}
th {
  background: var(--surface-alt);
  text-align: left;
  font-weight: 600;
}
th,
td {
  padding: 14px 20px;
  border: none;
}
tbody tr {
  border-top: 1px solid var(--line);
  background: transparent;
}
.state {
  display: inline-flex;
  align-items: center;
  gap: 8px;
}
.state-dot {
  width: 8px;
  height: 8px;
  border-radius: 4px;
  background: var(--tw-green);
}
.state-dot.planned {
  background: transparent;
  border: 1.5px solid var(--fg-2);
  box-sizing: border-box;
}

/* CTA and footer */
.cta-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(min(420px, 100%), 1fr));
  gap: 48px;
  align-items: center;
  padding-top: 96px;
  padding-bottom: 96px;
}
.foot {
  border-top: 1px solid #1e2740;
}
.foot-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(min(200px, 100%), 1fr));
  gap: 32px;
  padding-top: 48px;
  padding-bottom: 56px;
  font-size: 14px;
}
.foot-grid a {
  text-decoration: none;
  width: fit-content;
}
.foot-grid a:hover {
  text-decoration: underline;
  text-underline-offset: 3px;
}
.foot-name {
  font-size: 18px;
}

@media (max-width: 640px) {
  .hero-grid {
    padding-top: 56px;
    padding-bottom: 72px;
    gap: 40px;
  }
  .band {
    padding: 72px 0;
  }
  .card {
    padding: 22px;
  }
  .code pre {
    padding: 56px 20px 20px;
  }
}
</style>
