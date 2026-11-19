import { FilterSelect } from "@/components/patterns/FilterSelect";
import { SearchInput } from "@/components/patterns/SearchInput";
import { SegmentedControl } from "@/components/patterns/SegmentedControl";
import { SortControl } from "@/components/patterns/SortControl";
import { ScopeFilter, type Scope } from "@/components/ScopeFilter";
import { AuthorPicker, tagOptions } from "@/components/patterns/AuthorPicker";
import type { FacetCount } from "@/api/portal/types";
import type { AssetStatusFilter } from "@/api/portal/hooks/assets";
import { ASSET_SORT_OPTIONS, type AssetSortKey, type ListSort } from "@/components/listSort";
import type { ViewMode } from "@/components/listView";
import { useIsMobile } from "@/hooks/useIsMobile";
import { CONTENT_TYPES, STATUS_OPTIONS, VIEW_OPTIONS } from "./assetFilterOptions";
import { PhoneAssetFilterBar } from "./PhoneAssetFilterBar";

export interface AssetFilterBarProps {
  scope: Scope;
  onScopeChange: (scope: Scope) => void;
  search: string;
  onSearchChange: (search: string) => void;
  contentType: string;
  onContentTypeChange: (contentType: string) => void;
  tag: string;
  onTagChange: (tag: string) => void;
  tagFacets: FacetCount[];
  owners: string[];
  onOwnersChange: (owners: string[]) => void;
  authorFacets: FacetCount[];
  /** The Status filter (#2502): a status, "none", or "" for any. */
  status?: AssetStatusFilter | "";
  onStatusChange?: (status: AssetStatusFilter | "") => void;
  /** Verified only (#2503). */
  verified?: boolean;
  onVerifiedChange?: (verified: boolean) => void;
  sort: ListSort<AssetSortKey>;
  onSortChange: (sort: ListSort<AssetSortKey>) => void;
  /** True while relevance ranking, not a column, decides the order. */
  sortDisabled: boolean;
  viewMode: ViewMode;
  onViewModeChange: (mode: ViewMode) => void;
  /** How many assets the filters match, for the phone sheet's button. */
  resultCount?: number;
}

/** Everything that narrows the Assets list: one row above it, and on a phone
 *  the scope and search with the rest folded behind Filters (#2723). */
export function AssetFilterBar(props: AssetFilterBarProps) {
  return useIsMobile() ? <PhoneAssetFilterBar {...props} /> : <WideAssetFilterBar {...props} />;
}

function WideAssetFilterBar({
  scope,
  onScopeChange,
  search,
  onSearchChange,
  contentType,
  onContentTypeChange,
  tag,
  onTagChange,
  tagFacets,
  owners,
  onOwnersChange,
  authorFacets,
  status = "",
  onStatusChange,
  verified = false,
  onVerifiedChange,
  sort,
  onSortChange,
  sortDisabled,
  viewMode,
  onViewModeChange,
}: AssetFilterBarProps) {
  return (
    <div className="flex flex-wrap items-center gap-3">
      <ScopeFilter value={scope} onChange={onScopeChange} />
      <SearchInput
        className="min-w-[200px] flex-1"
        value={search}
        onChange={(e) => onSearchChange(e.target.value)}
        // Every scope is ranked by meaning on the server (#2500).
        placeholder="Search assets by meaning..."
      />
      <FilterSelect
        label="Filter by content type"
        value={contentType}
        onChange={onContentTypeChange}
        options={CONTENT_TYPES}
        className="w-[9.5rem]"
      />
      <FilterSelect
        label="Filter by tag"
        value={tag}
        onChange={onTagChange}
        options={tagOptions(tagFacets, tag)}
        className="w-[10rem]"
      />
      <AuthorPicker authors={authorFacets} value={owners} onChange={onOwnersChange} className="w-[11rem]" />
      {onStatusChange && (
        <FilterSelect
          label="Filter by status"
          value={status}
          onChange={(v) => onStatusChange(v as AssetStatusFilter | "")}
          options={STATUS_OPTIONS}
          className="w-[9.5rem]"
        />
      )}
      {onVerifiedChange && (
        <label className="flex h-9 items-center gap-2 rounded-md border px-3 text-sm" data-testid="verified-only">
          <input type="checkbox" checked={verified} onChange={(e) => onVerifiedChange(e.target.checked)} />
          Verified only
        </label>
      )}
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

