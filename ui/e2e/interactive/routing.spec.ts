import { test, expect } from "@playwright/test";
import { authenticate } from "../screenshots/helpers/auth";

// What the portal does with a path it has no page for (#1359).
//
// The unit tests decide which paths lib/portalRoutes calls real. Only the
// assembled app can show the thing the bug was actually about: that the shell
// consults it at all, and that a path with no page stops looking like a page
// with no data.

test.describe("A path the portal has no page for", () => {
  test("says there is no page there rather than rendering an empty section", async ({
    page,
  }) => {
    await authenticate(page);
    await page.goto("/portal/nonesuch");

    await expect(page.getByText("There is no page at this address.")).toBeVisible();
    await expect(page.getByText("/nonesuch")).toBeVisible();
    // The title is what the header used to get wrong: an unmatched path fell
    // back to "Assets", so a missing page carried the name of a real section.
    await expect(page.getByRole("heading", { name: "Not found", level: 1 })).toBeVisible();
    await expect(page.getByRole("heading", { name: "Assets", level: 1 })).toHaveCount(0);
  });

  test("offers the way out of the dead end", async ({ page }) => {
    await authenticate(page);
    await page.goto("/portal/nonesuch");

    await page.getByRole("button", { name: "Go to your assets" }).click();
    await expect(page).toHaveURL(/\/portal\/$/);
    await expect(page.getByRole("heading", { name: "Assets", level: 1 })).toBeVisible();
  });

  // The reported path. /portal/assets/ rendered the chrome, the title "Assets",
  // and nothing else -- and never requested a list, so it was indistinguishable
  // from being told you own no assets.
  test("sends the guessed assets path to the assets", async ({ page }) => {
    await authenticate(page);
    await page.goto("/portal/assets/");

    await expect(page).toHaveURL(/\/portal\/$/);
    await expect(page.getByRole("heading", { name: "Assets", level: 1 })).toBeVisible();
    await expect(page.getByText("There is no page at this address.")).toHaveCount(0);
  });

  // A redirect that pushed a history entry would leave the redirecting path
  // behind it, so Back would land there and redirect forward again -- Back
  // would appear to do nothing.
  test("leaves Back working after a redirect", async ({ page }) => {
    await authenticate(page);
    await page.goto("/portal/collections");
    await expect(page.getByRole("heading", { name: "Collections", level: 1 })).toBeVisible();

    await page.goto("/portal/assets/");
    await expect(page).toHaveURL(/\/portal\/$/);

    await page.goBack();
    await expect(page).toHaveURL(/\/portal\/collections$/);
    await expect(page.getByRole("heading", { name: "Collections", level: 1 })).toBeVisible();
  });

  test("drops a trailing slash from a section that exists without one", async ({ page }) => {
    await authenticate(page);
    await page.goto("/portal/scripts/");

    await expect(page).toHaveURL(/\/portal\/scripts$/);
    await expect(page.getByRole("heading", { name: "Scripts", level: 1 })).toBeVisible();
  });

  // A real page is not collateral damage: recognition runs before the switch,
  // so every route that renders something has to still render it.
  test("still renders a section and a detail it does have", async ({ page }) => {
    await authenticate(page);
    await page.goto("/portal/scripts");
    await expect(page.getByRole("heading", { name: "Scripts", level: 1 })).toBeVisible();

    await page.getByRole("row").filter({ hasText: "Daily Sales Report" }).click();
    await expect(page.getByRole("heading", { name: "Daily Sales Report" })).toBeVisible();
    await expect(page.getByText("There is no page at this address.")).toHaveCount(0);
  });
});
