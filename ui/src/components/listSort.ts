import type { FilterOption } from "@/components/patterns/FilterSelect";

/** Ascending or descending, the direction half of an ordering. */
export type SortDir = "asc" | "desc";

/** A chosen ordering: which column, and which way. */
export interface ListSort<K extends string> {
  key: K;
  dir: SortDir;
}

/**
 * The columns the Assets list can be ordered by. These strings are sent to the
 * server as `sort` and must stay in step with `AssetSortColumns`
 * (internal/portal/portaldomain/sort.go); a value outside that set is not an
 * error, it silently falls back to the default ordering.
 */
export type AssetSortKey = "updated_at" | "created_at" | "name" | "size_bytes";

/** The same for Collections, which have no size. */
export type CollectionSortKey = "updated_at" | "created_at" | "name";

/**
 * Both lists open on most-recently-touched. "Newest first" means newest work,
 * not newest row: an asset created in June and revised today belongs above one
 * created yesterday and never opened since.
 */
export const DEFAULT_ASSET_SORT: ListSort<AssetSortKey> = { key: "updated_at", dir: "desc" };
export const DEFAULT_COLLECTION_SORT: ListSort<CollectionSortKey> = {
  key: "updated_at",
  dir: "desc",
};

/**
 * The sort options each list offers. The labels are the column, not the
 * direction — the direction is its own toggle beside the select, so a label
 * that said "Newest" would be a second, contradicting statement of it.
 */
export const ASSET_SORT_OPTIONS: FilterOption[] = [
  { value: "updated_at", label: "Updated" },
  { value: "created_at", label: "Created" },
  { value: "name", label: "Name" },
  { value: "size_bytes", label: "Size" },
];

export const COLLECTION_SORT_OPTIONS: FilterOption[] = ASSET_SORT_OPTIONS.filter(
  (o) => o.value !== "size_bytes",
);

/**
 * The direction a column takes when it first becomes the sorted one: text
 * reads A-Z, everything else reads largest or most recent first.
 */
export function defaultDirFor(key: string): SortDir {
  return key === "name" ? "asc" : "desc";
}

/**
 * Apply a column choice the way a sortable header does: clicking the active
 * column reverses it, clicking another column adopts that column at its own
 * default direction.
 */
export function toggleSort<K extends string>(current: ListSort<K>, key: K): ListSort<K> {
  if (current.key === key) {
    return { key, dir: current.dir === "asc" ? "desc" : "asc" };
  }
  return { key, dir: defaultDirFor(key) };
}

/** The two timestamps a row can display. */
export type DateColumn = "created_at" | "updated_at";

/**
 * What a row's date means, for a card that shows the date with no visible
 * label. A row shared with the reader shows when it was shared, not when its
 * owner last touched it, so it names that instead.
 */
export function dateLabelFor(key: DateColumn, shared: boolean): string {
  if (shared) return "Shared";
  return key === "created_at" ? "Created" : "Updated";
}

/**
 * Which timestamp a list should show for the active ordering: the one it is
 * ordered by, so the dates on screen are always in the order the rows are.
 * Ordering by name or size leaves it on the last-touched time, which is the
 * list's default sense of recency.
 */
export function dateColumnFor(key: string): DateColumn {
  return key === "created_at" ? "created_at" : "updated_at";
}

/**
 * A timestamp as a comparable number. Timestamps arrive as RFC3339 strings
 * whose UTC offset is the server's, so comparing them as text would order two
 * equal instants by their offset; an unparseable or missing one sorts as the
 * epoch rather than poisoning the comparison with NaN.
 */
export function timeValue(value?: string): number {
  const t = Date.parse(value ?? "");
  return Number.isNaN(t) ? 0 : t;
}

/**
 * Order rows client-side, for the scopes the server cannot order: shared-with-me
 * and the merged "all" list are two paginated queries stitched together here, so
 * only this side can put them in one order.
 *
 * The id tie-breaker mirrors the server's (portaldomain.ResolveOrder): none of
 * the sortable columns is unique, and without it two rows saved in the same
 * second would swap places between renders.
 */
export function sortRowsBy<T>(
  rows: T[],
  dir: SortDir,
  valueOf: (row: T) => string | number,
  idOf: (row: T) => string,
): T[] {
  const factor = dir === "asc" ? 1 : -1;
  return [...rows].sort((a, b) => {
    const av = valueOf(a);
    const bv = valueOf(b);
    const cmp =
      typeof av === "number" && typeof bv === "number"
        ? av - bv
        : String(av).localeCompare(String(bv));
    return factor * (cmp !== 0 ? cmp : idOf(a).localeCompare(idOf(b)));
  });
}
