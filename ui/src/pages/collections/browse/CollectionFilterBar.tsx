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
import { Button } from "@/components/ui/button";

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
  sort: ListSort<CollectionSortKey>;
  onSortChange: (sort: ListSort<CollectionSortKey>) => void;
  /** True while relevance ranking, not a column, decides the order. */
  sortDisabled: boolean;
  viewMode: ViewMode;
  onViewModeChange: (mode: ViewMode) => void;
  onCreate: () => void;
  creating: boolean;
}) {
  return (
    <div className="flex flex-wrap items-center gap-3">
      <ScopeFilter value={scope} onChange={onScopeChange} />
      <SearchInput
        className="min-w-[200px] flex-1"
        value={search}
        onChange={(e) => onSearchChange(e.target.value)}
        placeholder={
          scope === "mine" ? "Search collections by meaning..." : "Search collections..."
        }
      />
      {/* Creating from the Shared scope would drop the new collection out of
          the list it was made from, so the action is not offered there. */}
      {scope !== "shared" && (
        <Button onClick={onCreate} disabled={creating}>
          <Plus />
          {creating ? "Creating..." : "New Collection"}
        </Button>
      )}
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
