import { test, expect } from "@playwright/test";
import { authenticate } from "../screenshots/helpers/auth";

// The webhooks page's history (#2150) and the Storage page (#2148), driven in
// a real browser against the mock portal.

test.describe("Webhook history", () => {
  test("says what it cannot count, and reads the last seven days", async ({ page }) => {
    await authenticate(page);
    await page.goto("/portal/admin/webhooks");
    await expect(page.getByTestId("not-counted-note")).toContainText("502 or 504");
    for (const title of ["Responses by status", "Received vs written", "Spool depth", "Acknowledgement", "Window size"]) {
      await expect(page.getByText(title, { exact: true })).toBeVisible();
    }
    await page.getByRole("button", { name: "Last 7 days" }).click();
    await expect(page.getByRole("button", { name: "Last 7 days" })).toHaveAttribute("aria-pressed", "true");
    await expect(page.getByText(/in the last 7 days/)).toBeVisible();
    await expect(page.getByTestId("window-sizing")).toContainText("of the memory limit");
  });

  test("reads a custom range once it is shown", async ({ page }) => {
    await authenticate(page);
    await page.goto("/portal/admin/webhooks");
    await page.getByRole("button", { name: "Custom" }).click();
    await expect(page.getByTestId("custom-range")).toBeVisible();
    await page.getByLabel("From", { exact: true }).fill("2026-10-06T09:00");
    await page.getByLabel("To", { exact: true }).fill("2026-10-06T17:00");
    await page.getByRole("button", { name: "Show" }).click();
    await expect(page.getByText("Responses by status", { exact: true })).toBeVisible();
  });
});

test.describe("Storage", () => {
  test("lists every store and opens one's history on its row", async ({ page }) => {
    await authenticate(page);
    await page.goto("/portal/admin/storage");
    const row = page.getByRole("row").filter({ hasText: "Bucket acme-portal-assets" });
    await expect(row).toContainText("Critical");
    await row.click();
    await expect(page.getByText("History: Bucket acme-portal-assets")).toBeVisible();
    await page.getByRole("button", { name: "Storage settings" }).click();
    await expect(page).toHaveURL(/\/portal\/admin\/settings\/storage$/);
    await expect(page.getByLabel("Database limit (GiB)")).toHaveValue("100");
  });

  test("the webhook alerts section shows each condition's thresholds", async ({ page }) => {
    await authenticate(page);
    await page.goto("/portal/admin/settings/webhooks");
    await expect(page.getByLabel("Spool fill warning")).toHaveValue("50");
    await expect(page.getByTestId("override-email-events")).toBeVisible();
  });
});
