import { defineConfig } from "@playwright/test";
import path from "path";
import { fileURLToPath } from "url";
import { acceptanceOutput } from "../acceptanceOutput";

const __dirname = path.dirname(fileURLToPath(import.meta.url));

// The browser half of #2837's acceptance criteria: the Resources page over a
// library of 50,000 folders and 100,000 files. Run by
// test/acceptance/issue_2837_test.go against the dev stack, which seeds the
// library and names it in SCALE_CASE.
export default defineConfig({
  testDir: __dirname,
  testMatch: "*.spec.ts",
  timeout: 180_000,
  expect: { timeout: 30_000 },
  use: {
    baseURL: process.env["SCALE_BASE_URL"],
    viewport: { width: 1440, height: 900 },
    screenshot: "only-on-failure",
    actionTimeout: 30_000,
    navigationTimeout: 60_000,
  },
  outputDir: acceptanceOutput("resources-scale"),
  workers: 1,
  reporter: [["list"]],
});
