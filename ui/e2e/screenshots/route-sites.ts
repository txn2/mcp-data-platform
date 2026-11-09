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
  { slug: "site-access", path: "/portal/sites/marketing/access", category: "user" },
  { slug: "site-domains", path: "/portal/sites/marketing/domains", category: "user" },
  { slug: "site-history", path: "/portal/sites/marketing/history", category: "user" },
  { slug: "admin-domains", path: "/portal/admin/domains", category: "admin" },
  { slug: "admin-settings-sites", path: "/portal/admin/settings/sites", category: "admin" },
];
