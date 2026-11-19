import type { ScreenshotRoute } from "./route-types";

// Home (#2504) and the tidying views beside Assets and Collections (#2509).
export const libraryRoutes: ScreenshotRoute[] = [
  {
    // Home: recent work, favorites, drafts and what is waiting.
    slug: "home",
    path: "/portal/",
    category: "user",
  },
  {
    slug: "assets-unused",
    path: "/portal/unused",
    category: "user",
  },
  {
    slug: "assets-duplicates",
    path: "/portal/duplicates",
    category: "user",
  },
  {
    slug: "assets-trash",
    path: "/portal/trash",
    category: "user",
  },
];
