import { type Page } from "@playwright/test";
import { type ScreenshotRoute } from "../route-manifest";

export async function waitForPageReady(
  page: Page,
  route: ScreenshotRoute,
): Promise<void> {
  // Bounded: pages that poll on an interval (e.g. the Indexing dashboard polls
  // every 5s) never reach a true network-idle, so this must time out and move
  // on rather than block until the per-test timeout.
  await page
    .waitForLoadState("networkidle", { timeout: 10_000 })
    .catch(() => {});

  if (route.waitFor) {
    await page
      .waitForSelector(route.waitFor, { timeout: 10_000 })
      .catch(() => {});
  }

  // Wait for transient spinners/skeletons to clear, but HARD-cap it: on pages
  // with a legitimately persistent spinner (e.g. the Indexing dashboard's
  // running-job indicator) that re-render on a poll interval, Playwright's
  // own waitForFunction timeout is not honored reliably (observed 30s+ for a
  // 10s cap, up to the full per-test timeout in a suite run). Racing it against
  // a fixed timer guarantees we move on. The losing promise keeps a .catch so
  // it cannot surface as an unhandled rejection.
  const spinnersSettled = page
    .waitForFunction(
      () =>
        document.querySelectorAll(".animate-spin, .animate-pulse").length === 0,
      { timeout: 5_000 },
    )
    .catch(() => {});
  await Promise.race([spinnersSettled, page.waitForTimeout(3_000)]);

  if (route.waitForThumbnails) {
    await waitForThumbnails(page, route.waitForThumbnails);
  }

  await page.waitForTimeout(800);

  if (route.beforeCapture) {
    await route.beforeCapture(page);
  }
}

async function waitForThumbnails(
  page: Page,
  maxWaitMs: number,
): Promise<void> {
  const start = Date.now();
  let lastImgCount = 0;
  let stableCount = 0;

  while (Date.now() - start < maxWaitMs) {
    const imgCount = await page.evaluate(() => {
      const imgs = document.querySelectorAll("img[src*='blob:']");
      return imgs.length;
    });

    if (imgCount > lastImgCount) {
      lastImgCount = imgCount;
      stableCount = 0;
    } else {
      stableCount++;
    }

    if (stableCount >= 3 && imgCount > 0) break;

    await page.waitForTimeout(2000);
  }

  await page.waitForLoadState("networkidle");
  await page.waitForTimeout(1000);
}
