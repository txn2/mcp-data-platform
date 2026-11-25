import { type ScreenshotRoute } from "./route-types";

// Platform feedback (#2820): the settings that let agents post, the posts an
// administrator reads, one post, and the list before anything is posted.
export const platformFeedbackRoutes: ScreenshotRoute[] = [
  { slug: "admin-settings-platform-feedback", path: "/portal/admin/settings/platform-feedback", category: "admin" },
  { slug: "admin-platform-feedback", path: "/portal/admin/platform-feedback", category: "admin" },
  { slug: "admin-platform-feedback-post", path: "/portal/admin/platform-feedback/pfb_shared", category: "admin" },
  { slug: "admin-platform-feedback-empty", path: "/portal/admin/platform-feedback?feedback=empty", category: "admin" },
];
