import { type Page } from "@playwright/test";
import { type ScreenshotRoute } from "../route-manifest";

export async function waitForPageReady(
  page: Page,
  route: ScreenshotRoute,
): Promise<void> {
  await page.waitForLoadState("networkidle");

  if (route.waitFor) {
    await page
      .waitForSelector(route.waitFor, { timeout: 10_000 })
      .catch(() => {});
  }

  await page
    .waitForFunction(
      () =>
        document.querySelectorAll(".animate-spin, .animate-pulse").length === 0,
      { timeout: 10_000 },
    )
    .catch(() => {});

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
