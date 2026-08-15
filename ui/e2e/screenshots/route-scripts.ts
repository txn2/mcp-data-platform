import {
  openScriptDeliveryGrant,
  openScriptFirstApproval,
  openScriptReview,
  openScriptRunHistory,
  openScriptRunLog,
  openScriptVersionHistory,
} from "./route-actions";
import { type ScreenshotRoute } from "./route-types";

// Every managed-script capture, on both surfaces: what a person who owns an
// automation reads (#1290), and what a reviewer decides on (#1287, #1288).
// They live beside the manifest rather than inside it for the same reason the
// drawer routes do — one feature's captures are read together, and the
// manifest stays a table of contents rather than a file that grows without
// bound.

export const userScriptRoutes: ScreenshotRoute[] = [
  {
    // The owner's view of their automations (#1290): what each script is
    // executing, on what cadence, and how its last run went.
    slug: "scripts",
    path: "/portal/scripts",
    category: "user",
  },
  {
    // The same page for an account with no automations at all, which is what
    // most people see before an agent has written one for them.
    slug: "scripts-empty",
    path: "/portal/scripts?empty=scripts",
    category: "user",
  },
  {
    // One script in full: the contract a reference to it resolves to, and the
    // parameters a run binds against.
    slug: "script-detail",
    path: "/portal/scripts/script-001",
    category: "user",
  },
  {
    // The version history, where the source of the version behind the
    // execution gate opens by default with the grant approving it bound.
    slug: "script-versions",
    path: "/portal/scripts/script-001",
    category: "user",
    beforeCapture: openScriptVersionHistory,
  },
  {
    // The refresh history of the automation: a success, a failure with its
    // reason, and a fire skipped because the previous run was still going.
    slug: "script-runs",
    path: "/portal/scripts/script-001",
    category: "user",
    beforeCapture: openScriptRunHistory,
  },
  {
    // One run opened in place: what it was given, what it cost, the asset
    // version it produced, and the log it printed while working.
    slug: "script-run-log",
    path: "/portal/scripts/script-001",
    category: "user",
    beforeCapture: openScriptRunLog,
  },
];

export const adminScriptRoutes: ScreenshotRoute[] = [
  {
    // Managed-script review: the queue of versions waiting for approval and
    // every script with what it is executing (#1287).
    slug: "admin-scripts",
    path: "/portal/admin/scripts",
    category: "admin",
  },
  {
    // The decision itself: the capability diff and the code diff for one
    // version, which is what makes approving a review rather than a stamp.
    slug: "admin-script-review",
    path: "/portal/admin/scripts",
    category: "admin",
    beforeCapture: openScriptReview,
  },
  {
    // The other decision: a script nothing has ever approved, where the whole
    // source is the change and approving starts something running.
    slug: "admin-script-first-approval",
    path: "/portal/admin/scripts",
    category: "admin",
    beforeCapture: openScriptFirstApproval,
  },
  {
    // The delivery grant: a script that sends data out of the platform, and the
    // address a reviewer has to supply before approving it (#1288).
    slug: "admin-script-delivery-grant",
    path: "/portal/admin/scripts",
    category: "admin",
    beforeCapture: openScriptDeliveryGrant,
  },
];
