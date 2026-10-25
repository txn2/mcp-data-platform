import type { ScreenshotRoute } from "./route-types";

// Notification channels (#1721-#1724): the Share dialog's channel
// destination (#2241), and the settings section where the channels and the scheduled-run
// failure alert live. Kept apart from route-manifest.ts for its size.
export const channelRoutes: ScreenshotRoute[] = [
  {
    // Post to a channel (#2241, #2291): the dialog's channel view with one
    // of the bot's chat channels chosen and the preview of what it will see.
    slug: "asset-share-to-channel",
    path: "/portal/assets/ast-001",
    category: "user",
    beforeCapture: async (page) => {
      await page.getByRole("button", { name: "Share", exact: true }).click();
      await page.getByRole("button", { name: "Post to a channel" }).click();
      await page.getByRole("combobox", { name: "Channel" }).click();
      await page.getByRole("option", { name: /#town-square · / }).click();
      await page.getByLabel("Message (optional)").fill("This week's numbers are in.");
      await page.waitForTimeout(300);
    },
  },
  {
    // The notification channels, each with its recent activity (#1722), on
    // their own settings section (#2093).
    slug: "admin-settings-channels",
    path: "/portal/admin/settings/channels",
    category: "admin",
  },
  {
    // The scheduled-run failure alert's channels (#1724).
    slug: "admin-settings-run-failures",
    path: "/portal/admin/settings/run-failures",
    category: "admin",
  },
];
