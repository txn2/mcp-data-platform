import { defineConfig } from "@playwright/test";
import path from "path";
import { fileURLToPath } from "url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));

// The browser half of #2579's and #2578's acceptance criteria. It is run by
// test/acceptance/issue_2579_test.go and issue_2578_test.go against the dev
// stack, whose Keycloak and portal it signs in to. *.localhost resolves to the
// loopback address in Chromium, which is how a site's own address is reached.
export default defineConfig({
  testDir: __dirname,
  testMatch: "*.spec.ts",
  timeout: 90_000,
  expect: { timeout: 20_000 },
  use: {
    baseURL: process.env["SITE_SIGNIN_BASE_URL"],
    viewport: { width: 1280, height: 800 },
  },
  workers: 1,
  reporter: [["list"]],
});
