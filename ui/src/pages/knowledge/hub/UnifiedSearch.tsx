import { useEffect, useState } from "react";
import { Search, ChevronRight, X, Lock } from "lucide-react";
import { useSearch, MIN_SEARCH_LEN } from "@/api/portal/hooks";
import type { SearchHit } from "@/api/portal/types";
import { formatEntityUrn } from "@/lib/formatEntityUrn";
import { useDebounced } from "@/lib/useDebounced";
import { FilterChip } from "@/components/FilterChip";

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

// WithheldBanner explains results the caller's persona hid because they belong
// to connections it is not granted. The copy comes from the server so the portal
// and the MCP search tool say the same thing; rendering it above the groups is
// deliberate, since a source can be filtered down to nothing and then has no
// group of its own to carry the count.
function WithheldBanner({ notice }: { notice: string }) {
  return (
    <div className="flex items-start gap-2 rounded-md border border-amber-500/30 bg-amber-500/10 p-3">
      <Lock className="mt-0.5 h-4 w-4 shrink-0 text-amber-600 dark:text-amber-400" />
      <p className="text-xs leading-relaxed text-amber-700 dark:text-amber-300">{notice}</p>
    </div>
  );
}

// UnifiedSearch fans one query across every source the caller can access and
// renders the result grouped by source with a coverage summary, a source
// filter, and a detail drawer per result.
export function UnifiedSearch({ onOpen }: { onOpen: (hit: SearchHit) => void }) {
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
          {data?.withheld_notice && <WithheldBanner notice={data.withheld_notice} />}
          {data && data.count === 0 && !data.withheld_notice && (
            <p className="py-8 text-center text-sm text-muted-foreground">
              Nothing matched &quot;{query.trim()}&quot;.
            </p>
          )}
          {data?.groups.map((group) => {
            const cov = coverageFor(group.source);
            const withheld = cov?.withheld ?? 0;
            return (
              <div key={group.source} className="space-y-2">
                <div className="flex items-baseline justify-between">
                  <h3 className="text-sm font-semibold">{sourceLabel(group.source)}</h3>
                  <span className="flex items-center gap-2 text-[11px] text-muted-foreground">
                    {cov && cov.matched > cov.shown && (
                      <span>
                        {cov.shown} of {cov.matched} shown
                      </span>
                    )}
                    {withheld > 0 && (
                      <span className="text-amber-600 dark:text-amber-400">{withheld} hidden</span>
                    )}
                  </span>
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
