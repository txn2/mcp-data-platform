import { defineConfig } from "@playwright/test";
import path from "path";
import { fileURLToPath } from "url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));

// #2495: the built portal served by `vite preview` from dist/, so the page is
// the index.html the build writes, recovery script and injected tags included.
// Build first (`npm run build`); nothing here rebuilds it. No backend answers
// the API, which these cases do not need.
//
// With BOOT_RECOVERY_BASE_URL set, the suite runs against that platform
// instead, which serves the index.html embedded in its binary:
// test/acceptance/issue_2495_test.go points it at the dev stack.
const PORT = Number(process.env["BOOT_RECOVERY_PORT"] ?? 4179);
const BASE = process.env["BOOT_RECOVERY_BASE_URL"];

export default defineConfig({
  testDir: __dirname,
  testMatch: "*.spec.ts",
  timeout: 60_000,
  expect: { timeout: 15_000 },
  use: {
    baseURL: BASE ?? `http://localhost:${PORT}`,
    viewport: { width: 1280, height: 800 },
  },
  workers: 1,
  reporter: [["list"]],
  webServer: BASE
    ? undefined
    : {
        command: `npx vite preview --port ${PORT} --strictPort`,
        // The ui directory, where vite.config.ts sets base /portal/ and dist/ is.
        cwd: path.resolve(__dirname, "../.."),
        port: PORT,
        reuseExistingServer: false,
        timeout: 60_000,
      },
});
