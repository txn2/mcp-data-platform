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
    await page.getByRole("row").filter({ hasText: "Daily Sales Report" }).click();

    // The contract: what will execute, on what cadence, and what it takes.
    await expect(page.getByRole("heading", { name: "Daily Sales Report" })).toBeVisible();
    // Both scoped to the contract: the schedule editor below states the same
    // cadence and names the same parameter, because that is the box its
    // binding goes in.
    await expect(page.getByRole("cell", { name: "report_date" })).toBeVisible();
    await expect(
      page.getByText("0 7 * * 1-5 (America/Los_Angeles)", { exact: true }),
    ).toBeVisible();

    // The served version's source, open without a click.
    await expect(page.getByText("Executing")).toBeVisible();
    await expect(page.getByText(/platform\.export/).first()).toBeVisible();

    // Every terminal state a run can end in.
    await expect(page.getByText("Skipped (overlap)")).toBeVisible();
    await expect(page.getByText(/relation "sales.orders" does not exist/).first()).toBeVisible();
  });

  test("opens a run and shows the log it captured", async ({ page }) => {
    await gotoScripts(page);
    await page.getByRole("row").filter({ hasText: "Daily Sales Report" }).click();
    await expect(page.getByText("Run history")).toBeVisible();

    await page.getByRole("row").filter({ hasText: "succeeded" }).first().click();

    await expect(page.getByText(/wrote asset version 42/)).toBeVisible();
    await expect(page.getByText("report_date=2026-08-13", { exact: true })).toBeVisible();
    // The asset the run versioned is reachable; the delivered copy is not.
    await expect(page.getByRole("button", { name: "daily-sales" })).toBeVisible();
  });

  // The cadence controls are the one mutation on this surface (#1307), so they
  // are exercised against the mock server rather than only through mocked
  // hooks: the request has to reach the route the server registers, and the
  // answer has to come back into the page that submitted it.
  test("an owner re-times their own script and pauses it", async ({ page }) => {
    await gotoScripts(page);
    await page.getByRole("row").filter({ hasText: "Daily Sales Report" }).click();

    // The cadence in force, as choices rather than as an expression, and the
    // binding every fire passes.
    await expect(page.getByRole("button", { name: "Weekdays" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    await expect(page.getByLabel("Time", { exact: true })).toHaveValue("07:00");
    await expect(page.getByLabel("report_date")).toHaveValue("${fire_date}");

    // Moving the report an hour earlier is a change to the time, not to a cron
    // field, and the page says what it will save before it saves it.
    await page.getByLabel("Time", { exact: true }).fill("06:30");
    await expect(page.getByText("Saves as: Every weekday at 6:30 AM")).toBeVisible();
    await page.getByRole("button", { name: "Update schedule" }).click();
    await expect(page.getByText(/Every weekday at 6:30 AM/).first()).toBeVisible();
    await expect(page.getByText("30 6 * * 1-5").first()).toBeVisible();

    await page.getByRole("button", { name: "Pause" }).click();
    await expect(page.getByText(/paused, and firing nothing/)).toBeVisible();
    await expect(page.getByRole("button", { name: "Resume" })).toBeVisible();
  });

  // A cadence on a script nothing has approved saves and fires nothing, and
  // the page has to say so: the owner cannot approve it themselves.
  test("says a schedule on an unapproved script will execute nothing", async ({ page }) => {
    await gotoScripts(page);
    await page.getByRole("row").filter({ hasText: "Dormant Accounts" }).click();

    await expect(page.getByText(/runs only when someone asks/)).toBeVisible();
    await expect(page.getByText(/stays inert/)).toBeVisible();

    await page.getByRole("button", { name: "Daily" }).click();
    await page.getByLabel("Time", { exact: true }).fill("05:00");
    await page.getByLabel("cutoff").fill("2026-01-01");
    await page.getByRole("button", { name: "Set schedule" }).click();
    await expect(page.getByText(/nothing will execute it/)).toBeVisible();
    await expect(page.getByText(/Every day at 5:00 AM/).first()).toBeVisible();
  });

  // The owner's summary is computed from the listing itself, so it is exercised
  // against the mock server rather than only through mocked hooks.
  test("summarizes the state of the caller's automations", async ({ page }) => {
    await gotoScripts(page);
    await expect(page.getByText("Automations")).toBeVisible();
    await expect(page.getByText("On a cadence")).toBeVisible();
    await expect(page.getByText("Last run failed")).toBeVisible();
  });

  // Editing the code is the second mutation on this surface, and the outcome
  // depends on the script: an approved one goes to review, an unapproved one
  // applies.
  test("an owner edits the code, and an approved script's edit goes to review", async ({
    page,
  }) => {
    await gotoScripts(page);
    await page.getByRole("row").filter({ hasText: "Daily Sales Report" }).click();

    await expect(page.getByText(/Version 2 is approved and keeps running/)).toBeVisible();
    const editor = page.locator(".cm-content").first();
    await editor.click();
    await page.keyboard.type("\n# checked by the owner\n");
    await page.getByRole("button", { name: "Save" }).click();

    await expect(page.getByText(/saved as a draft awaiting review/)).toBeVisible();
  });

  test("says a run history's success rate over the runs it actually loaded", async ({ page }) => {
    await gotoScripts(page);
    await page.getByRole("row").filter({ hasText: "Daily Sales Report" }).click();
    await expect(page.getByText(/succeeded over the last \d+ runs/)).toBeVisible();
  });

  test("returns to the listing from a script", async ({ page }) => {
    await gotoScripts(page);
    await page.getByRole("row").filter({ hasText: "Warehouse Freshness Check" }).click();
    await expect(page.getByRole("heading", { name: "Warehouse Freshness Check" })).toBeVisible();

    await page.locator("main").getByRole("button", { name: "Scripts" }).click();
    await expect(page.getByRole("heading", { name: "Scripts", level: 1 })).toBeVisible();
    await expect(page.getByText("Dormant Accounts")).toBeVisible();
  });
});
