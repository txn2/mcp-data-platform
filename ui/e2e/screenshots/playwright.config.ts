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

export default defineConfig({
  testDir: ".",
  testMatch: "screenshot.spec.ts",
  timeout: 300_000,
  use: {
    baseURL: "http://localhost:5173",
    viewport: { width: 1440, height: 900 },
    reducedMotion: "reduce",
    colorScheme: "light",
  },
  workers: 1,
  retries: 0,
  reporter: [["list"]],
  webServer: {
    command: "VITE_MSW=true pnpm dev --port 5173",
    port: 5173,
    reuseExistingServer: true,
    timeout: 30_000,
  },
  outputDir,
});
