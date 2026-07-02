import { test, expect, type Page } from "@playwright/test";
import { authenticate } from "../screenshots/helpers/auth";

// Interactive coverage for the Context Docs sub-tab (#720): browse, search, open,
// create, edit, and delete. Runs against MSW (admin /portal/me, writable
// "primary" connection), so the create/edit/delete controls are visible.

async function gotoContextDocs(page: Page): Promise<void> {
  await authenticate(page);
  await page.goto("/portal/knowledge/context-docs");
  await expect(page.getByLabel("DataHub connection")).toBeVisible();
}

test.describe("Context Docs", () => {
  test.beforeEach(async ({ page }) => {
    await gotoContextDocs(page);
  });

  test("browses seeded documents", async ({ page }) => {
    await expect(page.getByText("Daily sales refresh runbook")).toBeVisible();
    await expect(page.getByText("Revenue definition")).toBeVisible();
  });

  test("searches documents", async ({ page }) => {
    await page.getByPlaceholder("Search context documents…").fill("runbook");
    await expect(page.getByText("Daily sales refresh runbook")).toBeVisible();
    await expect(page.getByText("Revenue definition")).toHaveCount(0);
  });

  test("opens a document and renders its markdown", async ({ page }) => {
    await page.getByText("Daily sales refresh runbook").click();
    await expect(page.getByRole("heading", { name: "Daily sales refresh runbook" })).toBeVisible();
    await expect(page.getByRole("heading", { name: "Daily sales refresh", level: 1 })).toBeVisible();
  });

  test("creates a document", async ({ page }) => {
    await page.getByRole("button", { name: "New document" }).click();
    await page.getByRole("textbox").first().fill("Clickstream schema notes");
    await page
      .getByPlaceholder(/urn:li:dataset/)
      .fill("urn:li:dataset:(urn:li:dataPlatform:trino,raw.events.clickstream,PROD)");
    await page.locator(".cm-content").first().click();
    await page.keyboard.type("# Clickstream\n\nOne row per page view.");
    await page.getByRole("button", { name: "Create document" }).click();
    await expect(page.getByRole("heading", { name: "Clickstream schema notes" })).toBeVisible();
  });

  test("rejects an unsupported entity type in the create form", async ({ page }) => {
    await page.getByRole("button", { name: "New document" }).click();
    await page.getByRole("textbox").first().fill("Dashboard note");
    await page.getByPlaceholder(/urn:li:dataset/).fill("urn:li:dashboard:(looker,42)");
    await expect(page.getByText(/attach only to Dataset/)).toBeVisible();
    await expect(page.getByRole("button", { name: "Create document" })).toBeDisabled();
  });

  test("edits a document", async ({ page }) => {
    await page.getByText("Revenue definition").click();
    await page.getByRole("button", { name: "Edit" }).click();
    const title = page.getByRole("textbox").first();
    await title.fill("Revenue definition (certified)");
    await page.getByRole("button", { name: "Save changes" }).click();
    await expect(page.getByRole("heading", { name: "Revenue definition (certified)" })).toBeVisible();
  });

  test("deletes a document", async ({ page }) => {
    await page.getByText("Revenue definition").click();
    await page.getByRole("button", { name: "Delete" }).click();
    await page.getByRole("button", { name: "Confirm delete" }).click();
    // Back on the list, the deleted doc is gone.
    await expect(page.getByText("Revenue definition")).toHaveCount(0);
  });
});
