import { test, expect, type Page } from "@playwright/test";
import { authenticate } from "../screenshots/helpers/auth";

// Interactive coverage for the canonical knowledge-pages portal surface (#633):
// browse, content search, open/read, create, and edit. Runs against MSW so the
// data is deterministic. The MSW /portal/me returns an admin, so the
// apply_knowledge-gated controls (New page / Edit / Remove) are visible.

async function gotoKnowledgePages(page: Page): Promise<void> {
  await authenticate(page);
  // #661: the canonical knowledge pages now browse under the Knowledge tab of
  // the unified /knowledge hub (the standalone /knowledge-pages route redirects
  // here). With the unified search box empty, the page list and its own
  // content-search box render below.
  await page.goto("/portal/knowledge#knowledge");
  await expect(page.getByPlaceholder("Search knowledge by content...")).toBeVisible();
}

test.describe("Knowledge Pages", () => {
  test.beforeEach(async ({ page }) => {
    await gotoKnowledgePages(page);
  });

  test("lists seeded pages", async ({ page }) => {
    await expect(page.getByText("Fiscal Calendar")).toBeVisible();
    await expect(page.getByText("Revenue Definition")).toBeVisible();
  });

  test("searches over page content", async ({ page }) => {
    // "gross margin" appears only in the Revenue Definition body, proving
    // content (not just title) search.
    await page.getByPlaceholder("Search knowledge by content...").fill("gross margin");
    await expect(page.getByText("Revenue Definition")).toBeVisible();
    await expect(page.getByText("Fiscal Calendar")).toHaveCount(0);
  });

  test("opens a page and renders its markdown body", async ({ page }) => {
    await page.getByText("Fiscal Calendar").click();
    await expect(page.getByRole("heading", { name: "Fiscal Calendar", level: 1 }).first()).toBeVisible();
    // Body markdown is rendered inside the article (a list item from the body).
    await expect(page.getByRole("article").getByText("Q1: February - April")).toBeVisible();
  });

  test("admin can create a new page", async ({ page }) => {
    await page.getByRole("button", { name: "New page" }).click();
    await page.getByPlaceholder("Title").fill("Operating Hours");
    await page.getByPlaceholder("One-line summary (optional)").fill("When the business runs");
    await page.locator(".cm-content").first().click();
    await page.keyboard.type("# Operating Hours\n\nMon-Fri 9-5 Pacific.");
    await page.getByRole("button", { name: "Create page" }).click();
    // Lands on the new page detail.
    await expect(page.getByRole("heading", { name: "Operating Hours", level: 1 })).toBeVisible();
  });

  test("admin can edit an existing page", async ({ page }) => {
    await page.getByText("Revenue Definition").click();
    await page.getByRole("button", { name: "Edit" }).click();
    const summary = page.getByPlaceholder("One-line summary (optional)");
    await expect(summary).toHaveValue("What the amount column means.");
    await summary.fill("Clarified gross-margin definition.");
    await page.getByRole("button", { name: "Save changes" }).click();
    await expect(page.getByText("Clarified gross-margin definition.")).toBeVisible();
  });

  test("shows version history", async ({ page }) => {
    await page.getByText("Fiscal Calendar").click();
    await page.getByRole("button", { name: "History" }).click();
    await expect(page.getByText("Version history")).toBeVisible();
  });
});
