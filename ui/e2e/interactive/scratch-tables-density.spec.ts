import { test, expect, type Page, type Locator } from "@playwright/test";
import { authenticate } from "../screenshots/helpers/auth";

// The Scratch Tables listing used to paint a long qualified name over the two
// columns beside it (#1796). The cell asked for a bound it never got: a
// `max-w` utility on a `<td>` does nothing under `table-layout: auto`, which
// sizes a column to its content. The listing is laid out `table-fixed` with
// declared column widths now, and Registered and State are one provenance cell
// rather than two columns repeating the same sentence on every row.
//
// reg_c71b45 is the fixture that makes the case: a table registered from a
// spreadsheet whose contents named it, 97 characters qualified.

const TABLES = "/portal/scratch-tables";
const LONG_ROW = "scratch-table-reg_c71b45";

async function openListing(page: Page): Promise<void> {
  await authenticate(page);
  await page.goto(TABLES);
  await expect(page.getByTestId(LONG_ROW)).toBeVisible({ timeout: 20_000 });
}

/**
 * Asserts no two cells in the row overlap horizontally.
 *
 * Boxes rather than a screenshot: the defect was one cell's text drawn across
 * the next cell's box, which a pixel comparison reports as "something moved"
 * and a geometric check reports as which two columns collided.
 */
async function expectNoOverlap(row: Locator): Promise<void> {
  const cells = row.locator("td");
  const count = await cells.count();
  const boxes: { left: number; right: number; text: string }[] = [];
  for (let i = 0; i < count; i += 1) {
    const cell = cells.nth(i);
    const box = await cell.boundingBox();
    expect(box, `cell ${i} has no box`).not.toBeNull();
    // The text's own width, not the cell's: a cell that contains an
    // overflowing span reports the cell's box while painting past it.
    const overflow = await cell.evaluate((el) => {
      const range = document.createRange();
      range.selectNodeContents(el);
      const r = range.getBoundingClientRect();
      range.detach();
      return { left: r.left, right: r.right };
    });
    boxes.push({
      left: Math.min(box!.x, overflow.left),
      right: Math.max(box!.x + box!.width, overflow.right),
      text: (await cell.innerText()).slice(0, 40).replace(/\s+/g, " "),
    });
  }
  for (let i = 1; i < boxes.length; i += 1) {
    const prev = boxes[i - 1]!;
    const here = boxes[i]!;
    expect(
      prev.right,
      `"${prev.text}" paints into the cell holding "${here.text}"`,
    ).toBeLessThanOrEqual(here.left + 1);
  }
}

test.describe("A long table name stays in its column", () => {
  for (const width of [1280, 1440, 1920]) {
    test(`no cell paints over another at ${width}px`, async ({ page }) => {
      await page.setViewportSize({ width, height: 900 });
      await openListing(page);
      await expectNoOverlap(page.getByTestId(LONG_ROW));
    });
  }

  test("the name is whole, and wraps inside its own column", async ({ page }) => {
    await openListing(page);
    const name = page.getByTestId(LONG_ROW).locator("td").first();
    // Whole, not truncated: it is what a reader retypes into a FROM clause.
    await expect(name).toContainText(
      "scratch.uploads.analyst_store_list_western_region_locations_addresses_opening_dates_by_store_code",
    );
    const wrapped = await name.evaluate((el) => el.getBoundingClientRect().height > 24);
    expect(wrapped, "a 97-character name should occupy more than one line").toBe(true);
  });

  test("every column is laid out by the table, not by its content", async ({ page }) => {
    await openListing(page);
    const layout = await page
      .getByTestId(LONG_ROW)
      .evaluate((tr) => getComputedStyle(tr.closest("table")!).tableLayout);
    expect(layout).toBe("fixed");
  });
});

test.describe("Provenance is one cell", () => {
  test("a row states who registered it, when, and its currency verdict", async ({ page }) => {
    await openListing(page);
    const row = page.getByTestId(LONG_ROW);
    await expect(row.locator("td")).toHaveCount(5);
    const provenance = row.locator("td").nth(4);
    await expect(provenance).toContainText("marcus.johnson@example.com");
    await expect(provenance).toContainText("Follows the file");
  });

  test("an exceptional verdict is fully visible, not clipped at the edge", async ({ page }) => {
    await openListing(page);
    // reg_a04e12's file is gone; reg_7b3d90 is behind its file. Both are the
    // verdicts a reader opens this page to find, and both used to be clipped.
    for (const [id, verdict] of [
      ["scratch-table-reg_a04e12", "Source deleted"],
      ["scratch-table-reg_7b3d90", "Behind the file"],
    ] as const) {
      const badge = page.getByTestId(id).getByText(verdict);
      await expect(badge).toBeVisible();
      const box = await badge.boundingBox();
      const table = await page.getByTestId(id).evaluate((tr) => {
        const r = tr.closest("table")!.getBoundingClientRect();
        return { right: r.right };
      });
      expect(box!.x + box!.width).toBeLessThanOrEqual(table.right + 1);
    }
  });
});

test.describe("One registration's page names the file it reads", () => {
  test("it shows the file's own description, and a long name keeps the actions", async ({ page }) => {
    await openListing(page);
    await page.getByTestId(LONG_ROW).click();

    await expect(page.getByText("The file behind this table")).toBeVisible();
    // The description is the source record's own, carried on the `source`
    // object by the two table routes (#1796).
    await expect(
      page.getByText("Western region stores with location codes", { exact: false }),
    ).toBeVisible();

    // The header holds a 78-character table name without pushing its actions
    // off the row.
    const actions = page.getByTestId("page-header-actions");
    await expect(actions).toBeVisible();
    const box = await actions.boundingBox();
    expect(box!.x + box!.width).toBeLessThanOrEqual(1440);
    await expect(page.getByLabel("Copy the table name")).toBeVisible();
  });

  test("the column type is stated once when every column carries it", async ({ page }) => {
    await openListing(page);
    await page.getByTestId(LONG_ROW).click();
    await expect(page.getByText("Every column is VARCHAR.")).toBeVisible();
    // Seven columns, and VARCHAR said once rather than seven times.
    const body = await page.locator("main").innerText();
    expect(body.match(/VARCHAR/g)?.length).toBe(1);
  });
});
