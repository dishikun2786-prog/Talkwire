import path from "node:path";
import { buildSandboxHost } from "./sandbox.ts";

const secret = process.env.JWT_SECRET;
if (!secret || secret.length < 32) {
  console.error("Set JWT_SECRET (at least 32 characters), the same value the IM server uses.");
  process.exit(1);
}

const app = await buildSandboxHost({
  secret,
  imBaseUrl: process.env.IM_BASE_URL ?? "http://127.0.0.1:6062",
  mediaDir: process.env.MEDIA_DIR ?? path.resolve("data/media"),
  resetTime: process.env.SANDBOX_RESET_TIME,
  docsUrl: process.env.SANDBOX_DOCS_URL,
});
// Behind a proxy on the same machine; never on a public address by itself.
await app.listen({ host: process.env.HOST ?? "127.0.0.1", port: Number(process.env.PORT ?? 6001) });
