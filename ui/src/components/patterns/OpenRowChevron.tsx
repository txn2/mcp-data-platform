import { ChevronRight } from "lucide-react";
import { TableCell, TableHead } from "@/components/ui/table";

// OpenRowChevron is the last cell of a row that opens its record
// (lib/openRow): the chevron that says the row goes somewhere, brighter on
// hover and focus.
export function OpenRowChevron() {
  return (
    <TableCell className="w-8 pr-3 align-middle">
      <ChevronRight
        aria-hidden
        className="ml-auto h-4 w-4 text-muted-foreground transition-transform group-hover:translate-x-0.5 group-hover:text-foreground group-focus-visible:text-foreground"
      />
    </TableCell>
  );
}

// OpenRowHead is the chevron column's header, read by a screen reader only.
export function OpenRowHead() {
  return (
    <TableHead className="w-8">
      <span className="sr-only">Open</span>
    </TableHead>
  );
}
