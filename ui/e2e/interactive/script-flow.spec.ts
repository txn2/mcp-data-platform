import { test, expect, type Page } from "@playwright/test";
import { authenticate } from "../screenshots/helpers/auth";

// The Flow tab (#1906), driven in the assembled app against the mock server:
// a script page opens on a diagram of the saved version, derived from its
// source, and the diagram and the code point at each other. The graph the mock
// serves is the server's own answer for the mocked source
// (src/mocks/data/scriptFlows.ts).

async function openOrdersLoad(page: Page): Promise<void> {
  await authenticate(page);
  await page.goto("/portal/automations");
  await page.getByRole("row").filter({ hasText: "Nightly Orders Load" }).click();
}

test.describe("a script's flow", () => {
  test("opens on the diagram, reads a card, and opens its lines in Source", async ({ page }) => {
    await openOrdersLoad(page);

    await expect(page.getByRole("tab", { name: "Flow" })).toHaveAttribute("aria-selected", "true");
    await expect(page.getByTestId("flow-canvas")).toBeVisible();
    await expect(page.getByRole("button", { name: "Function stage(day)" })).toBeVisible();
    const side = page.getByTestId("flow-side-panel");
    await expect(side).toContainText("How to read this");

    const exp = page.locator('[data-node="op:3"]');
    await exp.dispatchEvent("pointerup");
    await expect(side).toContainText("Export JSONL to resources");
    await expect(side).toContainText("Table on acme-warehouse");

    // A parameter lights exactly the steps its value reaches.
    await side.getByRole("button", { name: "start" }).click();
    await expect(side).toContainText("Parameter");

    // Double-clicking a card opens its lines in the source.
    await exp.dblclick();
    await expect(page.getByRole("tab", { name: "Source" })).toHaveAttribute("aria-selected", "true");
    await expect(page.locator(".cm-marked-line").first()).toContainText("platform.export");
  });

  test("lights the cards the lines selected in Source produced", async ({ page }) => {
    await openOrdersLoad(page);
    await expect(page.getByTestId("flow-canvas")).toBeVisible();

    await page.getByRole("tab", { name: "Source" }).click();
    // Select the two lines of the summary's query and region lookup.
    const from = page.locator(".cm-line").filter({ hasText: "totals = platform.query" });
    const to = page.locator(".cm-line").filter({ hasText: "regions = crm(" });
    await from.click({ position: { x: 4, y: 4 } });
    await to.click({ modifiers: ["Shift"] });

    await page.getByRole("tab", { name: "Flow" }).click();
    const lit = page.locator('[data-node][aria-pressed="true"]');
    await expect(lit).toHaveCount(2);
    await expect(page.locator('[data-node="op:4"]')).toHaveAttribute("aria-pressed", "true");
    await expect(page.locator('[data-node="op:5"]')).toHaveAttribute("aria-pressed", "true");
  });
});
