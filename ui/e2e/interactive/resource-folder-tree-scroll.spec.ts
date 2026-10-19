import { test, expect, type Page } from "@playwright/test";
import { authenticate } from "../screenshots/helpers/auth";
import { ADMIN_RESOURCES, gotoFolder } from "../screenshots/helpers/resources";

// The folder tree is its own scroll container (#2167). It used to grow to the
// height of its content inside an aside that clipped it, so a folder below the
// panel's bottom edge could not be reached. jsdom computes no layout, so this
// is asserted here in a real browser, at a desktop width and below 761px where
// the aside is capped at 200px.

async function overfilledTree(page: Page, size: { width: number; height: number }): Promise<void> {
  // Signed in at the default viewport: the sign-in helper waits for the
  // navigation, which a narrow viewport collapses.
  await authenticate(page);
  await page.setViewportSize(size);
  await gotoFolder(page, ADMIN_RESOURCES, "user");
  const tree = page.getByTestId("folder-tree");
  // Expand every collapsed node the tree shows until the content is taller
  // than the panel, so the case is overfilled whatever the mock holds.
  for (let i = 0; i < 20; i++) {
    const over = await tree.evaluate((el) => el.scrollHeight > el.clientHeight);
    if (over) return;
    const closed = tree.locator('[role="treeitem"][aria-expanded="false"]').first();
    if ((await closed.count()) === 0) break;
    const id = await closed.getAttribute("data-node-id");
    // The twisty toggles; a click on the rest of a folder row opens the folder.
    await closed.locator("span").first().click();
    await expect(tree.locator(`[data-node-id="${id}"]`)).toHaveAttribute("aria-expanded", "true");
  }
}

async function assertTreeScrolls(page: Page): Promise<void> {
  const tree = page.getByTestId("folder-tree");
  const box = await tree.evaluate((el) => {
    const aside = el.parentElement!.getBoundingClientRect();
    const own = el.getBoundingClientRect();
    return {
      scrollHeight: el.scrollHeight,
      clientHeight: el.clientHeight,
      bottom: own.bottom,
      asideBottom: aside.bottom,
      overflowY: getComputedStyle(el).overflowY,
    };
  });
  expect(box.overflowY).toBe("auto");
  expect(box.scrollHeight).toBeGreaterThan(box.clientHeight);
  // Bounded by the aside rather than clipped by it.
  expect(box.bottom).toBeLessThanOrEqual(box.asideBottom + 1);

  // The last item can be scrolled to and clicked.
  const last = tree.locator('[role="treeitem"]').last();
  await last.scrollIntoViewIfNeeded();
  await expect(last).toBeInViewport();
  expect(await tree.evaluate((el) => el.scrollTop)).toBeGreaterThan(0);
  await last.click();
}

test.describe("Resources folder tree", () => {
  test("scrolls within its panel at a desktop width", async ({ page }) => {
    await overfilledTree(page, { width: 1280, height: 560 });
    await assertTreeScrolls(page);
  });

  test("scrolls within its capped panel below 761px", async ({ page }) => {
    await overfilledTree(page, { width: 600, height: 900 });
    await assertTreeScrolls(page);
  });

  test("the listing still scrolls on its own", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 560 });
    await authenticate(page);
    await gotoFolder(page, ADMIN_RESOURCES, "user");
    const listing = page.getByTestId("listing");
    const scroller = await listing.evaluate((el) => {
      let n: HTMLElement | null = el;
      while (n && getComputedStyle(n).overflowY !== "auto") n = n.parentElement;
      return n ? { found: true, bounded: n.clientHeight < window.innerHeight } : { found: false, bounded: false };
    });
    expect(scroller).toEqual({ found: true, bounded: true });
  });
});
