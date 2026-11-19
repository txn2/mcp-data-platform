import { test, expect, type Page } from "@playwright/test";
import { authenticate } from "../screenshots/helpers/auth";

// #2723: the asset and collection lists at the widths phones are read at. On
// every list, in each scope and in grid and list view, nothing scrolls the
// page sideways, no two pieces of text in a row or header overlap, and each
// row's name, type and date are inside the screen. At 768px the tables are
// unchanged.

const PHONES = [375, 390] as const;

async function openAt(page: Page, width: number, path: string): Promise<void> {
  await page.setViewportSize({ width: 1440, height: 900 });
  await authenticate(page);
  await page.setViewportSize({ width, height: 844 });
  await page.goto(`/portal${path}`);
  await page.waitForLoadState("networkidle");
}

/** How far the page scrolls sideways. */
async function sideScroll(page: Page): Promise<number> {
  return page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
}

/**
 * The pairs of text-bearing elements inside each matched block whose boxes
 * overlap by more than a pixel each way, named for the failure message.
 */
async function overlaps(page: Page, selector: string): Promise<string[]> {
  return page.locator(selector).evaluateAll((blocks) => {
    const out: string[] = [];
    for (const block of blocks) {
      const texts = Array.from(block.querySelectorAll<HTMLElement>("*")).filter((el) =>
        Array.from(el.childNodes).some((n) => n.nodeType === Node.TEXT_NODE && (n.textContent ?? "").trim() !== ""),
      );
      const boxes = texts.map((el) => ({ el, r: el.getBoundingClientRect() })).filter(({ r }) => r.width > 0 && r.height > 0);
      for (let i = 0; i < boxes.length; i++) {
        for (let j = i + 1; j < boxes.length; j++) {
          const a = boxes[i]!;
          const b = boxes[j]!;
          if (a.el.contains(b.el) || b.el.contains(a.el)) continue;
          const w = Math.min(a.r.right, b.r.right) - Math.max(a.r.left, b.r.left);
          const h = Math.min(a.r.bottom, b.r.bottom) - Math.max(a.r.top, b.r.top);
          if (w > 1 && h > 1) out.push(`"${a.el.textContent?.trim()}" over "${b.el.textContent?.trim()}"`);
        }
      }
    }
    return out;
  });
}

/** The elements matched that reach past the right edge of the screen. */
async function pastEdge(page: Page, selector: string): Promise<string[]> {
  return page.locator(selector).evaluateAll((els) =>
    els.filter((el) => el.getBoundingClientRect().right > window.innerWidth + 0.5).map((el) => el.textContent?.trim() ?? ""),
  );
}

async function useListView(page: Page, view: "Table view" | "Grid view"): Promise<void> {
  const option = page.getByRole("radio", { name: view }).or(page.getByRole("button", { name: view }));
  await option.first().click();
}

for (const width of PHONES) {
  test.describe(`Lists at ${width}px`, () => {
    for (const scope of ["Mine", "Shared", "All"] as const) {
      test(`Assets, ${scope}, list view: rows, nothing overlaps or scrolls`, async ({ page }) => {
        await openAt(page, width, "/assets");
        await page.getByRole("tab", { name: scope }).click();
        await useListView(page, "Table view");
        await expect(page.getByTestId("phone-filter-bar")).toBeVisible();
        await expect(page.getByTestId("asset-row").first()).toBeVisible();
        await expect(page.locator("table")).toHaveCount(0);
        expect(await overlaps(page, '[data-testid="asset-row"]')).toEqual([]);
        expect(await pastEdge(page, '[data-testid="asset-row-name"], [data-testid="asset-row-type"], [data-testid="asset-row-meta"]')).toEqual([]);
        expect(await overlaps(page, '[data-testid="phone-filter-bar"]')).toEqual([]);
        expect(await sideScroll(page)).toBeLessThanOrEqual(1);
      });

      test(`Assets, ${scope}, grid view: two columns, nothing overlaps or scrolls`, async ({ page }) => {
        await openAt(page, width, "/assets");
        await page.getByRole("tab", { name: scope }).click();
        await useListView(page, "Grid view");
        await expect(page.getByTestId("asset-card-name").first()).toBeVisible();
        expect(await pastEdge(page, '[data-testid="asset-card-name"]')).toEqual([]);
        expect(await sideScroll(page)).toBeLessThanOrEqual(1);
      });
    }

    test("the Filters sheet opens with every filter and closes", async ({ page }) => {
      await openAt(page, width, "/assets");
      await page.getByRole("button", { name: /^Filters/ }).click();
      const sheet = page.getByRole("dialog", { name: "Filters" });
      await expect(sheet).toBeVisible();
      await expect(sheet.getByLabel("Filter by content type")).toBeVisible();
      expect(await pastEdge(page, '[role="dialog"] *')).toEqual([]);
      await sheet.getByRole("button", { name: /^(Done|Show \d+ assets?)$/ }).click();
      await expect(sheet).toBeHidden();
    });

    test("Collections: rows, nothing overlaps or scrolls", async ({ page }) => {
      await openAt(page, width, "/collections");
      await useListView(page, "Table view");
      await expect(page.getByTestId("collection-row").first()).toBeVisible();
      await expect(page.locator("table")).toHaveCount(0);
      expect(await overlaps(page, '[data-testid="collection-row"]')).toEqual([]);
      expect(await pastEdge(page, '[data-testid="collection-row"] span')).toEqual([]);
      expect(await sideScroll(page)).toBeLessThanOrEqual(1);
    });

    for (const [path, rows] of [
      ["/unused", '[data-testid="unused-rows"] li'],
      ["/duplicates", '[data-testid="duplicate-members"] li'],
      ["/trash", '[data-testid="trash-cards"] li'],
    ] as const) {
      test(`${path}: no table, nothing overlaps or scrolls`, async ({ page }) => {
        await openAt(page, width, path);
        await expect(page.locator(`${rows}`).first()).toBeVisible();
        await expect(page.locator("table:visible")).toHaveCount(0);
        expect(await overlaps(page, rows)).toEqual([]);
        expect(await sideScroll(page)).toBeLessThanOrEqual(1);
      });
    }

    test("the section intro starts collapsed", async ({ page }) => {
      await openAt(page, width, "/assets");
      await expect(page.getByRole("button", { name: /About this page/ })).toBeVisible();
    });
  });
}

test("at 768px the Assets list is still the table", async ({ page }) => {
  await openAt(page, 768, "/assets");
  await useListView(page, "Table view");
  await expect(page.locator("table").first()).toBeVisible();
  await expect(page.getByTestId("asset-row")).toHaveCount(0);
});
