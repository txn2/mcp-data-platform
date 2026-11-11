import { test, expect, type Page } from "@playwright/test";
import { authenticate } from "../screenshots/helpers/auth";

// The Sites list at the widths it is read at (#2612), against MSW's crowded
// list: a site with a long name and description, a long owner address, an
// address of about 60 characters, four environments and two domains on each.
// The table's columns follow the width the list HAS (the sidebar takes 16rem
// beside it); every row is at most two lines, nothing is cut at the card's
// edge, nothing scrolls sideways, and a phone gets a list.

const CROWDED = "marketing-launch-fall-2026-regional-pages";

async function openAt(page: Page, width: number, height = 900): Promise<void> {
  await page.setViewportSize({ width: 1440, height: 900 });
  await authenticate(page);
  await page.setViewportSize({ width, height });
  await page.goto("/portal/sites?state=crowded");
  await expect(page.getByTestId(`site-row-${CROWDED}`)).toBeVisible();
}

async function sideScroll(page: Page): Promise<{ table: number; doc: number }> {
  return page.evaluate(() => {
    const table = document.querySelector('[data-testid="site-table"] table')?.parentElement;
    const root = document.documentElement;
    return {
      table: table ? table.scrollWidth - table.clientWidth : 0,
      doc: root.scrollWidth - root.clientWidth,
    };
  });
}

async function rowHeights(page: Page): Promise<number[]> {
  return page.locator('[data-testid^="site-row-"]').evaluateAll((rows) => rows.map((r) => r.getBoundingClientRect().height));
}

/** Whether an element's text is cut: it holds more than it shows. */
async function cut(page: Page, selector: string): Promise<boolean> {
  return page.locator(selector).first().evaluate((el) => el.scrollWidth > el.clientWidth);
}

for (const scheme of ["light", "dark"] as const) {
  test.describe(`Sites list, ${scheme}`, () => {
    test.use({ colorScheme: scheme });

    for (const [width, layout] of [
      [1440, "wide"],
      [1280, "medium"],
      [900, "narrow"],
    ] as const) {
      test(`at ${width}px every row is two lines, Environments and every arrow are on the card, nothing scrolls`, async ({ page }) => {
        await openAt(page, width);
        await expect(page.getByTestId("site-table")).toHaveAttribute("data-layout", layout);
        const heights = await rowHeights(page);
        expect(heights.length).toBeGreaterThan(3);
        expect(Math.max(...heights), "a row is taller than two lines").toBeLessThan(72);
        const s = await sideScroll(page);
        expect(s.table, "the table scrolls sideways").toBeLessThanOrEqual(1);
        expect(s.doc, "the page scrolls sideways").toBeLessThanOrEqual(1);

        const card = await page.getByTestId("site-table").boundingBox();
        const envs = await page.getByRole("columnheader", { name: "Environments" }).boundingBox();
        expect(envs, "no Environments header").not.toBeNull();
        expect(envs!.x + envs!.width, "Environments runs past the card").toBeLessThanOrEqual(card!.x + card!.width + 1);
        const rows = page.locator('[data-testid^="site-row-"]');
        for (let i = 0; i < (await rows.count()); i++) {
          const arrow = await rows.nth(i).locator("td").last().locator("svg").boundingBox();
          expect(arrow, `row ${i} has no arrow`).not.toBeNull();
          expect(arrow!.x + arrow!.width, `row ${i}'s arrow is past the card`).toBeLessThanOrEqual(card!.x + card!.width + 1);
        }
      });
    }

    test("the long description and address are cut with an ellipsis and whole on hover", async ({ page }) => {
      await openAt(page, 1280);
      const row = page.getByTestId(`site-row-${CROWDED}`);
      const desc = row.getByTestId("site-description");
      await expect(desc).toHaveAttribute("title", /^Landing pages for the fall launch/);
      expect(await cut(page, `[data-testid="site-row-${CROWDED}"] [data-testid="site-description"]`), "the description is not cut").toBe(true);
      await expect(desc).toHaveCSS("text-overflow", "ellipsis");
      const address = row.getByTestId("site-address");
      await expect(address).toHaveAttribute("title", `${CROWDED}.pages.example.net, opens in a new tab`);
      const text = address.locator(".truncate");
      await expect(text).toHaveCSS("text-overflow", "ellipsis");
      expect(await cut(page, `[data-testid="site-row-${CROWDED}"] [data-testid="site-address"] .truncate`), "the address is not cut").toBe(true);
      // The domains are on the site's page, not in the list.
      await expect(row.getByText("prod.fall-launch.example.com")).toHaveCount(0);
    });

    test("the owner's address is cut and whole on hover where it shows", async ({ page }) => {
      await openAt(page, 1440);
      const row = page.getByTestId(`site-row-${CROWDED}`);
      const owner = row.getByRole("img", { name: "priya.ramanathan-oconnell@example.com" });
      await expect(owner).toHaveText("PR");
      const email = row.getByText("priya.ramanathan-oconnell@example.com");
      await expect(email).toHaveAttribute("title", "priya.ramanathan-oconnell@example.com");
      await expect(email).toHaveCSS("text-overflow", "ellipsis");
    });

    test("a click anywhere on a row but its address opens the site", async ({ page }) => {
      await openAt(page, 1280);
      await page.getByTestId("site-row-marketing").getByText("Company website").click();
      await expect(page).toHaveURL(/\/portal\/sites\/marketing(\/environments)?$/);
    });

    test("at 390px the list is a list with no table and nothing scrolls sideways", async ({ page }) => {
      await openAt(page, 390, 844);
      await expect(page.getByTestId("site-list")).toBeVisible();
      await expect(page.getByTestId("site-table")).toHaveCount(0);
      const s = await sideScroll(page);
      expect(s.doc, "the page scrolls sideways").toBeLessThanOrEqual(1);
      const row = page.getByTestId(`site-row-${CROWDED}`);
      await expect(row.getByTestId("env-badge-prod")).toBeVisible();
      const box = await row.boundingBox();
      expect(box!.x + box!.width, "a row runs past the screen").toBeLessThanOrEqual(390);
      await row.getByText(CROWDED, { exact: true }).click();
      await expect(page).toHaveURL(new RegExp(`/portal/sites/${CROWDED}(/environments)?$`));
    });
  });
}
