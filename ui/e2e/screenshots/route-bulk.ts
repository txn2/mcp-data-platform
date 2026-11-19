import type { ScreenshotRoute } from "./route-types";

// Bulk actions on assets (#2507) and the tag list (#2508). Kept apart from
// route-manifest.ts for its size.
export const bulkRoutes: ScreenshotRoute[] = [
  {
    // Admin > Tags: every tag with its spellings and count, rename and merge.
    slug: "admin-tags",
    path: "/portal/admin/tags",
    category: "admin",
  },
  {
    // Two assets ticked: the bulk bar over the gallery.
    slug: "assets-bulk-selected",
    path: "/portal/assets",
    category: "user",
    beforeCapture: async (page) => {
      const boxes = page.getByRole("checkbox", { name: /^Select / });
      await boxes.nth(0).click();
      await boxes.nth(1).click();
      await page.getByRole("toolbar", { name: "Bulk actions" }).first().waitFor();
    },
  },
];
