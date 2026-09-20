import { test, expect, type Page } from "@playwright/test";
import { authenticate } from "../screenshots/helpers/auth";

// The scripts listing could not be ordered, and every row carried a pill wall
// (#1795). Four narrowing controls stacked above a four-column table, one of
// them an uncapped tag cloud, and the only ordering the platform could produce
// was `updated_at DESC` -- the store took none from the caller.
//
// It is one filter bar, one health line and one sortable table now, and the
// ordering is the server's: sorting in the browser would order the page the
// 200-row cap returned, so "A-Z" would silently mean "A-Z within the most
// recently updated 200".

const SCRIPTS = "/portal/scripts";

async function openScripts(page: Page): Promise<void> {
  await authenticate(page);
  await page.goto(SCRIPTS);
  await expect(page.getByRole("columnheader", { name: /Script/ })).toBeVisible({
    timeout: 20_000,
  });
}

/** The query string of the most recent listing request. */
async function listingQueries(page: Page): Promise<string[]> {
  return page.evaluate(() => (window as unknown as { __queries: string[] }).__queries ?? []);
}

/** Records every /scripts listing request the page makes. */
async function recordQueries(page: Page): Promise<void> {
  await page.addInitScript(() => {
    const seen: string[] = [];
    (window as unknown as { __queries: string[] }).__queries = seen;
    const original = window.fetch;
    window.fetch = (input, init) => {
      const url = typeof input === "string" ? input : (input as Request).url ?? String(input);
      if (url.includes("/portal/scripts?") || url.endsWith("/portal/scripts")) {
        seen.push(url);
      }
      return original(input, init);
    };
  });
}

// The display names in the table, top to bottom. By test id rather than by
// class: the category badge sits in the same cell and carries the same weight
// class, so a class selector returns the two interleaved.
async function names(page: Page): Promise<string[]> {
  return page.getByTestId("script-name").allInnerTexts();
}

test.describe("The listing can be ordered", () => {
  test("it opens on most recently updated, and the server is asked", async ({ page }) => {
    await recordQueries(page);
    await openScripts(page);

    const queries = await listingQueries(page);
    expect(queries.some((q) => q.includes("sort=updated_at") && q.includes("dir=desc"))).toBe(true);
  });

  test("clicking Script orders by name and marks the header", async ({ page }) => {
    await recordQueries(page);
    await openScripts(page);

    await page.getByRole("columnheader", { name: /Script/ }).click();
    await expect
      .poll(async () => (await listingQueries(page)).some((q) => q.includes("sort=display_name")))
      .toBe(true);

    // Ordered, not merely requested.
    const ordered = await names(page);
    expect(ordered).toEqual([...ordered].sort((a, b) => a.localeCompare(b)));
  });

  test("clicking it again reverses the listing", async ({ page }) => {
    await openScripts(page);

    const header = page.getByRole("columnheader", { name: /Script/ });
    await header.click();
    const ascending = await names(page);
    await header.click();
    await expect.poll(async () => (await names(page)).join("|")).toBe(
      [...ascending].reverse().join("|"),
    );
  });

  test("Author sorts; Last run carries no sort affordance", async ({ page }) => {
    await recordQueries(page);
    await openScripts(page);

    await page.getByRole("columnheader", { name: /Author/ }).click();
    await expect
      .poll(async () => (await listingQueries(page)).some((q) => q.includes("sort=owner_email")))
      .toBe(true);

    await page.getByRole("columnheader", { name: "Last run" }).click();
    const queries = await listingQueries(page);
    expect(queries.some((q) => q.includes("sort=last_run"))).toBe(false);
  });
});

test.describe("One filter bar, no chip cloud", () => {
  test("the tag vocabulary is reachable only through its facet", async ({ page }) => {
    await openScripts(page);

    for (const facet of ["author", "category", "tag", "status"]) {
      await expect(page.getByLabel(`Filter by ${facet}`)).toBeVisible();
    }
    // The three counting tiles are gone, and with them ~90px of the page.
    await expect(page.getByText("Scheduled", { exact: true })).toHaveCount(0);
  });

  test("a row carries no tag badges, and its category once", async ({ page }) => {
    await openScripts(page);

    const firstRow = page.locator("tbody tr").first();
    const badges = firstRow.locator("td:first-child span[data-slot='badge']");
    // At most one badge beside the name: the category. The inert badge only
    // appears on a script that will execute nothing.
    expect(await badges.count()).toBeLessThanOrEqual(2);
  });
});

test.describe("The health line", () => {
  test("it states the counts and filters to the failures", async ({ page }) => {
    await openScripts(page);

    await expect(page.getByTestId("script-health-total")).toBeVisible();

    const failing = page.getByTestId("script-health-failing");
    if ((await failing.count()) === 0) {
      test.skip(true, "no fixture script failed its last run");
      return;
    }
    await expect(failing).toHaveAttribute("aria-pressed", "false");
    await failing.click();
    await expect(failing).toHaveAttribute("aria-pressed", "true");

    // Every row left is one whose last run failed.
    const statuses = await page.locator("tbody tr td:nth-child(4)").allInnerTexts();
    for (const status of statuses) {
      expect(status.toLowerCase()).toContain("failed");
    }
  });
});

test.describe("Scope", () => {
  test("it opens on the reader's own and remembers what they chose", async ({ page }) => {
    await openScripts(page);

    await expect(page.getByRole("tab", { name: "Mine" })).toHaveAttribute(
      "data-state",
      "active",
    );

    await page.getByRole("tab", { name: "All" }).click();
    await expect(page.getByRole("tab", { name: "All" })).toHaveAttribute(
      "data-state",
      "active",
    );

    // Its own key, not the assets one: browsing every asset says nothing about
    // whose scripts a reader wants.
    await expect
      .poll(() => page.evaluate(() => localStorage.getItem("script-scope")))
      .toBe("all");
    expect(await page.evaluate(() => localStorage.getItem("asset-scope"))).toBeNull();

    await page.reload();
    await expect(page.getByRole("tab", { name: "All" })).toHaveAttribute(
      "data-state",
      "active",
    );
  });

  test("a script the reader does not own shows no run state", async ({ page }) => {
    await openScripts(page);
    await page.getByRole("tab", { name: "All" }).click();
    await expect(page.locator("tbody tr")).not.toHaveCount(0);

    // Somebody else's rows are listed, and their last run is withheld.
    const rows = page.locator("tbody tr");
    const count = await rows.count();
    let sawUnowned = false;
    for (let i = 0; i < count; i += 1) {
      const lastRun = (await rows.nth(i).locator("td:nth-child(4)").innerText()).trim();
      if (lastRun === "—") sawUnowned = true;
    }
    expect(sawUnowned, "scope=all should list a script the reader does not own").toBe(true);
  });
});

test.describe("Row click", () => {
  test("a row opens the script, unchanged", async ({ page }) => {
    await openScripts(page);
    await page.locator("tbody tr").first().click();
    await expect(page).toHaveURL(/\/portal\/scripts\/.+/);
  });
});
