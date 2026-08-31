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
  test("lists every script with what it executes, its schedule, and its last run", async ({
    page,
  }) => {
    await gotoScripts(page);

    await expect(page.getByText("Daily Sales Report")).toBeVisible();
    await expect(page.getByText("succeeded").first()).toBeVisible();
    // The version a run executes is a fact about every healthy script, so it
    // is on the script's own page rather than in a column here (#1407).
    await expect(page.getByText("Runs v2")).toHaveCount(0);

    // The schedule in the words the editor two clicks away states it in, rather
    // than the expression the platform stores it as (#1358).
    await expect(
      page.getByText("Every weekday at 7:00 AM, America/Los_Angeles"),
    ).toBeVisible();
    // Every schedule is in words, including the step expressions the builder
    // has no control for (#1405): a cron expression appears only in the editor.
    await expect(page.getByText("Every 30 minutes, UTC")).toBeVisible();
    await expect(page.getByText("*/30 * * * *")).toHaveCount(0);

    // A disabled schedule says so rather than showing a fire that will not happen.
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

  test("opens one script's details, its source, and its run history", async ({ page }) => {
    await gotoScripts(page);
    await page.getByRole("row").filter({ hasText: "Daily Sales Report" }).click();

    // The details: what will execute, on what schedule, and what it takes,
    // read in one section rather than a card apart from it (#1406).
    await expect(page.getByRole("heading", { name: "Daily Sales Report" })).toBeVisible();
    // Both scoped to the details: the schedule editor below states the same
    // schedule and names the same parameter, because that is the box its
    // binding goes in.
    await expect(page.getByRole("cell", { name: "report_date" })).toBeVisible();
    // In words, as every surface states a cadence (#1407): the expression is
    // read and written in the schedule editor, and nowhere else.
    await expect(
      page.getByText("Every weekday at 7:00 AM, America/Los_Angeles", { exact: true }),
    ).toBeVisible();
    await expect(page.getByText("0 7 * * 1-5", { exact: true })).toHaveCount(0);

    // The sections in the order somebody debugging a script reads them, with
    // the run history directly under the code it is explained by (#1406).
    // Owner is last, and is the administrator's — the mock caller is one.
    await expect(page.getByRole("heading", { level: 3 })).toHaveText([
      "Details",
      "Schedule",
      "About",
      "Source",
      "Run history",
      "Files written (3)",
      "State",
      "Owner",
    ]);

    // The bindings every fire passes are inside the folded schedule section.
    await page.getByRole("button", { name: /^Schedule/ }).click();
    await expect(page.locator("#script-param-schedule-report_date")).toHaveValue("${fire_date}");

    // The version that runs is the text in the editor, without a click.
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

  // The schedule controls are one mutation on this surface (#1307), so they
  // are exercised against the mock server rather than only through mocked
  // hooks: the request has to reach the route the server registers, and the
  // answer has to come back into the page that submitted it.
  test("an owner re-times their own script and pauses it", async ({ page }) => {
    await gotoScripts(page);
    await page.getByRole("row").filter({ hasText: "Daily Sales Report" }).click();

    // The section is folded, and says what the script does without being
    // opened (#1407). The builder is behind the reveal.
    await expect(
      page.getByText("Runs: Every weekday at 7:00 AM, America/Los_Angeles"),
    ).toBeVisible();
    await expect(page.getByLabel("Time", { exact: true })).toHaveCount(0);
    await page.getByRole("button", { name: /^Schedule/ }).click();

    // The schedule in force, as choices rather than as an expression, and the
    // binding every fire passes.
    await expect(page.getByRole("button", { name: "Weekdays" })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    await expect(page.getByLabel("Time", { exact: true })).toHaveValue("07:00");
    // The page carries two parameter forms — the one a run and a dry run
    // share, and these bindings — so a control is named by the form it belongs
    // to rather than by the parameter alone, which matches both.
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
    // The folded header would say the same thing: what it does is nothing.
    await expect(page.getByRole("button", { name: "Resume" })).toBeVisible();
  });

  // Running one is the other mutation the owner performs here (#1363), and it
  // is done from the section that holds the code (#1406). It goes through the
  // mock server: the request has to reach the route the server registers, and
  // the answer has to come back into the page.
  test("an owner runs their own script from the section that holds its code", async ({
    page,
  }) => {
    await gotoScripts(page);
    await page.getByRole("row").filter({ hasText: "Daily Sales Report" }).click();

    // Run sits beside Dry run, over the one parameter form they both bind.
    const source = page.locator("div[data-slot=card]").filter({
      has: page.getByRole("heading", { name: "Source" }),
    });
    const run = source.getByRole("button", { name: "Run", exact: true });
    await expect(source.getByRole("button", { name: "Dry run" })).toBeVisible();

    // Both are refused until every required value is bound, and the page says
    // which are missing rather than submitting a request it knows is bad.
    await expect(run).toBeDisabled();
    await expect(page.getByText("report_date, source are required before a run.")).toBeVisible();

    await page.locator("#script-param-run-report_date").fill("2026-08-17");
    // The connection comes from the set this script's caller reaches, so it is
    // chosen. A name outside that set never reaches the form.
    await page.locator("#script-param-run-source").click();
    await page.getByRole("option", { name: /acme-warehouse/ }).click();
    await expect(page.getByRole("option", { name: /acme-lake/ })).toHaveCount(0);

    await expect(run).toBeEnabled();
    await run.click();
    await expect(page.getByText(/Queued\. It appears in this script's run history/)).toBeVisible();
  });

  // The version history is folded into the Source section behind a reveal
  // (#1406): the editor already holds the version that runs, so the history is
  // the versions before it and is not what the page opens on.
  test("an owner opens the versions written before the one in the editor", async ({ page }) => {
    await gotoScripts(page);
    await page.getByRole("row").filter({ hasText: "Daily Sales Report" }).click();

    const reveal = page.getByRole("button", { name: /Version history/ });
    await expect(reveal).toHaveAttribute("aria-expanded", "false");
    await reveal.click();

    // The roles a run of a version presents are the point of the history, and
    // they are one click in rather than on the page by default.
    await page.getByText(/^v1$/).click();
    await expect(page.getByText(/A run of this version presents/)).toBeVisible();
  });

  // Checking an edit before saving the version that runs (#1364). Both actions
  // reach the server: one parses, the other executes as the caller.
  test("an owner validates and dry-runs an edit without persisting anything", async ({ page }) => {
    await gotoScripts(page);
    await page.getByRole("row").filter({ hasText: "Daily Sales Report" }).click();

    await page.getByRole("button", { name: "Validate" }).click();
    await expect(page.getByText("Parses")).toBeVisible();
    // What the edit reaches, which is what its author is otherwise guessing at.
    await expect(page.getByText("platform.query, platform.export").first()).toBeVisible();

    // A dry run binds the live contract's values on the same form a run does,
    // and is unavailable until the required ones are supplied.
    await expect(page.getByRole("button", { name: "Dry run" })).toBeDisabled();
    await page.locator("#script-param-run-report_date").fill("2026-08-17");
    await page.locator("#script-param-run-source").click();
    await page.getByRole("option", { name: /acme-warehouse/ }).click();

    await page.getByRole("button", { name: "Dry run" }).click();
    // The report's own sentences, not the status badge: "succeeded" also
    // appears on every successful row in the run history below.
    await expect(page.getByText(/Nothing was persisted/)).toBeVisible();
    await expect(page.getByText(/would write 1284 rows as csv/)).toBeVisible();
    await expect(page.getByText(/1,284 rows for 2026-08-17/)).toBeVisible();
  });

  // A script with no schedule runs on demand, and the owner gives it one here
  // without asking anybody: every fire executes the latest saved version.
  test("an owner gives an on-demand script a schedule", async ({ page }) => {
    await gotoScripts(page);
    await page.getByRole("row").filter({ hasText: "Dormant Accounts" }).click();

    await expect(page.getByText("Not scheduled")).toBeVisible();
    await page.getByRole("button", { name: /^Schedule/ }).click();
    await expect(page.getByText(/runs only when someone asks/)).toBeVisible();

    await page.getByRole("button", { name: "Daily" }).click();
    await page.getByLabel("Time", { exact: true }).fill("05:00");
    await page.locator("#script-param-schedule-cutoff").fill("2026-01-01");
    await page.getByRole("button", { name: "Set schedule" }).click();
    await expect(page.getByText(/Every day at 5:00 AM/).first()).toBeVisible();
  });

  // The tiles are computed from the listing itself and are the page's own
  // filters (#1405), so they are exercised against the mock server rather than
  // only through mocked hooks.
  test("counts the caller's scripts in tiles that filter the listing", async ({ page }) => {
    await gotoScripts(page);
    // Scoped to the page: the sidebar carries a "Scripts" control of its own,
    // which is the section's nav entry rather than the tile.
    const main = page.locator("main");

    // Three tiles, each named plainly enough to need no caption under it, and
    // the word this page no longer uses is nowhere on it.
    await expect(main.getByRole("button", { name: /^Scripts \d/ })).toBeVisible();
    await expect(main.getByRole("button", { name: /^Scheduled/ })).toBeVisible();
    await expect(main.getByRole("button", { name: /^Failing/ })).toBeVisible();
    await expect(page.getByText(/Automation/i)).toHaveCount(0);

    // Pressing one shows the scripts it counted, and pressing "Scripts" is the
    // way back to all of them.
    await main.getByRole("button", { name: /^Scheduled/ }).click();
    await expect(page.getByText("Dormant Accounts")).toHaveCount(0);
    await main.getByRole("button", { name: /^Scripts \d/ }).click();
    await expect(page.getByText("Dormant Accounts")).toBeVisible();
  });

  // The search is a query predicate, so it reaches the route the server
  // registers rather than filtering rows the page already holds.
  test("narrows the listing by what the reader typed", async ({ page }) => {
    await gotoScripts(page);

    await page.getByLabel("Search scripts").fill("margin");
    await expect(page.getByText("My Margin Check")).toBeVisible();
    await expect(page.getByText("Daily Sales Report")).toHaveCount(0);

    await page.getByLabel("Search scripts").fill("");
    await expect(page.getByText("Daily Sales Report")).toBeVisible();
  });

  // The Runs tab (#1405): every run of every script this person owns, and the
  // way from one of them to the run itself. It goes through the mock server
  // because the listing, the address it links to, and the run that address
  // opens are three different requests.
  test("reads every run across the caller's scripts, and opens one", async ({ page }) => {
    await gotoScripts(page);
    await page.getByRole("tab", { name: "Runs" }).click();

    // Runs from more than one script, newest first, with the reason a failure
    // failed in the row rather than behind it.
    await expect(page.getByRole("cell", { name: /Daily Sales Report/ }).first()).toBeVisible();
    await expect(page.getByRole("cell", { name: /Warehouse Freshness Check/ })).toBeVisible();
    await expect(page.getByText(/relation "sales.orders" does not exist/)).toBeVisible();

    // A row opens the run itself: its script's page, with that run's log,
    // parameters and outputs already open.
    await page.getByRole("row").filter({ hasText: /Daily Sales Report/ }).first().click();
    await expect(page).toHaveURL(/\/scripts\/script-001\/runs\//);
    await expect(page.getByText(/wrote asset version 42/)).toBeVisible();
  });

  // Moving a script to another person is the administrator's mutation on this
  // surface (#1404), and the one that changes who the script is for. It goes
  // through the mock server because the outcome the page reports — where the
  // script landed and what it means for the next run — comes back from the
  // route rather than from the form.
  test("an administrator moves a script to another owner", async ({ page }) => {
    await gotoScripts(page);
    await page.getByRole("row").filter({ hasText: "Daily Sales Report" }).click();

    // The section states who has it before it offers to move it.
    await expect(page.getByText(/only person who sees it/)).toBeVisible();

    // The new owner is chosen from the people who have actually signed in
    // (#1407): an address nobody has authenticated with cannot open the portal,
    // so a script handed to one would be visible to administrators alone.
    await page.getByLabel("New owner").click();
    await page.getByRole("option", { name: /marcus.johnson@example.com/ }).click();
    await page.getByRole("button", { name: "Transfer ownership" }).click();

    // Both ends of the move are named before it is made, because the person
    // losing the script is the part an administrator can overlook.
    await expect(page.getByText(/will no longer see it/)).toBeVisible();
    await page.getByRole("button", { name: "Transfer", exact: true }).click();

    await expect(page.getByText(/now belongs to marcus.johnson@example.com/)).toBeVisible();
    // The page re-reads the script, so the contract shows where it landed.
    await expect(page.getByText("marcus.johnson@example.com").first()).toBeVisible();
  });

  // The state a script carries between runs (#1537): read on its page at the
  // revision the platform holds it, and reset there. The clear goes through
  // the mock server, so what the card reads back is the revision the reset
  // moved it to, which is what a run in flight fails against.
  test("an owner reads the state a script carries between runs, and clears it", async ({
    page,
  }) => {
    await gotoScripts(page);
    await page.getByRole("row").filter({ hasText: "Daily Sales Report" }).click();

    // Folded, with where the state stands in the header.
    await expect(page.getByRole("button", { name: /^State/ })).toContainText("Revision 41");
    await page.getByRole("button", { name: /^State/ }).click();
    await expect(page.getByTestId("script-state")).toContainText('"synced_through": "2026-08-13"');
    await expect(page.getByText("run run-001")).toBeVisible();

    // A clear is confirmed before it lands, and the answer says what it
    // means for the next run.
    await page.getByRole("button", { name: "Clear state" }).click();
    await expect(page.getByText(/starts from an empty object/)).toBeVisible();
    await page.getByRole("button", { name: "Clear", exact: true }).click();
    await expect(page.getByText(/State cleared/)).toBeVisible();
    await expect(page.getByTestId("script-state")).toHaveText("{}");
    await expect(page.getByText("sarah.chen@example.com").last()).toBeVisible();
    // Folded again, the header states the revision the clear moved it to.
    await page.getByRole("button", { name: /^State/ }).click();
    await expect(page.getByRole("button", { name: /^State/ })).toContainText("Revision 42");
  });

  // Editing the code is the second mutation on this surface, and there is one
  // outcome: the save IS the version that runs from then on.
  test("an owner edits the code, and the save becomes the version that runs", async ({
    page,
  }) => {
    await gotoScripts(page);
    await page.getByRole("row").filter({ hasText: "Daily Sales Report" }).click();

    await expect(page.getByText(/Saving makes this the version that runs/)).toBeVisible();
    const editor = page.locator(".cm-content").first();
    await editor.click();
    await page.keyboard.type("\n# checked by the owner\n");
    await page.getByRole("button", { name: "Save" }).click();

    await expect(page.getByText(/This is the version that runs/)).toBeVisible();
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
