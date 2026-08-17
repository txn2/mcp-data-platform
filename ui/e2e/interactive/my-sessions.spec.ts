import { test, expect, type Page } from "@playwright/test";
import { authenticate } from "../screenshots/helpers/auth";

// Interactive coverage for the reader's own sessions (#1319). The unit tests
// drive the pages with their hooks mocked; these drive the assembled app
// against the mock server, so the tabs, the routes, the query keys, and the
// walk between an asset and the session that made it are exercised end to end
// -- including the parts a mocked hook cannot show: that the session route
// actually fetches that session, and that the asset's link lands on it.

const ACTIVITY = "/portal/activity";
const MY_SESSIONS = "/portal/activity/sessions";

async function gotoMySessions(page: Page): Promise<void> {
  await authenticate(page);
  await page.goto(MY_SESSIONS);
  await expect(page.getByRole("tab", { name: "My Sessions" })).toHaveAttribute(
    "data-state",
    "active",
  );
}

test.describe("My Sessions", () => {
  test("is a tab of Activity, reached from the aggregates beside it", async ({
    page,
  }) => {
    await authenticate(page);
    await page.goto(ACTIVITY);

    // The Overview tab is the aggregates the dashboard has always shown.
    await expect(page.getByText("Total Calls")).toBeVisible();

    await page.getByRole("tab", { name: "My Sessions" }).click();
    await expect(page).toHaveURL(new RegExp(`${MY_SESSIONS}$`));
    await expect(page.getByRole("table")).toBeVisible();

    // And back, so the tab bar is navigation rather than a one-way door.
    await page.getByRole("tab", { name: "Overview" }).click();
    await expect(page).toHaveURL(new RegExp(`${ACTIVITY}$`));
    await expect(page.getByText("Total Calls")).toBeVisible();
  });

  // The list is always the reader's own, so a caller facet would be a control
  // the server ignores and a caller column would repeat one name down the page.
  test("carries neither a user column nor a user facet", async ({ page }) => {
    await gotoMySessions(page);

    await expect(page.getByRole("columnheader", { name: "User" })).toHaveCount(0);
    await expect(page.getByLabel("Filter by user")).toHaveCount(0);
    await expect(page.getByLabel("Filter by time window")).toBeVisible();
    await expect(page.getByLabel("Filter by session kind")).toBeVisible();
  });

  test("opens a session on row click and reads its calls in order", async ({
    page,
  }) => {
    await gotoMySessions(page);
    await page.getByRole("row").nth(1).click();

    await expect(page).toHaveURL(/\/portal\/activity\/sessions\/.+/);
    await expect(page.getByText("Calls").first()).toBeVisible();
    await expect(page.getByRole("columnheader", { name: "Purpose" })).toBeVisible();

    // Back to the list, from the detail's own back link.
    await page.getByRole("button", { name: "My Sessions" }).click();
    await expect(page).toHaveURL(new RegExp(`${MY_SESSIONS}$`));
  });

  // A session that is not the reader's own is answered not-found, so the page
  // says so rather than rendering an empty shell of a session.
  test("explains a session it cannot read", async ({ page }) => {
    await authenticate(page);
    await page.goto("/portal/activity/sessions/dps_not_mine");

    await expect(
      page.getByText(/No calls of yours are recorded for this session/),
    ).toBeVisible();
  });

  test("walks from an asset to the session that made it, and back", async ({
    page,
  }) => {
    await authenticate(page);
    await page.goto("/portal/assets/ast-001");

    // The metadata sidebar is behind a toggle in the viewer's toolbar.
    await page.getByRole("button", { name: /details/i }).click();
    await page.getByRole("button", { name: "Open session" }).click();

    await expect(page).toHaveURL(/\/portal\/activity\/sessions\/dps_.+/);
    await expect(page.getByRole("columnheader", { name: "Purpose" })).toBeVisible();

    // The session lists what it produced, which is where the walk started.
    await expect(page.getByText("Q4 Revenue Dashboard")).toBeVisible();
    await page.getByText("Q4 Revenue Dashboard").click();
    await expect(page).toHaveURL(/\/portal\/assets\/ast-001$/);
  });
});
