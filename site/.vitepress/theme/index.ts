import DefaultTheme from "vitepress/theme-without-fonts";
import type { Theme } from "vitepress";
import { inBrowser } from "vitepress";
import "@fontsource/ibm-plex-sans/400.css";
import "@fontsource/ibm-plex-sans/500.css";
import "@fontsource/ibm-plex-sans/600.css";
import "@fontsource/jetbrains-mono/400.css";
import "@fontsource/jetbrains-mono/500.css";
import "@fontsource-variable/bricolage-grotesque/index.css";
import "./style.css";
import HomePage from "./components/HomePage.vue";
import { rememberPlatform, applyPlatform } from "./platform";

export default {
  extends: DefaultTheme,
  enhanceApp({ app, router }) {
    app.component("HomePage", HomePage);
    if (!inBrowser) return;
    rememberPlatform();
    const after = router.onAfterRouteChange;
    router.onAfterRouteChange = (to) => {
      after?.(to);
      requestAnimationFrame(() => applyPlatform());
    };
    requestAnimationFrame(() => applyPlatform());
  },
} satisfies Theme;
