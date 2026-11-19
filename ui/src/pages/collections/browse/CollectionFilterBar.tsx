import { LayoutGrid, List, Plus } from "lucide-react";
import {
  COLLECTION_SORT_OPTIONS,
  type CollectionSortKey,
  type ListSort,
} from "@/components/listSort";
import type { ViewMode } from "@/components/listView";
import { SearchInput } from "@/components/patterns/SearchInput";
import { SegmentedControl } from "@/components/patterns/SegmentedControl";
import { SortControl } from "@/components/patterns/SortControl";
import { ScopeFilter, type Scope } from "@/components/ScopeFilter";
import { AuthorPicker } from "@/components/patterns/AuthorPicker";
import { Button } from "@/components/ui/button";
import type { FacetCount } from "@/api/portal/types";
import { usePortalAccess } from "@/hooks/usePortalAccess";
import { useIsMobile } from "@/hooks/useIsMobile";
import { PhoneFilterBar } from "@/components/patterns/PhoneFilterBar";

const VIEW_OPTIONS = [
  { value: "grid" as const, label: "Grid view", icon: LayoutGrid },
  { value: "table" as const, label: "Table view", icon: List },
];

/** Everything that narrows the Collections list, plus the way to start one. */
export function CollectionFilterBar({
  scope,
  onScopeChange,
  search,
  onSearchChange,
  owners,
  onOwnersChange,
  authorFacets,
  sort,
  onSortChange,
  sortDisabled,
  viewMode,
  onViewModeChange,
  onCreate,
  creating,
}: {
  scope: Scope;
  onScopeChange: (scope: Scope) => void;
  search: string;
  onSearchChange: (search: string) => void;
  owners: string[];
  onOwnersChange: (owners: string[]) => void;
  authorFacets: FacetCount[];
  sort: ListSort<CollectionSortKey>;
  onSortChange: (sort: ListSort<CollectionSortKey>) => void;
  /** True while relevance ranking, not a column, decides the order. */
  sortDisabled: boolean;
  viewMode: ViewMode;
  onViewModeChange: (mode: ViewMode) => void;
  onCreate: () => void;
  creating: boolean;
}) {
  const canCreate = usePortalAccess().assets === "own";
  const isMobile = useIsMobile();
  const createButton = scope !== "shared" && canCreate && (
    <Button onClick={onCreate} disabled={creating}>
      <Plus />
      {creating ? "Creating..." : "New Collection"}
    </Button>
  );
  if (isMobile) {
    // A phone keeps the scope and the search on screen and folds the author
    // filter behind Filters (#2723).
    return (
      <PhoneFilterBar
        scope={<ScopeFilter value={scope} onChange={onScopeChange} />}
        search={<SearchInput value={search} onChange={(e) => onSearchChange(e.target.value)} placeholder="Search collections" />}
        active={
          owners.length > 0
            ? [{ key: "owners", label: owners.length === 1 ? `Author: ${owners[0]}` : `${owners.length} authors`, onClear: () => onOwnersChange([]) }]
            : []
        }
        sort={<SortControl value={sort} onChange={onSortChange} options={COLLECTION_SORT_OPTIONS} disabled={sortDisabled} />}
        view={<SegmentedControl label="List layout" value={viewMode} onChange={onViewModeChange} options={VIEW_OPTIONS} />}
        action={createButton || undefined}
        filters={<AuthorPicker authors={authorFacets} value={owners} onChange={onOwnersChange} className="w-full" />}
      />
    );
  }
  return (
    <div className="flex flex-wrap items-center gap-3">
      <ScopeFilter value={scope} onChange={onScopeChange} />
      <SearchInput
        className="min-w-[200px] flex-1"
        value={search}
        onChange={(e) => onSearchChange(e.target.value)}
        placeholder="Search collections by meaning..."
      />
      <AuthorPicker authors={authorFacets} value={owners} onChange={onOwnersChange} className="w-[11rem]" />
      {/* Creating from the Shared scope would drop the new collection out of
          the list it was made from, so the action is not offered there; nor
          to a persona that only reads what is shared with it (#2225). */}
      {createButton}
      <SortControl
        value={sort}
        onChange={onSortChange}
        options={COLLECTION_SORT_OPTIONS}
        disabled={sortDisabled}
      />
      <SegmentedControl
        label="List layout"
        value={viewMode}
        onChange={onViewModeChange}
        options={VIEW_OPTIONS}
      />
    </div>
  );
}
