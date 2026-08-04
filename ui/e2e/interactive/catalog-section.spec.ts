import { test, expect, type Page } from "@playwright/test";
import { authenticate } from "../screenshots/helpers/auth";

// Interactive coverage for the Catalog information architecture (#1194): the
// Knowledge sub-tab row, the inner tabs nested under the one Catalog route, the
// connection shared across them, and the two routes that were cut. These assert
// the assembled app -- AppShell's route matching plus KnowledgeHub plus
// CatalogSection -- not the container in isolation.

const tagFilter = /Filter tags by name/;
const domainFilter = /Filter domains by name/;
const tableSearch = /Search tables by name/;
const docSearch = /Search context documents/;

async function gotoCatalog(page: Page): Promise<void> {
  await authenticate(page);
  await page.goto("/portal/knowledge/catalog");
  await expect(page.getByPlaceholder(tableSearch)).toBeVisible();
}

test.describe("Catalog section", () => {
  test("the Knowledge sub-tab row is four tabs, with the DataHub surfaces inside Catalog", async ({
    page,
  }) => {
    await authenticate(page);
    await page.goto("/portal/knowledge");

    for (const label of ["Search All", "Knowledge Pages", "Catalog", "Changesets"]) {
      await expect(page.getByRole("button", { name: label, exact: true })).toBeVisible();
    }
    // Tags and Context Docs are no longer siblings; they live one level down.
    await expect(page.getByRole("button", { name: "Tags", exact: true })).toHaveCount(0);
    await expect(page.getByRole("button", { name: "Context Docs", exact: true })).toHaveCount(0);

    await page.getByRole("button", { name: "Catalog", exact: true }).click();
    for (const label of ["Tables", "Context Docs", "Tags", "Domains", "Glossary"]) {
      await expect(page.getByRole("button", { name: label, exact: true })).toBeVisible();
    }
  });

  test("renders one connection picker for the whole section", async ({ page }) => {
    await gotoCatalog(page);
    await expect(page.getByLabel("DataHub connection")).toHaveCount(1);
    await page.getByRole("button", { name: "Tags", exact: true }).click();
    await expect(page.getByLabel("DataHub connection")).toHaveCount(1);
  });

  test("moves between inner tabs and records each one in the URL", async ({ page }) => {
    await gotoCatalog(page);

    await page.getByRole("button", { name: "Tags", exact: true }).click();
    await expect(page.getByPlaceholder(tagFilter)).toBeVisible();
    expect(page.url()).toContain("/knowledge/catalog#tags");

    await page.getByRole("button", { name: "Domains", exact: true }).click();
    await expect(page.getByPlaceholder(domainFilter)).toBeVisible();
    expect(page.url()).toContain("/knowledge/catalog#domains");

    await page.getByRole("button", { name: "Glossary", exact: true }).click();
    await expect(page.getByRole("heading", { name: "Glossary" })).toBeVisible();
    expect(page.url()).toContain("/knowledge/catalog#glossary");

    await page.getByRole("button", { name: "Context Docs", exact: true }).click();
    await expect(page.getByPlaceholder(docSearch)).toBeVisible();
    expect(page.url()).toContain("/knowledge/catalog#context-docs");

    await page.getByRole("button", { name: "Tables", exact: true }).click();
    await expect(page.getByPlaceholder(tableSearch)).toBeVisible();
    expect(page.url()).toContain("/knowledge/catalog#tables");
  });

  test("the inner tab survives a reload", async ({ page }) => {
    await gotoCatalog(page);
    await page.getByRole("button", { name: "Tags", exact: true }).click();
    await expect(page.getByPlaceholder(tagFilter)).toBeVisible();

    await page.reload();
    await expect(page.getByPlaceholder(tagFilter)).toBeVisible();
    await expect(page.getByPlaceholder(tableSearch)).toHaveCount(0);
  });

  test("the inner tab survives back and forward", async ({ page }) => {
    await authenticate(page);
    await page.goto("/portal/knowledge");
    await page.getByRole("button", { name: "Catalog", exact: true }).click();
    await page.getByRole("button", { name: "Context Docs", exact: true }).click();
    await expect(page.getByPlaceholder(docSearch)).toBeVisible();

    // Selecting an inner tab rewrites the hash rather than pushing an entry, so
    // Back leaves the section entirely and Forward returns to the tab that was
    // open, not to the section's default.
    await page.goBack();
    await expect(page.getByPlaceholder(/Search all knowledge/i)).toBeVisible();
    await page.goForward();
    await expect(page.getByPlaceholder(docSearch)).toBeVisible();
  });

  test("keeps the entity deep link working, and its hash alongside it", async ({ page }) => {
    // `/knowledge/catalog?urn=` is what a catalog reference links to from
    // anywhere in the portal, and the restructure must not change it: it opens
    // the entity on Tables, with no hash.
    await authenticate(page);
    const urn = "urn:li:dataset:(urn:li:dataPlatform:trino,analytics.public.daily_sales,PROD)";
    await page.goto(`/portal/knowledge/catalog?urn=${encodeURIComponent(urn)}`);
    await expect(page.getByRole("heading", { name: /daily_sales/ })).toBeVisible();

    // Switching inner tabs rewrites only the hash, so the deep link survives.
    await page.getByRole("button", { name: "Tags", exact: true }).click();
    await expect(page.getByPlaceholder(tagFilter)).toBeVisible();
    expect(page.url()).toContain("urn=");
    expect(page.url()).toContain("#tags");

    // And coming back re-opens the entity the URL still names.
    await page.getByRole("button", { name: "Tables", exact: true }).click();
    await expect(page.getByRole("heading", { name: /daily_sales/ })).toBeVisible();
  });

  test("the retired sub-tab routes no longer resolve", async ({ page }) => {
    await authenticate(page);

    await page.goto("/portal/knowledge/tags");
    await expect(page.getByPlaceholder(tagFilter)).toHaveCount(0);
    await expect(page.getByRole("button", { name: "Catalog", exact: true })).toHaveCount(0);

    await page.goto("/portal/knowledge/context-docs");
    await expect(page.getByPlaceholder(docSearch)).toHaveCount(0);
    await expect(page.getByRole("button", { name: "Catalog", exact: true })).toHaveCount(0);
  });
});
