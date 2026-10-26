import { defineConfig, type DefaultTheme } from "vitepress";

const repo = "https://github.com/dishikun2786-prog/Talkwire";

// Chinese has no spaces between words, so the default tokenizer would index a
// whole sentence as one token. Intl.Segmenter splits it into words; latin
// identifiers (sync_summary, JWT_SECRET) stay whole. VitePress ships this
// function to the browser as source text, so it must not use anything from the
// enclosing module: the segmenter is created inside and cached on globalThis.
function tokenize(text: string): string[] {
  const g = globalThis as { __talkwireSegmenter?: Intl.Segmenter };
  const segmenter = (g.__talkwireSegmenter ??= new Intl.Segmenter("zh-CN", { granularity: "word" }));
  const out: string[] = [];
  for (const part of text.split(/[^\p{L}\p{N}_]+/u)) {
    if (!part) continue;
    if (/^[\x00-\x7f]+$/.test(part)) {
      out.push(part);
      continue;
    }
    for (const s of segmenter.segment(part)) if (s.isWordLike) out.push(s.segment);
  }
  return out;
}

const sidebar: DefaultTheme.SidebarItem[] = [
  {
    text: "入门",
    items: [
      { text: "简介", link: "/guide/" },
      { text: "架构与概念", link: "/guide/architecture" },
      { text: "快速开始", link: "/guide/quickstart" },
      { text: "术语表", link: "/guide/glossary" },
    ],
  },
  {
    text: "服务端",
    collapsed: false,
    items: [
      { text: "安装与配置", link: "/server/" },
      { text: "环境变量", link: "/server/env" },
      { text: "数据库与迁移", link: "/server/database" },
      { text: "部署与运维", link: "/server/operations" },
      { text: "压测", link: "/server/loadtest" },
    ],
  },
  {
    text: "宿主接入",
    collapsed: false,
    items: [
      { text: "宿主接口契约", link: "/host/" },
      { text: "签发 IM 令牌", link: "/host/token" },
      { text: "Node 宿主 SDK", link: "/host/node-sdk" },
      { text: "用其他语言实现", link: "/host/other-languages" },
    ],
  },
  {
    text: "客户端",
    collapsed: false,
    items: [
      { text: "Web SDK", link: "/client/web" },
      { text: "React 界面套件", link: "/client/react" },
      { text: "Android SDK", link: "/client/android" },
      { text: "Compose 界面套件", link: "/client/compose" },
      { text: "本地存储与加密", link: "/client/storage" },
      { text: "自己的消息类型", link: "/client/message-types" },
    ],
  },
  {
    text: "协议",
    collapsed: false,
    items: [
      { text: "概述", link: "/protocol/" },
      { text: "legacy 与 strict", link: "/protocol/profiles" },
      { text: "帧格式与错误码", link: "/protocol/frames" },
      { text: "同步与补拉", link: "/protocol/sync" },
      { text: "多端与推送", link: "/protocol/devices-push" },
      { text: "已知缺陷", link: "/protocol/defects" },
      { text: "规范全文", link: "/protocol/spec" },
    ],
  },
  {
    text: "可选模块",
    collapsed: false,
    items: [
      { text: "音视频通话", link: "/modules/call" },
      { text: "扫码入群", link: "/modules/group-join" },
      { text: "群助手钩子", link: "/modules/bot-hook" },
      { text: "端到端加密", link: "/modules/e2ee" },
      { text: "视频压缩", link: "/modules/video-compress" },
      { text: "后台页面", link: "/modules/admin" },
    ],
  },
  {
    text: "迁移",
    collapsed: true,
    items: [{ text: "从现有 IM 系统迁移", link: "/migrate/" }],
  },
  {
    text: "参与贡献",
    collapsed: true,
    items: [
      { text: "开发环境", link: "/contributing/" },
      { text: "测试与红线", link: "/contributing/testing" },
      { text: "发布流程", link: "/contributing/release" },
    ],
  },
];

export default defineConfig({
  lang: "zh-CN",
  title: "Talkwire",
  titleTemplate: ":title · Talkwire",
  description: "自己部署的开源即时通讯框架：Go 实时服务端、Android 与 Web 客户端 SDK、一份可验证的线协议。",
  cleanUrls: true,
  lastUpdated: true,
  srcExclude: ["README.md"],
  head: [
    ["link", { rel: "icon", type: "image/svg+xml", href: "/logo.svg" }],
    ["meta", { name: "theme-color", content: "#0B1020" }],
    ["meta", { property: "og:title", content: "Talkwire · 开源即时通讯框架" }],
    ["meta", { property: "og:description", content: "Go 实时服务端、Android 与 Web SDK、一份可验证的线协议。" }],
  ],
  markdown: {
    theme: { light: "github-light", dark: "github-dark" },
  },
  // The local search index holds every page, the spec included (about 530 kB,
  // about 100 kB compressed). It loads only when a reader opens search.
  vite: { build: { chunkSizeWarningLimit: 800 } },
  themeConfig: {
    logo: "/logo.svg",
    nav: [
      { text: "开发手册", link: "/guide/", activeMatch: "^/(guide|server|host|client|migrate|contributing)/" },
      { text: "协议", link: "/protocol/", activeMatch: "^/protocol/" },
      { text: "可选模块", link: "/modules/call", activeMatch: "^/modules/" },
      { text: "更新日志", link: `${repo}/blob/main/CHANGELOG.md` },
    ],
    sidebar: {
      "/guide/": sidebar,
      "/server/": sidebar,
      "/host/": sidebar,
      "/client/": sidebar,
      "/protocol/": sidebar,
      "/modules/": sidebar,
      "/migrate/": sidebar,
      "/contributing/": sidebar,
    },
    socialLinks: [{ icon: "github", link: repo }],
    editLink: { pattern: `${repo}/edit/main/site/:path`, text: "在 GitHub 上编辑此页" },
    lastUpdated: { text: "最后更新", formatOptions: { dateStyle: "medium" } },
    outline: { level: [2, 3], label: "本页内容" },
    docFooter: { prev: "上一篇", next: "下一篇" },
    returnToTopLabel: "回到顶部",
    sidebarMenuLabel: "目录",
    darkModeSwitchLabel: "外观",
    lightModeSwitchTitle: "切换到浅色",
    darkModeSwitchTitle: "切换到深色",
    langMenuLabel: "语言",
    notFound: { title: "页面不存在", quote: "这个地址没有内容，可能已经移动或删除。", linkText: "回到首页" },
    search: {
      provider: "local",
      options: {
        miniSearch: {
          options: { tokenize },
          searchOptions: { tokenize, prefix: true, fuzzy: 0.15, combineWith: "AND" },
        },
        translations: {
          button: { buttonText: "搜索文档", buttonAriaLabel: "搜索文档" },
          modal: {
            displayDetails: "显示详细列表",
            resetButtonTitle: "清除",
            backButtonTitle: "关闭搜索",
            noResultsText: "没有找到相关结果",
            footer: { selectText: "打开", navigateText: "选择", closeText: "关闭" },
          },
        },
      },
    },
    footer: {
      message: "核心以 Apache-2.0 发布；端到端加密模块为 AGPLv3。",
      copyright: "© 2026 Talkwire 贡献者",
    },
  },
});
