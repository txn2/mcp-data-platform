import {
  useCollectionFacets,
  useInfiniteCollections,
  useInfiniteSearchCollections,
  useThreadCounts,
} from "@/api/portal/hooks";
import type {
  Collection,
  FacetCount,
  PaginatedResponse,
  ScoredCollection,
  ShareSummary,
} from "@/api/portal/types";
import type { CollectionSortKey, ListSort } from "@/components/listSort";
import { loadMoreState } from "@/components/listView";
import type { Scope } from "@/components/ScopeFilter";
import { useDebounced } from "@/lib/useDebounced";
import type { DisplayCollection } from "./types";

export interface CollectionBrowse {
  displayItems: DisplayCollection[];
  collections: Collection[];
  threadCounts?: Record<string, number>;
  shareSummaries?: Record<string, ShareSummary>;
  /** The server's count of every row the scope, query and filters cover. */
  total?: number;
  /** The authors of the collections the scope covers. */
  authorFacets: FacetCount[];
  isLoadingList: boolean;
  /** Whether the debounced query is non-empty; it is ranked by relevance. */
  searching: boolean;
  /** The debounced, trimmed query — what the result copy quotes back. */
  query: string;
  canLoadMore: boolean;
  loadingMore: boolean;
  loadMore: () => void;
}

/**
 * useCollectionBrowse resolves the Collections list for one scope, query and
 * author filter. It mirrors `useAssetBrowse`: every scope and the search run
 * on the server over exactly the collections the caller can open (#2500), and
 * both page.
 */
export function useCollectionBrowse({
  scope,
  search,
  owners,
  sort,
}: {
  scope: Scope;
  /** The raw search box value; the hook owns its debounce. */
  search: string;
  /** Owner addresses a collection must have one of: the Author filter. */
  owners: string[];
  sort: ListSort<CollectionSortKey>;
}): CollectionBrowse {
  const debouncedSearch = useDebounced(search, 300);
  const query = debouncedSearch.trim();
  const searching = query.length > 0;

  const filters = { scope, owners: owners.length > 0 ? owners : undefined };
  const listQuery = useInfiniteCollections({ ...filters, sort: sort.key, dir: sort.dir });
  const searchQuery = useInfiniteSearchCollections(searching ? query : "", filters);
  const facets = useCollectionFacets(scope);

  const active = searching ? searchQuery : listQuery;
  const collections = searching ? rankedRows(searchQuery.data) : (listQuery.data?.data ?? []);
  const page = active.data as PaginatedResponse<unknown> | undefined;
  const displayItems = collections.map((c) => describe(c, page?.permissions));

  const { data: threadCounts } = useThreadCounts(
    "collection",
    collections.map((c) => c.id),
  );

  return {
    displayItems,
    collections,
    threadCounts,
    shareSummaries: page?.share_summaries,
    total: page?.total,
    authorFacets: authorsOf(facets.data),
    isLoadingList: active.isLoading,
    searching,
    query,
    ...loadMoreState([active]),
  };
}

function rankedRows(data: PaginatedResponse<ScoredCollection> | undefined): Collection[] {
  return (data?.data ?? []).map((s) => s.collection);
}

function authorsOf(data: { authors: FacetCount[] } | undefined): FacetCount[] {
  return data?.authors ?? [];
}

/** A row for display; one the caller does not own names its owner as the sharer. */
function describe(
  collection: Collection,
  permissions: PaginatedResponse<unknown>["permissions"],
): DisplayCollection {
  const permission = permissions?.[collection.id];
  if (!permission) return { collection };
  return { collection, share: { shared_by: collection.owner_email || collection.owner_id, permission } };
}
