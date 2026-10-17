import type { ScreenshotRoute } from "./route-types";

// Storage capacity (#2148) and the webhook receiver's alerts (#2149): the
// Storage page and the two settings sections. Kept apart from
// route-manifest.ts for its size.
export const storageRoutes: ScreenshotRoute[] = [
  {
    // The webhook receiver's alerts (#2149): each condition's warning and
    // critical, and a source's own overrides.
    slug: "admin-settings-webhook-alerts",
    path: "/portal/admin/settings/webhooks",
    category: "admin",
  },
  {
    // Storage capacity settings (#2148): limits, budgets, the SeaweedFS
    // cluster, history and alert thresholds.
    slug: "admin-settings-storage",
    path: "/portal/admin/settings/storage",
    category: "admin",
  },
  {
    // Storage capacity (#2148): every store used and free, its growth and
    // when it fills, and the buckets, spools and tables behind them.
    slug: "admin-storage",
    path: "/portal/admin/storage",
    category: "admin",
  },
];
