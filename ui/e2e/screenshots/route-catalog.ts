import { type ScreenshotRoute } from "./route-types";

// The platform catalog's admin page: the Scans tab (#1918) with each
// connection's last scan, the Settings tab with the interval and the
// per-connection exclusions, the Documents tab with each connection's schema
// document, and the DataHub import (#1923) with its form, its
// runs, and the latest run's report. Kept beside the manifest, like the
// connection and persona routes, so the manifest stays under its line budget.
export const adminCatalogRoutes: ScreenshotRoute[] = [
  {
    slug: "admin-catalog",
    path: "/portal/admin/catalog",
    category: "admin",
  },
  {
    slug: "admin-catalog-settings",
    path: "/portal/admin/catalog",
    category: "admin",
    beforeCapture: async (page) => {
      await page.getByRole("tab", { name: "Settings" }).click();
      await page.waitForTimeout(300);
    },
  },
  {
    slug: "admin-catalog-documents",
    path: "/portal/admin/catalog",
    category: "admin",
    beforeCapture: async (page) => {
      await page.getByRole("tab", { name: "Documents" }).click();
      await page.waitForTimeout(300);
    },
  },
  {
    slug: "admin-catalog-import",
    path: "/portal/admin/catalog",
    category: "admin",
    beforeCapture: async (page) => {
      await page.getByRole("tab", { name: "Import" }).click();
      await page.waitForTimeout(300);
    },
  },
];
