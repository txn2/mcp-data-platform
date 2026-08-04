import { test, expect, type Page } from "@playwright/test";
import { authenticate } from "../screenshots/helpers/auth";

// Interactive coverage for the canonical knowledge-pages portal surface (#633):
// browse, content search, open/read, create, and edit. Runs against MSW so the
// data is deterministic. The MSW /portal/me returns an admin, so the
// apply_knowledge-gated controls (New page / Edit / Remove) are visible.

async function gotoKnowledgePages(page: Page): Promise<void> {
  await authenticate(page);
  // #709: Knowledge Pages is a URL-addressable sub-tab of the unified
  // knowledge hub with its own first-class route.
  await page.goto("/portal/knowledge/pages");
  await expect(page.getByPlaceholder("Search knowledge by content...")).toBeVisible();
}

test.describe("Knowledge Pages", () => {
  test.beforeEach(async ({ page }) => {
    await gotoKnowledgePages(page);
  });

  test("lists seeded pages", async ({ page }) => {
    await expect(page.getByText("Fiscal Calendar")).toBeVisible();
    // exact: true because the seed also contains "Net Revenue Definition".
    await expect(page.getByText("Revenue Definition", { exact: true })).toBeVisible();
  });

  test("searches over page content", async ({ page }) => {
    // "gross margin" appears only in the Revenue Definition body, proving
    // content (not just title) search.
    await page.getByPlaceholder("Search knowledge by content...").fill("gross margin");
    await expect(page.getByText("Revenue Definition", { exact: true })).toBeVisible();
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
    await page.getByPlaceholder("A sentence or two summarizing the page").fill("When the business runs");
    await page.locator(".cm-content").first().click();
    await page.keyboard.type("# Operating Hours\n\nMon-Fri 9-5 Pacific.");
    await page.getByRole("button", { name: "Create page" }).click();
    // Lands on the new page detail. The body typed above opens with its own
    // `# Operating Hours`, so once the markdown renders there are two level-1
    // headings by that name and an unqualified locator is a strict-mode
    // violation the moment the article beats the assertion. Take the first,
    // which is the page title, as the sibling render test does.
    await expect(
      page.getByRole("heading", { name: "Operating Hours", level: 1 }).first(),
    ).toBeVisible();
  });

  test("admin can edit an existing page", async ({ page }) => {
    await page.getByText("Revenue Definition", { exact: true }).click();
    await page.getByRole("button", { name: "Edit" }).click();
    const summary = page.getByPlaceholder("A sentence or two summarizing the page");
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
