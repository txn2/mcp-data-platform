import { test, expect, type Page } from "@playwright/test";
import { authenticate } from "../screenshots/helpers/auth";

// The Share dialog answers who can open an item (#2291). These run the
// dialog's flows in a browser against the mock service worker, which keeps
// the shares it is sent the way the server does: one general link per item,
// changed in place.

test.use({ permissions: ["clipboard-read", "clipboard-write"] });

async function openShare(page: Page, path = "/portal/assets/ast-001") {
  await authenticate(page);
  await page.goto(path);
  await page.getByRole("button", { name: "Share", exact: true }).first().click();
  const dialog = page.getByRole("dialog");
  await expect(dialog.getByText("People with access")).toBeVisible();
  return dialog;
}

const clipboard = (page: Page) => page.evaluate(() => navigator.clipboard.readText());

test("opens on who has access, with the box focused and no create buttons", async ({ page }) => {
  const dialog = await openShare(page);
  await expect(dialog.getByRole("heading", { name: /^Share “.+”$/ })).toBeVisible();
  await expect(dialog.getByRole("combobox", { name: "Add people or groups" })).toBeFocused();
  await expect(dialog.getByText("Owner", { exact: true })).toBeVisible();
  await expect(dialog.getByText("Marcus Johnson")).toBeVisible();
  await expect(dialog.getByText(/Group · 14 people/)).toBeVisible();
  await expect(dialog.getByText("Guest", { exact: true })).toBeVisible();
  await expect(dialog.getByTestId("posted-row")).toContainText("#town-square");
  // The footer is the view's only way out: no form of its own to submit.
  await expect(dialog.getByTestId("share-footer").getByRole("button")).toHaveText(["Copy link", "Post to a channel", "Done"]);
});

test("adds a person, a group and a guest in one Share", async ({ page }) => {
  const dialog = await openShare(page);
  const box = dialog.getByRole("combobox", { name: "Add people or groups" });
  await box.fill("da");
  await expect(page.getByRole("listbox").getByText("Groups")).toBeVisible();
  await expect(page.getByRole("listbox").getByText("6 people")).toBeVisible();
  await page.getByRole("option", { name: /David Park/ }).click();

  const more = dialog.getByRole("combobox", { name: "Add more" });
  await more.fill("data");
  await page.getByRole("option", { name: /Data Engineer/ }).click();
  await more.fill("lee@partner.example");
  await page.getByRole("option", { name: /lee@partner\.example/ }).click();
  await expect(dialog.getByTestId("share-chip")).toHaveCount(3);
  await expect(dialog.getByTestId("guest-note")).toContainText("lee@partner.example doesn’t have an account.");
  await expect(dialog.getByLabel("Send an email")).toBeChecked();

  await dialog.getByRole("button", { name: "Share", exact: true }).click();
  await expect(dialog.getByTestId("share-toast")).toContainText("Shared with David Park and 2 others");
  const rows = dialog.getByTestId("access-row");
  await expect(rows.filter({ hasText: "David Park" })).toHaveCount(1);
  await expect(rows.filter({ hasText: "Data Engineer" })).toHaveCount(1);
  await expect(rows.filter({ hasText: "lee@partner.example" })).toContainText("Guest");
});

test("changes a permission in place, removes access and undoes it", async ({ page }) => {
  const dialog = await openShare(page);
  await dialog.getByRole("button", { name: "What Marcus Johnson can do: Can edit" }).click();
  await page.getByRole("menuitem", { name: "Can view" }).click();
  await expect(dialog.getByRole("button", { name: "What Marcus Johnson can do: Can view" })).toBeVisible();

  await dialog.getByRole("button", { name: /What Marcus Johnson can do/ }).click();
  await page.getByRole("menuitem", { name: "Remove access" }).click();
  await expect(dialog.getByTestId("share-toast")).toContainText("Removed Marcus Johnson");
  const marcus = dialog.getByTestId("access-row").filter({ hasText: "Marcus Johnson" });
  await expect(marcus).toHaveCount(0);
  await dialog.getByRole("button", { name: "Undo" }).click();
  await expect(marcus).toHaveCount(1);
});

test("changes general access in place, keeping the one link's address", async ({ page }) => {
  const dialog = await openShare(page);
  await dialog.getByRole("button", { name: "Copy link" }).click();
  const first = await clipboard(page);
  expect(first).toMatch(/\/portal\/view\//);

  await dialog.getByRole("button", { name: /^General access:/ }).click();
  await page.getByRole("menuitem", { name: /Anyone with the link/ }).click();
  await expect(dialog.getByTestId("share-toast")).toContainText("Link access changed to Anyone with the link");
  await expect(dialog.getByTestId("link-settings")).toContainText("Link stops working");
  await dialog.getByRole("button", { name: "Copy link" }).click();
  await expect(dialog.getByRole("button", { name: "Copied" })).toBeVisible();
  expect(await clipboard(page)).toBe(first);

  await dialog.getByRole("button", { name: "Edit", exact: true }).click();
  await dialog.getByLabel("Banner on the shared page").fill("Board only");
  await dialog.getByRole("button", { name: "Save" }).click();
  await expect(dialog.getByTestId("banner-text")).toHaveText("“Board only”");

  await dialog.getByRole("button", { name: /^General access:/ }).click();
  await page.getByRole("menuitem", { name: /Only people added/ }).click();
  await expect(dialog.getByTestId("general-sentence")).toHaveText("Only the people above can open it.");
  await dialog.getByRole("button", { name: "Copy link" }).click();
  expect(await clipboard(page)).toMatch(/\/portal\/assets\/ast-001$/);
});

test("a collection offers general access and no channel", async ({ page }) => {
  const dialog = await openShare(page, "/portal/collections/col-001");
  await expect(dialog.getByRole("button", { name: /^General access:/ })).toBeVisible();
  await expect(dialog.getByRole("button", { name: "Post to a channel" })).toHaveCount(0);
});

test.describe("at phone width", () => {
  test("the dialog fits with its footer buttons wrapping", async ({ page }) => {
    // Signed in at desktop width: the sign-in helper waits for the sidebar,
    // which a phone does not show.
    const dialog = await openShare(page);
    await page.setViewportSize({ width: 390, height: 844 });
    const box = await dialog.boundingBox();
    expect(box!.width).toBeLessThanOrEqual(390);
    const scroll = await page.evaluate(() => document.documentElement.scrollWidth);
    expect(scroll).toBeLessThanOrEqual(390);
    await expect(dialog.getByRole("button", { name: "Done" })).toBeInViewport();
  });
});
