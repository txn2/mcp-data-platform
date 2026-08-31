import { FileUp, LayoutGrid, List } from "lucide-react";
import { FilterSelect, type FilterOption } from "@/components/patterns/FilterSelect";
import { SearchInput } from "@/components/patterns/SearchInput";
import { SegmentedControl } from "@/components/patterns/SegmentedControl";
import { Button } from "@/components/ui/button";
import type { ViewMode } from "@/components/listView";
import type { LibraryChoice } from "../scopes";
import type { ResourceSort } from "./libraryUrl";

/** How the library orders its files, for the administrator who curates one. */
const SORT_OPTIONS: FilterOption[] = [
  { value: "updated", label: "Recently updated" },
  { value: "last_read", label: "Recently read" },
];

const VIEW_OPTIONS = [
  { value: "grid" as const, label: "Grid view", icon: LayoutGrid },
  { value: "table" as const, label: "Table view", icon: List },
];

/**
 * Everything that narrows the library, in one row above it: which library,
 * what in it, and how it is drawn.
 *
 * The library is a listbox rather than a strip of tabs. An administrator's
 * strip carried a face per persona the deployment defines and ran off the edge
 * of the bar; a reader's carried three. One control that says which library is
 * open is the same control at both ends of that range (#1553).
 */
export function ResourceFilterBar({
  libraries,
  activeLibrary,
  onLibraryChange,
  search,
  onSearchChange,
  tag,
  onTagChange,
  tagOptions,
  sort,
  onSortChange,
  showSort,
  viewMode,
  onViewModeChange,
  canUpload,
  onUpload,
  readOnlyNote,
}: {
  libraries: LibraryChoice[];
  activeLibrary: string;
  onLibraryChange: (key: string) => void;
  search: string;
  onSearchChange: (value: string) => void;
  tag: string;
  onTagChange: (tag: string) => void;
  tagOptions: FilterOption[];
  sort: ResourceSort;
  onSortChange: (sort: ResourceSort) => void;
  /** The ordering facet, offered on the administrator's curation page only. */
  showSort: boolean;
  viewMode: ViewMode;
  onViewModeChange: (mode: ViewMode) => void;
  canUpload: boolean;
  onUpload: () => void;
  /**
   * Where this library's material comes from, set only when the caller may not
   * add to it. It replaces the Upload control rather than sitting beside it, so
   * the bar cannot offer an upload and name a publisher at once.
   */
  readOnlyNote?: string;
}) {
  return (
    <div className="flex flex-wrap items-center gap-3">
      <FilterSelect
        label="Library"
        title="Which library is open"
        value={activeLibrary}
        onChange={onLibraryChange}
        options={libraries.map((l) => ({ value: l.key, label: l.label }))}
        className="w-[10rem]"
      />
      <SearchInput
        className="min-w-[200px] flex-1"
        value={search}
        onChange={(e) => onSearchChange(e.target.value)}
        placeholder="Search the whole library..."
        aria-label="Search resources"
      />
      <FilterSelect
        label="Filter by tag"
        value={tag}
        onChange={onTagChange}
        options={tagOptions}
        disabled={tagOptions.length === 1}
        className="w-[9.5rem]"
      />
      {showSort && (
        <FilterSelect
          label="Sort resources"
          value={sort}
          onChange={(v) => onSortChange(v as ResourceSort)}
          options={SORT_OPTIONS}
          className="w-[10rem]"
        />
      )}
      <SegmentedControl
        label="List layout"
        value={viewMode}
        onChange={onViewModeChange}
        options={VIEW_OPTIONS}
      />
      {canUpload ? (
        <Button onClick={onUpload}>
          <FileUp />
          Upload
        </Button>
      ) : (
        readOnlyNote && (
          <p data-testid="scope-read-only" className="text-xs text-muted-foreground">
            {readOnlyNote}
          </p>
        )
      )}
    </div>
  );
}
