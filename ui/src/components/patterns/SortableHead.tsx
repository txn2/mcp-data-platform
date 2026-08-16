import { ChevronDown, ChevronsUpDown, ChevronUp } from "lucide-react";
import { TableHead } from "@/components/ui/table";
import { cn } from "@/lib/utils";

// SortableHead is a `ui/table` column header that sorts on click: the label,
// the current direction when this is the sorted column, and the neutral
// up/down affordance when it is not. Every sortable table states its ordering
// this way so a header's meaning does not change from list to list.
export function SortableHead<K extends string>({
  label,
  sortKey,
  sortBy,
  sortDir,
  onSort,
  className,
}: {
  label: string;
  sortKey: K;
  // null when nothing is sorted yet. A caller must not stand a sentinel string
  // in for that: a table whose keys are data (a CSV's own column names) can
  // hold any string, including "", and the sentinel would then mark a real
  // column as sorted.
  sortBy: K | null;
  sortDir: "asc" | "desc";
  // Omitted when this list cannot be reordered right now — a relevance search
  // ranks its rows, so a header that still offered to sort them would be
  // claiming an ordering the rows do not have. The column then reads as a
  // plain header rather than a dead affordance.
  onSort?: (key: K) => void;
  className?: string;
}) {
  if (!onSort) {
    return <TableHead className={cn("text-muted-foreground", className)}>{label}</TableHead>;
  }
  const active = sortBy === sortKey;
  const Chevron = active ? (sortDir === "asc" ? ChevronUp : ChevronDown) : ChevronsUpDown;
  return (
    <TableHead
      onClick={() => onSort(sortKey)}
      className={cn(
        "cursor-pointer text-muted-foreground select-none hover:bg-muted/80",
        className,
      )}
    >
      <span className="inline-flex items-center gap-1">
        {label}
        <Chevron className={cn("size-3", active ? "text-foreground" : "text-muted-foreground/50")} />
      </span>
    </TableHead>
  );
}
