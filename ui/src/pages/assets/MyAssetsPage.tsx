import { useState } from "react";
import type { Asset } from "@/api/portal/types";
import { AssetPreviewModal } from "@/components/AssetPreviewModal";
import { AssetsTabs } from "@/components/AssetsTabs";
import { InfiniteFooter } from "@/components/InfiniteFooter";
import {
  DEFAULT_ASSET_SORT,
  dateColumnFor,
  toggleSort,
  type AssetSortKey,
  type ListSort,
} from "@/components/listSort";
import { getStoredViewMode, storeViewMode, type ViewMode } from "@/components/listView";
import { getStoredScope, storeScope, type Scope } from "@/components/ScopeFilter";
import { ThumbnailQueue } from "@/components/ThumbnailQueue";
import { useResolvedDark } from "@/stores/theme";
import { AssetFilterBar } from "./browse/AssetFilterBar";
import { AssetGrid } from "./browse/AssetGrid";
import { AssetsEmpty } from "./browse/AssetsEmpty";
import { AssetTable } from "./browse/AssetTable";
import { useAssetBrowse } from "./browse/useAssetBrowse";

interface Props {
  onNavigate: (path: string) => void;
}

type Previewing = { id: string; name: string; contentType: string; sizeBytes: number };

export function MyAssetsPage({ onNavigate }: Props) {
  const isDark = useResolvedDark();
  const [scope, setScope] = useState<Scope>(getStoredScope);
  const [search, setSearch] = useState("");
  const [contentType, setContentType] = useState("");
  const [tag, setTag] = useState("");
  const [sort, setSort] = useState<ListSort<AssetSortKey>>(DEFAULT_ASSET_SORT);
  const [viewMode, setViewMode] = useState<ViewMode>(getStoredViewMode);
  const [previewing, setPreviewing] = useState<Previewing | null>(null);

  const browse = useAssetBrowse({ scope, search, contentType, tag, sort });

  function changeViewMode(mode: ViewMode) {
    setViewMode(mode);
    storeViewMode(mode);
  }

  function changeScope(next: Scope) {
    setScope(next);
    storeScope(next);
  }

  return (
    <div className="space-y-4">
      <AssetsTabs active="assets" onNavigate={onNavigate} />

      <AssetFilterBar
        scope={scope}
        onScopeChange={changeScope}
        search={search}
        onSearchChange={setSearch}
        contentType={contentType}
        onContentTypeChange={setContentType}
        tag={tag}
        onTagChange={setTag}
        sort={sort}
        onSortChange={setSort}
        // Relevance ranking, not a column, decides the order of a search.
        sortDisabled={browse.semanticSearch}
        viewMode={viewMode}
        onViewModeChange={changeViewMode}
      />

      <Results
        browse={browse}
        scope={scope}
        viewMode={viewMode}
        isDark={isDark}
        sort={sort}
        onSort={(key) => setSort((s) => toggleSort(s, key))}
        onNavigate={onNavigate}
        onPreview={(asset: Asset) =>
          setPreviewing({
            id: asset.id,
            name: asset.name,
            contentType: asset.content_type,
            sizeBytes: asset.size_bytes,
          })
        }
      />

      <InfiniteFooter
        hasMore={browse.canLoadMore}
        isLoadingMore={browse.loadingMore}
        onLoadMore={browse.loadMore}
      />

      <ResultCount browse={browse} scope={scope} />

      <ThumbnailQueue assets={browse.assets} />

      {previewing && (
        <AssetPreviewModal
          assetId={previewing.id}
          assetName={previewing.name}
          contentType={previewing.contentType}
          sizeBytes={previewing.sizeBytes}
          onClose={() => setPreviewing(null)}
        />
      )}
    </div>
  );
}

type BrowseResult = ReturnType<typeof useAssetBrowse>;

/** The list itself: loading, empty for one of several reasons, or the rows. */
function Results({
  browse,
  scope,
  viewMode,
  isDark,
  sort,
  onSort,
  onNavigate,
  onPreview,
}: {
  browse: BrowseResult;
  scope: Scope;
  viewMode: ViewMode;
  isDark: boolean;
  sort: ListSort<AssetSortKey>;
  onSort: (key: AssetSortKey) => void;
  onNavigate: (path: string) => void;
  onPreview: (asset: Asset) => void;
}) {
  if (browse.isLoadingList) {
    return (
      <p className="py-12 text-center text-sm text-muted-foreground">
        {browse.semanticSearch ? "Searching..." : "Loading..."}
      </p>
    );
  }

  if (browse.displayItems.length === 0) {
    // With more pages available (shared/all client-side filters only see the
    // loaded rows), steer to "Load more" instead of a contradictory "no
    // assets" empty state next to a Load-more button.
    return browse.canLoadMore ? (
      <p className="py-12 text-center text-sm text-muted-foreground">
        No matching assets in the loaded set yet &mdash; load more to keep looking.
      </p>
    ) : (
      <AssetsEmpty scope={scope} searching={browse.searching} query={browse.query} />
    );
  }

  return viewMode === "grid" ? (
    <AssetGrid
      items={browse.displayItems}
      shareSummaries={browse.shareSummaries}
      threadCounts={browse.threadCounts}
      isDark={isDark}
      dateKey={dateColumnFor(sort.key)}
      onNavigate={onNavigate}
    />
  ) : (
    <AssetTable
      items={browse.displayItems}
      shareSummaries={browse.shareSummaries}
      threadCounts={browse.threadCounts}
      sort={sort}
      // A relevance search is ranked, not sorted; its headers would otherwise
      // claim an ordering the rows do not have.
      onSort={browse.semanticSearch ? undefined : onSort}
      onNavigate={onNavigate}
      onPreview={onPreview}
    />
  );
}

/** How much of the list is on screen, phrased for the scope that produced it. */
function ResultCount({ browse, scope }: { browse: BrowseResult; scope: Scope }) {
  if (scope !== "mine") {
    if (browse.displayItems.length === 0) return null;
    return (
      <p className="text-center text-sm text-muted-foreground">
        Showing {browse.displayItems.length} {scope === "shared" ? "shared " : ""}asset
        {browse.displayItems.length === 1 ? "" : "s"}
      </p>
    );
  }
  if (browse.semanticSearch) {
    if (browse.assets.length === 0) return null;
    return (
      <p className="text-center text-xs text-muted-foreground">
        Ranked by relevance to &ldquo;{browse.query}&rdquo; across your assets.
      </p>
    );
  }
  if (browse.total === undefined || browse.assets.length >= browse.total) return null;
  return (
    <p className="text-center text-sm text-muted-foreground">
      Showing {browse.assets.length} of {browse.total} assets
    </p>
  );
}
