import { test, expect } from "@playwright/test";
import { authenticate } from "../screenshots/helpers/auth";

// Export PDF on a deck (#1983).
//
// The platform prints the deck in its renderer, in the deck's own colors and
// one page per slide at its last build step, and the viewer downloads what it
// printed. Nothing is printed in the reader's browser any more: no print frame
// is mounted and no print dialog opens. ast-deck is the mock library's deck, a
// dark one, which is the case the browser print used to wash out (#1772).

test("Export PDF downloads the deck the platform printed (#1983)", async ({ page }) => {
  await authenticate(page);
  await page.goto("/portal/assets/ast-deck");
  await expect(page.getByTestId("viewer-content").locator("iframe")).toBeVisible();

  const requested = page.waitForRequest((req) => req.url().endsWith("/api/v1/portal/assets/ast-deck/pdf"));
  const download = page.waitForEvent("download");
  await page.getByRole("button", { name: "Export PDF" }).click();

  await requested;
  expect((await download).suggestedFilename()).toMatch(/\.pdf$/);
  await expect(page.locator("iframe[sandbox*='allow-modals']")).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Export PDF" })).toBeEnabled();
});
