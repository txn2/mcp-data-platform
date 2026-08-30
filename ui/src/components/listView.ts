import type { SharePermission } from "@/api/portal/types";
import type { Scope } from "@/components/ScopeFilter";

const VIEW_STORAGE_KEY = "asset-view-mode";

/**
 * Where the Resources library remembers its layout.
 *
 * Its own key rather than the assets one: a library is a folder tree and the
 * Assets page is a flat gallery, so a reader who wants rows in one and tiles in
 * the other is not confused, they are looking at two different things.
 */
export const RESOURCE_VIEW_STORAGE_KEY = "resource-view-mode";

/** A gallery of thumbnails, or a dense table. */
export type ViewMode = "grid" | "table";

/**
 * Read the persisted layout. Shared by the Assets and Collections lists so the
 * choice carries across them, the way `ScopeFilter` shares the ownership scope;
 * the Resources library passes its own key and keeps its own choice.
 * Defensive against environments without localStorage (jsdom/SSR); defaults to
 * the gallery, which is what a saved asset is recognised by.
 */
export function getStoredViewMode(key: string = VIEW_STORAGE_KEY): ViewMode {
  try {
    return globalThis.localStorage?.getItem(key) === "table" ? "table" : "grid";
  } catch {
    return "grid";
  }
}

export function storeViewMode(mode: ViewMode, key: string = VIEW_STORAGE_KEY) {
  try {
    globalThis.localStorage?.setItem(key, mode);
  } catch {
    /* persistence is best-effort */
  }
}

/** The subset of an infinite-query result the Load-more control needs. */
export interface LoadMoreControls {
  hasNextPage: boolean;
  isFetchingNextPage: boolean;
  fetchNextPage: () => void;
}

/** Share metadata attached to an item that was shared with the current user. */
export interface ShareMeta {
  shared_by: string;
  permission: SharePermission;
  shared_at: string;
}

/**
 * The paginated lists the active scope draws from: the browse list for "mine",
 * shared-with-me for "shared", both for "all" — and neither while relevance
 * ranking answers the query, since a ranked top-K has no further pages.
 */
export function activeSources(
  scope: Scope,
  semanticSearch: boolean,
  mine: LoadMoreControls,
  shared: LoadMoreControls,
): LoadMoreControls[] {
  if (semanticSearch) return [];
  const sources: LoadMoreControls[] = [];
  if (scope !== "shared") sources.push(mine);
  if (scope !== "mine") sources.push(shared);
  return sources;
}

/**
 * The Load-more state for those sources. Deriving all three from one list keeps
 * the button's enabled state and the fetch it performs from drifting apart.
 */
export function loadMoreState(sources: LoadMoreControls[]) {
  return {
    canLoadMore: sources.some((q) => q.hasNextPage),
    loadingMore: sources.some((q) => q.isFetchingNextPage),
    loadMore: () => {
      for (const q of sources) {
        if (q.hasNextPage && !q.isFetchingNextPage) q.fetchNextPage();
      }
    },
  };
}

/** Which source the active scope is waiting on before it can render rows. */
export function scopeIsLoading({
  scope,
  semanticSearch,
  searchLoading,
  mineLoading,
  sharedLoading,
}: {
  scope: Scope;
  semanticSearch: boolean;
  searchLoading: boolean;
  mineLoading: boolean;
  sharedLoading: boolean;
}): boolean {
  if (scope === "mine") return semanticSearch ? searchLoading : mineLoading;
  if (scope === "shared") return sharedLoading;
  return mineLoading || sharedLoading;
}
