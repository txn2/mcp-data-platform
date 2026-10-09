import { test, expect } from "@playwright/test";
import { authenticate } from "../screenshots/helpers/auth";

// A provenance call's dialog holds a request whose longest unbroken run is
// wider than the dialog: a JSON body written on one line (#2063). jsdom lays
// nothing out, so whether the dialog keeps it inside is only visible here.
test.describe("A provenance call with a long request", () => {
  test("opens a dialog no wider than its panel, with every row inside it", async ({ page }) => {
    await authenticate(page);
    await page.goto("/portal/assets/ast-001");
    await page.getByRole("button", { name: "Show details" }).click();
    await page.getByText("Provenance").first().waitFor();
    // The call sits in the version 4 capture, behind the newest one.
    await page.getByRole("button", { name: /earlier capture/ }).click();
    await page.getByRole("button", { name: /^Version 4/ }).click();

    await page.getByRole("button", { name: /\/v1\/reports\/query/ }).first().click();
    const dialog = page.getByRole("dialog");
    await expect(dialog.getByText("Stated purpose")).toBeVisible();

    const box = await dialog.boundingBox();
    expect(box).not.toBeNull();
    // max-w-lg is 32rem.
    expect(box!.width).toBeLessThanOrEqual(512);

    // Nothing the dialog holds is drawn past its right edge: not the request,
    // and not the purpose prose beside it, which the widened column used to
    // carry out with it.
    const overflow = await dialog.evaluate((el) => {
      const right = el.getBoundingClientRect().right;
      return Array.from(el.querySelectorAll("p, pre"))
        .filter((n) => n.getBoundingClientRect().right > right + 0.5)
        .map((n) => n.textContent?.slice(0, 40));
    });
    expect(overflow).toEqual([]);

    // The request is readable in place: it wraps inside its own block.
    const request = dialog.locator("pre");
    await expect(request).toContainText('"metrics":["bookings"');
    const fits = await request.evaluate((el) => el.scrollWidth <= el.clientWidth);
    expect(fits).toBe(true);
  });
});
