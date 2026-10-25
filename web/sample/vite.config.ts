import react from "@vitejs/plugin-react";
import { fileURLToPath } from "node:url";
import { defineConfig } from "vite";

const src = (pkg: string) => fileURLToPath(new URL(`../packages/${pkg}/src/index.ts`, import.meta.url));
const host = process.env.SAMPLE_HOST_URL ?? "http://127.0.0.1:6001";
const im = process.env.SAMPLE_IM_URL ?? "http://127.0.0.1:6062";

// The dev server proxies the sample host (sign-in, contacts, media) and the
// IM server's WebSocket, so the page talks to one origin.
export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      "@imkit/protocol": src("protocol"),
      "@imkit/client": src("client"),
      "@imkit/storage-idb": src("storage-idb"),
      "@imkit/react": src("react"),
    },
  },
  server: {
    host: "127.0.0.1",
    port: Number(process.env.SAMPLE_PORT ?? 5173),
    strictPort: true,
    proxy: {
      "/dev": host,
      "/api": host,
      "/media": host,
      "/ws": { target: im, ws: true },
    },
  },
});
