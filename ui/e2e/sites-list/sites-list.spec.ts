import { expect, test, type Page } from "@playwright/test";

// #2587: the whole row of the Sites list opens the site's page, from a click
// anywhere on it or from Enter on the focused row, and the address in it still
// opens the live site in a new tab without opening the row. Share offers its
// owner Can manage. Neither the list nor the dialog overflows a phone.
//
// SITES_BASE_URL is the portal's origin and SITES_API_KEY the key of the
// person who owns the site this spec creates (on the dev stack,
// acme-owner-key).

function env(name: string): string {
  const v = process.env[name];
  if (!v) throw new Error(`${name} is not set`);
  return v;
}

const PHONE = { width: 375, height: 800 };
const site = `e2e2587-${Date.now() % 1_000_000_000}`;

async function api(page: Page, method: string, path: string, data?: unknown) {
  return page.request.fetch(`${env("SITES_BASE_URL")}/api/v1/portal${path}`, {
    method,
    headers: { "X-API-Key": env("SITES_API_KEY"), "Content-Type": "application/json" },
    data,
  });
}

async function signInWithKey(page: Page): Promise<void> {
  await page.goto("/portal/?login=api_key");
  const key = page.locator('input[placeholder="X-API-Key"]');
  await key.fill(env("SITES_API_KEY"));
  await page.getByRole("button", { name: "Sign in with API key" }).click();
  await expect(key).toHaveCount(0);
}

/** expectNoSidewaysScroll fails when the page scrolls sideways. */
async function expectNoSidewaysScroll(page: Page): Promise<void> {
  const { scroll, client } = await page.evaluate(() => ({ scroll: document.documentElement.scrollWidth, client: document.documentElement.clientWidth }));
  expect(scroll, "the page scrolls sideways").toBeLessThanOrEqual(client);
}

test.beforeAll(async ({ browser }) => {
  const page = await browser.newPage();
  const res = await api(page, "POST", "/sites", { name: site, description: "#2587 browser criteria" });
  expect(res.status(), await res.text()).toBe(201);
  await page.close();
});

test.afterAll(async ({ browser }) => {
  const page = await browser.newPage();
  await api(page, "DELETE", `/sites/${site}`);
  await page.close();
});

test.beforeEach(async ({ page }) => {
  await signInWithKey(page);
});

test("a click anywhere on a row but its address opens the site", async ({ page }) => {
  await page.goto("/portal/sites");
  const row = page.getByTestId(`site-row-${site}`);
  await row.locator("td").nth(1).click();
  await expect(page).toHaveURL(new RegExp(`/portal/sites/${site}(/environments)?$`));
});

test("Enter on the focused row opens the site", async ({ page }) => {
  await page.goto("/portal/sites");
  const row = page.getByTestId(`site-row-${site}`);
  await row.focus();
  await expect(row).toBeFocused();
  await page.keyboard.press("Enter");
  await expect(page).toHaveURL(new RegExp(`/portal/sites/${site}(/environments)?$`));
});

test("the address opens the live site in a new tab and leaves the list where it is", async ({ page, context }) => {
  await page.goto("/portal/sites");
  const link = page.getByTestId(`site-row-${site}`).locator("a[target=_blank]").first();
  await expect(link).toBeVisible();
  // The link names the site's own address; dev is private, so the new tab
  // goes on to sign-in, which is why its final URL is not checked.
  expect(await link.getAttribute("href")).toContain(site);
  const opened = context.waitForEvent("page");
  await link.click();
  const tab = await opened;
  expect(tab).toBeTruthy();
  await tab.close();
  await expect(page).toHaveURL(/\/portal\/sites$/);
});

test("Share offers the owner Can manage", async ({ page }) => {
  await page.goto(`/portal/sites/${site}/environments`);
  await page.getByTestId("env-dev").getByRole("button", { name: "Share" }).click();
  const dialog = page.getByRole("dialog", { name: `Share ${site}` });
  await expect(dialog).toBeVisible();
  const manage = dialog.getByRole("radio", { name: "Can manage" });
  await expect(manage).toBeEnabled();
  await manage.check();
  await expect(dialog.getByTestId("manage-covers")).toBeVisible();
  await expect(dialog.getByRole("checkbox", { name: "dev" })).toHaveCount(0);
});

test("at 375px neither the list nor the Share dialog overflows", async ({ page }) => {
  await page.setViewportSize(PHONE);
  await page.goto("/portal/sites");
  await expect(page.getByTestId(`site-row-${site}`)).toBeVisible();
  await expectNoSidewaysScroll(page);

  await page.goto(`/portal/sites/${site}/environments`);
  await page.getByTestId("env-dev").getByRole("button", { name: "Share" }).click();
  const dialog = page.getByRole("dialog", { name: `Share ${site}` });
  await expect(dialog).toBeVisible();
  const box = await dialog.boundingBox();
  expect(box, "the dialog has no box").not.toBeNull();
  expect(box!.x, "the dialog starts off the screen").toBeGreaterThanOrEqual(0);
  expect(box!.x + box!.width, "the dialog runs past the screen's edge").toBeLessThanOrEqual(PHONE.width);
  await expectNoSidewaysScroll(page);
});
