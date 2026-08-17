import { test, expect, type Page } from "@playwright/test";
import { authenticate } from "../screenshots/helpers/auth";

// Interactive coverage for the call catalog (#1321). The unit tests drive the
// pages with their hooks mocked; these drive the assembled app against the mock
// server, so the tabs, the routes, the query keys, and the decisions a reviewer
// makes are exercised end to end -- including the parts a mocked hook cannot
// show: that publishing a record actually reaches the server and that the page
// then reads back what the record became.

const MY_SESSIONS = "/portal/activity/sessions";
const MY_CALLS = "/portal/activity/calls";
const ADMIN_CALLS = "/portal/admin/calls";

async function gotoMyCalls(page: Page): Promise<void> {
  await authenticate(page);
  await page.goto(MY_CALLS);
  await expect(page.getByRole("tab", { name: "My Calls" })).toHaveAttribute(
    "data-state",
    "active",
  );
}

/** Opens the first satisfied record in the list, which is the one a decision applies to. */
async function openSatisfied(page: Page): Promise<void> {
  await page.getByRole("row").filter({ hasText: "satisfied" }).first().click();
  await expect(page.getByText("Where it came from")).toBeVisible();
}

test.describe("My Calls", () => {
  test("is a tab of Activity, beside the sessions its calls belong to", async ({
    page,
  }) => {
    await authenticate(page);
    await page.goto(MY_SESSIONS);

    await page.getByRole("tab", { name: "My Calls" }).click();
    await expect(page).toHaveURL(new RegExp(`${MY_CALLS}$`));
    await expect(page.getByRole("table")).toBeVisible();

    // And back, so the tab bar is navigation rather than a one-way door.
    await page.getByRole("tab", { name: "My Sessions" }).click();
    await expect(page).toHaveURL(new RegExp(`${MY_SESSIONS}$`));
  });

  // The list is always the reader's own, so a caller facet would be a control
  // the server ignores and a caller column would repeat one name down the page.
  test("carries neither a user column nor a user facet", async ({ page }) => {
    await gotoMyCalls(page);

    await expect(page.getByRole("columnheader", { name: "User" })).toHaveCount(0);
    await expect(page.getByLabel("Filter by user")).toHaveCount(0);
    await expect(page.getByLabel("Filter by kind")).toBeVisible();
    await expect(page.getByLabel("Filter by outcome")).toBeVisible();
  });

  test("opens a record on row click and shows what came of the call", async ({
    page,
  }) => {
    await gotoMyCalls(page);
    await openSatisfied(page);

    await expect(page).toHaveURL(/\/portal\/activity\/calls\/.+/);
    // The reference is what an agent cites the call by, and what fetch reads.
    await expect(page.getByText(/^mcp:call:/).first()).toBeVisible();
    await expect(page.getByText("What it addressed")).toBeVisible();
  });

  test("publishes a record, and then says what it became", async ({ page }) => {
    await gotoMyCalls(page);
    await openSatisfied(page);

    await page.getByRole("button", { name: "Publish" }).click();

    // The page reads back the promotion rather than assuming it: the controls
    // are replaced by what the record became.
    await expect(page.getByText("Published")).toBeVisible();
    await expect(page.getByRole("button", { name: "Publish" })).toHaveCount(0);
  });

  test("declines a record with a note, which stops it being offered", async ({
    page,
  }) => {
    await gotoMyCalls(page);
    await openSatisfied(page);

    await page
      .getByLabel("Why this call is not worth publishing")
      .fill("Superseded by the revenue view.");
    await page.getByRole("button", { name: "Decline" }).click();

    await expect(page.getByRole("heading", { name: "Declined" })).toBeVisible();
    await expect(page.getByText(/Declined by .*Superseded by the revenue view\./)).toBeVisible();
  });

  test("says a record that answered nothing cannot be published yet", async ({
    page,
  }) => {
    await gotoMyCalls(page);
    await page.getByRole("row").filter({ hasText: "ran" }).first().click();

    await expect(page.getByText("Not yet publishable")).toBeVisible();
    await expect(page.getByRole("button", { name: "Publish" })).toHaveCount(0);
  });
});

test.describe("The operator's call catalog", () => {
  test("carries the user facet the caller's own list cannot", async ({ page }) => {
    await authenticate(page);
    await page.goto(ADMIN_CALLS);

    await expect(page.getByRole("columnheader", { name: "User" })).toBeVisible();
    await expect(page.getByLabel("Filter by user")).toBeVisible();
  });

  test("narrows to the records awaiting review", async ({ page }) => {
    await authenticate(page);
    await page.goto(ADMIN_CALLS);

    await page.getByLabel("Filter by review state").click();
    await page.getByRole("option", { name: "Awaiting review" }).click();

    // The queue is the satisfied records with no decision on them yet.
    const outcomes = page.getByRole("row").filter({ hasText: "ran" });
    await expect(outcomes).toHaveCount(0);
    await expect(page.getByRole("row").filter({ hasText: "satisfied" }).first()).toBeVisible();
  });
});
