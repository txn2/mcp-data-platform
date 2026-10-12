import { test, expect } from "@playwright/test";
import { authenticate } from "../screenshots/helpers/auth";

// The asset page's Post to a channel dialog (#2091). A post that was accepted
// used to leave the form open with Post enabled, and every further click was
// another message in the channel. Now an accepted post leaves only Close, a
// double click sends one post, and posting again takes reopening the dialog.
//
// The publish requests are counted by wrapping the page's fetch before the
// app loads, since the mock service worker answers them in the browser.

test.beforeEach(async ({ page }) => {
  await page.addInitScript(() => {
    const w = window as unknown as { __publishes: number };
    w.__publishes = 0;
    const native = window.fetch.bind(window);
    window.fetch = (input: RequestInfo | URL, init?: RequestInit) => {
      const url = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
      if (url.includes("/channels/") && url.endsWith("/publish")) w.__publishes++;
      return native(input, init);
    };
  });
  await authenticate(page);
  await page.goto("/portal/assets/ast-001");
  await expect(page.getByRole("button", { name: "Post to a channel" })).toBeVisible({ timeout: 20_000 });
});

const publishes = (page: import("@playwright/test").Page) =>
  page.evaluate(() => (window as unknown as { __publishes: number }).__publishes);

test("a double click on Post sends one post and leaves only Close", async ({ page }) => {
  await page.getByRole("button", { name: "Post to a channel" }).click();
  const dialog = page.getByRole("dialog");
  await dialog.getByRole("combobox", { name: "Channel" }).click();
  await page.getByRole("option", { name: "ops-alerts" }).click();

  await dialog.getByRole("button", { name: "Post", exact: true }).dblclick();
  await expect(dialog.getByRole("status")).toContainText('accepted for delivery to "ops-alerts"');
  await expect(dialog.getByRole("button", { name: "Post", exact: true })).toHaveCount(0);
  await expect(dialog.getByRole("combobox", { name: "Channel" })).toHaveCount(0);
  expect(await publishes(page)).toBe(1);

  // Posting the asset again is a deliberate second action: reopen, post.
  // The footer's Close; the corner X carries the same name.
  await dialog.getByRole("button", { name: "Close", exact: true }).first().click();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await page.getByRole("button", { name: "Post to a channel" }).click();
  const again = page.getByRole("dialog");
  await expect(again.getByRole("button", { name: "Post", exact: true })).toBeEnabled();
  await again.getByRole("button", { name: "Post", exact: true }).click();
  await expect(again.getByRole("status")).toContainText("accepted for delivery");
  expect(await publishes(page)).toBe(2);
});
