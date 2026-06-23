import { useEffect, useState } from "react";
import {
  Search,
  Database,
  Lightbulb,
  BookOpen,
  ChevronRight,
} from "lucide-react";
import { useSearch } from "@/api/portal/hooks";
import { useAuthStore } from "@/stores/auth";
import type { SearchHit } from "@/api/portal/types";
import { formatEntityUrn } from "@/lib/formatEntityUrn";
import { useDebounced } from "@/lib/useDebounced";
import { KnowledgePagesPage } from "@/pages/knowledge-pages/KnowledgePagesPage";
import {
  MyKnowledgeSection,
  MyMemorySection,
} from "@/pages/knowledge/MyKnowledgePage";
import {
  KnowledgeCaptureTab,
  ChangesetsTab,
  AllMemoryTab,
} from "@/pages/knowledge/KnowledgePage";

type Tab = "knowledge" | "insights" | "memory";

const TABS: { key: Tab; label: string }[] = [
  { key: "knowledge", label: "Knowledge" },
  { key: "insights", label: "Insights" },
  { key: "memory", label: "Memory" },
];

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

// LifecycleHeader teaches the Memory to Insight to Knowledge model so a
// first-time reader can state what each is and how one becomes the next. The
// stages render left to right even though the tabs lead with the payoff.
function LifecycleHeader() {
  const stages = [
    { icon: Database, title: "Memory", caption: "captured automatically" },
    { icon: Lightbulb, title: "Insight", caption: "proposed for review" },
    { icon: BookOpen, title: "Knowledge", caption: "promoted, shared, canonical" },
  ];
  return (
    <div className="rounded-lg border bg-card p-4">
      <div className="flex flex-wrap items-center gap-2">
        {stages.map((s, i) => (
          <div key={s.title} className="flex items-center gap-2">
            <div className="flex items-center gap-2 rounded-md bg-muted/60 px-3 py-1.5">
              <s.icon className="h-4 w-4 text-primary" />
              <div className="leading-tight">
                <div className="text-sm font-medium">{s.title}</div>
                <div className="text-[11px] text-muted-foreground">{s.caption}</div>
              </div>
            </div>
            {i < stages.length - 1 && (
              <ChevronRight className="h-4 w-4 shrink-0 text-muted-foreground" />
            )}
          </div>
        ))}
      </div>
      <p className="mt-3 text-sm text-muted-foreground">
        Everything the platform learns is a <strong>Memory</strong>. Most
        memories are personal or operational and stay yours. When a memory
        asserts something true about the business or the data that others would
        benefit from, it becomes an <strong>Insight</strong>, a proposal awaiting
        review. Whoever holds the <code className="text-xs">apply_knowledge</code>{" "}
        capability reviews insights and promotes the good ones into{" "}
        <strong>Knowledge</strong>: shared, trusted, and canonical. Business and
        domain facts become knowledge pages; technical and entity facts go to the
        DataHub catalog.
      </p>
    </div>
  );
}

function HitRow({ hit }: { hit: SearchHit }) {
  return (
    <div className="rounded-md border bg-card p-3">
      <div className="flex items-start justify-between gap-2">
        <p className="text-sm">{hit.text}</p>
        {hit.status && (
          <span className="shrink-0 rounded-full bg-muted px-2 py-0.5 text-[11px] text-muted-foreground">
            {hit.status}
          </span>
        )}
      </div>
      <div className="mt-1 flex flex-wrap items-center gap-2 text-[11px] text-muted-foreground">
        <span className="font-mono" title={hit.ref}>
          {hit.ref}
        </span>
        {(hit.entity_urns ?? []).slice(0, 3).map((urn) => (
          <span key={urn} title={urn} className="rounded bg-muted px-1.5 py-0.5 font-mono">
            {formatEntityUrn(urn)}
          </span>
        ))}
      </div>
    </div>
  );
}

// UnifiedSearch fans one query across every source the caller can access and
// renders the result grouped by source with a coverage summary. An empty query
// renders nothing, so the Knowledge tab falls back to browsing knowledge pages.
function UnifiedSearch({ onActiveChange }: { onActiveChange: (active: boolean) => void }) {
  const [input, setInput] = useState("");
  const query = useDebounced(input, 300);
  const active = query.trim().length > 0;
  const { data, isLoading, isError } = useSearch(query);

  useEffect(() => {
    onActiveChange(active);
  }, [active, onActiveChange]);

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
                    <HitRow key={`${hit.source}:${hit.ref}`} hit={hit} />
                  ))}
                </div>
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}

function SectionDivider({ title, subtitle }: { title: string; subtitle?: string }) {
  return (
    <div className="border-t pt-6">
      <h2 className="text-base font-semibold">{title}</h2>
      {subtitle && <p className="mt-1 text-sm text-muted-foreground">{subtitle}</p>}
    </div>
  );
}

/**
 * KnowledgeHub is the single home for the Memory to Insight to Knowledge
 * lifecycle (#661). It merges the former /knowledge-pages, /my-knowledge, and
 * /admin/knowledge surfaces into three capability-gated tabs:
 *
 *   - Knowledge (default): unified search across every source, grouped by
 *     source, plus browse of canonical knowledge pages.
 *   - Insights: your captured insights, and for apply_knowledge holders the
 *     full review queue and changesets.
 *   - Memory: your raw memory substrate classified by sink_class, and for
 *     apply_knowledge holders every user's memory.
 *
 * Review and promote affordances gate on the apply_knowledge capability, never
 * on an admin role.
 */
export function KnowledgeHub({ initialTab }: { initialTab?: string }) {
  const [tab, setTab] = useState<Tab>(() => normalizeTab(initialTab));
  const [searchActive, setSearchActive] = useState(false);
  // Review and promote affordances gate on the apply_knowledge capability (not
  // an admin role), or admin. This mirrors the REST handler's userHasToolAccess:
  // the capability grants non-admins, and admins are allowed too since the tool
  // may be unregistered on a deployment.
  const canApply = useAuthStore(
    (s) => (s.user?.tools?.includes("apply_knowledge") ?? false) || s.isAdmin(),
  );

  // Reflect the active tab in the URL hash so the view is deep-linkable and
  // survives a refresh, without forcing a full navigation.
  const selectTab = (next: Tab) => {
    setTab(next);
    window.history.replaceState(null, "", `#${next}`);
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
            className={`px-4 py-2 text-sm font-medium transition-colors ${
              tab === t.key
                ? "border-b-2 border-primary text-primary"
                : "text-muted-foreground hover:text-foreground"
            }`}
          >
            {t.label}
          </button>
        ))}
      </div>

      {tab === "knowledge" && (
        <div className="space-y-6">
          <UnifiedSearch onActiveChange={setSearchActive} />
          {/* Empty query falls back to browsing canonical knowledge pages. */}
          {!searchActive && <KnowledgePagesPage />}
        </div>
      )}

      {tab === "insights" && (
        <div className="space-y-6">
          <MyKnowledgeSection />
          {canApply && (
            <>
              <SectionDivider
                title="Review queue"
                subtitle="Insights captured across all users. Approve to promote into knowledge, or reject."
              />
              <KnowledgeCaptureTab />
              <SectionDivider
                title="Changesets"
                subtitle="Catalog changes applied from approved knowledge. Roll back if needed."
              />
              <ChangesetsTab />
            </>
          )}
        </div>
      )}

      {tab === "memory" && (
        <div className="space-y-6">
          <MyMemorySection />
          {canApply && (
            <>
              <SectionDivider
                title="All memory"
                subtitle="Every memory record across all users, classified by lifecycle class."
              />
              <AllMemoryTab />
            </>
          )}
        </div>
      )}
    </div>
  );
}
