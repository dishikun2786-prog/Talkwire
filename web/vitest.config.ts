import { fileURLToPath } from "node:url";
import { defineConfig } from "vitest/config";

const src = (pkg: string, file = "index.ts") => fileURLToPath(new URL(`./packages/${pkg}/src/${file}`, import.meta.url));

export default defineConfig({
  esbuild: { jsx: "automatic" },
  resolve: {
    // Tests run against the sources, not a stale dist/.
    alias: {
      "@talkwire/protocol": src("protocol"),
      "@talkwire/client": src("client"),
      "@talkwire/storage-idb": src("storage-idb"),
      "@talkwire/react": src("react"),
      // A subpath first: "@talkwire/group-join" would also match it as a prefix.
      "@talkwire/group-join/react": src("group-join", "react.tsx"),
      "@talkwire/group-join": src("group-join"),
      "@talkwire/call/react": src("call", "react.tsx"),
      "@talkwire/call": src("call"),
      "@talkwire/admin": src("admin"),
      "@talkwire/call-dingrtc": src("call-dingrtc"),
    },
  },
  test: {
    include: ["packages/*/test/**/*.test.ts", "packages/*/test/**/*.test.tsx", "sample/test/**/*.test.tsx"],
    testTimeout: 60_000,
    hookTimeout: 30_000,
  },
});
