import { defineConfig } from "@playwright/test";
import path from "path";
import { fileURLToPath } from "url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));

// Playwright WIPES `outputDir` before every run. It must never point at the
// screenshot destination, or a partial run (e.g. `-g <subset>`) deletes the
// whole committed screenshot set. Screenshots are written independently by
// screenshot.spec.ts to SCREENSHOT_OUTPUT_DIR (or docs/images/screenshots);
// this dir only holds Playwright's own traces/attachments and is gitignored.
const outputDir = path.resolve(__dirname, "../../test-results/screenshots");

// The port the MSW dev server is served on. Overridable because
// `reuseExistingServer` below cannot tell an MSW server from any other Vite on
// the same port: `make dev` runs one without VITE_MSW, and the capture run then
// drives the live backend and fails at sign-in. Set E2E_PORT to capture beside
// a dev stack.
const PORT = Number(process.env["E2E_PORT"] ?? 5173);

export default defineConfig({
  testDir: ".",
  testMatch: "screenshot.spec.ts",
  timeout: 300_000,
  use: {
    baseURL: `http://localhost:${PORT}`,
    viewport: { width: 1440, height: 900 },
    reducedMotion: "reduce",
    colorScheme: "light",
  },
  workers: 1,
  retries: 0,
  reporter: [["list"]],
  webServer: {
    command: `VITE_MSW=true npm run dev -- --port ${PORT}`,
    port: PORT,
    reuseExistingServer: true,
    timeout: 30_000,
  },
  outputDir,
});
