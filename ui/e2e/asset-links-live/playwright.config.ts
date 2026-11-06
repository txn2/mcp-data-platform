import { defineConfig } from "@playwright/test";
import path from "path";
import { fileURLToPath } from "url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));

// The browser half of #2501's acceptance criteria: links in an HTML, JSX and
// Markdown asset, on the asset page signed in and on its public share signed
// out. Run by test/acceptance/issue_2501_test.go against the dev stack, which
// saves the assets, makes the shares and names the cases in LINKS_* variables.
export default defineConfig({
  testDir: __dirname,
  testMatch: "*.spec.ts",
  timeout: 120_000,
  expect: { timeout: 30_000 },
  use: {
    baseURL: process.env["LINKS_BASE_URL"],
    viewport: { width: 1280, height: 900 },
    screenshot: "only-on-failure",
    actionTimeout: 30_000,
    navigationTimeout: 30_000,
  },
  outputDir: path.join(__dirname, "../../../build/asset-links-results"),
  workers: 1,
  reporter: [["list"]],
});
