import { type ScreenshotRoute } from "./route-types";

// The webhook source routes (#1870, #2247): the list, a posted source, its
// form, and a mailbox source with its editor. Kept beside the manifest so it
// stays under its line budget.

export const adminWebhookRoutes: ScreenshotRoute[] = [
  {
    // Inbound webhook sources (#1870): the list, one source with its address,
    // request counts, compaction and rejections, and the form a source is
    // created with.
    slug: "admin-webhooks",
    path: "/portal/admin/webhooks",
    category: "admin",
  },
  {
    slug: "admin-webhook-detail",
    path: "/portal/admin/webhooks/email-events",
    category: "admin",
  },
  {
    slug: "admin-webhook-new",
    path: "/portal/admin/webhooks/new",
    category: "admin",
  },
  {
    // A source whose events are a mailbox's messages (#2247): the server,
    // the allowed senders and each folder's counts.
    slug: "admin-webhook-mailbox",
    path: "/portal/admin/webhooks/meeting-summaries",
    category: "admin",
  },
  {
    slug: "admin-webhook-mailbox-edit",
    path: "/portal/admin/webhooks/meeting-summaries/edit",
    category: "admin",
  },
];
