import react from "@vitejs/plugin-react";
import { fileURLToPath } from "node:url";
import { defineConfig } from "vite";

const src = (pkg: string, file = "index.ts") => fileURLToPath(new URL(`../packages/${pkg}/src/${file}`, import.meta.url));
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
      // A subpath first: "@imkit/group-join" would also match it as a prefix.
      "@imkit/group-join/react": src("group-join", "react.tsx"),
      "@imkit/group-join": src("group-join"),
      "@imkit/call/react": src("call", "react.tsx"),
      "@imkit/call": src("call"),
      "@imkit/admin": src("admin"),
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
