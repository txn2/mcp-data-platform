import { test, expect } from "@playwright/test";
import { authenticate } from "../screenshots/helpers/auth";

// #2468: a click outside a form dialog, on the dimmed backdrop, closed it and
// discarded what was typed. The reported case is Admin > API Catalogs, Add
// spec: a click on the backdrop leaves the dialog open with its value, and
// Cancel still closes it.

test("a backdrop click leaves the Add spec dialog open with what was typed", async ({ page }) => {
  await authenticate(page);
  await page.goto("/portal/admin/api-catalogs");
  await page.getByRole("button", { name: /Add spec/i }).click();

  const dialog = page.getByRole("dialog", { name: "Add component spec" });
  await expect(dialog).toBeVisible();
  const name = dialog.getByPlaceholder("default");
  await name.fill("drive");

  // The top-left corner of the backdrop, well outside the centered panel.
  await page.getByTestId("modal-overlay").click({ position: { x: 5, y: 5 } });

  await expect(dialog).toBeVisible();
  await expect(name).toHaveValue("drive");

  await dialog.getByRole("button", { name: "Cancel" }).click();
  await expect(dialog).toBeHidden();
});
