import { type ScreenshotRoute } from "./route-types";

// The platform catalog's admin page: the Scans tab (#1918) with each
// connection's last scan, the Settings tab with the interval and the
// per-connection exclusions, and the Documents tab with each connection's
// schema document. Kept beside the manifest, like the connection and persona
// routes, so the manifest stays under its line budget.
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
];

const DAILY_SALES = encodeURIComponent("mcp:dataset:(trino,acme-warehouse,analytics.public.daily_sales)");

// The Catalog section's curation pages (#1921): a dataset with its curation,
// columns, notes, lineage and history; a column opened for editing; a glossary
// node and a term; the tag list.
export const knowledgeCatalogRoutes: ScreenshotRoute[] = [
  {
    slug: "knowledge-catalog-dataset",
    path: `/portal/knowledge/catalog?ref=${DAILY_SALES}#tables`,
    category: "user",
  },
  {
    slug: "knowledge-catalog-column-edit",
    path: `/portal/knowledge/catalog?ref=${DAILY_SALES}#tables`,
    category: "user",
    beforeCapture: async (page) => {
      // The page is reused across themes, so the editor may already be open
      // from the previous capture; only a closed one is opened.
      const toggle = page.getByRole("button", { name: "Edit column revenue" });
      if ((await toggle.textContent())?.trim() === "Edit") await toggle.click();
      await page.waitForTimeout(300);
    },
  },
  {
    slug: "knowledge-catalog-glossary",
    path: "/portal/knowledge/catalog#glossary",
    category: "user",
  },
  {
    slug: "knowledge-catalog-glossary-term",
    path: `/portal/knowledge/catalog?ref=${encodeURIComponent("mcp:glossary_term:t1")}#glossary`,
    category: "user",
  },
  {
    slug: "knowledge-catalog-tags",
    path: "/portal/knowledge/catalog#tags",
    category: "user",
  },
];
