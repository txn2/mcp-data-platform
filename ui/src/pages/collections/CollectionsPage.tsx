import { useState } from "react";
import { useCreateCollection } from "@/api/portal/hooks";
import { AssetsTabs } from "@/components/AssetsTabs";
import { InfiniteFooter } from "@/components/InfiniteFooter";
import {
  DEFAULT_COLLECTION_SORT,
  dateColumnFor,
  toggleSort,
  type CollectionSortKey,
  type ListSort,
} from "@/components/listSort";
import { getStoredViewMode, storeViewMode, type ViewMode } from "@/components/listView";
import { getStoredScope, storeScope, type Scope } from "@/components/ScopeFilter";
import { CollectionFilterBar } from "./browse/CollectionFilterBar";
import { CollectionGrid } from "./browse/CollectionGrid";
import { CollectionsEmpty } from "./browse/CollectionsEmpty";
import { CollectionTable } from "./browse/CollectionTable";
import { useCollectionBrowse, type CollectionBrowse } from "./browse/useCollectionBrowse";
import { scopePhrase } from "@/components/ScopeFilter";

interface Props {
  onNavigate: (path: string) => void;
}

export function CollectionsPage({ onNavigate }: Props) {
  const [scope, setScope] = useState<Scope>(getStoredScope);
  const [search, setSearch] = useState("");
  const [owners, setOwners] = useState<string[]>([]);
  const [sort, setSort] = useState<ListSort<CollectionSortKey>>(DEFAULT_COLLECTION_SORT);
  const [viewMode, setViewMode] = useState<ViewMode>(getStoredViewMode);
  const createMutation = useCreateCollection();

  const browse = useCollectionBrowse({ scope, search, owners, sort });

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
        owners={owners}
        onOwnersChange={setOwners}
        authorFacets={browse.authorFacets}
        sort={sort}
        onSortChange={setSort}
        // Relevance ranking, not a column, decides the order of a search.
        sortDisabled={browse.searching}
        viewMode={viewMode}
        onViewModeChange={changeViewMode}
        onCreate={() => void handleCreate()}
        creating={createMutation.isPending}
      />

      <Results
        browse={browse}
        scope={scope}
        viewMode={viewMode}
        sort={sort}
        onSort={(key) => setSort((s) => toggleSort(s, key))}
        onNavigate={onNavigate}
      />

      <InfiniteFooter
        hasMore={browse.canLoadMore}
        isLoadingMore={browse.loadingMore}
        onLoadMore={browse.loadMore}
      />

      <ResultCount browse={browse} scope={scope} />

    </div>
  );
}

/** The list itself: loading, empty for one of several reasons, or the rows. */
function Results({
  browse,
  scope,
  viewMode,
  sort,
  onSort,
  onNavigate,
}: {
  browse: CollectionBrowse;
  scope: Scope;
  viewMode: ViewMode;
  sort: ListSort<CollectionSortKey>;
  onSort: (key: CollectionSortKey) => void;
  onNavigate: (path: string) => void;
}) {
  if (browse.isLoadingList) {
    return (
      <p className="py-12 text-center text-sm text-muted-foreground">
        {browse.searching ? "Searching..." : "Loading..."}
      </p>
    );
  }

  if (browse.displayItems.length === 0) {
    return <CollectionsEmpty scope={scope} searching={browse.searching} query={browse.query} />;
  }

  return viewMode === "grid" ? (
    <CollectionGrid
      items={browse.displayItems}
      shareSummaries={browse.shareSummaries}
      threadCounts={browse.threadCounts}
      dateKey={dateColumnFor(sort.key)}
      onNavigate={onNavigate}
    />
  ) : (
    <CollectionTable
      items={browse.displayItems}
      shareSummaries={browse.shareSummaries}
      threadCounts={browse.threadCounts}
      sort={sort}
      // A relevance search is ranked, not sorted; its headers would otherwise
      // claim an ordering the rows do not have.
      onSort={browse.searching ? undefined : onSort}
      onNavigate={onNavigate}
    />
  );
}

/** How much of the list is on screen, phrased for the scope that produced it. */
function ResultCount({ browse, scope }: { browse: CollectionBrowse; scope: Scope }) {
  if (browse.total === undefined || browse.collections.length === 0) return null;
  const where = scopePhrase(scope, "collections");
  if (browse.searching) {
    return (
      <p className="text-center text-xs text-muted-foreground">
        {browse.total} {browse.total === 1 ? "match" : "matches"} for &ldquo;{browse.query}&rdquo; in{" "}
        {where}, ranked by relevance.
      </p>
    );
  }
  // flattenPages de-dupes by id but total is the raw server count, so only
  // a list that can still load more advertises a remainder.
  if (!browse.canLoadMore || browse.collections.length >= browse.total) return null;
  return (
    <p className="text-center text-sm text-muted-foreground">
      Showing {browse.collections.length} of {browse.total} collections
    </p>
  );
}
