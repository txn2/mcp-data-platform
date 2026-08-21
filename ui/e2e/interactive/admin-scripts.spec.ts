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
  test("lists every script with whose it is, when it runs, and how it last ran", async ({
    page,
  }) => {
    await gotoAdminScripts(page);

    await expect(page.getByText("daily-sales-report")).toBeVisible();
    // Whose script it is, which is the fact an administrator comes for
    // (#1404), and the cadence in the words the editor states it in (#1407).
    await expect(page.getByRole("columnheader", { name: "Owner" })).toBeVisible();
    await expect(page.getByText("sarah.chen@example.com").first()).toBeVisible();
    await expect(
      page.getByText("Every weekday at 7:00 AM, America/Los_Angeles"),
    ).toBeVisible();
    await expect(page.getByText("succeeded").first()).toBeVisible();
    // The version a run executes is a fact about every healthy script, so it
    // is on the script's own page rather than in a column here.
    await expect(page.getByText("Runs v2")).toHaveCount(0);
  });

  // The listing an administrator reads is the one the owners read, so it
  // narrows the same way: a query predicate, not a filter over loaded rows.
  test("narrows every script by what an administrator typed", async ({ page }) => {
    await gotoAdminScripts(page);

    await page.getByLabel("Search scripts").fill("margin");
    await expect(page.getByText("My Margin Check")).toBeVisible();
    await expect(page.getByText("Daily Sales Report")).toHaveCount(0);

    await page.getByLabel("Search scripts").fill("");
    await expect(page.getByText("Daily Sales Report")).toBeVisible();
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

  // A run in the operator's listing opens the run itself, in the section the
  // reader came from (#1407): the rows used to be inert.
  test("a run in the operator's listing opens that run", async ({ page }) => {
    await gotoAdminScripts(page);
    await page.getByRole("tab", { name: "Runs" }).click();

    await page.getByRole("row").filter({ hasText: /Daily Sales Report/ }).first().click();
    await expect(page).toHaveURL(/\/admin\/scripts\/script-001\/runs\//);
    await expect(page.getByText(/wrote asset version 42/)).toBeVisible();
  });

  // A metric that names a script leads to it: to the script, and to the runs
  // behind the number (#1407).
  test("a metric that names a script opens it and the runs behind it", async ({ page }) => {
    await gotoAdminScripts(page);
    await page.getByRole("tab", { name: "Runs" }).click();

    const busiest = page.locator("div[data-slot=card]").filter({
      has: page.getByRole("heading", { name: "Busiest scripts" }),
    });
    await busiest.getByRole("button", { name: "Runs" }).first().click();
    await expect(page.getByText(/Narrowed to/)).toBeVisible();

    await page.getByRole("button", { name: "Show every script" }).click();
    await expect(page.getByText(/Narrowed to/)).toHaveCount(0);

    await busiest.getByRole("button", { name: "Daily Sales Report" }).click();
    await expect(page).toHaveURL(/\/admin\/scripts\/script-001$/);
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
