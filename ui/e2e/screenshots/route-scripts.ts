import {
  openScriptDocumentation,
  openScriptDryRun,
  openScriptRunHistory,
  openScriptRunLog,
  openScriptRunPanel,
  openScriptRunsTab,
  openScriptSource,
  openScriptSchedule,
  openScriptVersionHistory,
} from "./route-actions";
import { type ScreenshotRoute } from "./route-types";

// Every managed-script capture, on both surfaces: what a person who owns an
// automation reads (#1290), and what an administrator reads across every
// script (#1307). They live beside the manifest rather than inside it for the
// same reason the drawer routes do — one feature's captures are read together,
// and the manifest stays a table of contents rather than a file that grows
// without bound.

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
    // The form an owner documents a script in (#1369): the four fields that say
    // what it is, edited on the page that shows them rather than by asking an
    // agent. The read state is on the detail capture above, where the
    // description is already rendered as markdown.
    slug: "script-documentation",
    path: "/portal/scripts/script-001",
    category: "user",
    beforeCapture: openScriptDocumentation,
  },
  {
    // A paused automation, and the control that resumes it (#1307). The
    // running case is on the detail capture above, where the cadence, its
    // zone, and the binding every fire passes are already in frame; what this
    // one adds is the state a report sits in when its owner has stopped it.
    slug: "script-schedule-paused",
    path: "/portal/scripts/script-003",
    category: "user",
    beforeCapture: openScriptSchedule,
  },
  {
    // The code, editable by the person who owns it (#1307). The portal's own
    // source editor, told the content is Python, which is what Starlark reads
    // as — and the saved version is the version that runs.
    slug: "script-source",
    path: "/portal/scripts/script-001",
    category: "user",
    beforeCapture: openScriptSource,
  },
  {
    // Running it now (#1363): the same run the schedule produces, asked for by
    // its owner, with every value the platform can offer offered rather than
    // typed.
    slug: "script-run-now",
    path: "/portal/scripts/script-001",
    category: "user",
    beforeCapture: openScriptRunPanel,
  },
  {
    // What an author gets back before saving the version that runs (#1364): a
    // real execution as themselves that persisted nothing, with the shape of
    // each output and the log it printed.
    slug: "script-dry-run",
    path: "/portal/scripts/script-001",
    category: "user",
    beforeCapture: openScriptDryRun,
  },
  {
    // The version history, where the executing version — the latest saved one
    // — opens by default with the roles a run of it presents.
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
    // The administrator's listing: every script on the platform and what it is
    // executing (#1307). A row opens the same detail page its owner opens.
    slug: "admin-scripts",
    path: "/portal/admin/scripts",
    category: "admin",
  },
  {
    // One script on the administrator's surface: the same page its owner
    // opens, with nothing taken away — running, editing, dry-running,
    // re-timing, and the version history with the roles each version runs
    // under.
    slug: "admin-script-detail",
    path: "/portal/admin/scripts/script-001",
    category: "admin",
    beforeCapture: openScriptVersionHistory,
  },
  {
    // What the platform has been running unattended (#1307): the metrics the
    // run worker emits, and the exact recent history beneath them.
    //
    // Last in the list on purpose: the tab it selects persists on the page the
    // captures share, so it must not sit in front of the ones that read the
    // script listing.
    slug: "admin-script-runs",
    path: "/portal/admin/scripts",
    category: "admin",
    beforeCapture: openScriptRunsTab,
  },
];
