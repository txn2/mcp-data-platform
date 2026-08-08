import { useEffect, useState } from "react";
import { Lock } from "lucide-react";

import { useSearch, MIN_SEARCH_LEN } from "@/api/portal/hooks";
import type { SearchHit, SearchResponse } from "@/api/portal/types";
import { FilterChip } from "@/components/FilterChip";
import { SearchInput } from "@/components/patterns/SearchInput";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { useDebounced } from "@/lib/useDebounced";
import { HitDetailDrawer, HitRow, sourceLabel } from "./searchHits";

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

  return (
    <div className="space-y-4">
      <SearchInput
        value={input}
        onChange={(e) => setInput(e.target.value)}
        placeholder="Search all knowledge: catalog, knowledge pages, memory, insights, assets, prompts..."
        aria-label="Search all knowledge"
      />

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
        <Results
          data={data}
          isLoading={isLoading}
          isError={isError}
          query={query.trim()}
          onSelect={setSelectedHit}
        />
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

// Results renders one query's answer: what the server could not show, then the
// hits grouped by the source they came from.
function Results({
  data,
  isLoading,
  isError,
  query,
  onSelect,
}: {
  data: SearchResponse | undefined;
  isLoading: boolean;
  isError: boolean;
  query: string;
  onSelect: (hit: SearchHit) => void;
}) {
  const coverageFor = (source: string) => data?.coverage.find((c) => c.source === source);

  return (
    <div className="space-y-4">
      {isLoading && <p className="text-sm text-muted-foreground">Searching...</p>}
      {isError && (
        <p className="text-sm text-muted-foreground">Search is unavailable right now.</p>
      )}
      {data?.withheld_notice && <WithheldBanner notice={data.withheld_notice} />}
      {data && data.count === 0 && !data.withheld_notice && (
        <p className="py-8 text-center text-sm text-muted-foreground">
          Nothing matched &quot;{query}&quot;.
        </p>
      )}
      {data?.groups.map((group) => (
        <div key={group.source} className="space-y-2">
          <div className="flex items-baseline justify-between">
            <h3 className="text-sm font-semibold">{sourceLabel(group.source)}</h3>
            <GroupCoverage coverage={coverageFor(group.source)} />
          </div>
          <div className="space-y-2">
            {group.hits.map((hit) => (
              <HitRow
                key={`${hit.source}:${hit.ref}`}
                hit={hit}
                onClick={() => onSelect(hit)}
              />
            ))}
          </div>
        </div>
      ))}
    </div>
  );
}

// GroupCoverage says how much of a source's match set is on screen, and how much
// of it the caller's persona is not allowed to see.
function GroupCoverage({
  coverage,
}: {
  coverage: { matched: number; shown: number; withheld?: number } | undefined;
}) {
  const withheld = coverage?.withheld ?? 0;
  return (
    <span className="flex items-center gap-2 text-[11px] text-muted-foreground">
      {coverage && coverage.matched > coverage.shown && (
        <span>
          {coverage.shown} of {coverage.matched} shown
        </span>
      )}
      {withheld > 0 && (
        <span className="text-amber-600 dark:text-amber-400">{withheld} hidden</span>
      )}
    </span>
  );
}

// WithheldBanner explains results the caller's persona hid because they belong
// to connections it is not granted. The copy comes from the server so the portal
// and the MCP search tool say the same thing; rendering it above the groups is
// deliberate, since a source can be filtered down to nothing and then has no
// group of its own to carry the count. It is a status rather than an alert: it
// re-renders on every keystroke's result, so it must not interrupt a reader.
function WithheldBanner({ notice }: { notice: string }) {
  return (
    <Alert variant="warning" role="status">
      <Lock />
      <AlertDescription>{notice}</AlertDescription>
    </Alert>
  );
}
