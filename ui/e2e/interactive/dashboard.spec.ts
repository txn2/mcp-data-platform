import { test, expect, type Page } from "@playwright/test";
import { authenticate } from "../screenshots/helpers/auth";

// Interactive coverage for the admin observability Dashboard (MCP / API
// Gateway / Events tabs). These encode the failures reported during the
// rebuild as regressions: empty dashboards on certain presets, blank views
// after switching back to a tab, and broken drilldowns. Runs against MSW so
// the data is rich and deterministic.

const PRESETS = ["1h", "6h", "24h", "7d"] as const;

async function gotoDashboard(page: Page): Promise<void> {
  await authenticate(page);
  await page.goto("/portal/admin");
  await expect(page.getByRole("button", { name: "MCP", exact: true })).toBeVisible();
}

async function clickTab(page: Page, name: string): Promise<void> {
  await page.getByRole("button", { name, exact: true }).click();
}

test.describe("Admin Dashboard", () => {
  test.beforeEach(async ({ page }) => {
    await gotoDashboard(page);
  });

  test("Health tab shows a card per node", async ({ page }) => {
    await clickTab(page, "Health");
    await expect(page.getByText("per-node runtime health")).toBeVisible();
    // The 3 mock nodes appear by pod name (the display default).
    await expect(page.getByText("mcp-data-platform-7d9f8c5b4-abcde")).toBeVisible();
    await expect(page.getByText("mcp-data-platform-7d9f8c5b4-fghij")).toBeVisible();
    await expect(page.getByText("mcp-data-platform-7d9f8c5b4-klmno")).toBeVisible();
    // Node 2 has a short uptime -> "restarted" health badge.
    await expect(page.getByText("restarted")).toBeVisible();
    await expect(page.getByText("healthy").first()).toBeVisible();
    // Each node has CPU + memory trend charts that render an area.
    await expect(page.getByText("CPU (cores)").first()).toBeVisible();
    await expect(page.getByText("Memory (RSS)").first()).toBeVisible();
    await expect(page.locator(".recharts-area-area").first()).toBeVisible();
  });

  test("MCP tab shows data on every time-range preset", async ({ page }) => {
    // Regression: the MCP dashboard previously rendered all "-" on some
    // presets. Every preset must show a non-empty Total Calls value.
    for (const preset of PRESETS) {
      await page.getByRole("button", { name: preset, exact: true }).click();
      const total = page
        .locator("div", { hasText: /^Total Calls/ })
        .first()
        .locator("span")
        .last();
      await expect(total).not.toHaveText("-", { timeout: 10_000 });
    }
  });

  test("MCP tab renders all visualization panels", async ({ page }) => {
    await expect(page.getByText("Usage Rhythm")).toBeVisible();
    await expect(page.getByText("Top Tools")).toBeVisible();
    await expect(page.getByText("By Persona")).toBeVisible();
    await expect(page.getByText("By Toolkit")).toBeVisible();
    await expect(page.getByText("Latency", { exact: true })).toBeVisible();
    // d3 heatmap draws a full 7x24 grid.
    const heatmap = page.locator('svg[aria-label="Call volume by day of week and hour of day"]');
    await expect(heatmap.locator("rect")).toHaveCount(168);
  });

  test("switching tabs and back keeps MCP data loaded", async ({ page }) => {
    // Regression: clicking back to MCP previously showed a blank/"-" view.
    await clickTab(page, "API Gateway");
    await expect(page.getByText("Traffic Flow")).toBeVisible();
    await clickTab(page, "Events");
    await clickTab(page, "MCP");
    const total = page
      .locator("div", { hasText: /^Total Calls/ })
      .first()
      .locator("span")
      .last();
    await expect(total).not.toHaveText("-");
  });

  test("API Gateway tab renders inbound/outbound, status mix, and flow", async ({ page }) => {
    await clickTab(page, "API Gateway");
    await expect(page.getByText("Inbound requests")).toBeVisible();
    await expect(page.getByText("Outbound calls")).toBeVisible();
    await expect(page.getByText("Status Mix")).toBeVisible();
    await expect(page.getByText("Traffic Flow")).toBeVisible();
    const sankey = page.locator('svg[aria-label="Connection to operation traffic flow"]');
    await expect(sankey).toBeVisible();
    // Sankey fills its container width and the bottom node label is within bounds.
    const flowBox = await sankey.boundingBox();
    const panelBox = await sankey.locator("xpath=ancestor::div[contains(@class,'rounded-lg')][1]").boundingBox();
    expect(flowBox!.width).toBeGreaterThan(panelBox!.width - 40);
    // Usage Rhythm heatmap is present here too, with the full 7x24 grid.
    const heatmap = page.locator('svg[aria-label="Call volume by day of week and hour of day"]');
    await expect(heatmap.locator("rect")).toHaveCount(168);
    await expect(page.getByText("Top connections by request volume")).toBeVisible();
  });

  test("API Gateway drilldown: connection -> endpoint -> back", async ({ page }) => {
    await clickTab(page, "API Gateway");
    // Click the salesforce row (the list <button>, not the sankey SVG label).
    const row = page
      .getByRole("button")
      .filter({ hasText: "salesforce" })
      .first();
    await row.click();
    await expect(page.getByText("Total requests")).toBeVisible();
    await expect(page.getByText("Error rate")).toBeVisible();
    await expect(page.getByText("Top endpoints by request volume")).toBeVisible();
    // Drill into an endpoint.
    await page.getByRole("button").filter({ hasText: "listContacts" }).first().click();
    await expect(page.getByText("Status class")).toBeVisible();
    await expect(page.getByText("Identity")).toBeVisible();
    // Breadcrumb back to root. Scope to <main> so it does not match the
    // sidebar's "Connections" nav item (which would navigate away).
    await page.locator("main").getByRole("button", { name: "Connections", exact: true }).click();
    await expect(page.getByText("Top connections by request volume")).toBeVisible();
  });

  test("API Gateway presets all render without error", async ({ page }) => {
    await clickTab(page, "API Gateway");
    for (const preset of PRESETS) {
      await page.getByRole("button", { name: preset, exact: true }).click();
      await expect(page.getByText("Top connections by request volume")).toBeVisible();
    }
  });

  test("Events tab renders the audit table", async ({ page }) => {
    await clickTab(page, "Events");
    await expect(page.locator("main").getByText("Timestamp")).toBeVisible();
    await expect(page.locator("main").getByText("Tool", { exact: true })).toBeVisible();
  });
});
