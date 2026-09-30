import { useMemo } from "react";
import type { ScheduleFireRow } from "@/api/portal/hooks/scheduleTimeline";
import { FilterSelect } from "@/components/patterns/FilterSelect";
import { facetOptions } from "./facetOptions";

/** ScheduleFacets are the filters the tab narrows its rows by (#1992): the
 * Automations list's author, category and tag. "" is no filter. */
export interface ScheduleFacets {
  owner: string;
  category: string;
  tag: string;
}

export const NO_FACETS: ScheduleFacets = { owner: "", category: "", tag: "" };

// matchesFacets reports whether a row passes every chosen filter.
export function matchesFacets(row: ScheduleFireRow, f: ScheduleFacets): boolean {
  return (
    (!f.owner || row.owner_email === f.owner) &&
    (!f.category || row.category === f.category) &&
    (!f.tag || row.tags.includes(f.tag))
  );
}

// ScheduleFilterBar is the Automations list's author, category and tag
// filters over the schedules on this tab. The options are read from every
// row, not the filtered ones, so choosing one value never removes the others,
// and each script is counted once however many sections it appears in.
export function ScheduleFilterBar({
  rows,
  facets,
  onFacets,
}: {
  rows: ScheduleFireRow[];
  facets: ScheduleFacets;
  onFacets: (facets: ScheduleFacets) => void;
}) {
  const options = useMemo(() => {
    const scripts = [...new Map(rows.map((r) => [r.script_id, r])).values()];
    return {
      owner: facetOptions(scripts, (r) => (r.owner_email ? [r.owner_email] : []), "All authors"),
      category: facetOptions(scripts, (r) => (r.category ? [r.category] : []), "All categories"),
      tag: facetOptions(scripts, (r) => r.tags, "All tags"),
    };
  }, [rows]);
  const set = (patch: Partial<ScheduleFacets>) => onFacets({ ...facets, ...patch });
  return (
    <div className="flex flex-wrap items-center gap-2">
      <FilterSelect
        label="Filter by author"
        value={facets.owner}
        onChange={(owner) => set({ owner })}
        options={options.owner}
      />
      <FilterSelect
        label="Filter by category"
        value={facets.category}
        onChange={(category) => set({ category })}
        options={options.category}
      />
      <FilterSelect
        label="Filter by tag"
        value={facets.tag}
        onChange={(tag) => set({ tag })}
        options={options.tag}
      />
    </div>
  );
}
