import { test, expect } from "@playwright/test";
import { authenticate } from "../screenshots/helpers/auth";

// Admin > Settings is a nav of sections, each at its own route (#2093), and a
// section with unsaved changes is asked about before it is left, whether by
// the section nav or the sidebar.
test.describe("Admin settings sections", () => {
  test("opens a section by its route and asks before leaving unsaved changes", async ({ page }) => {
    await authenticate(page);
    await page.goto("/portal/admin/settings/maps");
    await expect(page.getByText("Add region").first()).toBeVisible();
    await expect(page.getByText("Email (SMTP)")).toHaveCount(0);

    const nav = page.getByTestId("settings-nav");
    await nav.getByRole("button", { name: "Email" }).click();
    await expect(page).toHaveURL(/\/portal\/admin\/settings\/email$/);
    await expect(page.getByText("Email (SMTP)")).toBeVisible();

    await page.getByLabel("Host").fill("smtp.changed.example.com");
    let asked = "";
    page.once("dialog", (d) => {
      asked = d.message();
      void d.dismiss();
    });
    await nav.getByRole("button", { name: "Channels" }).click();
    expect(asked).toBe("Discard unsaved changes?");
    await expect(page).toHaveURL(/\/portal\/admin\/settings\/email$/);
    await expect(page.getByLabel("Host")).toHaveValue("smtp.changed.example.com");

    page.once("dialog", (d) => void d.accept());
    await nav.getByRole("button", { name: "Channels" }).click();
    await expect(page).toHaveURL(/\/portal\/admin\/settings\/channels$/);
    await expect(page.getByText("Notification Channels")).toBeVisible();
  });
});
