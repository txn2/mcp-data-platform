import { type Page } from "@playwright/test";

/**
 * Capture actions that more than one route in the manifest performs, or that
 * are long enough to crowd it out. A route with a one-off `beforeCapture`
 * keeps it inline; these live here so the manifest stays a readable list of
 * routes rather than a file of closures.
 */

/** openShareDialog opens the asset viewer's Share dialog. */
export async function openShareDialog(page: Page): Promise<void> {
  const btn = page.locator("button:has-text('Share')").first();
  if (await btn.isVisible()) {
    await btn.click();
    await page.waitForTimeout(600);
  }
}

/**
 * openShareDialogWithRecipient opens the Share dialog and names a recipient,
 * which is the only state showing the notify checkbox and the sharer's
 * message box (#1016). It builds on openShareDialog so the two share captures
 * cannot drift apart.
 */
export async function openShareDialogWithRecipient(page: Page): Promise<void> {
  await openShareDialog(page);
  const recipient = page.locator("input[type='email']").first();
  if (await recipient.isVisible()) {
    await recipient.fill("marcus.johnson@example.com");
    await page.waitForTimeout(400);
  }
}
