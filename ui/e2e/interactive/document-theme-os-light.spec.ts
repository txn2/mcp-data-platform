import { test, expect } from "@playwright/test";
import { documentSeesDark, openDocument, osScheme, storeTheme } from "./helpers/documentTheme";

// On a light OS: an HTML asset's document is light until the reader picks
// dark, and then it is dark, whatever the OS says (#1789).

test.use(osScheme("light"));

test("with no choice made, the document follows the OS", async ({ page }) => {
  await storeTheme(page, "system");
  const frame = await openDocument(page);
  await expect.poll(() => documentSeesDark(frame)).toBe(false);
});

test("a reader who picked dark gets a dark document", async ({ page }) => {
  await storeTheme(page, "dark");
  const frame = await openDocument(page);
  await expect.poll(() => documentSeesDark(frame)).toBe(true);
});

test("the document follows the toggle as it is pressed", async ({ page }) => {
  await storeTheme(page, "system");
  const frame = await openDocument(page);
  const theme = page.getByRole("group", { name: "Theme" });

  await theme.getByRole("button", { name: "Dark" }).click();
  await expect.poll(() => documentSeesDark(frame)).toBe(true);

  await theme.getByRole("button", { name: "System" }).click();
  await expect.poll(() => documentSeesDark(frame)).toBe(false);
});
