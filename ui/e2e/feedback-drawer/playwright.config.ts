import { defineConfig } from "@playwright/test";
import path from "path";
import { fileURLToPath } from "url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));

// The browser half of #2580's and #2546's acceptance criteria, run by
// test/acceptance/issue_2580_test.go and issue_2546_test.go against the dev
// stack. A layout overflow needs a real browser: vitest's DOM has no layout.
export default defineConfig({
  testDir: __dirname,
  testMatch: "*.spec.ts",
  timeout: 90_000,
  expect: { timeout: 20_000 },
  use: {
    baseURL: process.env["FEEDBACK_BASE_URL"],
    viewport: { width: 1280, height: 800 },
  },
  workers: 1,
  reporter: [["list"]],
});
