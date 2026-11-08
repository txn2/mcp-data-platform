import { defineConfig } from "@playwright/test";
import path from "path";
import { fileURLToPath } from "url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));

// The browser half of #2530's acceptance criteria: Download of an HTML or JSX
// asset from the asset page and from its shares, each file opened from disk
// with the network off. Run by test/acceptance/issue_2530_test.go against the
// dev stack, which saves the assets, makes the shares and names the cases in
// SA_* variables.
export default defineConfig({
  testDir: __dirname,
  testMatch: "*.spec.ts",
  timeout: 180_000,
  expect: { timeout: 30_000 },
  use: {
    baseURL: process.env["SA_BASE_URL"],
    viewport: { width: 1280, height: 900 },
    screenshot: "only-on-failure",
    actionTimeout: 30_000,
    navigationTimeout: 30_000,
    acceptDownloads: true,
  },
  outputDir: path.join(__dirname, "../../../build/standalone-results"),
  workers: 1,
  reporter: [["list"]],
});
