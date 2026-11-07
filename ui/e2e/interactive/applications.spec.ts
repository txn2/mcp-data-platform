import { test, expect } from "@playwright/test";
import { authenticate } from "../screenshots/helpers/auth";

// The application page's Parts card and Add parts dialog (#2527), against
// MSW's "shop" and "intake" applications.

const UUID = /[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/i;

test.describe("Application parts", () => {
  test("names every part, never an id, and asks before removing one", async ({ page }) => {
    await authenticate(page);
    await page.goto("/portal/applications/app-shop");
    const card = page.getByTestId("application-parts");
    await expect(card.getByText("Daily sales report")).toBeVisible();
    await expect(card.getByText("Sales dashboard")).toBeVisible();
    expect(await card.innerText()).not.toMatch(UUID);
    await page.getByRole("button", { name: "Actions for Order intake API" }).click();
    await page.getByRole("menuitem", { name: "Remove from application" }).click();
    await expect(page.getByText(/Remove Order intake API from shop\?/)).toBeVisible();
    await page.getByRole("button", { name: "Cancel" }).click();
    await expect(card.getByText("Order intake API")).toBeVisible();
  });

  test("adds two parts picked by name, and names the application holding another", async ({ page }) => {
    await authenticate(page);
    await page.goto("/portal/applications/app-intake");
    await page.getByRole("button", { name: "Add parts" }).click();
    const dialog = page.getByRole("dialog", { name: "Add parts to intake" });
    await expect(dialog.getByRole("checkbox", { name: "Daily sales report, already in shop" })).toBeDisabled();
    await expect(dialog.getByTestId("candidate-script-script-001")).toContainText("Already part of shop.");
    await dialog.getByPlaceholder("Search automations, assets and webhook sources").fill("revenue");
    await dialog.getByRole("checkbox", { name: "Monthly revenue close" }).check();
    await dialog.getByRole("checkbox", { name: "Revenue by region" }).check();
    await dialog.getByRole("button", { name: "Add 2 parts" }).click();
    await expect(dialog).toHaveCount(0);
    const card = page.getByTestId("application-parts");
    await expect(card.getByText("Monthly revenue close")).toBeVisible();
    await expect(card.getByText("Revenue by region")).toBeVisible();
  });
});
