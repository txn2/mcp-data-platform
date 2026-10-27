import { defineConfig } from "@playwright/test";
import path from "path";
import { fileURLToPath } from "url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));

// The browser half of the forms' acceptance criteria (#2177, #2180, #2331):
// forms embedded in a shared asset or framed by another site, submitted by an
// anonymous browser, and the form page in light and dark at desktop and phone
// width; and #2337's Changesets tab opened by a signed-in reviewer. It is run
// by test/acceptance/issue_2177_test.go, issue_2180_test.go, issue_2331_test.go
// and issue_2337_test.go against the dev stack, which point it at what they
// made through FORMS_* variables. Nothing else runs it.
export default defineConfig({
  testDir: __dirname,
  testMatch: "*.spec.ts",
  timeout: 120_000,
  expect: { timeout: 30_000 },
  use: {
    baseURL: process.env["FORMS_BASE_URL"],
    viewport: { width: 1280, height: 900 },
    // The snippet case (#2331) frames the platform from a site Playwright
    // answers at a public name, and the dev stack's platform is on localhost:
    // Chrome's Local Network Access check refuses a public page reaching
    // localhost, which a deployed platform, at a public address, never meets.
    launchOptions: { args: ["--disable-features=LocalNetworkAccessChecks"] },
  },
  workers: 1,
  reporter: [["list"]],
});
