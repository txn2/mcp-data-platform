import { ArrowDownWideNarrow, ArrowUpNarrowWide } from "lucide-react";
import { FilterSelect, type FilterOption } from "@/components/patterns/FilterSelect";
import { Button } from "@/components/ui/button";
import { defaultDirFor, type ListSort, type SortDir } from "@/components/listSort";
import { cn } from "@/lib/utils";

// SortControl is the ordering facet of a filter bar: which column, then which
// way. The two halves are separate controls because reversing an ordering
// should not make a reader re-pick the column it is on.
//
// It is a select rather than a set of clickable table headers because it has to
// state the ordering in the gallery view too, where there are no headers to
// click. Where a table is showing, its headers drive this same state — so
// picking a column here adopts that column's own default direction, exactly as
// clicking its header would. Two controls over one state that disagreed about
// what a column means would make the list's order depend on which was used.
export function SortControl<K extends string>({
  value,
  onChange,
  options,
  disabled = false,
  className,
}: {
  value: ListSort<K>;
  onChange: (sort: ListSort<K>) => void;
  options: FilterOption[];
  // Set when the list is ordered by something this control does not choose
  // (a relevance search), so it reads as inert rather than as an ordering that
  // silently does nothing.
  disabled?: boolean;
  className?: string;
}) {
  const ascending = value.dir === "asc";
  const Icon = ascending ? ArrowUpNarrowWide : ArrowDownWideNarrow;
  const nextDir: SortDir = ascending ? "desc" : "asc";
  return (
    <div className={cn("flex shrink-0 items-center gap-1", className)}>
      <FilterSelect
        label="Sort by"
        title="Order the list by this column"
        value={value.key}
        onChange={(key) =>
          onChange(key === value.key ? value : { key: key as K, dir: defaultDirFor(key) })
        }
        options={options}
        disabled={disabled}
        className="w-[7.5rem]"
      />
      <Button
        type="button"
        variant="outline"
        size="icon-sm"
        disabled={disabled}
        // The arrow states the order the rows are in; the label states what
        // pressing it does, so the two never claim the same thing.
        aria-label={ascending ? "Sorted ascending, sort descending" : "Sorted descending, sort ascending"}
        title={ascending ? "Ascending" : "Descending"}
        onClick={() => onChange({ ...value, dir: nextDir })}
      >
        <Icon />
      </Button>
    </div>
  );
}
