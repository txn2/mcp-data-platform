import type { SharePermission } from "@/api/portal/types";

const VIEW_STORAGE_KEY = "asset-view-mode";

/** A gallery of thumbnails, or a dense table. */
export type ViewMode = "grid" | "table";

/**
 * Read the persisted layout. Shared by the Assets and Collections lists so the
 * choice carries across them, the way `ScopeFilter` shares the ownership scope;
 * a list that keeps its own choice passes its own key.
 * Defensive against environments without localStorage (jsdom/SSR); defaults to
 * the gallery, which is what a saved asset is recognised by.
 */
export function getStoredViewMode(key: string = VIEW_STORAGE_KEY, fallback: ViewMode = "grid"): ViewMode {
  try {
    const stored = globalThis.localStorage?.getItem(key);
    return stored === "table" || stored === "grid" ? stored : fallback;
  } catch {
    return fallback;
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
  /** When it was shared, where the list that carried it knows. */
  shared_at?: string;
}

/** The date a row shows: when it was shared, where known, else its own. */
export function rowDate(share: ShareMeta | undefined, own: string): string {
  return share?.shared_at ?? own;
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
