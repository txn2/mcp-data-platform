import { defineConfig } from "@playwright/test";
import path from "path";
import { fileURLToPath } from "url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));

const outputDir =
  process.env["SCREENSHOT_OUTPUT_DIR"] ||
  path.resolve(__dirname, "../../../docs/images/screenshots");

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
