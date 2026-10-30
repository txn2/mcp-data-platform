import { defineConfig } from "@playwright/test";
import path from "path";
import { fileURLToPath } from "url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));

// The browser half of #2303's and #2296's acceptance criteria: a Graphviz DOT
// source and a Vega-Lite spec saved as assets, drawn on the asset page, an
// authenticated share and a public share signed out, with the source one
// toggle away. Run by test/acceptance/issue_2303_test.go and
// issue_2296_test.go against the dev stack, which save the assets, make the
// shares and name them in DRAWN_* variables.
export default defineConfig({
  testDir: __dirname,
  testMatch: "*.spec.ts",
  timeout: 180_000,
  expect: { timeout: 30_000 },
  use: {
    baseURL: process.env["DRAWN_BASE_URL"],
    viewport: { width: 1280, height: 900 },
    screenshot: "only-on-failure",
    actionTimeout: 30_000,
    navigationTimeout: 30_000,
  },
  outputDir: path.join(__dirname, "../../../build/drawn-sources-results"),
  workers: 1,
  reporter: [["list"]],
});
