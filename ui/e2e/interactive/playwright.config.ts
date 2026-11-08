import { defineConfig } from "@playwright/test";
import path from "path";
import { fileURLToPath } from "url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));

// Interactive end-to-end tests for the admin observability dashboards.
// Runs against the MSW mock server (rich deterministic data) so every tab,
// time-range preset, and drilldown is exercised without a live backend.
// Reuses an MSW server already running on its port.
// The port the MSW dev server is served on: 5273, a port `make dev` never
// takes (#2523). `reuseExistingServer` below cannot tell an MSW server from
// any other Vite on the same port, and `make dev` serves one without VITE_MSW
// on 5173, so a suite defaulting there bound to the live backend and failed
// every case at sign-in. TestPlaywrightDefaultsAvoidDevPorts (test/gates)
// fails when this default equals a fixed port dev/start.sh takes. E2E_PORT
// overrides it.
const PORT = Number(process.env["E2E_PORT"] ?? 5273);

export default defineConfig({
  testDir: __dirname,
  testMatch: "*.spec.ts",
  // Loads the portal once before any worker starts, so the dev server has
  // compiled the app before a test's first page asks for it (#2077).
  globalSetup: path.join(__dirname, "global-setup.ts"),
  timeout: 60_000,
  expect: { timeout: 10_000 },
  use: {
    baseURL: `http://localhost:${PORT}`,
    viewport: { width: 1440, height: 900 },
    reducedMotion: "reduce",
  },
  // Half the machine's cores. Each test drives its own browser context with its
  // own MSW worker, so nothing is shared between them but the one dev server,
  // which `reuseExistingServer` already has them share. On an 18-core machine
  // this took the suite from 238s to 114s at four workers; the fraction rather
  // than a fixed number keeps it honest on a smaller CI runner.
  workers: "50%",
  retries: process.env["CI"] ? 1 : 0,
  reporter: [["list"]],
  webServer: {
    command: `VITE_MSW=true npm run dev -- --port ${PORT}`,
    port: PORT,
    reuseExistingServer: true,
    timeout: 60_000,
  },
});
