import { chromium, type FullConfig } from "@playwright/test";

// Warm the dev server before any worker starts (#2077).
//
// The suite runs against the Vite dev server, which transforms a module on its
// first request, and `webServer` waits only for the port to answer. With
// several workers starting at once, the first page of each was the one that
// paid for compiling the app; beside verify's Go lane that compile could
// outlast a test's 10s expect timeout, and four specs failed together on their
// first toBeVisible. Loading the portal once here, until the app has drawn its
// sign-in form or its navigation, transforms every module the shell imports
// (the routes are imported eagerly), so no test's first page waits on the
// compiler and no test's timeout is raised to absorb it.
//
// The bound is generous because this is the one load that compiles the app.
const WARM_TIMEOUT_MS = 180_000;

export default async function globalSetup(config: FullConfig): Promise<void> {
  const baseURL = config.projects[0]?.use.baseURL;
  if (!baseURL) throw new Error("the interactive suite has no baseURL to warm");
  const browser = await chromium.launch();
  try {
    const page = await browser.newPage();
    await page.goto(new URL("/portal/", baseURL).href, { timeout: WARM_TIMEOUT_MS });
    await page
      .locator('nav, input[placeholder="API Key"]')
      .first()
      .waitFor({ state: "visible", timeout: WARM_TIMEOUT_MS });
  } finally {
    await browser.close();
  }
}
