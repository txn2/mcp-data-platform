import { defineConfig } from "@playwright/test";
import path from "path";
import { fileURLToPath } from "url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));

// Separate from playwright.config.ts on purpose. An MCP App is a static
// document the host renders in an iframe, so this capture needs no MSW dev
// server, no sign-in, and no portal: starting one would couple these shots to
// a stack they do not touch. Playwright wipes `outputDir`, so it points at the
// gitignored traces directory and never at the screenshot destination -- the
// spec writes screenshots itself, to SCREENSHOT_OUTPUT_DIR or
// docs/images/screenshots.
export default defineConfig({
  testDir: ".",
  testMatch: "mcpapp.spec.ts",
  timeout: 60_000,
  use: {
    reducedMotion: "reduce",
  },
  workers: 1,
  retries: 0,
  reporter: [["list"]],
  outputDir: path.resolve(__dirname, "../../test-results/mcpapps"),
});
