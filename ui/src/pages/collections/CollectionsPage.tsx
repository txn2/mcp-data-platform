import { useState } from "react";
import { useCreateCollection } from "@/api/portal/hooks";
import { AssetsTabs } from "@/components/AssetsTabs";
import { CollectionThumbnailQueue } from "@/components/CollectionThumbnailQueue";
import { InfiniteFooter } from "@/components/InfiniteFooter";
import { getStoredViewMode, storeViewMode, type ViewMode } from "@/components/listView";
import { getStoredScope, storeScope, type Scope } from "@/components/ScopeFilter";
import { CollectionFilterBar } from "./browse/CollectionFilterBar";
import { CollectionGrid } from "./browse/CollectionGrid";
import { CollectionsEmpty } from "./browse/CollectionsEmpty";
import { CollectionTable } from "./browse/CollectionTable";
import { useCollectionBrowse, type CollectionBrowse } from "./browse/useCollectionBrowse";

interface Props {
  onNavigate: (path: string) => void;
}

export function CollectionsPage({ onNavigate }: Props) {
  const [scope, setScope] = useState<Scope>(getStoredScope);
  const [search, setSearch] = useState("");
  const [viewMode, setViewMode] = useState<ViewMode>(getStoredViewMode);
  const createMutation = useCreateCollection();

  const browse = useCollectionBrowse({ scope, search });

  function changeViewMode(mode: ViewMode) {
    setViewMode(mode);
    storeViewMode(mode);
  }

  function changeScope(next: Scope) {
    setScope(next);
    storeScope(next);
  }

  // A new collection is created empty and opened straight in its editor: there
  // is nothing to see on a collection with no sections yet.
  async function handleCreate() {
    if (createMutation.isPending) return;
    const result = await createMutation.mutateAsync({ name: "Untitled Collection" });
    onNavigate(`/collections/${result.id}/edit`);
  }

  return (
    <div className="space-y-4">
      <AssetsTabs active="collections" onNavigate={onNavigate} />

      <CollectionFilterBar
        scope={scope}
        onScopeChange={changeScope}
        search={search}
        onSearchChange={setSearch}
        viewMode={viewMode}
        onViewModeChange={changeViewMode}
        onCreate={() => void handleCreate()}
        creating={createMutation.isPending}
      />

      <Results browse={browse} scope={scope} viewMode={viewMode} onNavigate={onNavigate} />

      <InfiniteFooter
        hasMore={browse.canLoadMore}
        isLoadingMore={browse.loadingMore}
        onLoadMore={browse.loadMore}
      />

      <ResultCount browse={browse} scope={scope} />

      <CollectionThumbnailQueue collections={browse.collections} />
    </div>
  );
}

/** The list itself: loading, empty for one of several reasons, or the rows. */
function Results({
  browse,
  scope,
  viewMode,
  onNavigate,
}: {
  browse: CollectionBrowse;
  scope: Scope;
  viewMode: ViewMode;
  onNavigate: (path: string) => void;
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
    // loaded rows), steer to "Load more" instead of a contradictory empty
    // state next to a Load-more button.
    return browse.canLoadMore ? (
      <p className="py-12 text-center text-sm text-muted-foreground">
        No matching collections in the loaded set yet &mdash; load more to keep looking.
      </p>
    ) : (
      <CollectionsEmpty scope={scope} searching={browse.searching} query={browse.query} />
    );
  }

  return viewMode === "grid" ? (
    <CollectionGrid
      items={browse.displayItems}
      shareSummaries={browse.shareSummaries}
      threadCounts={browse.threadCounts}
      onNavigate={onNavigate}
    />
  ) : (
    <CollectionTable
      items={browse.displayItems}
      shareSummaries={browse.shareSummaries}
      threadCounts={browse.threadCounts}
      onNavigate={onNavigate}
    />
  );
}

/** How much of the list is on screen, phrased for the scope that produced it. */
function ResultCount({ browse, scope }: { browse: CollectionBrowse; scope: Scope }) {
  if (scope !== "mine") {
    if (browse.displayItems.length === 0) return null;
    return (
      <p className="text-center text-sm text-muted-foreground">
        Showing {browse.displayItems.length} {scope === "shared" ? "shared " : ""}collection
        {browse.displayItems.length === 1 ? "" : "s"}
      </p>
    );
  }
  if (browse.semanticSearch) {
    if (browse.collections.length === 0) return null;
    return (
      <p className="text-center text-xs text-muted-foreground">
        Ranked by relevance to &ldquo;{browse.query}&rdquo; across your collections.
      </p>
    );
  }
  // Only advertise a remaining count while more can actually be loaded.
  // flattenPages de-dupes by id but total is the raw server count, so a
  // concurrent insert that re-emits a deduped row would otherwise leave a
  // permanent "Showing 119 of 120" with no Load-more control to close it.
  if (!browse.canLoadMore || browse.total === undefined) return null;
  if (browse.collections.length >= browse.total) return null;
  return (
    <p className="text-center text-sm text-muted-foreground">
      Showing {browse.collections.length} of {browse.total} collections
    </p>
  );
}
