import {
  useInfiniteAssets,
  useInfiniteSharedWithMe,
  useSearchAssets,
  useThreadCounts,
} from "@/api/portal/hooks";
import type { Asset, ShareSummary } from "@/api/portal/types";
import { activeSources, loadMoreState, scopeIsLoading } from "@/components/listView";
import type { Scope } from "@/components/ScopeFilter";
import { useDebounced } from "@/lib/useDebounced";
import type { DisplayAsset } from "./types";

export interface AssetBrowseFilters {
  scope: Scope;
  /** The raw search box value; the hook owns its debounce. */
  search: string;
  contentType: string;
  tag: string;
}

export interface AssetBrowse {
  /** The rows to render, after scope selection and client-side filtering. */
  displayItems: DisplayAsset[];
  assets: Asset[];
  threadCounts?: Record<string, number>;
  shareSummaries?: Record<string, ShareSummary>;
  /** The server's count of the caller's own assets, once the first page lands. */
  total?: number;
  isLoadingList: boolean;
  /** Whether the debounced query is non-empty. */
  searching: boolean;
  /** Whether that query is being answered by relevance ranking. */
  semanticSearch: boolean;
  /** The debounced, trimmed query — what the result copy quotes back. */
  query: string;
  canLoadMore: boolean;
  loadingMore: boolean;
  loadMore: () => void;
}

/**
 * useAssetBrowse resolves the Assets list for one set of filters.
 *
 * Three sources feed it and the active scope decides which: browse (the
 * caller's own assets, filtered server-side), relevance search (own assets
 * only), and shared-with-me. Browse and shared paginate; relevance search
 * returns a ranked top-K with no further pages.
 */
export function useAssetBrowse({ scope, search, contentType, tag }: AssetBrowseFilters): AssetBrowse {
  const debouncedSearch = useDebounced(search, 300);
  const query = debouncedSearch.trim();
  const searching = query.length > 0;
  // Semantic ranking only covers the caller's own assets; in shared/all scopes
  // the search box falls back to client-side name/description matching.
  const semanticSearch = searching && scope === "mine";

  const mineQuery = useInfiniteAssets(serverFilters(scope, contentType, tag));
  const { data, isLoading } = mineQuery;
  const searchResults = useSearchAssets(semanticSearch ? debouncedSearch : "");
  const sharedQuery = useInfiniteSharedWithMe();
  const { data: sharedData, isLoading: sharedLoading } = sharedQuery;

  const items = mergeScope(
    scope,
    semanticSearch ? rankedAssets(searchResults.data) : browsedAssets(data),
    sharedAssets(sharedData),
  );
  // The mine scope is already filtered and ranked server-side; the merged
  // scopes only ever see the rows loaded so far, so they filter here.
  const displayItems =
    scope === "mine"
      ? items
      : items.filter((it) => matchesClientFilters(it.asset, { query, contentType, tag }));

  const assets = displayItems.map((it) => it.asset);
  const { data: threadCounts } = useThreadCounts(
    "asset",
    assets.map((a) => a.id),
  );

  return {
    displayItems,
    assets,
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

/**
 * The browse query's server-side filters. They only apply to the caller's own
 * assets, so the other scopes send none and filter the merged set themselves.
 */
function serverFilters(scope: Scope, contentType: string, tag: string) {
  if (scope !== "mine") return {};
  return { content_type: contentType || undefined, tag: tag || undefined };
}

type SearchData = ReturnType<typeof useSearchAssets>["data"];
type BrowseData = ReturnType<typeof useInfiniteAssets>["data"];
type SharedData = ReturnType<typeof useInfiniteSharedWithMe>["data"];

function rankedAssets(data: SearchData): Asset[] {
  return (data?.data ?? []).map((s) => s.asset);
}

function browsedAssets(data: BrowseData): Asset[] {
  return data?.data ?? [];
}

function sharedAssets(data: SharedData): DisplayAsset[] {
  return (data?.data ?? []).map((s) => ({
    asset: s.asset,
    share: { shared_by: s.shared_by, permission: s.permission, shared_at: s.shared_at },
  }));
}

/** The rows the scope draws from, with owned items winning a duplicate id. */
function mergeScope(scope: Scope, mine: Asset[], shared: DisplayAsset[]): DisplayAsset[] {
  if (scope === "mine") return mine.map((asset) => ({ asset }));
  if (scope === "shared") return shared;
  const mineIds = new Set(mine.map((a) => a.id));
  return [...mine.map((asset) => ({ asset })), ...shared.filter((s) => !mineIds.has(s.asset.id))];
}

function matchesClientFilters(
  a: Asset,
  { query, contentType, tag }: { query: string; contentType: string; tag: string },
): boolean {
  return matchesText(a, query) && matchesType(a, contentType) && matchesTag(a, tag);
}

function matchesText(a: Asset, query: string): boolean {
  if (!query) return true;
  const q = query.toLowerCase();
  return (a.name ?? "").toLowerCase().includes(q) || (a.description ?? "").toLowerCase().includes(q);
}

function matchesType(a: Asset, contentType: string): boolean {
  return !contentType || a.content_type === contentType;
}

function matchesTag(a: Asset, tag: string): boolean {
  if (!tag) return true;
  const t = tag.toLowerCase();
  return (a.tags ?? []).some((x) => x.toLowerCase().includes(t));
}
