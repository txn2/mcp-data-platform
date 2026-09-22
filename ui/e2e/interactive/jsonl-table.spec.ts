import { test, expect, type Page } from "@playwright/test";
import { authenticate } from "../screenshots/helpers/auth";

// A JSON-lines file that is a table reads as one (#1833).
//
// Every trino_export and platform.export in format jsonl writes a table: one
// record per row, the same keys on every line. The viewer showed it only as a
// list of records, one JSON tree per line. It now opens such a file on a Table
// view built from the union of the keys, with the CSV viewer's search, sort and
// row dialog, and keeps the record list one toggle away. An event log, whose
// lines differ in shape, still opens on the record list.

/** The asset viewer's content region, which the toggle and the table are in. */
const viewer = (page: Page) => page.getByTestId("viewer-content");

test.describe("A JSON-lines asset", () => {
  test("that is a table opens on the Table view, searchable and sortable", async ({ page }) => {
    await authenticate(page);
    await page.goto("/portal/assets/ast-jsonl-table");

    await expect(viewer(page).getByRole("button", { name: "Table", exact: true })).toHaveAttribute("aria-pressed", "true");
    const heads = page.getByRole("columnheader");
    await expect(heads).toHaveText(["store", "region", "revenue", "orders", "manager"]);

    await page.getByLabel("Search all columns").fill("north");
    await expect(page.getByRole("button", { name: /^Open row/ })).toHaveCount(1);
    await page.getByLabel("Search all columns").fill("");

    await page.getByRole("columnheader", { name: /revenue/ }).click();
    await expect(page.getByRole("button", { name: /^Open row/ }).first()).toContainText("STR-042");
    await page.getByRole("columnheader", { name: /revenue/ }).click();
    await expect(page.getByRole("button", { name: /^Open row/ }).first()).toContainText("STR-031");
  });

  test("opens a nested value in the row dialog", async ({ page }) => {
    await authenticate(page);
    await page.goto("/portal/assets/ast-jsonl-table");

    const first = page.getByRole("button", { name: "Open row 1" });
    await expect(first).toContainText('{"name":"Ana Ruiz","since":2019}');
    await first.click();
    const fields = page.getByTestId("row-detail-fields");
    await expect(fields).toContainText('"name": "Ana Ruiz"');
    await expect(fields).toContainText('"since": 2019');
  });

  test("toggles to the record list and back", async ({ page }) => {
    await authenticate(page);
    await page.goto("/portal/assets/ast-jsonl-table");

    await viewer(page).getByRole("button", { name: "Records", exact: true }).click();
    await expect(page.getByRole("columnheader")).toHaveCount(0);
    await expect(viewer(page).getByRole("button", { name: /"store":"STR-027"/ })).toBeVisible();
    await viewer(page).getByRole("button", { name: "Table", exact: true }).click();
    await expect(page.getByRole("columnheader")).toHaveCount(5);
  });

  test("that is an event log opens on the record list", async ({ page }) => {
    await authenticate(page);
    await page.goto("/portal/assets/ast-010");

    await expect(viewer(page).getByRole("button", { name: "Records", exact: true })).toHaveAttribute("aria-pressed", "true");
    await expect(page.getByRole("columnheader")).toHaveCount(0);
  });
});
