import { defineConfig } from "@playwright/test";
import path from "path";
import { fileURLToPath } from "url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));

// The browser half of #2373's acceptance criteria: every skeleton the
// visual-libraries knowledge page carries, saved as an asset, drawn on the
// asset page, an authenticated share and a public share, in light and dark,
// with nothing refused by the page's policy. It is run by
// test/acceptance/issue_2373_test.go against the dev stack, which saves the
// assets, makes the shares and names them in VISUALS_* variables. The
// libraries load from public CDNs, so the run needs network egress.
export default defineConfig({
  testDir: __dirname,
  testMatch: "*.spec.ts",
  timeout: 180_000,
  expect: { timeout: 30_000 },
  use: {
    baseURL: process.env["VISUALS_BASE_URL"],
    viewport: { width: 1280, height: 900 },
  },
  workers: 1,
  reporter: [["list"]],
});
