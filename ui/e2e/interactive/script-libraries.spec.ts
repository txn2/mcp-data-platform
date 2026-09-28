import { test, expect, type Page } from "@playwright/test";
import { authenticate } from "../screenshots/helpers/auth";

// Interactive coverage for libraries on the portal script pages (#1941),
// against the mock server: the listing's Library badge and kind filter, a
// library's own page, and the refusal to delete one a script still loads.

async function gotoScripts(page: Page): Promise<void> {
  await authenticate(page);
  await page.goto("/portal/automations");
  await expect(page.getByRole("heading", { name: "Automations", level: 1 })).toBeVisible();
}

// openSource switches the code card to its Source tab (#1906).
async function openSource(page: Page) {
  await page.getByRole("tab", { name: "Source" }).click();
}

test.describe("Portal script libraries", () => {
  // A library (#1941): code other scripts load, never run or scheduled
  // itself. The listing badges it and narrows to either kind on the server;
  // its page carries nothing that belongs to running, says who loads it, and
  // shows the platform's refusal to delete a library still loaded.
  test("narrows the listing to libraries and opens one, with who loads it", async ({ page }) => {
    await gotoScripts(page);
    const main = page.locator("main");

    const libraryRow = page.getByRole("row").filter({ hasText: "Date Windows" });
    // The badge beside the name, and the Kind column.
    await expect(libraryRow.getByText("Library", { exact: true })).toHaveCount(2);
    await expect(libraryRow.getByTestId("automation-kind")).toHaveText("Library");
    await expect(
      page.getByRole("row").filter({ hasText: "Daily Sales Report" }).getByText("Library", { exact: true }),
    ).toHaveCount(0);

    await main.getByLabel("Filter by kind").click();
    await page.getByRole("option", { name: "Libraries" }).click();
    await expect(page.getByRole("row").filter({ hasText: "Daily Sales Report" })).toHaveCount(0);
    await expect(libraryRow).toBeVisible();

    await main.getByLabel("Filter by kind").click();
    await page.getByRole("option", { name: "Automations" }).click();
    await expect(page.getByRole("row").filter({ hasText: "Date Windows" })).toHaveCount(0);
    await expect(page.getByRole("row").filter({ hasText: "Daily Sales Report" })).toBeVisible();

    await main.getByLabel("Filter by kind").click();
    await page.getByRole("option", { name: "All" }).click();
    await libraryRow.click();

    await expect(page.getByRole("heading", { name: "Date Windows" })).toBeVisible();
    await expect(main.getByText("Library", { exact: true })).toBeVisible();
    await expect(page.getByText("Not running")).toHaveCount(0);
    // No schedule, runs, outputs, state or run access; the administrator's
    // Owner section and the delete stay.
    await expect(page.getByRole("heading", { level: 3 })).toHaveText([
      "Details",
      "About",
      "Used by",
      "Owner",
      "Delete",
    ]);
    await expect(page.getByTestId("library-load-line")).toHaveText('load("lib:date-windows@2", ...)');

    await openSource(page);
    await expect(page.getByRole("button", { name: "Run", exact: true })).toHaveCount(0);
    await expect(page.getByRole("button", { name: "Dry run" })).toHaveCount(0);

    // Deleting a library a script still loads is refused, in the server's words.
    await main.getByRole("button", { name: "Delete script" }).click();
    await page.getByRole("dialog").getByRole("button", { name: "Delete script" }).click();
    await expect(
      page.getByText(/this library is loaded by daily-sales-report, which would fail at their next run/),
    ).toBeVisible();
    await page.getByRole("dialog").getByRole("button", { name: "Cancel" }).click();

    // A row of Used by opens the script that loads it.
    await page.getByTestId("used-by-script-001").click();
    await expect(page.getByRole("heading", { name: "Daily Sales Report" })).toBeVisible();
  });
});
