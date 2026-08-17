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
      // Pick a row that STATED a purpose (#1317), not simply the first one:
      // most tools are outside the gated set, so the first row is usually a
      // call with no purpose and the drawer would capture without the section
      // the feature exists to show. The purpose cell carries the full text as
      // its title, so a non-empty title is exactly "this call stated one".
      const withPurpose = page
        .locator('table tbody tr:has(td:nth-child(4)[title]:not([title=""]))')
        .first();
      const row = (await withPurpose.count())
        ? withPurpose
        : page.locator("table tbody tr").first();
      await row.click({ timeout: 2_000 }).catch(() => {});
      await page.waitForTimeout(600);
    },
  },
  {
    // A session opened: the summary, what it produced, and the ordered
    // timeline. The detail lives behind an id, so it is reached by clicking
    // the first row rather than by a static URL.
    slug: "admin-session-detail",
    path: "/portal/admin/sessions",
    category: "admin",
    beforeCapture: async (page) => {
      // Prefer a session that saved something, so the capture shows the
      // outputs section populated rather than its empty state.
      const withOutput = page
        .locator("table tbody tr:not(:has(td:nth-child(7):text-is('-')))")
        .first();
      const row = (await withOutput.count())
        ? withOutput
        : page.locator("table tbody tr").first();
      await row.click({ timeout: 2_000 }).catch(() => {});
      await page.waitForTimeout(600);
    },
  },
  {
    // The reader's own session opened, reached the way the reader reaches it:
    // by clicking a row in My Sessions. The detail hangs off an id, so there
    // is no static URL to capture it at.
    slug: "my-session-detail",
    path: "/portal/activity/sessions",
    category: "user",
    beforeCapture: async (page) => {
      // Prefer a session that left something behind, so the capture shows the
      // outputs populated rather than their empty state. Produced is the last
      // column, one earlier than the admin table's since this one drops User.
      const withOutput = page
        .locator("table tbody tr:not(:has(td:nth-child(6):text-is('-')))")
        .first();
      const row = (await withOutput.count())
        ? withOutput
        : page.locator("table tbody tr").first();
      await row.click({ timeout: 2_000 }).catch(() => {});
      await page.waitForTimeout(600);
    },
  },
  {
    // One recorded call opened by an operator: what ran, what came of it, and
    // the decision to publish it. The detail hangs off an id, so it is reached
    // by clicking a row rather than by a static URL.
    slug: "admin-call-detail",
    path: "/portal/admin/calls",
    category: "admin",
    beforeCapture: async (page) => {
      // Prefer a satisfied record, so the capture shows what was built from
      // the call and the publish decision rather than the not-yet state.
      const satisfied = page.locator("table tbody tr:has-text('satisfied')").first();
      const row = (await satisfied.count())
        ? satisfied
        : page.locator("table tbody tr").first();
      await row.click({ timeout: 2_000 }).catch(() => {});
      await page.waitForTimeout(600);
    },
  },
  {
    // The reader's own call opened, reached the way the reader reaches it: by
    // clicking a row in My Calls.
    slug: "my-call-detail",
    path: "/portal/activity/calls",
    category: "user",
    beforeCapture: async (page) => {
      const satisfied = page.locator("table tbody tr:has-text('satisfied')").first();
      const row = (await satisfied.count())
        ? satisfied
        : page.locator("table tbody tr").first();
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
