import { fileURLToPath } from "node:url";
import { defineConfig } from "vitest/config";

const src = (pkg: string) => fileURLToPath(new URL(`./packages/${pkg}/src/index.ts`, import.meta.url));

export default defineConfig({
  esbuild: { jsx: "automatic" },
  resolve: {
    // Tests run against the sources, not a stale dist/.
    alias: {
      "@imkit/protocol": src("protocol"),
      "@imkit/client": src("client"),
      "@imkit/storage-idb": src("storage-idb"),
      "@imkit/react": src("react"),
    },
  },
  test: {
    include: ["packages/*/test/**/*.test.ts", "packages/*/test/**/*.test.tsx", "sample/test/**/*.test.tsx"],
    testTimeout: 60_000,
    hookTimeout: 30_000,
  },
});
