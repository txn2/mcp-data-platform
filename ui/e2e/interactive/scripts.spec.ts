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
    await expect(page.getByText("succeeded").first()).toBeVisible();

    // The cadence in the words the editor two clicks away states it in, rather
    // than the expression the platform stores it as (#1358).
    await expect(
      page.getByText("Every weekday at 7:00 AM, America/Los_Angeles"),
    ).toBeVisible();
    // An expression the builder cannot express is shown as itself.
    await expect(page.getByText("*/30 * * * *")).toBeVisible();

    // A script nothing has approved runs nothing, whatever else is true of it.
    await expect(page.getByText("Nothing approved")).toBeVisible();
    // A disabled cadence says so rather than showing a fire that will not happen.
    await expect(page.getByText("Paused")).toBeVisible();
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

    // Run history in three columns, with what repeats folded into the row it
    // qualifies (#1362): no Trigger, Version, or Outputs column of its own.
    await expect(page.getByRole("columnheader", { name: "Run" })).toBeVisible();
    await expect(page.getByRole("columnheader", { name: "Produced" })).toBeVisible();
    await expect(page.getByRole("columnheader", { name: "Trigger" })).toHaveCount(0);
    await expect(page.getByRole("columnheader", { name: "Version" })).toHaveCount(0);
    await expect(page.getByText("schedule · v2").first()).toBeVisible();
    await expect(page.getByText("1 output").first()).toBeVisible();
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
    // The page carries three parameter forms — run now, dry run, and these
    // bindings — so a control is named by the form it belongs to rather than by
    // the parameter alone, which now matches all three.
    await expect(page.locator("#script-param-schedule-report_date")).toHaveValue("${fire_date}");

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

  // Running one is the other mutation the owner performs here (#1363), so it
  // goes through the mock server too: the request has to reach the route the
  // server registers, and the answer has to come back into the page.
  test("an owner runs their own script, choosing the connection rather than typing it", async ({
    page,
  }) => {
    await gotoScripts(page);
    await page.getByRole("row").filter({ hasText: "Daily Sales Report" }).click();

    // The run is refused until every required value is bound, and the page says
    // which are missing rather than submitting a request it knows is bad.
    const run = page.getByRole("button", { name: "Run", exact: true });
    await expect(run).toBeDisabled();
    await expect(page.getByText("report_date, source are required.")).toBeVisible();

    await page.locator("#script-param-run-report_date").fill("2026-08-17");
    // The connection comes from the set this script was APPROVED to reach, so
    // it is chosen. A name outside that set never reaches the form.
    await page.locator("#script-param-run-source").click();
    await page.getByRole("option", { name: /acme-warehouse/ }).click();
    await expect(page.getByRole("option", { name: /acme-lake/ })).toHaveCount(0);

    await expect(run).toBeEnabled();
    await run.click();
    await expect(page.getByText(/Queued\. It appears in this script's run history/)).toBeVisible();
  });

  // Checking an edit before asking anyone to approve it (#1364). Both actions
  // reach the server: one parses, the other executes as the caller.
  test("an owner validates and dry-runs an edit without persisting anything", async ({ page }) => {
    await gotoScripts(page);
    await page.getByRole("row").filter({ hasText: "Daily Sales Report" }).click();

    await page.getByRole("button", { name: "Validate" }).click();
    await expect(page.getByText("Parses")).toBeVisible();
    // What the edit reaches, which is the capability change a reviewer would
    // otherwise be the first to see.
    await expect(page.getByText("platform.query, platform.export").first()).toBeVisible();

    // A dry run binds the live contract's values, and is unavailable until the
    // required ones are supplied.
    await expect(page.getByRole("button", { name: "Dry run" })).toBeDisabled();
    await page.locator("#script-param-draft-report_date").fill("2026-08-17");
    await page.locator("#script-param-draft-source").click();
    await page.getByRole("option", { name: /acme-warehouse/ }).click();

    await page.getByRole("button", { name: "Dry run" }).click();
    // The report's own sentences, not the status badge: "succeeded" also
    // appears on every successful row in the run history below.
    await expect(page.getByText(/Nothing was persisted/)).toBeVisible();
    await expect(page.getByText(/would write 1284 rows as csv/)).toBeVisible();
    await expect(page.getByText(/1,284 rows for 2026-08-17/)).toBeVisible();
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
    await page.locator("#script-param-schedule-cutoff").fill("2026-01-01");
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

    // Each caption states what its number counts and over what population, and
    // the failure tile names the smaller population it is computed over (#1360).
    await expect(page.getByText("scripts visible to you")).toBeVisible();
    await expect(page.getByText("have a version the platform may execute")).toBeVisible();
    await expect(page.getByText("run on a schedule, unattended")).toBeVisible();
    await expect(page.getByText(/of the \d+ you own/)).toBeVisible();
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

  // The third outcome (#1367): the owner's own personal script is approved by
  // the save itself, so the page says it runs rather than that somebody will
  // decide.
  test("an owner edits their own personal script, and the save approves it", async ({ page }) => {
    await gotoScripts(page);
    await page.getByRole("row").filter({ hasText: "My Margin Check" }).click();

    await expect(page.getByText(/This script is yours alone, so saving approves it/)).toBeVisible();
    // The contract says who admitted it, and that nobody reviewed it.
    await expect(page.getByText(/v1 automatically on .* nobody reviewed it/)).toBeVisible();

    const editor = page.locator(".cm-content").first();
    await editor.click();
    await page.keyboard.type("\n# checked by the owner\n");
    await page.getByRole("button", { name: "Save" }).click();

    await expect(page.getByText(/It runs now, and on its schedule/)).toBeVisible();
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
