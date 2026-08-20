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
    // The PageHeader title is a level-2 heading; the body's own `# Fiscal
    // Calendar` is the level-1 one, so the level separates them.
    await expect(page.getByRole("heading", { name: "Fiscal Calendar", level: 2 })).toBeVisible();
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
    // `# Operating Hours`, so the page title is named by the level rather than
    // by position: the PageHeader title is level 2, the body's heading level 1.
    await expect(
      page.getByRole("heading", { name: "Operating Hours", level: 2 }),
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

  // A built-in page (#1390) is the platform's own documentation: badged,
  // offered no Edit, and its delete affordance is a Hide the reconcile
  // respects. Restore built-in on the list is the way back.
  test("built-in page is badged, uneditable, and hidable with a way back", async ({ page }) => {
    await page.getByText("Writing a managed script").click();
    await expect(page.getByText("Built-in", { exact: true })).toBeVisible();
    await expect(page.getByRole("button", { name: "Edit" })).toHaveCount(0);
    await page.getByRole("button", { name: "Hide" }).click();
    await expect(page.getByText(/upgrades will not bring it back/)).toBeVisible();
    await page.getByRole("dialog").getByRole("button", { name: "Hide" }).click();
    // Back on the list, the page is gone; Restore built-in brings it back.
    await expect(page.getByText("Writing a managed script")).toHaveCount(0);
    await page.getByRole("button", { name: "Restore built-in" }).click();
    await expect(page.getByText("Restored 1 built-in page.")).toBeVisible();
    await expect(page.getByText("Writing a managed script")).toBeVisible();
  });
});
