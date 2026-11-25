import { type ScreenshotRoute } from "./route-types";

// Sites (#2559): the listing and each of its states, a site's tabs, one
// release, the administrators' Domains page and the settings section.
export const siteRoutes: ScreenshotRoute[] = [
  { slug: "sites", path: "/portal/sites", category: "user" },
  { slug: "sites-empty", path: "/portal/sites?state=empty", category: "user" },
  { slug: "sites-previews-off", path: "/portal/sites?state=previews-off", category: "user" },
  { slug: "site-environments", path: "/portal/sites/marketing", category: "user" },
  { slug: "site-releases", path: "/portal/sites/marketing/releases", category: "user" },
  { slug: "site-release", path: "/portal/sites/marketing/releases/12", category: "user" },
  // A release's type check (#2793), in the states it can end in besides the
  // three errors release 12 shows.
  { slug: "site-release-typecheck-clean", path: "/portal/sites/marketing/releases/12?typecheck=clean", category: "user" },
  { slug: "site-release-typecheck-waiting", path: "/portal/sites/marketing/releases/12?typecheck=queued", category: "user" },
  { slug: "site-release-typecheck-over-limit", path: "/portal/sites/marketing/releases/12?typecheck=over_limit", category: "user" },
  { slug: "site-release-typecheck-unsupported", path: "/portal/sites/marketing/releases/12?typecheck=unsupported", category: "user" },
  { slug: "site-access", path: "/portal/sites/marketing/access", category: "user" },
  { slug: "site-domains", path: "/portal/sites/marketing/domains", category: "user" },
  { slug: "site-history", path: "/portal/sites/marketing/history", category: "user" },
  { slug: "admin-domains", path: "/portal/admin/domains", category: "admin" },
  { slug: "admin-settings-sites", path: "/portal/admin/settings/sites", category: "admin" },
];
