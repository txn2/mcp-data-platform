import { defineConfig } from "@playwright/test";
import path from "path";
import { fileURLToPath } from "url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));

// The browser half of #2435's acceptance criteria, against the dev stack: the
// Automations list's Type filter and type badges, and a schedule added from a
// script's Handlers section, need the real portal.
export default defineConfig({
  testDir: __dirname,
  testMatch: "*.spec.ts",
  timeout: 90_000,
  expect: { timeout: 20_000 },
  use: {
    baseURL: process.env["HANDLERS_BASE_URL"],
    viewport: { width: 1280, height: 800 },
  },
  workers: 1,
  reporter: [["list"]],
});
