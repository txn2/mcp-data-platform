import { LayoutGrid, List } from "lucide-react";
import { FilterSelect } from "@/components/patterns/FilterSelect";
import { SearchInput } from "@/components/patterns/SearchInput";
import { SegmentedControl } from "@/components/patterns/SegmentedControl";
import { SortControl } from "@/components/patterns/SortControl";
import { ScopeFilter, type Scope } from "@/components/ScopeFilter";
import { Input } from "@/components/ui/input";
import { ASSET_SORT_OPTIONS, type AssetSortKey, type ListSort } from "@/components/listSort";
import type { ViewMode } from "@/components/listView";

/** The content types assets are actually saved as, as a filter facet. */
const CONTENT_TYPES = [
  { value: "", label: "All types" },
  { value: "text/html", label: "HTML" },
  { value: "text/jsx", label: "JSX" },
  { value: "image/svg+xml", label: "SVG" },
  { value: "text/markdown", label: "Markdown" },
  { value: "text/csv", label: "CSV" },
];

const VIEW_OPTIONS = [
  { value: "grid" as const, label: "Grid view", icon: LayoutGrid },
  { value: "table" as const, label: "Table view", icon: List },
];

/** Everything that narrows the Assets list, in one row above it. */
export function AssetFilterBar({
  scope,
  onScopeChange,
  search,
  onSearchChange,
  contentType,
  onContentTypeChange,
  tag,
  onTagChange,
  sort,
  onSortChange,
  sortDisabled,
  viewMode,
  onViewModeChange,
}: {
  scope: Scope;
  onScopeChange: (scope: Scope) => void;
  search: string;
  onSearchChange: (search: string) => void;
  contentType: string;
  onContentTypeChange: (contentType: string) => void;
  tag: string;
  onTagChange: (tag: string) => void;
  sort: ListSort<AssetSortKey>;
  onSortChange: (sort: ListSort<AssetSortKey>) => void;
  /** True while relevance ranking, not a column, decides the order. */
  sortDisabled: boolean;
  viewMode: ViewMode;
  onViewModeChange: (mode: ViewMode) => void;
}) {
  return (
    <div className="flex flex-wrap items-center gap-3">
      <ScopeFilter value={scope} onChange={onScopeChange} />
      <SearchInput
        className="min-w-[200px] flex-1"
        value={search}
        onChange={(e) => onSearchChange(e.target.value)}
        // Relevance ranking is a Mine-scope feature; say so in the affordance
        // rather than letting the other scopes promise a search they downgrade.
        placeholder={scope === "mine" ? "Search assets by meaning..." : "Search assets..."}
      />
      <FilterSelect
        label="Filter by content type"
        value={contentType}
        onChange={onContentTypeChange}
        options={CONTENT_TYPES}
        className="w-[9.5rem]"
      />
      <Input
        type="text"
        value={tag}
        onChange={(e) => onTagChange(e.target.value)}
        aria-label="Filter by tag"
        placeholder="Filter by tag..."
        className="w-[10rem]"
      />
      <SortControl
        value={sort}
        onChange={onSortChange}
        options={ASSET_SORT_OPTIONS}
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
