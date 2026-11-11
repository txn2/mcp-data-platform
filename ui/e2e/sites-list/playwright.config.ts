import { defineConfig } from "@playwright/test";
import path from "path";
import { fileURLToPath } from "url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));

// The browser half of #2587's acceptance criteria, against the dev stack: a
// row opening from a click and from Enter, a link inside it that does not, and
// a layout that does not overflow at 375px need a real browser; vitest's DOM
// has no layout and no focus ring.
export default defineConfig({
  testDir: __dirname,
  testMatch: "*.spec.ts",
  timeout: 90_000,
  expect: { timeout: 20_000 },
  use: {
    baseURL: process.env["SITES_BASE_URL"],
    viewport: { width: 1280, height: 800 },
  },
  workers: 1,
  reporter: [["list"]],
});
