import { test, expect, type Page } from "@playwright/test";
import { authenticate } from "../screenshots/helpers/auth";

// Interactive coverage for the Domains tab of the Catalog section (#1157,
// #1194): list and filter the domains, open one to see what is in it, create,
// describe, and retire one, and move a table in and out. Runs against MSW, whose
// /portal/me returns an admin so the write controls are visible and whose
// "primary" connection is writable.

async function gotoDomains(page: Page): Promise<void> {
  await authenticate(page);
  // Domains is an inner tab of the one Catalog route, addressed in the hash.
  await page.goto("/portal/knowledge/catalog#domains");
  await expect(page.getByLabel("DataHub connection")).toBeVisible();
}

test.describe("DataHub Domains", () => {
  test.beforeEach(async ({ page }) => {
    await gotoDomains(page);
  });

  test("lists the domains with descriptions", async ({ page }) => {
    await expect(page.getByRole("button", { name: /Finance/ })).toBeVisible();
    await expect(page.getByText("Revenue, billing, and reporting.")).toBeVisible();
    await expect(page.getByRole("button", { name: /Marketing/ })).toBeVisible();
  });

  test("filters domains by name", async ({ page }) => {
    await page.getByPlaceholder(/Filter domains by name/).fill("mark");
    await expect(page.getByRole("button", { name: /Marketing/ })).toBeVisible();
    await expect(page.getByRole("button", { name: /Finance/ })).toHaveCount(0);
  });

  test("opens a domain and lists the tables in it", async ({ page }) => {
    await page.getByRole("button", { name: /^Finance/ }).click();
    await expect(page.getByRole("heading", { name: /Finance/ })).toBeVisible();
    await expect(page.getByText("urn:li:domain:finance")).toBeVisible();
    // daily_sales is in the Finance domain in the seeded catalog; customers is not.
    await expect(page.getByText("analytics.public.daily_sales")).toBeVisible();
    await expect(page.getByText("analytics.public.customers")).toHaveCount(0);
  });

  test("a member deep-links into the catalog entity editor", async ({ page }) => {
    await page.getByRole("button", { name: /^Finance/ }).click();
    await page.getByText("analytics.public.daily_sales").click();
    await expect(page.getByRole("heading", { name: /daily_sales/ })).toBeVisible();
    expect(page.url()).toContain("/knowledge/catalog?urn=");
  });

  test("creates a domain and it appears in the list", async ({ page }) => {
    await page.getByRole("button", { name: "New domain" }).click();
    await page.getByPlaceholder("e.g. Finance").fill("Supply Chain");
    await page.getByPlaceholder(/What this domain covers/).fill("Procurement and logistics.");
    await page.getByRole("button", { name: "Create domain" }).click();
    await expect(page.getByRole("button", { name: /Supply Chain/ })).toBeVisible();
    await expect(page.getByText("Procurement and logistics.")).toBeVisible();
  });

  test("edits a domain description and the change reflects on read", async ({ page }) => {
    await page.getByRole("button", { name: /^Finance/ }).click();
    await page.getByRole("button", { name: "Edit description" }).click();
    await page.getByLabel("Domain description").fill("Everything money touches.");
    await page.getByRole("button", { name: "Save" }).click();
    await expect(page.getByRole("button", { name: "Save" })).toHaveCount(0);
    await expect(page.getByText("Everything money touches.")).toBeVisible();
  });

  test("adds a table to the domain, then removes it again", async ({ page }) => {
    await page.getByRole("button", { name: /^Marketing/ }).click();
    // Marketing starts empty.
    await expect(page.getByText("No table in this connection is in this domain.")).toBeVisible();

    await page.getByPlaceholder("Search tables by name…").fill("clickstream");
    await page.getByRole("button", { name: "Add" }).click();
    await expect(page.getByText("raw.events.clickstream")).toBeVisible();

    await page.getByRole("button", { name: /Remove raw.events.clickstream from this domain/ }).click();
    await expect(page.getByText("No table in this connection is in this domain.")).toBeVisible();
  });

  test("states the membership before deleting, then retires the domain", async ({ page }) => {
    await page.getByRole("button", { name: /^Finance/ }).click();
    await page.getByRole("button", { name: "Delete domain" }).click();
    // The confirmation states the blast radius: daily_sales is in Finance.
    await expect(
      page.getByText(/1 table in this connection is in this domain/),
    ).toBeVisible();
    await page.getByRole("button", { name: "Confirm delete" }).click();
    // Deleting returns to the list, without the retired domain.
    await expect(page.getByPlaceholder(/Filter domains by name/)).toBeVisible();
    await expect(page.getByRole("button", { name: /^Finance/ })).toHaveCount(0);
  });
});
