import { test, expect, type Locator } from "@playwright/test";
import { authenticate } from "../screenshots/helpers/auth";

// An environment's addresses on its card (#2607): the address people use
// first, a domain before the pages address, the rest muted after it. A
// two-character site (#2611): its environments other than the main one have no
// pages address, the page says why in the server's words, and the New site
// dialog refuses such a name before the server does. Against MSW's sites.

/** Each link on a card: its text, and whether it is drawn in the muted color. */
async function links(card: Locator): Promise<[string, boolean][]> {
  return card.getByRole("link").evaluateAll((as) => as.map((a) => [a.textContent ?? "", a.classList.contains("text-muted-foreground")] as [string, boolean]));
}

test.describe("Environment addresses", () => {
  test.beforeEach(async ({ page }) => {
    await authenticate(page);
  });

  test("a card lists its public domain first and its private addresses after it, muted", async ({ page }) => {
    await page.goto("/portal/sites/marketing");
    const prod = page.getByTestId("env-prod");
    await expect(prod.getByRole("link").first()).toHaveText("example.com");
    expect(await links(prod)).toEqual([
      ["example.com", false],
      ["www.example.com", true],
      ["marketing.pages.example.net", true],
    ]);
    const first = prod.getByRole("link").first();
    const last = prod.getByRole("link").last();
    expect(await first.evaluate((a) => getComputedStyle(a).color)).not.toBe(await last.evaluate((a) => getComputedStyle(a).color));
    await expect(prod.getByText("Main address", { exact: true })).toBeVisible();
    await expect(prod.getByText("Domain", { exact: true })).toHaveCount(2);
    await expect(prod.getByText("Public", { exact: true })).toHaveCount(1);
    expect(await links(page.getByTestId("env-dev"))).toEqual([["marketing--dev.pages.example.net", false]]);
  });

  test("a two-character site says why its environments have no pages address", async ({ page }) => {
    await page.goto("/portal/sites/ab");
    const note = page.getByTestId("address-note");
    await expect(note).toContainText("The environments of ab have no pages address");
    await expect(note).toContainText("an address like ab--prod.pages.example.net. DNS reserves that form, so certificate authorities refuse it.");
    await expect(page.getByTestId("env-staging")).toContainText("No address");
    expect(await links(page.getByTestId("env-prod"))).toEqual([["ab.example.com", false]]);
    expect(await links(page.getByTestId("env-dev"))).toEqual([["ab.pages.example.net", false]]);
    await expect(page.getByRole("link", { name: /ab--/ })).toHaveCount(0);
  });

  test("at 390px the note and the cards fit the screen", async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto("/portal/sites/ab");
    await expect(page.getByTestId("address-note")).toBeVisible();
    const overflow = () => page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
    await expect.poll(overflow, "the page scrolls sideways").toBeLessThanOrEqual(0);
  });

  test("the New site dialog refuses a two-character name and says why", async ({ page }) => {
    await page.goto("/portal/sites");
    await page.getByRole("button", { name: "New site" }).first().click();
    const dialog = page.getByRole("dialog", { name: "New site" });
    const name = dialog.getByLabel("Name");
    await name.fill("ab");
    await expect(dialog.getByText("A two-character name gets addresses like ab--prod, which certificate authorities refuse.", { exact: false })).toBeVisible();
    await expect(name).toHaveAttribute("aria-invalid", "true");
    await expect(dialog.getByRole("button", { name: "Create site" })).toBeDisabled();
    await name.fill("abc");
    await expect(dialog.getByText("two-character name", { exact: false })).toHaveCount(0);
    await expect(dialog.getByRole("button", { name: "Create site" })).toBeEnabled();
  });
});
