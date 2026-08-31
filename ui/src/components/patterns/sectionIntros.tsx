import { Fragment, type ReactNode } from "react";
import { Database, Lightbulb, BookOpen } from "lucide-react";

/**
 * What one portal section says about itself.
 *
 * The name and the icon are not here: they come from the section's own nav
 * entry (portalNavItems), so the header a reader sees and the item they
 * clicked cannot disagree. SECTION_INTRO_COVERAGE fails when a reader-facing
 * nav entry has no entry in this table.
 *
 * The wording is drawn from docs/concepts/content-model.md and each section's
 * own doc page. It is what a person is told; RESOURCE_POSITIONING
 * (lib/positioning.ts) is what the agent is told. They agree because both are
 * drawn from that document, not because one renders the other.
 */
export interface SectionIntroCopy {
  /** The section's nav path, and the key this table is read by. */
  path: string;
  /** The one line a returning reader sees, next to the section's icon. */
  summary: string;
  /** What belongs in this section. */
  belongs: string;
  /** What does not, and the section it goes to instead. */
  notHere: string;
  /** localStorage key holding this section's expanded/collapsed choice. */
  storageKey: string;
  /** Disclosure button label when collapsed. Defaults to "What is this?". */
  toggleLabel?: string;
  /**
   * Drawn in place of the icon/name/summary row, for a section whose summary
   * is a picture. One line tall, like the row it replaces.
   */
  compact?: ReactNode;
  /** Rendered above the belongs/not-here lines when the intro is expanded. */
  detail?: ReactNode;
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

function KnowledgeDetail() {
  return (
    <p className="text-sm leading-relaxed text-muted-foreground">
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
    </p>
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
    summary: "What your agents and scripts produced, kept to share.",
    belongs:
      "Reports, dashboards, exports and documents an agent or a script made during a session or a run.",
    notHere: "A file you uploaded for an agent to work from. That is a Resource.",
    storageKey: "portal.intro.assets",
  },
  {
    path: "/prompts",
    summary: "Reusable instructions for a job an agent does often.",
    belongs: "Templates for procedures you want run the same way every time.",
    notHere: "Facts you want an agent to look up. Those are Knowledge pages.",
    storageKey: "portal.intro.prompts",
  },
  {
    path: "/scripts",
    summary: "Jobs the platform runs for you, on demand or on a schedule.",
    belongs: "Saved jobs that query data, call APIs, and write assets or resources.",
    notHere: "One-off work. Ask an agent directly.",
    storageKey: "portal.intro.scripts",
  },
  {
    path: "/resources",
    summary: "Files you give agents and scripts to work from.",
    belongs:
      "Brand material, spreadsheets and CSV data, templates and reference documents. Uploaded by you, or written by a script.",
    notHere: "Something an agent produced. That is an Asset.",
    storageKey: "portal.intro.resources",
  },
  {
    path: "/scratch-tables",
    summary: "CSV files registered so you can query them with SQL.",
    belongs:
      "Tables built over an asset or a resource, with the name to write in a FROM clause. A table reads whatever its file holds now, and anyone granted its connection can query it.",
    notHere:
      "The file itself, and registering a new table. Both stay on the file's own page in Assets or Resources.",
    storageKey: "portal.intro.scratch-tables",
  },
  {
    path: "/feedback",
    summary: "Comments and corrections people left on your work.",
    belongs:
      "Questions, corrections and approvals on an asset, collection, prompt or knowledge page, and threads in the shared channel.",
    notHere:
      "A change you want made. Say it on the asset itself, so the person who owns it sees it in context.",
    storageKey: "portal.intro.feedback",
  },
  {
    path: "/knowledge",
    summary: "Facts worth keeping, that agents search and cite.",
    belongs:
      "Reviewed business and domain facts, and the memories and insights they are promoted from.",
    notHere: "A document you want reproduced word for word. That is a Resource.",
    // The key predates this component: a reader who had already collapsed the
    // Knowledge lifecycle header does not get it back (#1570).
    storageKey: "knowledge.lifecycle.expanded",
    toggleLabel: "How it works",
    compact: <KnowledgeLifecycle />,
    detail: <KnowledgeDetail />,
  },
  {
    path: "/apis",
    summary: "The API operations agents can call here.",
    belongs: "The operations of every connected API, with the call each one produces.",
    notHere: "Adding an API. An administrator does that under Connections and API Catalogs.",
    storageKey: "portal.intro.apis",
  },
  {
    path: "/activity",
    summary: "What you and your agents have been doing.",
    belongs: "Your sessions, the calls they made, and what each one was for.",
    notHere: "Everyone's activity. That is the admin Sessions page.",
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
