import { FilterSelect } from "@/components/patterns/FilterSelect";
import { PhoneFilterBar, type ActiveFilter } from "@/components/patterns/PhoneFilterBar";
import { SearchInput } from "@/components/patterns/SearchInput";
import { SegmentedControl } from "@/components/patterns/SegmentedControl";
import { SortControl } from "@/components/patterns/SortControl";
import { AuthorPicker, tagOptions } from "@/components/patterns/AuthorPicker";
import { ScopeFilter } from "@/components/ScopeFilter";
import type { AssetStatusFilter } from "@/api/portal/hooks/assets";
import { ASSET_SORT_OPTIONS } from "@/components/listSort";
import { CONTENT_TYPES, STATUS_OPTIONS, VIEW_OPTIONS, labelOf } from "./assetFilterOptions";
import type { AssetFilterBarProps } from "./AssetFilterBar";

/** The filters in force, each a chip that clears it. */
function activeFilters(p: AssetFilterBarProps): ActiveFilter[] {
  const out: ActiveFilter[] = [];
  if (p.contentType) out.push({ key: "type", label: `Type: ${labelOf(CONTENT_TYPES, p.contentType)}`, onClear: () => p.onContentTypeChange("") });
  if (p.tag) out.push({ key: "tag", label: `Tag: ${p.tag}`, onClear: () => p.onTagChange("") });
  if (p.owners.length > 0) {
    const label = p.owners.length === 1 ? `Author: ${p.owners[0]}` : `${p.owners.length} authors`;
    out.push({ key: "owners", label, onClear: () => p.onOwnersChange([]) });
  }
  const { status, onStatusChange, verified, onVerifiedChange } = p;
  if (status && onStatusChange) out.push({ key: "status", label: `Status: ${labelOf(STATUS_OPTIONS, status)}`, onClear: () => onStatusChange("") });
  if (verified && onVerifiedChange) out.push({ key: "verified", label: "Verified only", onClear: () => onVerifiedChange(false) });
  return out;
}

/** The sheet's closing button: the count of matches when it is known. */
function showLabel(count?: number): string {
  if (count === undefined) return "Done";
  return `Show ${count} ${count === 1 ? "asset" : "assets"}`;
}

/** The Assets filter bar on a phone (#2723). */
export function PhoneAssetFilterBar(p: AssetFilterBarProps) {
  return (
    <PhoneFilterBar
      scope={<ScopeFilter value={p.scope} onChange={p.onScopeChange} />}
      search={<SearchInput value={p.search} onChange={(e) => p.onSearchChange(e.target.value)} placeholder="Search assets" />}
      active={activeFilters(p)}
      showLabel={showLabel(p.resultCount)}
      sort={<SortControl value={p.sort} onChange={p.onSortChange} options={ASSET_SORT_OPTIONS} disabled={p.sortDisabled} />}
      view={<SegmentedControl label="List layout" value={p.viewMode} onChange={p.onViewModeChange} options={VIEW_OPTIONS} />}
      filters={<PhoneAssetFilters {...p} />}
    />
  );
}

/** What the Filters sheet holds, each control full width. */
function PhoneAssetFilters(p: AssetFilterBarProps) {
  const { status = "", onStatusChange, verified = false, onVerifiedChange } = p;
  return (
    <>
      <FilterSelect label="Filter by content type" value={p.contentType} onChange={p.onContentTypeChange} options={CONTENT_TYPES} />
      <FilterSelect label="Filter by tag" value={p.tag} onChange={p.onTagChange} options={tagOptions(p.tagFacets, p.tag)} />
      <AuthorPicker authors={p.authorFacets} value={p.owners} onChange={p.onOwnersChange} className="w-full" />
      {onStatusChange && (
        <FilterSelect label="Filter by status" value={status} onChange={(v) => onStatusChange(v as AssetStatusFilter | "")} options={STATUS_OPTIONS} />
      )}
      {onVerifiedChange && (
        <label className="flex min-h-11 items-center gap-3 text-sm" data-testid="verified-only">
          <input type="checkbox" className="size-5" checked={verified} onChange={(e) => onVerifiedChange(e.target.checked)} />
          Verified only
        </label>
      )}
    </>
  );
}
