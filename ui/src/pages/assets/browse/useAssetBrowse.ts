import {
  useAssetFacets,
  useInfiniteAssets,
  useInfiniteSearchAssets,
  useThreadCounts,
} from "@/api/portal/hooks";
import type { AssetQueryFilters } from "@/api/portal/hooks/assets";
import type {
  Asset,
  AssetFacets,
  FacetCount,
  PaginatedResponse,
  ScoredAsset,
  ShareSummary,
} from "@/api/portal/types";
import type { AssetSortKey, ListSort } from "@/components/listSort";
import { loadMoreState } from "@/components/listView";
import type { Scope } from "@/components/ScopeFilter";
import { useDebounced } from "@/lib/useDebounced";
import type { DisplayAsset } from "./types";

export interface AssetBrowseFilters {
  scope: Scope;
  /** The raw search box value; the hook owns its debounce. */
  search: string;
  contentType: string;
  tag: string;
  /** Owner addresses an asset must have one of: the Author filter. */
  owners: string[];
  sort: ListSort<AssetSortKey>;
}

export interface AssetBrowse {
  /** The rows to render, in the server's order. */
  displayItems: DisplayAsset[];
  assets: Asset[];
  threadCounts?: Record<string, number>;
  shareSummaries?: Record<string, ShareSummary>;
  /** The server's count of every row the scope, query and filters cover. */
  total?: number;
  /** The tags and authors of the assets the scope and filters cover. */
  tagFacets: FacetCount[];
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
 * useAssetBrowse resolves the Assets list for one set of filters.
 *
 * Every scope, the search and every filter run on the server over exactly the
 * assets the caller can open (#2500): a listing pages in the chosen order, a
 * search pages by relevance, and both report the count of all their rows. The
 * caller's permission on each row they do not own comes back with the page.
 */
export function useAssetBrowse({
  scope,
  search,
  contentType,
  tag,
  owners,
  sort,
}: AssetBrowseFilters): AssetBrowse {
  const debouncedSearch = useDebounced(search, 300);
  const query = debouncedSearch.trim();
  const searching = query.length > 0;

  const filters = queryFilters(scope, owners, contentType, tag);
  // The listing stays mounted while a search runs, so clearing the box returns
  // to the page already loaded.
  const listQuery = useInfiniteAssets({ ...filters, sort: sort.key, dir: sort.dir });
  const searchQuery = useInfiniteSearchAssets(searching ? query : "", filters);
  const facets = useAssetFacets(filters);

  const active = searching ? searchQuery : listQuery;
  const assets = searching ? rankedRows(searchQuery.data) : (listQuery.data?.data ?? []);
  const page = active.data as PaginatedResponse<unknown> | undefined;
  const displayItems = assets.map((asset) => describe(asset, page?.permissions));

  const { data: threadCounts } = useThreadCounts(
    "asset",
    assets.map((a) => a.id),
  );

  return {
    displayItems,
    assets,
    threadCounts,
    shareSummaries: page?.share_summaries,
    total: page?.total,
    ...facetLists(facets.data),
    isLoadingList: active.isLoading,
    searching,
    query,
    ...loadMoreState([active]),
  };
}

/** The server-side filters, with the unset ones left out. */
function queryFilters(
  scope: Scope,
  owners: string[],
  contentType: string,
  tag: string,
): AssetQueryFilters {
  return {
    scope,
    owners: owners.length > 0 ? owners : undefined,
    content_type: contentType || undefined,
    tag: tag || undefined,
  };
}

function rankedRows(data: PaginatedResponse<ScoredAsset> | undefined): Asset[] {
  return (data?.data ?? []).map((s) => s.asset);
}

function facetLists(data: AssetFacets | undefined): { tagFacets: FacetCount[]; authorFacets: FacetCount[] } {
  return { tagFacets: data?.tags ?? [], authorFacets: data?.authors ?? [] };
}

/**
 * A row for display. A row the caller does not own carries the permission a
 * share gives them and its owner, which is who the grid names as having
 * shared it.
 */
function describe(asset: Asset, permissions: PaginatedResponse<unknown>["permissions"]): DisplayAsset {
  const permission = permissions?.[asset.id];
  if (!permission) return { asset };
  return { asset, share: { shared_by: asset.owner_email || asset.owner_id, permission } };
}
