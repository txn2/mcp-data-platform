import { test, expect, type Page } from "@playwright/test";
import { authenticate } from "../screenshots/helpers/auth";

// The Automations listing at the widths it is read at (#2601). The table's
// columns follow the width the listing HAS (the sidebar takes 16rem beside
// it), every row is at most two lines, nothing is clipped at the card's edge,
// nothing scrolls sideways, and a phone gets a list.

async function openAt(page: Page, width: number, height = 900): Promise<void> {
  await page.setViewportSize({ width: 1440, height: 900 });
  await authenticate(page);
  await page.setViewportSize({ width, height });
  await page.goto("/portal/automations");
}

async function sideScroll(page: Page): Promise<{ table: number; main: number; doc: number }> {
  return page.evaluate(() => {
    const table = document.querySelector('[data-testid="script-table"] table')?.parentElement;
    const main = document.querySelector("main");
    const root = document.documentElement;
    return {
      table: table ? table.scrollWidth - table.clientWidth : 0,
      main: main ? main.scrollWidth - main.clientWidth : 0,
      doc: root.scrollWidth - root.clientWidth,
    };
  });
}

async function rowHeights(page: Page): Promise<number[]> {
  return page.locator('[data-testid^="script-row-"]').evaluateAll((rows) => rows.map((r) => r.getBoundingClientRect().height));
}

for (const scheme of ["light", "dark"] as const) {
  test.describe(`Automations listing, ${scheme}`, () => {
    test.use({ colorScheme: scheme });

    test("at 1280px every row is two lines, the endpoint folded under the name, nothing scrolls", async ({ page }) => {
      await openAt(page, 1280);
      await expect(page.locator('[data-testid="script-table"]')).toBeVisible();
      const heights = await rowHeights(page);
      expect(heights.length).toBeGreaterThan(0);
      const tallest = Math.max(...heights);
      expect(tallest, "a row is taller than two lines").toBeLessThan(64);
      const s = await sideScroll(page);
      expect(s.table, "the table scrolls sideways").toBeLessThanOrEqual(1);
      expect(s.doc, "the page scrolls sideways").toBeLessThanOrEqual(1);
      // No endpoint shows the prefix every endpoint shares; the full address is on hover.
      for (const path of await page.getByTestId("endpoint-path").all()) {
        await expect(path).not.toContainText("/endpoints/");
        await expect(path).toHaveAttribute("title", /\/endpoints\//);
      }
    });

    test("with room for every column, Kind is an icon column no wider than 34px", async ({ page }) => {
      await openAt(page, 1640);
      await expect(page.locator('[data-testid="script-table"]')).toHaveAttribute("data-layout", "wide");
      const kind = await page.getByRole("columnheader", { name: "Kind" }).boundingBox();
      expect(kind!.width).toBeLessThanOrEqual(34.5);
      await expect(page.getByRole("img", { name: /^Library: / }).first()).toBeVisible();
      await expect(page.getByRole("img", { name: /^Script: / }).first()).toBeVisible();
      // Updated is short, with the exact time on hover; never toLocaleString with seconds.
      for (const cell of await page.getByTestId("script-updated").all()) {
        await expect(cell).not.toHaveText(/\d:\d\d:\d\d/);
        await expect(cell).toHaveAttribute("title", /\d/);
      }
      expect((await sideScroll(page)).table).toBeLessThanOrEqual(1);
    });

    test("a row opens from a click, and from Enter on the focused row", async ({ page }) => {
      await openAt(page, 1440);
      const row = page.locator('[data-testid^="script-row-"]').first();
      await row.focus();
      await page.keyboard.press("Enter");
      await expect(page).toHaveURL(/\/portal\/automations\/[^/]+$/);
    });

    test("at 900px there is no sideways scroll", async ({ page }) => {
      await openAt(page, 900);
      const s = await sideScroll(page);
      expect(s.table).toBeLessThanOrEqual(1);
      expect(s.main).toBeLessThanOrEqual(1);
      expect(s.doc).toBeLessThanOrEqual(1);
    });

    test("on a phone the listing is a list, with search and Filters", async ({ page }) => {
      await openAt(page, 390, 844);
      await expect(page.getByTestId("script-list")).toBeVisible();
      await expect(page.locator('[data-testid="script-table"]')).toHaveCount(0);
      await expect(page.getByLabel("Search automations")).toBeVisible();
      await expect(page.getByLabel("Filter by kind")).toHaveCount(0);
      await page.getByRole("button", { name: /Filters/ }).click();
      await expect(page.getByRole("dialog", { name: "Filters" }).getByLabel("Filter by kind")).toBeVisible();
      await page.keyboard.press("Escape");
      const s = await sideScroll(page);
      expect(s.main).toBeLessThanOrEqual(1);
      expect(s.doc).toBeLessThanOrEqual(1);
      await page.getByTestId("script-list").getByRole("button").first().click();
      await expect(page).toHaveURL(/\/portal\/automations\/[^/]+$/);
    });
  });
}
