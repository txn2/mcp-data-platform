import { test, expect, type Page } from "@playwright/test";
import { authenticate } from "../screenshots/helpers/auth";

// Interactive coverage for the Knowledge > Tags sub-tab (#1156): list and filter
// the tag vocabulary, open a tag to see what carries it, and create, describe,
// and retire one. Runs against MSW, whose /portal/me returns an admin so the
// write controls are visible and whose "primary" connection is writable.

async function gotoTags(page: Page): Promise<void> {
  await authenticate(page);
  await page.goto("/portal/knowledge/tags");
  await expect(page.getByLabel("DataHub connection")).toBeVisible();
}

test.describe("DataHub Tags", () => {
  test.beforeEach(async ({ page }) => {
    await gotoTags(page);
  });

  test("lists the tag vocabulary with descriptions", async ({ page }) => {
    await expect(page.getByRole("button", { name: /certified/ })).toBeVisible();
    await expect(page.getByText("Reviewed and approved by the data team.")).toBeVisible();
    await expect(page.getByRole("button", { name: /finance/ })).toBeVisible();
  });

  test("filters tags by name", async ({ page }) => {
    await page.getByPlaceholder(/Filter tags by name/).fill("pii");
    await expect(page.getByRole("button", { name: /pii/ })).toBeVisible();
    await expect(page.getByRole("button", { name: /certified/ })).toHaveCount(0);
  });

  test("opens a tag and lists the datasets carrying it", async ({ page }) => {
    await page.getByRole("button", { name: /^pii/ }).click();
    await expect(page.getByRole("heading", { name: /pii/ })).toBeVisible();
    await expect(page.getByText("urn:li:tag:pii")).toBeVisible();
    // customers carries the pii tag in the seeded catalog; daily_sales does not.
    await expect(page.getByText("analytics.public.customers")).toBeVisible();
    await expect(page.getByText("analytics.public.daily_sales")).toHaveCount(0);
  });

  test("a carrier deep-links into the catalog entity editor", async ({ page }) => {
    await page.getByRole("button", { name: /^pii/ }).click();
    await page.getByText("analytics.public.customers").click();
    await expect(page.getByRole("heading", { name: /customers/ })).toBeVisible();
    expect(page.url()).toContain("/knowledge/catalog?urn=");
  });

  test("creates a tag and it appears in the list", async ({ page }) => {
    await page.getByRole("button", { name: "New tag" }).click();
    await page.getByPlaceholder("e.g. certified").fill("golden");
    await page.getByPlaceholder(/What this tag means/).fill("Trusted for exec reporting.");
    await page.getByRole("button", { name: "Create tag" }).click();
    await expect(page.getByRole("button", { name: /golden/ })).toBeVisible();
    await expect(page.getByText("Trusted for exec reporting.")).toBeVisible();
  });

  test("edits a tag description and the change reflects on read", async ({ page }) => {
    await page.getByRole("button", { name: /certified/ }).click();
    await page.getByRole("button", { name: "Edit description" }).click();
    await page.getByLabel("Tag description").fill("Certified for reuse across teams.");
    await page.getByRole("button", { name: "Save" }).click();
    await expect(page.getByRole("button", { name: "Save" })).toHaveCount(0);
    await expect(page.getByText("Certified for reuse across teams.")).toBeVisible();
  });

  test("states the usage before deleting, then retires the tag", async ({ page }) => {
    await page.getByRole("button", { name: /^finance/ }).click();
    await page.getByRole("button", { name: "Delete tag" }).click();
    // The confirmation states the blast radius: daily_sales carries finance.
    await expect(page.getByText(/1 dataset in this connection carries this tag/)).toBeVisible();
    await page.getByRole("button", { name: "Confirm delete" }).click();
    // Deleting returns to the list, without the retired tag.
    await expect(page.getByPlaceholder(/Filter tags by name/)).toBeVisible();
    await expect(page.getByRole("button", { name: /^finance/ })).toHaveCount(0);
  });
});
