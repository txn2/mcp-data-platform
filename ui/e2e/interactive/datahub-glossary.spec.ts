import { test, expect, type Page } from "@playwright/test";
import { authenticate } from "../screenshots/helpers/auth";

// Interactive coverage for the Glossary tab of the Catalog section (#1158): walk
// the hierarchy, open a term for its definition, place, attached documents, and
// the tables using it, and create, describe, and retire a term or an empty node.
// Runs against MSW, whose /portal/me returns an admin so the write controls are
// visible and whose "primary" connection is writable. The seeded glossary is one
// root node (Finance) holding a node (Billing) and a term (Revenue), plus one
// root term (Net Sales).

async function gotoGlossary(page: Page): Promise<void> {
  await authenticate(page);
  // Glossary is an inner tab of the one Catalog route, addressed in the hash.
  await page.goto("/portal/knowledge/catalog#glossary");
  await expect(page.getByLabel("DataHub connection")).toBeVisible();
}

test.describe("DataHub Glossary", () => {
  test.beforeEach(async ({ page }) => {
    await gotoGlossary(page);
  });

  test("lists the root branch: its nodes and the terms with no parent", async ({ page }) => {
    await expect(page.getByRole("button", { name: /^Finance/ })).toBeVisible();
    await expect(page.getByText("Revenue, billing, and reporting vocabulary.")).toBeVisible();
    await expect(page.getByRole("button", { name: /^Net Sales/ })).toBeVisible();
    // Revenue is inside Finance, so it is not on the root branch.
    await expect(page.getByRole("button", { name: /^Revenue/ })).toHaveCount(0);
  });

  test("walks into a node and back out through the breadcrumb", async ({ page }) => {
    await page.getByRole("button", { name: /^Finance/ }).click();
    await expect(page.getByRole("heading", { name: /Finance/ })).toBeVisible();
    await expect(page.getByRole("button", { name: /^Billing/ })).toBeVisible();
    await expect(page.getByRole("button", { name: /^Revenue/ })).toBeVisible();

    await page
      .getByRole("navigation", { name: "Glossary location" })
      .getByRole("button", { name: "Glossary" })
      .click();
    await expect(page.getByRole("button", { name: /^Net Sales/ })).toBeVisible();
  });

  test("opens a term with its place, definition, documents, and usage", async ({ page }) => {
    await page.getByRole("button", { name: /^Finance/ }).click();
    await page.getByRole("button", { name: /^Revenue/ }).click();

    await expect(page.getByRole("heading", { name: /Revenue/ })).toBeVisible();
    const crumbs = page.getByRole("navigation", { name: "Glossary location" });
    await expect(crumbs).toContainText("Finance");
    // The note attached to the term in the seeded corpus. Scoped to the
    // document list, because the term detail also lists the knowledge pages
    // referencing it (#1159) and one of them is titled "Revenue Definition".
    await expect(page.locator("li").filter({ hasText: "Revenue definition" })).toBeVisible();
    // daily_sales carries the term on the table AND on a column; clickstream
    // carries it on the table only, so only one row is marked.
    await expect(page.getByText("analytics.public.daily_sales")).toBeVisible();
    await expect(page.getByText("raw.events.clickstream")).toBeVisible();
    await expect(page.getByText("on a column")).toHaveCount(1);
  });

  test("a table using the term deep-links into the catalog entity editor", async ({ page }) => {
    await page.getByRole("button", { name: /^Finance/ }).click();
    await page.getByRole("button", { name: /^Revenue/ }).click();
    await page.getByText("analytics.public.daily_sales").click();
    await expect(page.getByRole("heading", { name: /daily_sales/ })).toBeVisible();
    expect(page.url()).toContain("/knowledge/catalog?urn=");
  });

  test("creates a term inside the open node and it appears in that branch", async ({ page }) => {
    await page.getByRole("button", { name: /^Finance/ }).click();
    await page.getByRole("button", { name: /New term/ }).click();
    await expect(page.getByText("Created in Finance.")).toBeVisible();
    await page.getByPlaceholder("e.g. Net Revenue").fill("Gross Margin");
    await page.getByPlaceholder(/What this term means/).fill("Revenue less cost of goods.");
    await page.getByRole("button", { name: "Create term" }).click();

    await expect(page.getByRole("button", { name: /^Gross Margin/ })).toBeVisible();
    await expect(page.getByText("Revenue less cost of goods.")).toBeVisible();
  });

  test("creates a node at the root and it appears there", async ({ page }) => {
    await page.getByRole("button", { name: /New node/ }).click();
    await expect(page.getByText("Created at the root of the glossary.")).toBeVisible();
    await page.getByPlaceholder("e.g. Finance").fill("Supply Chain");
    await page.getByRole("button", { name: "Create node" }).click();
    await expect(page.getByRole("button", { name: /^Supply Chain/ })).toBeVisible();
  });

  test("edits a definition and the change reflects on read", async ({ page }) => {
    await page.getByRole("button", { name: /^Net Sales/ }).click();
    await page.getByRole("button", { name: "Edit description" }).click();
    await page.getByLabel("Term definition").fill("Revenue after refunds and discounts.");
    await page.getByRole("button", { name: "Save" }).click();
    await expect(page.getByRole("button", { name: "Save" })).toHaveCount(0);
    await expect(page.getByText("Revenue after refunds and discounts.")).toBeVisible();
  });

  test("states what uses a term before deleting, then retires it", async ({ page }) => {
    await page.getByRole("button", { name: /^Finance/ }).click();
    await page.getByRole("button", { name: /^Revenue/ }).click();
    await page.getByRole("button", { name: "Delete term" }).click();
    await expect(
      page.getByText(/2 tables in this connection are annotated with this term/),
    ).toBeVisible();
    await page.getByRole("button", { name: "Confirm delete" }).click();

    // Deleting returns to the branch it came from — Finance — with the retired
    // term gone from it and the rest of the branch intact.
    await expect(page.getByRole("heading", { name: /Finance/ })).toBeVisible();
    await expect(page.getByRole("button", { name: /^Revenue/ })).toHaveCount(0);
    await expect(page.getByRole("button", { name: /^Billing/ })).toBeVisible();
  });

  test("refuses a node delete while it holds entries, and allows it once empty", async ({
    page,
  }) => {
    await page.getByRole("button", { name: /^Finance/ }).click();
    await expect(page.getByText(/This node holds 2 entries/)).toBeVisible();
    await expect(page.getByRole("button", { name: "Delete node" })).toHaveCount(0);

    // Billing is empty, so its delete is offered and completes.
    await page.getByRole("button", { name: /^Billing/ }).click();
    await expect(page.getByText("This node is empty.")).toBeVisible();
    await page.getByRole("button", { name: "Delete node" }).click();
    await page.getByRole("button", { name: "Confirm delete" }).click();
    await expect(page.getByRole("button", { name: /^Billing/ })).toHaveCount(0);
  });

  test("a term created here is immediately pickable on a table", async ({ page }) => {
    // One glossary backs both surfaces, as it does in DataHub: what a steward
    // defines here is what the entity editor's picker offers.
    await page.getByRole("button", { name: /New term/ }).click();
    await page.getByPlaceholder("e.g. Net Revenue").fill("Churn Rate");
    await page.getByRole("button", { name: "Create term" }).click();
    await expect(page.getByRole("button", { name: /^Churn Rate/ })).toBeVisible();

    await page.getByRole("tab", { name: "Tables", exact: true }).click();
    await page.getByText("analytics.public.customers").click();
    await expect(page.getByRole("heading", { name: /customers/ })).toBeVisible();
    await page.getByPlaceholder("Search glossary terms by name…").fill("Churn");
    await expect(page.getByRole("button", { name: /Churn Rate/ })).toBeVisible();
  });
});
