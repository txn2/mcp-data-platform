import { Fragment, type ReactNode } from "react";
import { Database, Lightbulb, BookOpen } from "lucide-react";

/**
 * What one portal section says it is.
 *
 * The name and the icon are not here: they come from the section's own nav
 * entry (portalNavItems), so the header a reader sees and the item they
 * clicked cannot disagree. A test fails when a reader-facing nav entry has no
 * entry in this table.
 *
 * Both fields describe the section. Neither instructs the reader: a person
 * reading this is finding out what they are looking at, not being told where
 * to file something.
 */
export interface SectionIntroCopy {
  /** The section's nav path, and the key this table is read by. */
  path: string;
  /**
   * The one line a returning reader sees, next to the section's icon. Absent
   * only where `compact` draws that line instead.
   */
  summary?: string;
  /** The fuller description behind the disclosure. */
  about: ReactNode;
  /** localStorage key holding this section's expanded/collapsed choice. */
  storageKey: string;
  /** Disclosure button label when collapsed. Defaults to "What is this?". */
  toggleLabel?: string;
  /**
   * Drawn in place of the icon and summary row, for a section whose summary is
   * a picture. One line tall, like the row it replaces, and the reason
   * `summary` may be absent.
   */
  compact?: ReactNode;
}

// The three lifecycle stages, color-coded by maturity: raw memory (neutral),
// proposed insight (amber, the "awaiting review" semantic used elsewhere), and
// canonical knowledge (primary). The tint progression itself teaches that data
// ripens from captured to reviewed to trusted.
const LIFECYCLE_STAGES = [
  {
    icon: Database,
    title: "Memory",
    caption: "captured automatically",
    iconClass: "text-slate-400",
    badgeClass: "bg-slate-400/10 ring-slate-400/20",
  },
  {
    icon: Lightbulb,
    title: "Insight",
    caption: "proposed for review",
    iconClass: "text-amber-500 dark:text-amber-400",
    badgeClass: "bg-amber-500/10 ring-amber-500/20",
  },
  {
    icon: BookOpen,
    title: "Knowledge",
    caption: "promoted, shared, canonical",
    iconClass: "text-primary",
    badgeClass: "bg-primary/10 ring-primary/20",
  },
] as const;

/**
 * The Memory -> Insight -> Knowledge pipeline, the Knowledge section's compact
 * row: nodes joined by a fading rail so the three stages read as one
 * progression rather than three separate chips.
 */
function KnowledgeLifecycle() {
  return (
    <ol className="flex min-w-0 flex-1 items-center gap-2 sm:gap-3">
      {LIFECYCLE_STAGES.map((s, i) => (
        <Fragment key={s.title}>
          <li className="flex min-w-0 items-center gap-2.5">
            <span
              className={`flex h-8 w-8 shrink-0 items-center justify-center rounded-full ring-1 ${s.badgeClass}`}
            >
              <s.icon className={`h-4 w-4 ${s.iconClass}`} />
            </span>
            <span className="min-w-0 leading-tight">
              <span className="block text-sm font-medium text-foreground">{s.title}</span>
              <span className="hidden truncate text-[11px] text-muted-foreground sm:block">
                {s.caption}
              </span>
            </span>
          </li>
          {i < LIFECYCLE_STAGES.length - 1 && (
            <li
              aria-hidden
              className="hidden h-px flex-1 bg-gradient-to-r from-border to-transparent sm:block"
            />
          )}
        </Fragment>
      ))}
    </ol>
  );
}

function KnowledgeAbout() {
  return (
    <>
      Everything the platform learns is a{" "}
      <strong className="font-medium text-foreground">Memory</strong>. Most memories are personal
      or operational and stay yours. When a memory asserts something true about the business or the
      data that others would benefit from, it becomes an{" "}
      <strong className="font-medium text-foreground">Insight</strong>, a proposal awaiting review.
      Whoever holds the <code className="rounded bg-muted px-1 py-0.5 text-xs">apply_knowledge</code>{" "}
      capability reviews insights and promotes the good ones into{" "}
      <strong className="font-medium text-foreground">Knowledge</strong>: shared, trusted, and
      canonical. Each promotion lands where it fits best, decided when it is applied: a fact tied to
      a specific dataset or column goes to the DataHub catalog, while broader business or domain
      knowledge becomes a knowledge page.
    </>
  );
}

/**
 * One intro per reader-facing section, in nav order.
 *
 * Settings is deliberately absent: it is controls, not a place things live.
 */
export const SECTION_INTROS: SectionIntroCopy[] = [
  {
    path: "/",
    summary: "Reports, dashboards, data exports and documents your agents and scripts produced.",
    about:
      "Every asset keeps its versions, so one an agent or a scheduled script rewrites can still be read as it stood. Assets are shared with people or by link, grouped into collections, and cited by other work.",
    storageKey: "portal.intro.assets",
  },
  {
    path: "/prompts",
    summary: "Formalized agent instructions for complex tasks.",
    about:
      "A prompt is written once and reused, so a task an agent performs often is performed the same way each time. Prompts take arguments, carry attachments, keep their version history, and are shared with other people.",
    storageKey: "portal.intro.prompts",
  },
  {
    path: "/scripts",
    summary: "Code an agent can write, run on demand or on a schedule.",
    about:
      "A script queries data, calls APIs, and writes assets and resources. Each run keeps its output and its history, and a script carries one object of state from one run to the next.",
    storageKey: "portal.intro.scripts",
  },
  {
    path: "/resources",
    summary:
      "Logos, templates and ad-hoc data stored as business resources, usable by agents and scripts.",
    about:
      "A place to upload the files your work depends on, and for scripts to add or update ad-hoc datasets such as CSVs. Reports, dashboards and data exports an agent produced are assets instead.",
    storageKey: "portal.intro.resources",
  },
  {
    path: "/scratch-tables",
    summary: "Spreadsheets that have become datasets, alongside your data warehouse.",
    about:
      "A registration points the query engine at a file already held in Assets or Resources. The table reads whatever that file holds now, and everyone granted its connection can use it, the same way they use the warehouse's own tables.",
    storageKey: "portal.intro.scratch-tables",
  },
  {
    path: "/feedback",
    summary: "Comments and corrections people left on assets, collections, prompts and knowledge pages.",
    about:
      "A thread hangs off the thing it is about, including work an agent or a script produced, and carries its status and its sign-off. A thread that settles on something true is captured as an insight.",
    storageKey: "portal.intro.feedback",
  },
  {
    path: "/knowledge",
    about: <KnowledgeAbout />,
    // The key predates this component: a reader who had already collapsed the
    // Knowledge lifecycle header does not get it back (#1570).
    storageKey: "knowledge.lifecycle.expanded",
    toggleLabel: "How it works",
    compact: <KnowledgeLifecycle />,
  },
  {
    path: "/apis",
    summary:
      "The configured and authenticated API endpoints agents reach through the platform.",
    about:
      "Every operation each connected API exposes, with its parameters, the shapes it returns, and the call an agent makes to invoke it.",
    storageKey: "portal.intro.apis",
  },
  {
    path: "/activity",
    summary: "Your sessions, the calls they made, and what each one produced.",
    about:
      "A session is read back long after it ended, as far as this deployment's audit history reaches. Each recorded call carries its statement, its stated purpose, its outcome, and what came of it.",
    storageKey: "portal.intro.activity",
  },
];

const BY_PATH = new Map(SECTION_INTROS.map((intro) => [intro.path, intro]));

export function sectionIntroFor(path: string): SectionIntroCopy | undefined {
  return BY_PATH.get(path);
}

/**
 * Routes that belong to a section they are not named after.
 */
const ALIASED_ROUTES: Record<string, string> = {
  "/collections": "/",
  "/knowledge/pages": "/knowledge",
  "/knowledge/catalog": "/knowledge",
  "/activity/sessions": "/activity",
  "/activity/calls": "/activity",
};

/** Sections whose listing surfaces address themselves by a path beneath them. */
const SECTION_PREFIXES: [string, string][] = [["/resources/lib/", "/resources"]];

/**
 * The section whose intro heads `route`, or null where none does.
 *
 * An intro heads a section's listing surfaces, not the detail pages beneath
 * them: a reader who opened one asset is past being told what an asset is, and
 * the space belongs to the thing they opened.
 */
export function sectionIntroPath(route: string): string | null {
  if (BY_PATH.has(route)) return route;
  const aliased = ALIASED_ROUTES[route];
  if (aliased) return aliased;
  for (const [prefix, section] of SECTION_PREFIXES) {
    if (route.startsWith(prefix)) return section;
  }
  return null;
}
