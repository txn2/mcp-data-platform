import { test, expect, type Page } from "@playwright/test";
import { authenticate } from "../screenshots/helpers/auth";

// Interactive coverage for curating the platform catalog in the portal
// (#1921): find a table, edit its description and a column's tags, see the
// change in its history and roll it back, and create a glossary term that a
// column then carries. Runs against MSW, whose /portal/me is an administrator
// and whose catalog answers writable.

const tableSearch = /Search tables by name, path/;

async function openDailySales(page: Page): Promise<void> {
  await authenticate(page);
  await page.goto("/portal/knowledge/catalog");
  await page.getByPlaceholder(tableSearch).fill("daily");
  await page.getByRole("button", { name: /daily_sales/ }).click();
  await expect(page.getByRole("heading", { name: "daily_sales" })).toBeVisible();
}

test.describe("Catalog curation", () => {
  test("finds a table by search and opens it with its curation", async ({ page }) => {
    await openDailySales(page);
    await expect(page.getByText("Daily aggregated sales by store and product category.").first()).toBeVisible();
    await expect(page.getByRole("cell", { name: "customer_email", exact: true })).toBeVisible();
    await expect(page.getByText("sarah.chen@example.com").first()).toBeVisible();
    await expect(page.getByText("How revenue is counted")).toBeVisible();
    // Lineage reads the upstream table by name and opens it in place.
    await page.getByRole("link", { name: "clickstream" }).click();
    await expect(page.getByRole("heading", { name: "clickstream" })).toBeVisible();
  });

  test("edits the description, sees the change in history, and rolls it back", async ({ page }) => {
    await openDailySales(page);
    await page.getByRole("button", { name: "Edit description" }).first().click();
    const box = page.getByRole("textbox").first();
    await box.fill("Daily sales, counted at **store close**.");
    await page.getByRole("button", { name: "Save" }).click();
    await expect(page.locator("strong", { hasText: "store close" })).toBeVisible();

    const history = page.locator("[data-slot='card']").filter({ hasText: "History" }).last();
    await expect(history.getByText("Daily sales, counted at **store close**.")).toBeVisible();
    await history.getByRole("button", { name: /Roll back the Description change/ }).first().click();
    await expect(page.getByText("Daily aggregated sales by store and product category.").first()).toBeVisible();
  });

  test("adds a tag to a column", async ({ page }) => {
    await openDailySales(page);
    await page.getByRole("button", { name: "Edit column store_id" }).click();
    const input = page.getByLabel("Add tag on store_id");
    await input.fill("pii");
    await page.getByRole("button", { name: /^pii/ }).click();
    await expect(page.getByRole("button", { name: "Remove tag on store_id pii" })).toBeVisible();
  });

  test("creates a glossary term and applies it to a column", async ({ page }) => {
    await authenticate(page);
    await page.goto("/portal/knowledge/catalog#glossary");
    await page.getByRole("button", { name: /New entry/ }).click();
    await page.getByLabel("New glossary term name").fill("Store Day");
    await page.getByLabel("New glossary term description").fill("One store on one calendar day.");
    await page.getByRole("button", { name: "Save" }).click();
    await expect(page.getByRole("heading", { name: "Store Day" })).toBeVisible();

    await page.getByRole("tab", { name: "Tables", exact: true }).click();
    await page.getByRole("button", { name: /daily_sales/ }).click();
    await page.getByRole("button", { name: "Edit column sale_date" }).click();
    await page.getByLabel("Add glossary term on sale_date").fill("store");
    await page.getByRole("button", { name: /Store Day/ }).click();
    await expect(page.getByRole("button", { name: "Remove glossary term on sale_date Store Day" })).toBeVisible();

    // The term's page now lists the column that carries it.
    await page.getByRole("tab", { name: "Glossary", exact: true }).click();
    await page.getByPlaceholder(/Filter terms and nodes/).fill("store");
    await page.getByRole("button", { name: /Store Day/ }).click();
    await expect(page.getByRole("link", { name: /daily_sales\.sale_date/ })).toBeVisible();
  });

  test("a tag still on a dataset cannot be deleted, and says why", async ({ page }) => {
    await authenticate(page);
    await page.goto(`/portal/knowledge/catalog?ref=${encodeURIComponent("mcp:tag:certified")}#tags`);
    await page.getByRole("button", { name: /Delete tag/ }).click();
    await page.getByRole("button", { name: "Confirm delete" }).click();
    await expect(page.getByText(/carried by 1 datasets or columns/)).toBeVisible();
  });
});
