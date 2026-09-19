import { test, expect } from "@playwright/test";
import { documentSeesDark, openDocument, osScheme, storeTheme } from "./helpers/documentTheme";

// On a dark OS: an HTML asset's document is dark until the reader picks light,
// and then it is light, whatever the OS says (#1789). The frame's color-scheme
// is what a framed document's prefers-color-scheme resolves against; left at
// `normal`, every frame followed the OS whatever the toggle said.

test.use(osScheme("dark"));

test("with no choice made, the document follows the OS", async ({ page }) => {
  await storeTheme(page, "system");
  const frame = await openDocument(page);
  await expect.poll(() => documentSeesDark(frame)).toBe(true);
});

test("a reader who picked light gets a light document", async ({ page }) => {
  await storeTheme(page, "light");
  const frame = await openDocument(page);
  await expect.poll(() => documentSeesDark(frame)).toBe(false);
});

test("the document follows the toggle as it is pressed", async ({ page }) => {
  await storeTheme(page, "system");
  const frame = await openDocument(page);
  const theme = page.getByRole("group", { name: "Theme" });

  await theme.getByRole("button", { name: "Light" }).click();
  await expect.poll(() => documentSeesDark(frame)).toBe(false);

  await theme.getByRole("button", { name: "System" }).click();
  await expect.poll(() => documentSeesDark(frame)).toBe(true);
});
