import { test, expect } from "@playwright/test";
import { authenticate } from "../screenshots/helpers/auth";

// Sharing an asset to a channel from Share (#2241): the Share dialog offers
// the bot's chat channels, a double click posts once (#2091), and posting
// again takes choosing the channel again. That the asset page carries no
// separate post action is AssetViewer.test.tsx's.
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
  await dialog.getByRole("combobox", { name: "Channel" }).click();
  await page.getByRole("option", { name: "#town-square (team-chat)" }).click();
  await expect(dialog.getByText(/everyone in this channel sees/i)).toBeVisible();

  await dialog.getByRole("button", { name: "Post", exact: true }).dblclick();
  await expect(dialog.getByRole("status")).toContainText("Posted to #town-square (team-chat).");
  await expect(dialog.getByRole("button", { name: "Post", exact: true })).toBeDisabled();
  expect(await posts(page)).toBe(1);

  await dialog.getByRole("combobox", { name: "Channel" }).click();
  await page.getByRole("option", { name: "#ops-alerts (team-chat)" }).click();
  await dialog.getByRole("button", { name: "Post", exact: true }).click();
  await expect(dialog.getByRole("status")).toContainText("Posted to #ops-alerts (team-chat).");
  expect(await posts(page)).toBe(2);
});
