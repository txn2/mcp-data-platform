import { test, expect } from "@playwright/test";
import { authenticate } from "../screenshots/helpers/auth";

// A signed-in platform user who opens a share link lands on the object in
// their own portal rather than on the public page (#1473). The server sends
// them there with the token they arrived on in the query string, and this is
// the half of that the browser owns: the way back to the shared page as its
// recipient sees it.
//
// The redirect itself is the server's and is covered in pkg/portal; what these
// exercise is the page the reader lands on.

const TOKEN = "a".repeat(64);
const ASSET = "/portal/assets/ast-001";
const COLLECTION = "/portal/collections/col-001";

test.describe("The way back to the shared page", () => {
  test("an asset opened from a share link offers the shared page", async ({ page }) => {
    await authenticate(page);
    await page.goto(`${ASSET}?share=${TOKEN}`);

    const link = page.getByRole("link", { name: /shared page/i });
    await expect(link).toBeVisible();
    await expect(link).toHaveAttribute("href", `/portal/view/${TOKEN}?public=1`);
    // A new tab: the reader is checking what the recipient sees, not leaving
    // the asset they were sent to.
    await expect(link).toHaveAttribute("target", "_blank");
  });

  test("a collection opened from a share link offers the shared page", async ({ page }) => {
    await authenticate(page);
    await page.goto(`${COLLECTION}?share=${TOKEN}`);

    const link = page.getByRole("link", { name: /shared page/i });
    await expect(link).toBeVisible();
    await expect(link).toHaveAttribute("href", `/portal/view/${TOKEN}?public=1`);
  });

  test("an asset opened any other way offers nothing", async ({ page }) => {
    await authenticate(page);
    await page.goto(ASSET);

    await expect(page.getByRole("link", { name: /shared page/i })).toHaveCount(0);
  });

  test("a token that is not the shape the server issues offers nothing", async ({ page }) => {
    await authenticate(page);
    await page.goto(`${ASSET}?share=%2F%2Fevil.example.com`);

    await expect(page.getByRole("link", { name: /shared page/i })).toHaveCount(0);
  });
});
