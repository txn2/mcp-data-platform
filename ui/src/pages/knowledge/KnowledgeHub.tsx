import { Fragment, useEffect, useState } from "react";
import {
  Search,
  Database,
  Lightbulb,
  BookOpen,
  ChevronRight,
  ChevronDown,
  X,
} from "lucide-react";
import { useSearch, MIN_SEARCH_LEN } from "@/api/portal/hooks";
import { useInsightStats } from "@/api/admin/hooks";
import { useAuthStore } from "@/stores/auth";
import type { SearchHit } from "@/api/portal/types";
import { formatEntityUrn } from "@/lib/formatEntityUrn";
import { entityHref } from "@/lib/entityRefs";
import { useDebounced } from "@/lib/useDebounced";
import { FilterChip } from "@/components/FilterChip";
import { KnowledgePagesPage } from "@/pages/knowledge-pages/KnowledgePagesPage";
import {
  MyKnowledgeSection,
  MyMemorySection,
} from "@/pages/knowledge/MyKnowledgePage";
import {
  KnowledgeCaptureTab,
  ChangesetsTab,
} from "@/pages/knowledge/KnowledgePage";

type Tab = "knowledge" | "insights" | "memory";

const TABS: { key: Tab; label: string }[] = [
  { key: "knowledge", label: "Knowledge" },
  { key: "insights", label: "Insights" },
  { key: "memory", label: "Memory" },
];

// The Knowledge tab is itself split into sub-tabs so federated search, the page
// browse, and changesets each get their own space and explanation rather than
// stacking on one screen.
type KnowledgeSubTab = "search" | "pages" | "changesets";

// The Insights tab splits your own captured insights from the reviewer queue.
type InsightSubTab = "mine" | "review";

function normalizeTab(raw?: string): Tab {
  return raw === "insights" || raw === "memory" ? raw : "knowledge";
}

// Human labels for the federated sources the unified search returns, so the
// grouped result set reads in product language rather than provider keys.
const SOURCE_LABELS: Record<string, string> = {
  datahub: "Catalog (DataHub)",
  knowledge_pages: "Knowledge pages",
  memory: "Memory",
  insights: "Insights",
  assets: "Assets",
  prompts: "Prompts",
  endpoints: "API endpoints",
  connections: "Connections",
};

function sourceLabel(source: string): string {
  return SOURCE_LABELS[source] ?? source;
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

const LIFECYCLE_STORE_KEY = "knowledge.lifecycle.expanded";

// LifecycleHeader teaches the Memory to Insight to Knowledge model. The connected
// pipeline (always visible) is the elegant at-a-glance summary; the full prose is
// progressively disclosed and the open/closed choice persists across visits, so
// it teaches a first-timer once without nagging a returning user.
function LifecycleHeader() {
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

// Sources the hub can open to a detail surface, and the action label. Sources
// absent here (datahub, endpoints, connections) have no portal viewer, so their
// drawer shows metadata only.
const OPEN_ACTIONS: Record<string, string> = {
  assets: "Open asset",
  prompts: "Open prompt",
  knowledge_pages: "Open page",
  memory: "View in Memory",
  insights: "View in Insights",
};

function HitRow({ hit, onClick }: { hit: SearchHit; onClick: () => void }) {
  return (
    <button
      type="button"
      onClick={onClick}
      className="flex w-full items-start gap-2 rounded-md border bg-card p-3 text-left transition-colors hover:border-primary/40 hover:bg-muted/40"
    >
      <div className="min-w-0 flex-1 space-y-1">
        <div className="flex items-start justify-between gap-2">
          <p className="text-sm">{hit.text}</p>
          {hit.status && (
            <span className="shrink-0 rounded-full bg-muted px-2 py-0.5 text-[11px] text-muted-foreground">
              {hit.status}
            </span>
          )}
        </div>
        <div className="flex flex-wrap items-center gap-2 text-[11px] text-muted-foreground">
          <span className="max-w-[18rem] truncate font-mono" title={hit.ref}>
            {hit.ref}
          </span>
          {(hit.entity_urns ?? []).slice(0, 2).map((urn) => (
            <span key={urn} title={urn} className="rounded bg-muted px-1.5 py-0.5 font-mono">
              {formatEntityUrn(urn)}
            </span>
          ))}
        </div>
      </div>
      <ChevronRight className="mt-0.5 h-4 w-4 shrink-0 text-muted-foreground" />
    </button>
  );
}

// HitDetailDrawer shows a result's metadata in a right slide-over and, when the
// source has a portal surface, a button to open the full item.
function HitDetailDrawer({
  hit,
  onClose,
  onOpen,
}: {
  hit: SearchHit;
  onClose: () => void;
  onOpen: (hit: SearchHit) => void;
}) {
  const openLabel = OPEN_ACTIONS[hit.source];
  // Escape closes the drawer, so a keyboard user is not trapped after opening it
  // from a result row.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);
  return (
    <>
      <div className="fixed inset-0 z-40 bg-black/40" onClick={onClose} />
      <div className="fixed inset-y-0 right-0 z-50 flex w-full max-w-md flex-col border-l bg-card shadow-xl">
        <div className="flex items-center justify-between border-b px-4 py-3">
          <span className="text-xs font-medium uppercase tracking-wide text-muted-foreground">
            {sourceLabel(hit.source)}
          </span>
          <button
            onClick={onClose}
            aria-label="Close"
            className="rounded p-1 text-muted-foreground hover:bg-muted"
          >
            <X className="h-4 w-4" />
          </button>
        </div>
        <div className="flex-1 space-y-4 overflow-auto p-4">
          <h3 className="text-base font-semibold">{hit.text}</h3>
          <dl className="space-y-3 text-sm">
            <div>
              <dt className="text-xs text-muted-foreground">Reference</dt>
              <dd className="break-all font-mono text-xs">{hit.ref}</dd>
            </div>
            {hit.status && (
              <div>
                <dt className="text-xs text-muted-foreground">Status</dt>
                <dd>{hit.status}</dd>
              </div>
            )}
            {hit.dimension && (
              <div>
                <dt className="text-xs text-muted-foreground">Category</dt>
                <dd>{hit.dimension}</dd>
              </div>
            )}
            {(hit.entity_urns?.length ?? 0) > 0 && (
              <div>
                <dt className="text-xs text-muted-foreground">Linked entities</dt>
                <dd className="flex flex-wrap gap-1">
                  {hit.entity_urns!.map((urn) => (
                    <span
                      key={urn}
                      title={urn}
                      className="rounded bg-muted px-1.5 py-0.5 font-mono text-xs"
                    >
                      {formatEntityUrn(urn)}
                    </span>
                  ))}
                </dd>
              </div>
            )}
          </dl>
          {!openLabel && (
            <p className="text-xs text-muted-foreground">
              {hit.source === "datahub"
                ? "This knowledge lives on the entity in the DataHub catalog."
                : "This result does not have a detail page in the portal."}
            </p>
          )}
        </div>
        {openLabel && (
          <div className="border-t p-4">
            <button
              onClick={() => onOpen(hit)}
              className="inline-flex w-full items-center justify-center gap-1.5 rounded-md bg-primary px-3 py-2 text-sm font-medium text-primary-foreground hover:opacity-90"
            >
              {openLabel}
            </button>
          </div>
        )}
      </div>
    </>
  );
}

// UnifiedSearch fans one query across every source the caller can access and
// renders the result grouped by source with a coverage summary, a source
// filter, and a detail drawer per result.
function UnifiedSearch({ onOpen }: { onOpen: (hit: SearchHit) => void }) {
  const [input, setInput] = useState("");
  const [selectedSource, setSelectedSource] = useState("");
  const [allSources, setAllSources] = useState<string[]>([]);
  const [selectedHit, setSelectedHit] = useState<SearchHit | null>(null);
  const query = useDebounced(input, 300);
  // Search activates only at the minimum query length, matching the hook gate,
  // so a single character neither queries the server nor flips the UI into a
  // "no results" state.
  const active = query.trim().length >= MIN_SEARCH_LEN;
  const { data, isLoading, isError } = useSearch(query, {
    sources: selectedSource ? [selectedSource] : undefined,
  });

  // A new query starts unfiltered and rebuilds its own source facet, so chips
  // never leak from a previous, unrelated query. (data is undefined while the
  // new query loads, so the accumulation effect below cannot re-add stale
  // sources before fresh results arrive.)
  useEffect(() => {
    setSelectedSource("");
    setAllSources([]);
  }, [query]);

  // Remember the full source set from this query's unfiltered results so the
  // filter chips do not collapse to just the selected source when a filter is
  // applied (filtered coverage reports only the selected source).
  useEffect(() => {
    if (selectedSource === "" && data?.coverage) {
      setAllSources((prev) => {
        const merged = new Set(prev);
        for (const c of data.coverage) merged.add(c.source);
        return [...merged];
      });
    }
  }, [data, selectedSource]);

  const coverageFor = (source: string) =>
    data?.coverage.find((c) => c.source === source);

  return (
    <div className="space-y-4">
      <div className="relative">
        <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
        <input
          type="text"
          value={input}
          onChange={(e) => setInput(e.target.value)}
          placeholder="Search all knowledge: catalog, knowledge pages, memory, insights, assets, prompts..."
          className="w-full rounded-md border bg-background pl-9 pr-3 py-2 text-sm outline-none ring-ring focus:ring-2"
          aria-label="Search all knowledge"
        />
      </div>

      {active && allSources.length > 1 && (
        <div className="flex flex-wrap items-center gap-1.5">
          <FilterChip
            label="All sources"
            active={selectedSource === ""}
            onClick={() => setSelectedSource("")}
          />
          {allSources.map((s) => (
            <FilterChip
              key={s}
              label={sourceLabel(s)}
              active={selectedSource === s}
              onClick={() => setSelectedSource(s)}
            />
          ))}
        </div>
      )}

      {active && (
        <div className="space-y-4">
          {isLoading && (
            <p className="text-sm text-muted-foreground">Searching...</p>
          )}
          {isError && (
            <p className="text-sm text-muted-foreground">
              Search is unavailable right now.
            </p>
          )}
          {data && data.count === 0 && (
            <p className="py-8 text-center text-sm text-muted-foreground">
              Nothing matched &quot;{query.trim()}&quot;.
            </p>
          )}
          {data?.groups.map((group) => {
            const cov = coverageFor(group.source);
            return (
              <div key={group.source} className="space-y-2">
                <div className="flex items-baseline justify-between">
                  <h3 className="text-sm font-semibold">{sourceLabel(group.source)}</h3>
                  {cov && cov.matched > cov.shown && (
                    <span className="text-[11px] text-muted-foreground">
                      {cov.shown} of {cov.matched} shown
                    </span>
                  )}
                </div>
                <div className="space-y-2">
                  {group.hits.map((hit) => (
                    <HitRow
                      key={`${hit.source}:${hit.ref}`}
                      hit={hit}
                      onClick={() => setSelectedHit(hit)}
                    />
                  ))}
                </div>
              </div>
            );
          })}
        </div>
      )}

      {selectedHit && (
        <HitDetailDrawer
          hit={selectedHit}
          onClose={() => setSelectedHit(null)}
          onOpen={(h) => {
            setSelectedHit(null);
            onOpen(h);
          }}
        />
      )}
    </div>
  );
}

// SubTabBar renders the secondary navigation inside the Knowledge and Insights
// tabs as a segmented (pill) control, visually subordinate to the primary
// underline tabs above it so the two levels read as a hierarchy rather than two
// identical bars. An optional badge surfaces a count (e.g. pending reviews).
function SubTabBar<T extends string>({
  tabs,
  active,
  onSelect,
}: {
  tabs: { key: T; label: string; badge?: number }[];
  active: T;
  onSelect: (key: T) => void;
}) {
  return (
    <div className="inline-flex flex-wrap items-center gap-1 rounded-lg border bg-muted/40 p-1">
      {tabs.map((t) => {
        const isActive = active === t.key;
        return (
          <button
            key={t.key}
            onClick={() => onSelect(t.key)}
            className={`flex items-center gap-2 rounded-md px-3 py-1.5 text-sm font-medium transition-colors ${
              isActive
                ? "bg-background text-foreground shadow-sm"
                : "text-muted-foreground hover:text-foreground"
            }`}
          >
            {t.label}
            {t.badge != null && t.badge > 0 && (
              <span
                className={`rounded-full px-1.5 text-[11px] font-semibold ${
                  isActive
                    ? "bg-primary/15 text-primary"
                    : "bg-muted-foreground/15 text-muted-foreground"
                }`}
              >
                {t.badge}
              </span>
            )}
          </button>
        );
      })}
    </div>
  );
}

/**
 * KnowledgeHub is the single home for the Memory to Insight to Knowledge
 * lifecycle (#661). It merges the former /knowledge-pages, /my-knowledge, and
 * /admin/knowledge surfaces into three capability-gated tabs:
 *
 *   - Knowledge (default): three sub-tabs - Search All (federated search),
 *     Knowledge Pages (browse internal pages), and Changesets (apply audit,
 *     reviewer-only).
 *   - Insights: your captured insights, and for apply_knowledge holders the
 *     full review queue and changesets.
 *   - Memory: your raw memory substrate classified by sink_class, and for
 *     apply_knowledge holders every user's memory.
 *
 * Review and promote affordances gate on the apply_knowledge capability, never
 * on an admin role.
 */
export function KnowledgeHub({
  initialTab,
  initialPageId,
  pagesSubActive,
  onNavigate,
}: {
  initialTab?: string;
  // The knowledge page open in detail, from the /knowledge/pages/:id route (#709).
  initialPageId?: string;
  // True when the route is /knowledge/pages or /knowledge/pages/:id, so the
  // Knowledge Pages sub-tab is the active, URL-addressable view.
  pagesSubActive?: boolean;
  onNavigate?: (path: string) => void;
}) {
  // On a pages route the top tab is always Knowledge; otherwise it comes from the
  // URL hash. The pages sub-tab is URL-driven (pagesSubActive), so it is never
  // stored in knowledgeSub, which only ever holds search or changesets.
  const [tab, setTab] = useState<Tab>(() => (pagesSubActive ? "knowledge" : normalizeTab(initialTab)));
  // The pages sub-tab is URL-driven (a /knowledge/pages route); the in-page sub-tabs
  // can be carried in the hash (e.g. /knowledge#changesets) so leaving the pages
  // route opens the chosen one directly rather than defaulting to Search (#709).
  const [knowledgeSub, setKnowledgeSub] = useState<KnowledgeSubTab>(() =>
    initialTab === "changesets" || initialTab === "search" ? initialTab : "search",
  );
  const [insightSub, setInsightSub] = useState<InsightSubTab>("mine");
  // Review and promote affordances gate on the apply_knowledge capability (not
  // an admin role), or admin. This mirrors the REST handler's userHasToolAccess:
  // the capability grants non-admins, and admins are allowed too since the tool
  // may be unregistered on a deployment.
  const canApply = useAuthStore(
    (s) => (s.user?.tools?.includes("apply_knowledge") ?? false) || s.isAdmin(),
  );
  const isAdmin = useAuthStore((s) => s.isAdmin());

  // Pending-review cue: the team-wide pending count comes from the admin-scoped
  // insight-stats endpoint, so the fetch is gated on isAdmin to avoid a 401 poll
  // for a non-admin reviewer (whose team queue is, today, also admin-gated; see
  // #662). The badge shows the count to admins; other users see no number.
  const insightStats = useInsightStats({ enabled: isAdmin });
  const pendingReviews = isAdmin ? (insightStats.data?.total_pending ?? 0) : 0;

  // Knowledge sub-tabs. Changesets is reviewer-only (it is the apply audit).
  const knowledgeSubTabs: {
    key: KnowledgeSubTab;
    label: string;
    description: string;
  }[] = [
    {
      key: "search",
      label: "Search All",
      description:
        "The same discovery your agent uses to find what the platform already knows, surfaced here for you to audit, review, and reference. One query fans across every source it can access (the DataHub catalog, knowledge pages, memory, captured insights, saved assets, prompts, API endpoints, and connections), grouped by source. It ranks semantically when an embedding provider is configured and falls back to keyword search otherwise.",
    },
    {
      key: "pages",
      label: "Knowledge Pages",
      description:
        "Canonical business and domain knowledge, written as markdown and stored in the portal. Pages are one of the two knowledge sinks; technical and entity knowledge lives in the DataHub catalog instead (reach it from Search All). Holders of apply_knowledge can create, edit, and remove pages.",
    },
    ...(canApply
      ? [
          {
            key: "changesets" as const,
            label: "Changesets",
            description:
              "The record of insights promoted into knowledge: the catalog and knowledge-page changes applied when your agent runs apply_knowledge. Roll back a changeset to undo its writes.",
          },
        ]
      : []),
  ];
  // The pages sub-tab is selected by the route; the others by in-page state.
  const activeSub: KnowledgeSubTab = pagesSubActive
    ? "pages"
    : knowledgeSubTabs.some((s) => s.key === knowledgeSub)
      ? knowledgeSub
      : "search";
  const activeSubMeta = knowledgeSubTabs.find((s) => s.key === activeSub)!;

  // Insights sub-tabs. The review queue is reviewer-only and carries the
  // pending-review count.
  const insightSubTabs: {
    key: InsightSubTab;
    label: string;
    description: string;
    badge?: number;
  }[] = [
    {
      key: "mine",
      label: "My Insights",
      description:
        "The insights captured from your sessions, with their review status. An insight is a memory worth sharing with your team; it stays a proposal until it is reviewed and promoted into knowledge.",
    },
    ...(canApply
      ? [
          {
            key: "review" as const,
            label: "Review queue",
            badge: pendingReviews,
            description:
              "Your account has permission to let your agent Apply Knowledge, which promotes your team's insights into team-wide, durable knowledge (knowledge pages and the DataHub catalog). Review what your team has captured: approve the insights worth promoting and reject the rest, so when you ask your agent to apply knowledge it works from a curated set.",
          },
        ]
      : []),
  ];
  const activeInsightSub = insightSubTabs.some((s) => s.key === insightSub)
    ? insightSub
    : "mine";
  const insightSubMeta = insightSubTabs.find((s) => s.key === activeInsightSub)!;

  // Reflect the active tab in the URL hash so the view is deep-linkable and
  // survives a refresh, without forcing a full navigation. On the URL-addressable
  // pages route (#709), switching the top tab must leave that path, so navigate
  // rather than only rewriting the hash; staying on Knowledge keeps the route.
  const selectTab = (next: Tab) => {
    if (pagesSubActive) {
      if (next !== "knowledge") onNavigate?.(`/knowledge#${next}`);
      return;
    }
    setTab(next);
    window.history.replaceState(null, "", `#${next}`);
  };

  // Knowledge Pages is the one URL-addressable sub-tab (#709): selecting it routes
  // to /knowledge/pages so page-detail deep-links and browser back/forward work.
  // The other sub-tabs are in-page state under the bare /knowledge route, so
  // leaving the pages route for one navigates back to /knowledge.
  const selectKnowledgeSub = (next: KnowledgeSubTab) => {
    if (next === "pages") {
      onNavigate?.("/knowledge/pages");
      return;
    }
    if (pagesSubActive) {
      // Leaving the pages route for an in-page sub-tab: carry the target in the
      // hash so the remount opens it in one click. A bare /knowledge would reset
      // to Search, so selecting Changesets here would otherwise cost two clicks.
      onNavigate?.(`/knowledge#${next}`);
      return;
    }
    setKnowledgeSub(next);
  };

  // Open a search result in its native surface: assets and prompts deep-link to
  // their portal viewers; knowledge pages open in the Knowledge Pages sub-tab;
  // memory and insights switch to their tabs. Catalog/endpoint/connection hits
  // have no portal viewer (the drawer shows their metadata only).
  const openHit = (hit: SearchHit) => {
    switch (hit.source) {
      case "assets":
        onNavigate?.(`/assets/${hit.ref}`);
        break;
      case "prompts":
        onNavigate?.(`/prompts/${hit.ref}`);
        break;
      case "knowledge_pages": {
        // Deep-link to the page's own URL so a search result opens the same
        // shareable detail route as any other reference, through the shared
        // entityHref builder so its safe-id guard applies here too (#709).
        const href = entityHref("knowledge_page", hit.ref);
        if (href) onNavigate?.(href);
        break;
      }
      case "memory":
        selectTab("memory");
        break;
      case "insights":
        selectTab("insights");
        break;
      default:
        break;
    }
  };

  return (
    <div className="space-y-6">
      <LifecycleHeader />

      {/* Tab bar */}
      <div className="flex gap-1 border-b">
        {TABS.map((t) => (
          <button
            key={t.key}
            onClick={() => selectTab(t.key)}
            className={`flex items-center gap-2 px-4 py-2 text-sm font-medium transition-colors ${
              tab === t.key
                ? "border-b-2 border-primary text-primary"
                : "text-muted-foreground hover:text-foreground"
            }`}
          >
            {t.label}
            {t.key === "insights" && pendingReviews > 0 && (
              <span
                className="rounded-full bg-primary/15 px-1.5 text-[11px] font-semibold text-primary"
                aria-label={`${pendingReviews} insights awaiting review`}
              >
                {pendingReviews}
              </span>
            )}
          </button>
        ))}
      </div>

      {tab === "knowledge" && (
        <div className="space-y-4">
          <SubTabBar
            tabs={knowledgeSubTabs}
            active={activeSub}
            onSelect={selectKnowledgeSub}
          />
          <p className="text-sm text-muted-foreground">
            {activeSubMeta.description}
          </p>

          {activeSub === "search" && <UnifiedSearch onOpen={openHit} />}
          {activeSub === "pages" && (
            <KnowledgePagesPage openPageId={initialPageId} onNavigate={onNavigate} />
          )}
          {/* Changesets live under Knowledge (the promoted layer), not Insights:
              a changeset is created only at apply time and records what was
              written, so it belongs with the knowledge it produced. */}
          {activeSub === "changesets" && <ChangesetsTab />}
        </div>
      )}

      {tab === "insights" && (
        <div className="space-y-4">
          <SubTabBar
            tabs={insightSubTabs}
            active={activeInsightSub}
            onSelect={setInsightSub}
          />
          <p className="text-sm text-muted-foreground">
            {insightSubMeta.description}
          </p>

          {activeInsightSub === "mine" && <MyKnowledgeSection />}
          {activeInsightSub === "review" && <KnowledgeCaptureTab />}
        </div>
      )}

      {tab === "memory" && (
        <div className="space-y-6">
          {/* Memory is personal. The only memory that crosses to other users is
              an insight (reviewed in the Insights tab), so this tab is scoped to
              the caller's own records. */}
          <MyMemorySection />
        </div>
      )}
    </div>
  );
}
