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
  // The Add parts dialog (#2527): a search by name across automations,
  // assets and webhook sources, one part held by another application.
  {
    slug: "application-add-parts",
    path: "/portal/applications/app-intake",
    category: "user",
    beforeCapture: async (page) => {
      await page.getByRole("button", { name: "Add parts" }).click();
      await page.getByRole("dialog", { name: "Add parts to intake" }).getByText("Already part of").waitFor();
    },
  },
  // The Approvals queue (#2494): a pending request for a form's public use,
  // with what changed since the version approved before.
  {
    slug: "approvals",
    path: "/portal/approvals",
    category: "user",
  },
];
