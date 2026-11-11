import type { ScreenshotRoute } from "./route-types";

// Settings sections kept apart from route-manifest.ts for its size: external
// accounts (#2092), requests for access (#2090) and General feedback (#2603).
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
  {
    // Who is told of a General feedback post: the people it reaches, the
    // personas with their counts, recipients, channels, the acknowledgement.
    slug: "admin-settings-general-feedback",
    path: "/portal/admin/settings/general-feedback",
    category: "admin",
  },
];
