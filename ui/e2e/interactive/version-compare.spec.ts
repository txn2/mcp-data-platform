import { test, expect } from "@playwright/test";
import { authenticate } from "../screenshots/helpers/auth";

// Comparing an asset's versions on its page (#2600), in the assembled app: the
// current version's "What changed" opens the line diff against the version
// before it, with the counts, and an older version compares with the current.

test.describe("Asset version compare", () => {
  test("what changed in the current version opens its diff with the counts", async ({ page }) => {
    await authenticate(page);
    await page.goto("/portal/assets/ast-001");
    await page.getByRole("button", { name: "Show details" }).click();

    await page.getByRole("button", { name: "What changed in v5" }).click();
    const dialog = page.getByTestId("version-compare");
    await expect(dialog.getByRole("heading", { name: /^Compare versions · / })).toBeVisible();
    await expect(dialog.getByText("+2 lines")).toBeVisible();
    await expect(dialog.getByText("−2 lines")).toBeVisible();
    await expect(dialog.getByTestId("version-compare-lines")).toContainText("(v5)");
    await expect(dialog.getByTestId("version-compare-lines")).toContainText("(v4)");

    await dialog.getByRole("button", { name: "Text" }).click();
    await expect(dialog.getByText("The words a reader sees, with the markup left out")).toBeVisible();
    await expect(dialog.getByTestId("version-compare-lines")).not.toContainText("<h1>");
  });
});
