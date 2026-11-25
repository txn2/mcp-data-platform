import { test, expect } from "@playwright/test";
import { authenticate } from "../screenshots/helpers/auth";

// Each environment lists where its endpoints answer (#2822), against MSW's
// "shop" (orders at /{id} in dev and prod, the storefront at /{route...} in
// prod) and "intake" (no endpoint handler).

test.describe("Application addresses", () => {
  test("an application with no endpoint says None and shows no placeholder", async ({ page }) => {
    await authenticate(page);
    await page.goto("/portal/applications/app-intake");
    await expect(page.getByTestId("environment-endpoints-prod")).toHaveText("None");
    await expect(page.getByText("/endpoints/…")).toHaveCount(0);
  });

  test("the last environment lists the platform URL and the application's own address", async ({ page }) => {
    await authenticate(page);
    await page.goto("/portal/applications/app-shop");
    const prod = page.getByTestId("environment-endpoints-prod");
    await expect(prod).toContainText("https://platform.acme.example/endpoints/shop/{route...}");
    await expect(prod).toContainText("https://shop.pages.acme.example");
    await expect(prod).toContainText("https://www.shop.example/endpoints/orders/{id}");
    await expect(page.getByText("/endpoints/…")).toHaveCount(0);
  });

  test("an environment before the last answers under /environments/<env>", async ({ page }) => {
    await authenticate(page);
    await page.goto("/portal/applications/app-shop");
    await expect(page.getByTestId("environment-endpoints-dev")).toContainText(
      "https://platform.acme.example/environments/dev/endpoints/orders/{id}",
    );
  });
});
