import { test, expect, type Page } from "@playwright/test";
import { authenticate } from "../screenshots/helpers/auth";

// Interactive coverage for the portal script pages (#1290). The unit tests
// drive the pages with their hooks mocked; these drive the assembled app
// against the mock server, so the routes, the query keys, and the navigation
// between the listing, one script, and one of its runs are exercised end to
// end -- including the part a mocked hook cannot show: that opening a run
// actually fetches that run and renders the log it captured.

async function gotoScripts(page: Page): Promise<void> {
  await authenticate(page);
  await page.goto("/portal/scripts");
  await expect(page.getByRole("heading", { name: "Scripts", level: 1 })).toBeVisible();
}

test.describe("Portal script pages", () => {
  test("lists every script with what it executes, its cadence, and its last run", async ({
    page,
  }) => {
    await gotoScripts(page);

    await expect(page.getByText("Daily Sales Report")).toBeVisible();
    await expect(page.getByText("Approved v2").first()).toBeVisible();
    await expect(page.getByText("0 7 * * 1-5")).toBeVisible();
    await expect(page.getByText("succeeded").first()).toBeVisible();

    // A script nothing has approved runs nothing, whatever else is true of it.
    await expect(page.getByText("Nothing approved")).toBeVisible();
    // A disabled cadence says so rather than showing a fire that will not happen.
    await expect(page.getByText("paused")).toBeVisible();
  });

  test("says plainly when there are no scripts at all", async ({ page }) => {
    await authenticate(page);
    // The mock server answers this surface as empty when the page URL asks it
    // to; the app itself ignores the parameter. Intercepting the request from
    // the test cannot work here, because the mock service worker answers it
    // before it ever reaches the network.
    await page.goto("/portal/scripts?empty=scripts");
    await expect(page.getByText("You have no scripts yet")).toBeVisible();
  });

  test("opens one script's contract, its source, and its run history", async ({ page }) => {
    await gotoScripts(page);
    await page
      .getByRole("row")
      .filter({ hasText: "Daily Sales Report" })
      .getByRole("button", { name: "Open" })
      .click();

    // The contract: what will execute, on what cadence, and what it takes.
    await expect(page.getByRole("heading", { name: "Daily Sales Report" })).toBeVisible();
    await expect(page.getByText("report_date")).toBeVisible();
    await expect(page.getByText(/0 7 \* \* 1-5 \(America\/Los_Angeles\)/).first()).toBeVisible();

    // The served version's source, open without a click.
    await expect(page.getByText("Executing")).toBeVisible();
    await expect(page.getByText(/platform\.export/).first()).toBeVisible();

    // Every terminal state a run can end in.
    await expect(page.getByText("Skipped (overlap)")).toBeVisible();
    await expect(page.getByText(/relation "sales.orders" does not exist/).first()).toBeVisible();
  });

  test("opens a run and shows the log it captured", async ({ page }) => {
    await gotoScripts(page);
    await page
      .getByRole("row")
      .filter({ hasText: "Daily Sales Report" })
      .getByRole("button", { name: "Open" })
      .click();
    await expect(page.getByText("Run history")).toBeVisible();

    await page
      .getByRole("row")
      .filter({ hasText: "succeeded" })
      .first()
      .getByRole("button", { name: "Open" })
      .click();

    await expect(page.getByText(/wrote asset version 42/)).toBeVisible();
    await expect(page.getByText("report_date=2026-08-13", { exact: true })).toBeVisible();
    // The asset the run versioned is reachable; the delivered copy is not.
    await expect(page.getByRole("button", { name: "daily-sales" })).toBeVisible();
  });

  test("returns to the listing from a script", async ({ page }) => {
    await gotoScripts(page);
    await page
      .getByRole("row")
      .filter({ hasText: "Warehouse Freshness Check" })
      .getByRole("button", { name: "Open" })
      .click();
    await expect(page.getByRole("heading", { name: "Warehouse Freshness Check" })).toBeVisible();

    await page.locator("main").getByRole("button", { name: "Scripts" }).click();
    await expect(page.getByRole("heading", { name: "Scripts", level: 1 })).toBeVisible();
    await expect(page.getByText("Dormant Accounts")).toBeVisible();
  });
});

test.describe("Portal script schedule", () => {
  test("an owner sets a cadence, pauses it, and resumes it", async ({ page }) => {
    await gotoScripts(page);
    await page
      .getByRole("row")
      .filter({ hasText: "Daily Sales Report" })
      .getByRole("button", { name: "Open" })
      .click();
    await expect(page.getByRole("heading", { name: "Daily Sales Report" })).toBeVisible();

    // The cadence the script already runs on is in the field, not an empty box.
    await expect(page.getByLabel("Cadence")).toHaveValue("0 7 * * 1-5");

    // A common cadence is a click; saving it reports the new state.
    await page.getByRole("button", { name: "Weekly, Monday 07:00" }).click();
    await expect(page.getByLabel("Cadence")).toHaveValue("0 7 * * 1");
    await page.getByRole("button", { name: "Update schedule" }).click();
    await expect(page.getByText("0 7 * * 1 (America/Los_Angeles)").first()).toBeVisible();

    // Pausing is its own action and leaves the cadence alone.
    await page.getByRole("button", { name: "Pause" }).click();
    await expect(page.getByText("Paused", { exact: true })).toBeVisible();
    await expect(page.getByLabel("Cadence")).toHaveValue("0 7 * * 1");

    await page.getByRole("button", { name: "Resume" }).click();
    await expect(page.getByText("Scheduled", { exact: true })).toBeVisible();
  });

  test("refuses a cadence the platform cannot parse", async ({ page }) => {
    await gotoScripts(page);
    await page
      .getByRole("row")
      .filter({ hasText: "Warehouse Freshness Check" })
      .getByRole("button", { name: "Open" })
      .click();

    await page.getByLabel("Cadence").fill("every other tuesday");
    await page.getByRole("button", { name: "Update schedule" }).click();
    await expect(page.getByText(/unparseable cron expression/)).toBeVisible();
  });

  test("offers to schedule a script that has none", async ({ page }) => {
    await gotoScripts(page);
    await page
      .getByRole("row")
      .filter({ hasText: "Dormant Accounts" })
      .getByRole("button", { name: "Open" })
      .click();

    await expect(page.getByRole("button", { name: "Schedule it" })).toBeVisible();
    // Nothing is approved, so the page says a cadence will be kept and inert.
    await expect(page.getByText(/will start firing as soon as a version/)).toBeVisible();
  });
});
