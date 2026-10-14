import { test, expect, type Page } from "@playwright/test";
import { authenticate } from "../screenshots/helpers/auth";

// Interactive coverage for the Catalog information architecture (#1194, #1921):
// the Knowledge sub-tab row, the inner tabs nested under the one Catalog
// route, and the routes that were cut. These assert the assembled app --
// AppShell's route matching plus KnowledgeHub plus CatalogSection -- not the
// container in isolation.

const tagFilter = /Filter tags by name/;
const domainFilter = /Filter domains by name/;
const productFilter = /Filter data products by name/;
const glossaryFilter = /Filter terms and nodes by name/;
const tableSearch = /Search tables by name, path/;

async function gotoCatalog(page: Page): Promise<void> {
  await authenticate(page);
  await page.goto("/portal/knowledge/catalog");
  await expect(page.getByPlaceholder(tableSearch)).toBeVisible();
}

test.describe("Catalog section", () => {
  test("the Knowledge sub-tab row is four tabs, with the catalog surfaces inside Catalog", async ({
    page,
  }) => {
    await authenticate(page);
    await page.goto("/portal/knowledge");

    for (const label of ["Search All", "Knowledge Pages", "Catalog", "Changesets"]) {
      await expect(page.getByRole("tab", { name: label, exact: true })).toBeVisible();
    }
    await expect(page.getByRole("tab", { name: "Tags", exact: true })).toHaveCount(0);

    await page.getByRole("tab", { name: "Catalog", exact: true }).click();
    for (const label of ["Tables", "Glossary", "Tags", "Domains", "Data products"]) {
      await expect(page.getByRole("tab", { name: label, exact: true })).toBeVisible();
    }
  });

  test("moves between inner tabs and records each one in the URL", async ({ page }) => {
    await gotoCatalog(page);

    await page.getByRole("tab", { name: "Tags", exact: true }).click();
    await expect(page.getByPlaceholder(tagFilter)).toBeVisible();
    expect(page.url()).toContain("/knowledge/catalog#tags");

    await page.getByRole("tab", { name: "Domains", exact: true }).click();
    await expect(page.getByPlaceholder(domainFilter)).toBeVisible();
    expect(page.url()).toContain("/knowledge/catalog#domains");

    await page.getByRole("tab", { name: "Glossary", exact: true }).click();
    await expect(page.getByPlaceholder(glossaryFilter)).toBeVisible();
    expect(page.url()).toContain("/knowledge/catalog#glossary");

    await page.getByRole("tab", { name: "Data products", exact: true }).click();
    await expect(page.getByPlaceholder(productFilter)).toBeVisible();
    expect(page.url()).toContain("/knowledge/catalog#data-products");

    await page.getByRole("tab", { name: "Tables", exact: true }).click();
    await expect(page.getByPlaceholder(tableSearch)).toBeVisible();
    expect(page.url()).toContain("/knowledge/catalog#tables");
  });

  test("the inner tab survives a reload", async ({ page }) => {
    await gotoCatalog(page);
    await page.getByRole("tab", { name: "Tags", exact: true }).click();
    await expect(page.getByPlaceholder(tagFilter)).toBeVisible();

    await page.reload();
    await expect(page.getByPlaceholder(tagFilter)).toBeVisible();
    await expect(page.getByPlaceholder(tableSearch)).toHaveCount(0);
  });

  test("the inner tab survives back and forward", async ({ page }) => {
    await authenticate(page);
    await page.goto("/portal/knowledge");
    await page.getByRole("tab", { name: "Catalog", exact: true }).click();
    await page.getByRole("tab", { name: "Glossary", exact: true }).click();
    await expect(page.getByPlaceholder(glossaryFilter)).toBeVisible();

    // Selecting an inner tab rewrites the hash rather than pushing an entry, so
    // Back leaves the section entirely and Forward returns to the tab that was
    // open, not to the section's default.
    await page.goBack();
    await expect(page.getByPlaceholder(/Search all knowledge/i)).toBeVisible();
    await page.goForward();
    await expect(page.getByPlaceholder(glossaryFilter)).toBeVisible();
  });

  test("a dataset deep link opens the dataset, and survives a reload", async ({ page }) => {
    // `/knowledge/catalog?ref=` is what a catalog reference links to from
    // anywhere in the portal.
    await authenticate(page);
    const ref = "mcp:dataset:(trino,acme-warehouse,analytics.public.daily_sales)";
    await page.goto(`/portal/knowledge/catalog?ref=${encodeURIComponent(ref)}#tables`);
    await expect(page.getByRole("heading", { name: "daily_sales" })).toBeVisible();
    await page.reload();
    await expect(page.getByRole("heading", { name: "daily_sales" })).toBeVisible();

    // Leaving for another tab drops the dataset, which belongs to Tables.
    await page.getByRole("tab", { name: "Tags", exact: true }).click();
    await expect(page.getByPlaceholder(tagFilter)).toBeVisible();
    expect(page.url()).not.toContain("ref=");
  });

  test("the retired sub-tab routes no longer resolve", async ({ page }) => {
    await authenticate(page);

    await page.goto("/portal/knowledge/tags");
    await expect(page.getByPlaceholder(tagFilter)).toHaveCount(0);
    await expect(page.getByRole("tab", { name: "Catalog", exact: true })).toHaveCount(0);

    await page.goto("/portal/knowledge/context-docs");
    await expect(page.getByRole("tab", { name: "Catalog", exact: true })).toHaveCount(0);
  });
});
