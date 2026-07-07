import { Fragment, useState } from "react";
import { Database, Lightbulb, BookOpen, ChevronDown } from "lucide-react";

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

const LIFECYCLE_STORE_KEY = "knowledge.lifecycle.expanded";

// LifecycleHeader teaches the Memory to Insight to Knowledge model. The connected
// pipeline (always visible) is the elegant at-a-glance summary; the full prose is
// progressively disclosed and the open/closed choice persists across visits, so
// it teaches a first-timer once without nagging a returning user.
export function LifecycleHeader() {
  const [expanded, setExpanded] = useState(() => {
    try {
      return localStorage.getItem(LIFECYCLE_STORE_KEY) === "1";
    } catch {
      return false;
    }
  });

  const toggle = () =>
    setExpanded((open) => {
      const next = !open;
      try {
        localStorage.setItem(LIFECYCLE_STORE_KEY, next ? "1" : "0");
      } catch {
        /* private mode: just don't persist */
      }
      return next;
    });

  return (
    <div className="overflow-hidden rounded-xl border bg-card">
      <div className="flex items-center gap-3 px-4 py-3">
        {/* The lifecycle pipeline: nodes joined by a fading rail so the three
            stages read as one progression rather than three separate chips. */}
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

        <button
          type="button"
          onClick={toggle}
          aria-expanded={expanded}
          className="flex shrink-0 items-center gap-1 rounded-md px-2 py-1 text-xs font-medium text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
        >
          <span className="hidden sm:inline">{expanded ? "Hide" : "How it works"}</span>
          <ChevronDown
            className={`h-4 w-4 transition-transform duration-200 ${expanded ? "rotate-180" : ""}`}
          />
        </button>
      </div>

      {/* Grid-rows 0fr/1fr animates to content height with no max-height guess. */}
      <div
        className={`grid transition-all duration-300 ease-out ${
          expanded ? "grid-rows-[1fr] opacity-100" : "grid-rows-[0fr] opacity-0"
        }`}
      >
        <div className="overflow-hidden">
          <p className="border-t px-4 py-3 text-sm leading-relaxed text-muted-foreground">
            Everything the platform learns is a <strong className="font-medium text-foreground">Memory</strong>.
            Most memories are personal or operational and stay yours. When a memory asserts something
            true about the business or the data that others would benefit from, it becomes an{" "}
            <strong className="font-medium text-foreground">Insight</strong>, a proposal awaiting review.
            Whoever holds the <code className="rounded bg-muted px-1 py-0.5 text-xs">apply_knowledge</code>{" "}
            capability reviews insights and promotes the good ones into{" "}
            <strong className="font-medium text-foreground">Knowledge</strong>: shared, trusted, and
            canonical. Each promotion lands where it fits best, decided when it is applied: a fact tied
            to a specific dataset or column goes to the DataHub catalog, while broader business or domain
            knowledge becomes a knowledge page. This is the substrate your agents draw on; it is surfaced
            here so you can audit, review, give feedback, and use it as a shared knowledgebase.
          </p>
        </div>
      </div>
    </div>
  );
}
