import { useEffect, useState } from "react";
import type { WebhookSourceStatus } from "@/api/admin/types";
import { FilterSelect } from "@/components/patterns/FilterSelect";
import { SearchInput } from "@/components/patterns/SearchInput";
import { NO_FILTER, isFiltered, kindOptions, readFilter, writeFilter, type SourceFilter } from "./sourceFilter";

/** useSourceFilter is the sources list's search and filters (#2792), read
 * from the page's address and written back as they change, so a filtered
 * list can be bookmarked or shared and survives a reload. */
export function useSourceFilter(): [SourceFilter, React.Dispatch<React.SetStateAction<SourceFilter>>] {
  const [filter, setFilter] = useState<SourceFilter>(() =>
    typeof window === "undefined" ? NO_FILTER : readFilter(window.location.search),
  );
  useEffect(() => {
    if (typeof window === "undefined") return;
    const query = writeFilter(new URLSearchParams(window.location.search), filter).toString();
    window.history.replaceState(window.history.state, "", `${window.location.pathname}${query ? `?${query}` : ""}`);
  }, [filter]);
  return [filter, setFilter];
}

/** SourceFilters is the bar above the sources table: a search box that
 * narrows the rows as you type, the kind, and the sources that rejected a
 * request in the last day. */
export function SourceFilters({
  filter,
  onChange,
  all,
  shown,
  isLoading,
}: {
  filter: SourceFilter;
  onChange: React.Dispatch<React.SetStateAction<SourceFilter>>;
  all: WebhookSourceStatus[];
  shown: number;
  isLoading: boolean;
}) {
  return (
    <div className="flex flex-wrap items-center gap-2" data-testid="webhook-source-filters">
      <SearchInput
        aria-label="Search sources"
        placeholder="Search sources"
        value={filter.q}
        onChange={(e) => {
          const q = e.target.value;
          onChange((f) => ({ ...f, q }));
        }}
        className="w-64"
        inputClassName="h-8 text-xs"
      />
      <FilterSelect
        label="Filter by kind"
        value={filter.kind}
        onChange={(kind) => onChange((f) => ({ ...f, kind }))}
        options={kindOptions(all, filter.kind)}
      />
      <FilterSelect
        label="Filter by rejected requests"
        value={filter.rejected ? "1" : ""}
        onChange={(v) => onChange((f) => ({ ...f, rejected: v === "1" }))}
        options={[
          { value: "", label: "Any requests" },
          { value: "1", label: "Rejected a request in the last day" },
        ]}
      />
      {isFiltered(filter) && !isLoading && (
        <span className="text-xs text-muted-foreground">
          {shown} of {all.length} sources
        </span>
      )}
    </div>
  );
}
