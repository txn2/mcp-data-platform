import { type ScreenshotRoute } from "./route-types";

// Applications (#2411): the listing, and one released application with its
// parts, what a release now would change, and its releases with the changes
// each made. The mock's "shop" application is released three times, the last a
// rollback, so the Releases section shows every kind of entry it can hold.
export const applicationRoutes: ScreenshotRoute[] = [
  {
    slug: "applications",
    path: "/portal/applications",
    category: "user",
  },
  {
    slug: "application-detail",
    path: "/portal/applications/app-shop",
    category: "user",
  },
];
