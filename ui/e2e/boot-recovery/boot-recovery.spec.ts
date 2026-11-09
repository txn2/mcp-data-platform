import { test, expect, type Page } from "@playwright/test";

// #2495: during a rolling update index.html can name an entry script the
// replica that answers does not have. The page reloads once on its own, and
// when the reload fails the same way it says the portal is being updated
// rather than staying blank.

const MESSAGE = "The portal is being updated. Reload in a moment.";

// No platform answers behind `vite preview`; a signed-out caller is what the
// portal draws its first screen for.
test.beforeEach(async ({ page }) => {
  await page.route(/\/api\//, (route) =>
    route.fulfill({ status: 401, contentType: "application/json", body: '{"error":"unauthorized"}' }),
  );
});

/** Counts the portal documents the page loads. */
function countDocuments(page: Page): { loads: number } {
  const state = { loads: 0 };
  page.on("request", (r) => {
    if (r.resourceType() === "document" && new URL(r.url()).pathname.startsWith("/portal")) state.loads++;
  });
  return state;
}

/**
 * A file the index.html the build wrote preloads. The bundler names and merges
 * its shared chunks, so a name written here stops matching when the portal's
 * imports change, and the case would then remove nothing.
 */
async function preloadedFile(page: Page): Promise<string> {
  const html = await (await page.request.get("/portal/")).text();
  const hrefs = [...html.matchAll(/<link rel="modulepreload"[^>]* href="([^"]+)"/g)].map((m) => m[1] as string);
  expect(hrefs.length, "the portal's index.html preloads a file").toBeGreaterThan(0);
  return hrefs[hrefs.length - 1] as string;
}

test("an entry script answering 404 reloads once, then shows the message", async ({ page }) => {
  await page.route(/\/portal\/assets\/index-[^/]+\.js$/, (route) =>
    route.fulfill({ status: 404, contentType: "text/plain", body: "404 page not found" }),
  );
  const docs = countDocuments(page);

  await page.goto("/portal/");

  const alert = page.getByRole("alert");
  await expect(alert).toContainText(MESSAGE);
  await expect(page.getByRole("button", { name: "Reload" })).toBeVisible();
  expect(docs.loads, "the page loaded once and reloaded once").toBe(2);

  // It stays put: no third load on its own.
  await page.waitForTimeout(1_000);
  expect(docs.loads).toBe(2);

  // The button reloads.
  await page.getByRole("button", { name: "Reload" }).click();
  await expect.poll(() => docs.loads).toBe(3);
  await expect(page.getByRole("alert")).toContainText(MESSAGE);
});

test("a modulepreload answering 404 is recovered the same way", async ({ page }) => {
  const preloaded = await preloadedFile(page);
  await page.route(
    (url) => url.pathname === preloaded,
    (route) => route.fulfill({ status: 404, contentType: "text/plain", body: "404 page not found" }),
  );
  const docs = countDocuments(page);

  await page.goto("/portal/");

  await expect(page.getByRole("alert")).toContainText(MESSAGE);
  expect(docs.loads).toBe(2);
});

test("a straddled load that recovers on the reload draws the portal", async ({ page }) => {
  let refused = 0;
  await page.route(/\/portal\/assets\/index-[^/]+\.js$/, (route) => {
    if (refused === 0) {
      refused++;
      return route.fulfill({ status: 404, contentType: "text/plain", body: "404 page not found" });
    }
    return route.continue();
  });
  const docs = countDocuments(page);

  await page.goto("/portal/");

  await expect.poll(() => page.locator("#root > *").count()).toBeGreaterThan(0);
  expect(docs.loads).toBe(2);
  await expect(page.getByText(MESSAGE)).toHaveCount(0);
});

test("a load with every file present draws the portal and never reloads", async ({ page }) => {
  const docs = countDocuments(page);
  await page.goto("/portal/");
  await expect.poll(() => page.locator("#root > *").count()).toBeGreaterThan(0);
  await page.waitForTimeout(500);
  expect(docs.loads).toBe(1);
  await expect(page.getByText(MESSAGE)).toHaveCount(0);
});
