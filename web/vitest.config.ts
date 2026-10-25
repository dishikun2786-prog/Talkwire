import { fileURLToPath } from "node:url";
import { defineConfig } from "vitest/config";

const src = (pkg: string, file = "index.ts") => fileURLToPath(new URL(`./packages/${pkg}/src/${file}`, import.meta.url));

export default defineConfig({
  esbuild: { jsx: "automatic" },
  resolve: {
    // Tests run against the sources, not a stale dist/.
    alias: {
      "@imkit/protocol": src("protocol"),
      "@imkit/client": src("client"),
      "@imkit/storage-idb": src("storage-idb"),
      "@imkit/react": src("react"),
      // A subpath first: "@imkit/group-join" would also match it as a prefix.
      "@imkit/group-join/react": src("group-join", "react.tsx"),
      "@imkit/group-join": src("group-join"),
      "@imkit/call/react": src("call", "react.tsx"),
      "@imkit/call": src("call"),
      "@imkit/admin": src("admin"),
      "@imkit/call-dingrtc": src("call-dingrtc"),
    },
  },
  test: {
    include: ["packages/*/test/**/*.test.ts", "packages/*/test/**/*.test.tsx", "sample/test/**/*.test.tsx"],
    testTimeout: 60_000,
    hookTimeout: 30_000,
  },
});
