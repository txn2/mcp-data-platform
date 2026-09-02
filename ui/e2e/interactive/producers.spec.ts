import { test, expect, type Page } from "@playwright/test";
import { authenticate } from "../screenshots/helpers/auth";

// openAssetSidebar opens the asset viewer's metadata column. Everything beside
// the content -- provenance, references, and what wrote the file -- is behind
// it, so a panel there is not merely below the fold but unmounted.
async function openAssetSidebar(page: Page): Promise<void> {
  await page.getByRole("button", { name: "Show details" }).click();
  await page.getByText("Provenance").first().waitFor();
}

// Interactive coverage for the producer relation (#1569), read from both ends
// through the assembled app rather than through a panel in isolation.
//
// The panels each have their own component tests. What only the whole app can
// show is the navigation the relation exists for: a report names the script
// that refreshes it, that link opens the script, and the script's own list of
// what it has written links back to the file. That round trip is the feature.

test.describe("What produced a file", () => {
  test("an asset names the script that refreshes it and the person who edits it", async ({
    page,
  }) => {
    await authenticate(page);
    await page.goto("/portal/assets/ast-001");
    await openAssetSidebar(page);

    const panel = page.getByTestId("producers-panel");
    await panel.scrollIntoViewIfNeeded();
    await expect(panel.getByText("Written by (2)")).toBeVisible();

    // Only one of the two brought the report into existence; the other has
    // only changed it since. Distinguishing them is the whole panel.
    await expect(panel.getByText("daily-sales-report")).toBeVisible();
    await expect(panel.getByText("created")).toBeVisible();
    await expect(panel.getByText("alice@example.com")).toBeVisible();
    await expect(panel.getByText("modified")).toBeVisible();
    await expect(panel.getByText(/41 writes, last/)).toBeVisible();
  });

  test("the producing script opens from the asset, and lists the file back", async ({ page }) => {
    await authenticate(page);
    await page.goto("/portal/assets/ast-001");
    await openAssetSidebar(page);

    await page.getByTestId("producers-panel").getByText("daily-sales-report").click();
    await expect(page).toHaveURL(/\/portal\/scripts\/script-001$/);

    const written = page.getByTestId("script-produced");
    await written.scrollIntoViewIfNeeded();
    await expect(written.getByText("Files written (4)")).toBeVisible();
    await expect(written.getByText("Q4 Revenue Dashboard")).toBeVisible();
    await expect(written.getByText("Q4 Performance Review")).toBeVisible();
    await expect(written.getByText("Regional Sales Extract")).toBeVisible();

    // Back to the report, from the row: the relation is a round trip.
    await written.getByText("Q4 Revenue Dashboard").click();
    await expect(page).toHaveURL(/\/portal\/assets\/ast-001$/);
  });

  test("a file the script wrote and that is gone stays listed and does not link", async ({
    page,
  }) => {
    await authenticate(page);
    await page.goto("/portal/scripts/script-001");

    const written = page.getByTestId("script-produced");
    await written.scrollIntoViewIfNeeded();
    // Named by the id the record kept, because there is no name left to read.
    await expect(written.getByText("ast-removed")).toBeVisible();
    await expect(written.getByText("deleted")).toBeVisible();

    await written.getByText("ast-removed").click();
    await expect(page).toHaveURL(/\/portal\/scripts\/script-001$/);
  });

  test("a script that no longer exists is named and not linked", async ({ page }) => {
    await authenticate(page);
    await page.goto("/portal/assets/ast-004");
    await openAssetSidebar(page);

    const panel = page.getByTestId("producers-panel");
    await panel.scrollIntoViewIfNeeded();
    await expect(panel.getByText("quarterly-rollup")).toBeVisible();
    await expect(panel.getByText(/This script no longer exists/)).toBeVisible();

    await panel.getByText("quarterly-rollup").click();
    await expect(page).toHaveURL(/\/portal\/assets\/ast-004$/);
  });

  test("a managed resource names the script that rewrites it and its uploader", async ({
    page,
  }) => {
    await authenticate(page);
    await page.goto("/portal/resources/res-029");

    const panel = page.getByTestId("producers-panel");
    await panel.scrollIntoViewIfNeeded();
    await expect(panel.getByText("Written by (2)")).toBeVisible();
    await expect(panel.getByText("warehouse-freshness")).toBeVisible();
    await expect(panel.getByText("marcus.webb@example.com")).toBeVisible();

    await panel.getByText("warehouse-freshness").click();
    await expect(page).toHaveURL(/\/portal\/scripts\/script-003$/);
  });
});
