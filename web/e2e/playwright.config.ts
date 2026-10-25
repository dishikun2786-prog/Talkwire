import { defineConfig } from "@playwright/test";
import { baseURL } from "./stack.js";

// Runs on the locally installed Microsoft Edge (`channel: "msedge"`), so no
// browser download is needed. Set E2E_CHANNEL=chrome (or "") to use another.
export default defineConfig({
  testDir: ".",
  testMatch: "*.spec.ts",
  globalSetup: "./global-setup.ts",
  timeout: 90_000,
  expect: { timeout: 15_000 },
  // One stack, shared accounts across files: run the files one at a time.
  workers: 1,
  fullyParallel: false,
  retries: 0,
  reporter: [["list"], ["html", { open: "never", outputFolder: "../playwright-report" }]],
  outputDir: "../test-results",
  use: {
    baseURL,
    channel: process.env.E2E_CHANNEL ?? "msedge",
    headless: process.env.E2E_HEADED ? false : true,
    locale: "en-US",
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
  },
});
