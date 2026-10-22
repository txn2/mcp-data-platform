import type { ScreenshotRoute } from "./route-types";

// The Access settings sections: external accounts (#2092) and requests for
// access (#2090). Kept apart from route-manifest.ts for its size.
export const accessSettingsRoutes: ScreenshotRoute[] = [
  {
    // How long an external user's session and sign-in links last.
    slug: "admin-settings-external-accounts",
    path: "/portal/admin/settings/external-accounts",
    category: "admin",
  },
  {
    // Whether the request page names the item, and how long a request for
    // access waits.
    slug: "admin-settings-access-requests",
    path: "/portal/admin/settings/access-requests",
    category: "admin",
  },
];
