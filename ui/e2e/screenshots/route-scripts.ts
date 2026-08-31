import {
  openScriptDocumentation,
  openScriptDryRun,
  openScriptOwner,
  openScriptRunHistory,
  openScriptRunLog,
  openScriptRunsTab,
  openScriptSource,
  openScriptSchedule,
  openScriptState,
  openScriptVersionHistory,
} from "./route-actions";
import { openScriptProduced } from "./route-actions-refs";
import { type ScreenshotRoute } from "./route-types";

// Every managed-script capture, on both surfaces: what a person who owns a
// script reads (#1290), and what an administrator reads across every
// script (#1307). They live beside the manifest rather than inside it for the
// same reason the drawer routes do — one feature's captures are read together,
// and the manifest stays a table of contents rather than a file that grows
// without bound.

export const userScriptRoutes: ScreenshotRoute[] = [
  {
    // The owner's view of their scripts (#1290): what each one is executing,
    // on what schedule, and how its last run went, over the three tiles that
    // count them and also filter them (#1405).
    slug: "scripts",
    path: "/portal/scripts",
    category: "user",
  },
  {
    // The same page for an account with no scripts at all, which is what most
    // people see before an agent has written one for them.
    slug: "scripts-empty",
    path: "/portal/scripts?empty=scripts",
    category: "user",
  },
  {
    // One script in full: the details every surface agrees on — who owns it,
    // which version runs, when it fires next — and the parameters a run binds
    // against, read in the same section (#1406).
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
    // A paused schedule, and the control that resumes it (#1307). The
    // running case is on the detail capture above, where the schedule, its
    // zone, and the binding every fire passes are already in frame; what this
    // one adds is the state a report sits in when its owner has stopped it.
    slug: "script-schedule-paused",
    path: "/portal/scripts/script-003",
    category: "user",
    beforeCapture: openScriptSchedule,
  },
  {
    // The state a script carries from one run to the next (#1537): the object
    // the next run reads, the revision and the run that wrote it, and the two
    // resets. The run history above it states what each run read and saved.
    slug: "script-state",
    path: "/portal/scripts/script-001",
    category: "user",
    beforeCapture: openScriptState,
  },
  {
    // The code, and everything done to it, in one place (#1406): the portal's
    // own source editor told the content is Python, which is what Starlark
    // reads as, with Run and Dry run side by side over the one parameter form
    // they both bind. The saved version is the version that runs.
    slug: "script-source",
    path: "/portal/scripts/script-001",
    category: "user",
    beforeCapture: openScriptSource,
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
    // The version history, folded into the Source section behind a reveal
    // (#1406): the versions before the one in the editor, each with the roles
    // a run of it presents.
    slug: "script-versions",
    path: "/portal/scripts/script-001",
    category: "user",
    beforeCapture: openScriptVersionHistory,
  },
  {
    // The refresh history of one script: a success, a failure with its reason,
    // and a fire skipped because the previous run was still going.
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
  {
    // Everything the script has written, across every run (#1569), as one list
    // rather than per-run output lines: the report it refreshes, the managed
    // resource it rewrites, and a file it wrote that has since been deleted.
    // It is the answer to "what does this script touch" and "what goes stale
    // if I retire it", which no run history can give.
    slug: "script-produced",
    path: "/portal/scripts/script-001",
    category: "user",
    beforeCapture: openScriptProduced,
  },
  {
    // Every run of every script this person owns (#1405), which is the question
    // the per-script history cannot answer: not how is this report going, but
    // how are my scripts going. A row opens the run it names.
    //
    // Last in the list on purpose, for the same reason the administrator's run
    // capture is: the tab it selects persists on the page these captures share,
    // so it must not sit in front of the ones that read the script listing.
    slug: "scripts-runs",
    path: "/portal/scripts",
    category: "user",
    beforeCapture: openScriptRunsTab,
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
    // opens, with nothing taken away — editing, running, dry-running,
    // re-timing, and the version history with the roles each version runs
    // under.
    slug: "admin-script-detail",
    path: "/portal/admin/scripts/script-001",
    category: "admin",
    beforeCapture: openScriptVersionHistory,
  },
  {
    // The one control on a script's page that is an administrator's rather
    // than its owner's (#1404): moving the script to another person, which
    // hands over what its owner sees, edits, runs, and schedules, and
    // re-captures the authority its runs present.
    slug: "admin-script-owner",
    path: "/portal/admin/scripts/script-001",
    category: "admin",
    beforeCapture: openScriptOwner,
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
