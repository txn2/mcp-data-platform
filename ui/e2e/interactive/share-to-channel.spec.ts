import { test, expect } from "@playwright/test";
import { authenticate } from "../screenshots/helpers/auth";

// Posting an asset to a channel from Share (#2241, #2291): the dialog's
// channel view offers the bot's chat channels, a double click posts once
// (#2091), and a post returns to the Share view, so a second post takes
// choosing again. That the asset page carries no separate post action is
// AssetViewer.test.tsx's.
//
// The share requests are counted by wrapping the page's fetch before the app
// loads, since the mock service worker answers them in the browser.

test.beforeEach(async ({ page }) => {
  await page.addInitScript(() => {
    const w = window as unknown as { __posts: number };
    w.__posts = 0;
    const native = window.fetch.bind(window);
    window.fetch = (input: RequestInfo | URL, init?: RequestInit) => {
      const url = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
      if (url.endsWith("/channel-shares")) w.__posts++;
      return native(input, init);
    };
  });
  await authenticate(page);
  await page.goto("/portal/assets/ast-001");
  await expect(page.getByRole("button", { name: "Share", exact: true })).toBeVisible({ timeout: 20_000 });
});

const posts = (page: import("@playwright/test").Page) =>
  page.evaluate(() => (window as unknown as { __posts: number }).__posts);

test("a double click on Post sends one post, and a second post takes choosing again", async ({ page }) => {
  await page.getByRole("button", { name: "Share", exact: true }).click();
  const dialog = page.getByRole("dialog");
  await dialog.getByRole("button", { name: "Post to a channel" }).click();
  await dialog.getByRole("combobox", { name: "Channel" }).click();
  await page.getByRole("option", { name: /#town-square · Mattermost/ }).click();
  await expect(dialog.getByText(/everyone in this channel sees the preview image/i)).toBeVisible();

  await dialog.getByRole("button", { name: "Post", exact: true }).dblclick();
  await expect(dialog.getByTestId("share-toast")).toContainText("Posted to #town-square");
  await expect(dialog.getByRole("combobox", { name: "Add people or groups" })).toBeVisible();
  expect(await posts(page)).toBe(1);

  await dialog.getByRole("button", { name: "Post to a channel" }).click();
  await expect(dialog.getByRole("button", { name: "Post", exact: true })).toBeDisabled();
  await dialog.getByRole("combobox", { name: "Channel" }).click();
  await page.getByRole("option", { name: /#ops-alerts · Mattermost/ }).click();
  await dialog.getByRole("button", { name: "Post", exact: true }).click();
  await expect(dialog.getByTestId("share-toast")).toContainText("Posted to #ops-alerts");
  expect(await posts(page)).toBe(2);
});
