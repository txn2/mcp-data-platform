import { SearchX } from "lucide-react";

import { FilterChip } from "@/components/FilterChip";
import { StatCard } from "@/components/cards/StatCard";
import { EmptyState } from "@/components/patterns/EmptyState";

export interface StatusOption {
  value: string;
  label: string;
}

// StatGrid is the summary row a personal list opens with: four counts of the
// same thing, sized to the reader's window.
export function StatGrid({ stats }: { stats: { label: string; value: number }[] }) {
  return (
    <div className="grid grid-cols-2 gap-4 md:grid-cols-4">
      {stats.map((s) => (
        <StatCard key={s.label} label={s.label} value={s.value} />
      ))}
    </div>
  );
}

interface ListQuery<T> {
  data?: { data: T[]; total?: number };
  isLoading: boolean;
}

/**
 * activeList picks which of a personal list's two reads is on screen: the
 * relevance search while there is a query, the paged browse otherwise. The total
 * always comes from the browse read — a relevance search returns a ranked top-K,
 * which is not a page of anything, so it never drives the pager.
 */
export function activeList<T>(
  searching: boolean,
  search: ListQuery<T>,
  browse: ListQuery<T>,
) {
  const source = searching ? search : browse;
  return {
    items: source.data?.data ?? [],
    loading: source.isLoading,
    total: browse.data?.total ?? 0,
  };
}

// StatusChips is the lifecycle facet both personal lists filter by: one chip per
// status, the unfiltered choice first. It is a chip row rather than a listbox
// because the whole vocabulary is four or five words wide and worth showing.
export function StatusChips({
  options,
  value,
  onChange,
}: {
  options: StatusOption[];
  value: string;
  onChange: (value: string) => void;
}) {
  return (
    <div className="flex flex-wrap items-center gap-1.5">
      {options.map((o) => (
        <FilterChip
          key={o.value}
          label={o.label}
          active={value === o.value}
          onClick={() => onChange(o.value)}
        />
      ))}
    </div>
  );
}

/**
 * Results renders the three states a personal list has before it has rows: still
 * loading, searched and matched nothing, and genuinely empty. The last is the
 * caller's own copy, because "no insights yet" and "no memories yet" explain
 * different things; the other two are the same everywhere.
 */
export function Results({
  loading,
  searching,
  query,
  count,
  empty,
  children,
}: {
  loading: boolean;
  searching: boolean;
  query: string;
  count: number;
  // What to show when the list is empty and nothing was searched for.
  empty: React.ReactNode;
  children: React.ReactNode;
}) {
  if (loading) {
    return (
      <p className="text-sm text-muted-foreground">
        {searching ? "Searching..." : "Loading..."}
      </p>
    );
  }
  if (count === 0 && searching) {
    return (
      <EmptyState icon={SearchX}>Nothing matched &quot;{query}&quot;.</EmptyState>
    );
  }
  if (count === 0) return <>{empty}</>;
  return <div className="space-y-3">{children}</div>;
}
