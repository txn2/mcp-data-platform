import { defineConfig } from "@playwright/test";
import path from "path";
import { fileURLToPath } from "url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));

// Interactive end-to-end tests for the admin observability dashboards.
// Runs against the MSW mock server (rich deterministic data) so every tab,
// time-range preset, and drilldown is exercised without a live backend.
// Reuses an already-running `make frontend-mock` server on :5173.
export default defineConfig({
  testDir: __dirname,
  testMatch: "*.spec.ts",
  timeout: 60_000,
  expect: { timeout: 10_000 },
  use: {
    baseURL: "http://localhost:5173",
    viewport: { width: 1440, height: 900 },
    reducedMotion: "reduce",
  },
  workers: 1,
  retries: process.env["CI"] ? 1 : 0,
  reporter: [["list"]],
  webServer: {
    command: "VITE_MSW=true npm run dev -- --port 5173",
    port: 5173,
    reuseExistingServer: true,
    timeout: 60_000,
  },
});
