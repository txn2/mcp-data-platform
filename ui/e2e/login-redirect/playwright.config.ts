import { defineConfig } from "@playwright/test";
import path from "path";
import { fileURLToPath } from "url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));

// The browser half of #2242's acceptance criteria. It is run by
// test/acceptance/issue_2242_test.go, which starts a platform replica with
// portal.login_redirect set and points this suite at it through
// LOGIN_REDIRECT_BASE_URL; the identity provider is the dev stack's Keycloak.
// Nothing else runs it: against a stack without the setting every case fails.
export default defineConfig({
  testDir: __dirname,
  testMatch: "*.spec.ts",
  timeout: 90_000,
  expect: { timeout: 20_000 },
  use: {
    baseURL: process.env["LOGIN_REDIRECT_BASE_URL"],
    viewport: { width: 1280, height: 800 },
  },
  workers: 1,
  reporter: [["list"]],
});
