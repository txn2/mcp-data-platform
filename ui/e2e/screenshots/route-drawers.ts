import {
  openInsightClaimConflict,
  openInsightNoRowEstimate,
  openInsightObservedState,
  openInsightReviewDrawer,
} from "./route-actions";
import { type ScreenshotRoute } from "./route-types";

/**
 * drawerRoutes are the states that live behind a row click rather than behind a
 * URL: detail drawers, review panels, and the blocks they carry. They are
 * composed into `routes` so the manifest stays a readable list of pages.
 */
export const drawerRoutes: ScreenshotRoute[] = [
  {
    slug: "admin-audit-event-detail",
    path: "/portal/admin/audit#events",
    category: "admin",
    beforeCapture: async (page) => {
      // The click may no-op when a drawer is already open from the prior theme
      // (light/dark share one page and same-hash nav doesn't reload): the
      // drawer's overlay covers the rows. That's fine — the open drawer is
      // exactly what we want to capture, so swallow the click failure.
      const row = page.locator("table tbody tr").first();
      await row.click({ timeout: 2_000 }).catch(() => {});
      await page.waitForTimeout(600);
    },
  },
  {
    slug: "knowledge-insight-detail",
    path: "/portal/knowledge#insights",
    category: "admin",
    beforeCapture: openInsightReviewDrawer,
  },
  {
    // The same review drawer on a claim whose entity the platform can see for
    // itself (#1219): the table it is queryable as and the rows it holds now,
    // beside the claim. A claim whose entity does not resolve, and a deployment
    // with no query provider, are the drawer above — the block is absent, not
    // empty, so they need no capture of their own.
    slug: "knowledge-insight-observed",
    path: "/portal/knowledge#insights",
    category: "admin",
    beforeCapture: openInsightObservedState,
  },
  {
    // The advisory conflict marker: the claim states a row count, the table
    // estimates another, and the reviewer still decides.
    slug: "knowledge-insight-conflict",
    path: "/portal/knowledge#insights",
    category: "admin",
    beforeCapture: openInsightClaimConflict,
  },
  {
    // The same block on a connection that does not estimate row counts: the
    // entity is queryable, and no count is claimed on the platform's behalf.
    slug: "knowledge-insight-no-estimate",
    path: "/portal/knowledge#insights",
    category: "admin",
    beforeCapture: openInsightNoRowEstimate,
  },
  {
    // Catalog "Add spec" modal (upload/paste/URL a component spec into the
    // selected catalog). The panel auto-selects the first catalog, so open
    // the modal directly.
    slug: "catalog-spec-modal",
    path: "/portal/admin/api-catalogs",
    category: "admin",
    beforeCapture: async (page) => {
      await page
        .getByRole("button", { name: /Add spec/i })
        .click({ timeout: 3_000 })
        .catch(() => {});
      await page.waitForTimeout(700);
    },
  },
];
