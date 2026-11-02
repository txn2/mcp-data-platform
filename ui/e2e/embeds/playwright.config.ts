import { defineConfig } from "@playwright/test";
import path from "path";
import { fileURLToPath } from "url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));

// The browser half of #2069's and #2395's acceptance criteria: an HTML asset
// carrying video, on the asset page signed in and its public share signed out.
// Run by test/acceptance/issue_2069_test.go and issue_2395_test.go against the
// dev stack, which save the assets, set the embed hosts and name the cases in
// EMBED_* variables.
export default defineConfig({
  testDir: __dirname,
  testMatch: "*.spec.ts",
  timeout: 240_000,
  expect: { timeout: 30_000 },
  use: {
    // Google Chrome rather than Playwright's Chromium: Chromium builds carry no
    // H.264 decoder, and the clips a deck embeds are H.264 MP4s.
    channel: "chrome",
    baseURL: process.env["EMBED_BASE_URL"],
    viewport: { width: 1280, height: 900 },
    screenshot: "only-on-failure",
    actionTimeout: 30_000,
    navigationTimeout: 30_000,
  },
  outputDir: path.join(__dirname, "../../../build/embeds-results"),
  workers: 1,
  reporter: [["list"]],
});
