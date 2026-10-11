import type { ScreenshotRoute } from "./route-types";

// Notification channels (#1721-#1724): the asset page's Post to a channel
// dialog, and the settings section where the channels and the scheduled-run
// failure alert live. Kept apart from route-manifest.ts for its size.
export const channelRoutes: ScreenshotRoute[] = [
  {
    // Post to a channel (#1723): the asset page's dialog with a channel
    // chosen, which is the state that says what the channel shows.
    slug: "asset-post-to-channel",
    path: "/portal/assets/ast-001",
    category: "user",
    beforeCapture: async (page) => {
      await page.getByRole("button", { name: "Post to a channel" }).click();
      await page.getByRole("combobox", { name: "Channel" }).click();
      await page.getByRole("option", { name: "ops-alerts" }).click();
      await page.getByLabel("Message").fill("This week's numbers are in.");
      await page.waitForTimeout(300);
    },
  },
  {
    // The scheduled-run failure alert (#1724) and the notification channels
    // below it, each channel with its recent activity (#1722), scrolled into
    // view: both sit below the fold of the capture above.
    slug: "admin-settings-channels",
    path: "/portal/admin/settings",
    category: "admin",
    beforeCapture: async (page) => {
      await page.locator("text=Scheduled run failures").first().scrollIntoViewIfNeeded();
      await page.waitForTimeout(300);
    },
    afterCapture: async (page) => {
      await page.evaluate(() => window.scrollTo(0, 0));
    },
  },
];
