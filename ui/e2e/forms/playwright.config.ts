import { defineConfig } from "@playwright/test";
import path from "path";
import { fileURLToPath } from "url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));

// The browser half of #2177's and #2180's acceptance criteria: forms embedded
// in a shared asset, submitted by an anonymous browser, and the form page in
// light and dark at desktop and phone width. It is run by
// test/acceptance/issue_2177_test.go and issue_2180_test.go against the dev
// stack, which point it at what they made through FORMS_* variables. Nothing
// else runs it.
export default defineConfig({
  testDir: __dirname,
  testMatch: "*.spec.ts",
  timeout: 120_000,
  expect: { timeout: 30_000 },
  use: {
    baseURL: process.env["FORMS_BASE_URL"],
    viewport: { width: 1280, height: 900 },
  },
  workers: 1,
  reporter: [["list"]],
});
