import { test, expect, type Page } from "@playwright/test";
import { authenticate } from "../screenshots/helpers/auth";

// Interactive coverage for the administrator's script section. The unit tests
// drive the page with its hooks mocked; these drive the assembled app against
// the mock server, so the route, the query keys, and the navigation from the
// listing into one script are exercised end to end.

async function gotoAdminScripts(page: Page): Promise<void> {
  await authenticate(page);
  await page.goto("/portal/admin/scripts");
  await expect(page.getByText("All scripts")).toBeVisible();
}

test.describe("Admin script pages", () => {
  test("lists every script with what it is executing", async ({ page }) => {
    await gotoAdminScripts(page);

    await expect(page.getByText("daily-sales-report")).toBeVisible();
    // A saved script runs its latest saved version, and the row says which.
    await expect(page.getByText("Runs v2").first()).toBeVisible();
  });

  // The operator's other question: not what exists, but what has been running
  // (#1307).
  test("the Runs tab shows what the platform has been running", async ({ page }) => {
    await gotoAdminScripts(page);
    await page.getByRole("tab", { name: "Runs" }).click();

    // The charts read the metrics the run worker emits; the table reads the
    // run rows. Both are on the page, and neither can do the other's job.
    await expect(page.getByText("Runs over time")).toBeVisible();
    await expect(page.getByText("Missed fires")).toBeVisible();
    await expect(page.getByText("Recent runs")).toBeVisible();
    await expect(page.getByRole("row").filter({ hasText: "succeeded" }).first()).toBeVisible();
  });

  test("a row opens the script, where an administrator does everything an owner does", async ({
    page,
  }) => {
    await gotoAdminScripts(page);

    await page.getByRole("row").filter({ hasText: "daily-sales-report" }).click();
    await expect(page).toHaveURL(/\/admin\/scripts\/script-001$/);
    // The shell names the page for what it is showing, which a detail route
    // under a section it does not know would otherwise get wrong.
    await expect(page.getByRole("heading", { name: "Script", level: 1 })).toBeVisible();

    // There is one script page rather than two: everything an owner has is
    // here for every script — run it, edit it, check the edit, re-time it,
    // read its history.
    await expect(page.getByRole("button", { name: "Run", exact: true })).toBeVisible();
    await expect(page.getByRole("heading", { name: "Source" })).toBeVisible();
    await expect(page.getByText("Version history")).toBeVisible();
    await expect(page.getByText("Run history")).toBeVisible();
  });
});
