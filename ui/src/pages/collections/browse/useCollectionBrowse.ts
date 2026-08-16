import {
  useInfiniteCollections,
  useInfiniteSharedCollections,
  useSearchCollections,
  useThreadCounts,
} from "@/api/portal/hooks";
import type { Collection, ShareSummary } from "@/api/portal/types";
import {
  sortRowsBy,
  timeValue,
  type CollectionSortKey,
  type ListSort,
} from "@/components/listSort";
import { activeSources, loadMoreState, scopeIsLoading } from "@/components/listView";
import type { Scope } from "@/components/ScopeFilter";
import { useDebounced } from "@/lib/useDebounced";
import type { DisplayCollection } from "./types";

export interface CollectionBrowse {
  displayItems: DisplayCollection[];
  collections: Collection[];
  threadCounts?: Record<string, number>;
  shareSummaries?: Record<string, ShareSummary>;
  /** The server's count of the caller's own collections, once a page lands. */
  total?: number;
  isLoadingList: boolean;
  searching: boolean;
  semanticSearch: boolean;
  /** The debounced, trimmed query — what the result copy quotes back. */
  query: string;
  canLoadMore: boolean;
  loadingMore: boolean;
  loadMore: () => void;
}

/**
 * useCollectionBrowse resolves the Collections list for one scope and query.
 *
 * It mirrors `useAssetBrowse`: browse and shared-with-me paginate, relevance
 * ranking covers the caller's own collections only and returns a ranked top-K
 * with no further pages, and the merged scopes filter client-side over the rows
 * loaded so far.
 */
export function useCollectionBrowse({
  scope,
  search,
  sort,
}: {
  scope: Scope;
  /** The raw search box value; the hook owns its debounce. */
  search: string;
  sort: ListSort<CollectionSortKey>;
}): CollectionBrowse {
  const debouncedSearch = useDebounced(search, 300);
  const query = debouncedSearch.trim();
  const searching = query.length > 0;
  const semanticSearch = searching && scope === "mine";

  // The Shared scope discards this query's rows entirely, so it sends no
  // ordering and its query key stays put; Mine and All both page through it.
  const mineQuery = useInfiniteCollections(
    scope === "shared" ? {} : { sort: sort.key, dir: sort.dir },
  );
  const { data, isLoading } = mineQuery;
  const searchResults = useSearchCollections(semanticSearch ? debouncedSearch : "");
  const sharedQuery = useInfiniteSharedCollections();
  const { data: sharedData, isLoading: sharedLoading } = sharedQuery;

  const items = mergeScope(
    scope,
    semanticSearch ? ranked(searchResults.data) : browsed(data),
    sharedCollections(sharedData),
  );
  // The mine scope is ordered server-side; the merged scopes are two paginated
  // queries stitched together here, so they order the rows loaded so far.
  const displayItems =
    scope === "mine"
      ? items
      : sortCollections(items.filter((it) => matchesQuery(it.collection, query)), sort);

  const collections = displayItems.map((it) => it.collection);
  const { data: threadCounts } = useThreadCounts(
    "collection",
    collections.map((c) => c.id),
  );

  return {
    displayItems,
    collections,
    threadCounts,
    shareSummaries: data?.share_summaries,
    total: data?.total,
    isLoadingList: scopeIsLoading({
      scope,
      semanticSearch,
      searchLoading: searchResults.isLoading,
      mineLoading: isLoading,
      sharedLoading,
    }),
    searching,
    semanticSearch,
    query,
    ...loadMoreState(activeSources(scope, semanticSearch, mineQuery, sharedQuery)),
  };
}

type SearchData = ReturnType<typeof useSearchCollections>["data"];
type BrowseData = ReturnType<typeof useInfiniteCollections>["data"];
type SharedData = ReturnType<typeof useInfiniteSharedCollections>["data"];

function ranked(data: SearchData): Collection[] {
  return (data?.data ?? []).map((s) => s.collection);
}

function browsed(data: BrowseData): Collection[] {
  return data?.data ?? [];
}

function sharedCollections(data: SharedData): DisplayCollection[] {
  return (data?.data ?? []).map((s) => ({
    collection: s.collection,
    share: { shared_by: s.shared_by, permission: s.permission, shared_at: s.shared_at },
  }));
}

/** The rows the scope draws from, with owned items winning a duplicate id. */
function mergeScope(
  scope: Scope,
  mine: Collection[],
  shared: DisplayCollection[],
): DisplayCollection[] {
  if (scope === "mine") return mine.map((collection) => ({ collection }));
  if (scope === "shared") return shared;
  const mineIds = new Set(mine.map((c) => c.id));
  return [
    ...mine.map((collection) => ({ collection })),
    ...shared.filter((s) => !mineIds.has(s.collection.id)),
  ];
}

/** The merged scopes' own ordering, over the rows loaded so far. */
function sortCollections(
  items: DisplayCollection[],
  sort: ListSort<CollectionSortKey>,
): DisplayCollection[] {
  return sortRowsBy(
    items,
    sort.dir,
    (it) => collectionSortValue(it.collection, sort.key),
    (it) => it.collection.id,
  );
}

function collectionSortValue(c: Collection, key: CollectionSortKey): string | number {
  switch (key) {
    case "name":
      return (c.name ?? "").toLowerCase();
    case "created_at":
      return timeValue(c.created_at);
    default:
      return timeValue(c.updated_at);
  }
}

function matchesQuery(c: Collection, query: string): boolean {
  if (!query) return true;
  const q = query.toLowerCase();
  return (
    (c.name ?? "").toLowerCase().includes(q) ||
    (c.description ?? "").toLowerCase().includes(q) ||
    (c.asset_tags ?? []).some((t) => t.toLowerCase().includes(q))
  );
}
