import { test, expect, type Page } from "@playwright/test";
import { authenticate } from "../screenshots/helpers/auth";

// Interactive coverage for the DataHub Catalog sub-tab (#719): connection select,
// browse, search, open an entity, and edit each metadata facet. Runs against MSW,
// whose /portal/me returns an admin so the edit controls are visible and whose
// "primary" connection is writable.

async function gotoCatalog(page: Page): Promise<void> {
  await authenticate(page);
  await page.goto("/portal/knowledge/catalog");
  await expect(page.getByLabel("DataHub connection")).toBeVisible();
}

test.describe("DataHub Catalog", () => {
  test.beforeEach(async ({ page }) => {
    await gotoCatalog(page);
  });

  test("browses seeded datasets", async ({ page }) => {
    await expect(page.getByText("analytics.public.daily_sales")).toBeVisible();
    await expect(page.getByText("analytics.public.customers")).toBeVisible();
  });

  test("searches datasets by name", async ({ page }) => {
    await page.getByPlaceholder(/Search datasets/).fill("clickstream");
    await expect(page.getByText("raw.events.clickstream")).toBeVisible();
    await expect(page.getByText("analytics.public.customers")).toHaveCount(0);
  });

  test("opens an entity and shows its metadata and columns", async ({ page }) => {
    await page.getByText("analytics.public.daily_sales").click();
    await expect(page.getByRole("heading", { name: /daily_sales/ })).toBeVisible();
    await expect(page.getByText("Daily aggregated sales by store and product category.")).toBeVisible();
    // Columns table renders with a PII/Sensitive classification.
    await expect(page.getByRole("cell", { name: "customer_email" })).toBeVisible();
    await expect(page.getByText("PII", { exact: true })).toBeVisible();
  });

  test("edits the description and the change reflects on read", async ({ page }) => {
    await page.getByText("analytics.public.daily_sales").click();
    await page
      .getByRole("heading", { name: "Description" })
      .locator("..")
      .getByRole("button", { name: "Edit" })
      .click();
    const box = page.getByRole("textbox").first();
    await box.fill("Daily sales, refreshed at 06:00 UTC.");
    await page.getByRole("button", { name: "Save" }).click();
    await expect(page.getByText("Daily sales, refreshed at 06:00 UTC.")).toBeVisible();
  });

  test("adds a tag and it appears in the tag set", async ({ page }) => {
    await page.getByText("analytics.public.customers").click();
    await page.getByPlaceholder("urn:li:tag:PII").fill("urn:li:tag:reviewed");
    await page
      .getByRole("heading", { name: "Tags" })
      .locator("..")
      .getByRole("button", { name: "Add" })
      .click();
    await expect(page.getByText("reviewed")).toBeVisible();
  });

  test("sets and clears the domain", async ({ page }) => {
    await page.getByText("analytics.public.customers").click();
    await page.getByPlaceholder("urn:li:domain:finance").fill("urn:li:domain:marketing");
    await page.getByRole("button", { name: "Set" }).click();
    await expect(page.getByText("marketing")).toBeVisible();
    await page.getByRole("button", { name: "Clear" }).click();
    await expect(page.getByText("None.").first()).toBeVisible();
  });
});
