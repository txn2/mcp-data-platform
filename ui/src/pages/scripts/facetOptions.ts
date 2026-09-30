import type { FilterOption } from "@/components/patterns/FilterSelect";

// facetOptions is a facet's vocabulary, ordered by how many items carry each
// value and then alphabetically — the order the chips were in, so a reader
// finds the same values in the same places. The Automations list and the
// Schedules tab (#1992) both build their author, category and tag filters with
// it, so the two offer the same options in the same order.
export function facetOptions<T>(
  items: T[],
  values: (item: T) => string[],
  allLabel: string,
): FilterOption[] {
  const counts = new Map<string, number>();
  for (const item of items) {
    for (const value of values(item)) {
      counts.set(value, (counts.get(value) ?? 0) + 1);
    }
  }
  const options = [...counts.entries()]
    .sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]))
    .map(([value, count]) => ({ value, label: `${value} (${count})` }));
  return [{ value: "", label: allLabel }, ...options];
}
