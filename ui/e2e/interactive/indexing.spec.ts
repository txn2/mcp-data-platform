import { test, expect, type Page } from "@playwright/test";
import { authenticate } from "../screenshots/helpers/auth";

// Interactive coverage for the admin Indexing dashboard. Runs against MSW
// so the data is rich and deterministic: two kinds (api_catalog with a
// real indexed/expected ratio and failures, tools with an indexed-only
// sync indicator and an in-flight job), spanning every job state. These
// assertions exercise the healthy, pending/in-flight, and failed states
// in a single load, plus the re-index action and the drill-down filter.

async function gotoIndexing(page: Page): Promise<void> {
  await authenticate(page);
  await page.goto("/portal/admin");
  // Indexing is a tab in the admin Dashboard tab bar (alongside MCP /
  // API Gateway / Health / Events).
  await expect(page.getByRole("button", { name: "MCP", exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Indexing", exact: true }).click();
  await expect(page.getByText(/Embedding provider active/i)).toBeVisible();
}

test.describe("Admin Indexing Dashboard", () => {
  test.beforeEach(async ({ page }) => {
    await gotoIndexing(page);
  });

  test("renders the provider banner and per-kind health", async ({ page }) => {
    await expect(page.getByText("nomic-embed-text", { exact: false })).toBeVisible();
    // Both registered kinds appear as cards.
    await expect(page.getByText("api_catalog").first()).toBeVisible();
    await expect(page.getByText("tools").first()).toBeVisible();
    // api_catalog shows a real coverage ratio; tools shows an indexed-only
    // sync indicator (expected_known=false).
    await expect(page.getByText(/142 \/ 168 indexed/)).toBeVisible();
    await expect(page.getByText(/87/).first()).toBeVisible();
  });

  test("renders the d3 state heatmap and throughput timeline", async ({ page }) => {
    const heatmap = page.locator('svg[aria-label="Index job state by kind"]');
    await expect(heatmap).toBeVisible();
    // 2 kinds x 4 states = 8 cells.
    await expect(heatmap.locator("rect")).toHaveCount(8);
    await expect(
      page.locator('svg[aria-label="Completed index jobs over time"]'),
    ).toBeVisible();
  });

  test("shows in-flight, retry backoff, and failure triage", async ({ page }) => {
    await expect(page.getByText("In flight")).toBeVisible();
    await expect(page.getByText("Retry backoff")).toBeVisible();
    await expect(page.getByText("Failure triage")).toBeVisible();
    // The failure-triage group surfaces the grouped error signature.
    await expect(page.getByText(/provider timeout/i).first()).toBeVisible();
  });

  test("retrying a failed job posts a reindex", async ({ page }) => {
    const retry = page.getByRole("button", { name: /Retry/i }).first();
    const [resp] = await Promise.all([
      page.waitForResponse(
        (r) => r.url().includes("/index-jobs/reindex") && r.request().method() === "POST",
      ),
      retry.click(),
    ]);
    expect(resp.status()).toBe(202);
  });

  test("re-indexing a kind posts a reindex", async ({ page }) => {
    const reindex = page.getByRole("button", { name: /Re-index/i }).first();
    const [resp] = await Promise.all([
      page.waitForResponse(
        (r) => r.url().includes("/index-jobs/reindex") && r.request().method() === "POST",
      ),
      reindex.click(),
    ]);
    expect(resp.status()).toBe(202);
  });

  test("drill-down filters the job table by status", async ({ page }) => {
    // Scope to the Jobs section's status select (the second select).
    const statusSelect = page.getByLabel("Filter by status");
    await statusSelect.selectOption("failed");
    // The two failed units remain in the table; the running tools unit is
    // filtered out of the table body.
    await expect(page.getByRole("cell", { name: "globex|v2" })).toBeVisible();
    await expect(page.getByRole("cell", { name: "platform" })).toHaveCount(0);
  });
});
