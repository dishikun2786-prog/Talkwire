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
