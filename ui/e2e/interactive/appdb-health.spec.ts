import { test, expect } from "@playwright/test";
import { authenticate } from "../screenshots/helpers/auth";

// An application database's Health card (#2528), against MSW's acme-shop
// report: a session idle past its threshold and a table's dead rows (two
// warnings), an unused index (advice), and two of the application's
// statements, with four of other roles hidden.

test.describe("Application database health", () => {
  test("states the status, lists the findings and statements one line each", async ({ page }) => {
    await authenticate(page);
    await page.goto("/portal/admin/connections?kind=appdb&name=acme-shop");
    const card = page.getByTestId("appdb-health");
    await expect(card.getByTestId("health-status")).toHaveText("Needs attention · 2");
    await card.getByRole("button", { name: "Expand health" }).click();
    await expect(card.getByText("A session has been idle in a transaction for 10 minutes")).toBeVisible();
    await expect(card.getByRole("button", { name: "End session" })).toBeVisible();

    const statements = card.getByTestId("health-statements");
    await expect(statements).not.toContainText("insufficient privilege");
    await expect(statements).toContainText("4 statements from other roles are hidden");
    const row = statements.getByTestId("statement-102");
    // One line, cut with an ellipsis, not wrapped mid-statement.
    const cell = row.locator("span.truncate").first();
    const { height, lineHeight } = await cell.evaluate((el) => ({
      height: el.getBoundingClientRect().height,
      lineHeight: parseFloat(getComputedStyle(el).lineHeight),
    }));
    expect(height).toBeLessThanOrEqual(lineHeight * 1.5);
    await row.getByRole("button").first().click();
    await expect(statements.getByTestId("statement-full")).toContainText("DELETE FROM carts");
    await expect(statements).toContainText("· 1 call");
  });
});
