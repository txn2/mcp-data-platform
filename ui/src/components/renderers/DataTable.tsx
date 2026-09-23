import { useMemo, useState, type ReactNode } from "react";
import { Table, TableBody, TableCell, TableHeader, TableRow } from "@/components/ui/table";
import { SearchInput } from "@/components/patterns/SearchInput";
import { SortableHead } from "@/components/patterns/SortableHead";
import { RowDetailDialog } from "./RowDetailDialog";
import { cellText, compareCells } from "./cellText";

/** Rows drawn at once; the footer says how many there are. */
export const MAX_DISPLAY_ROWS = 500;

/** Enter and Space open a row, the way they open a button. */
function openOnEnterOrSpace(e: React.KeyboardEvent, open: () => void) {
  if (e.key !== "Enter" && e.key !== " ") {
    return;
  }
  e.preventDefault();
  open();
}

export interface DataTableProps {
  columns: string[];
  rows: Record<string, unknown>[];
  /** Controls beside the search box, such as a download button. */
  actions?: ReactNode;
  /** What follows the row count in the footer. */
  footer?: ReactNode;
  /** The label of the search box, which names what is searched. */
  searchLabel?: string;
}

/**
 * The table every tabular viewer draws: a search over all columns, sortable
 * headers, a row cap, and a row that opens into the dialog where its values
 * are read in full. The CSV viewer, the JSON-lines table view and the Parquet
 * grid are one table so they cannot drift apart (#1833).
 */
export function DataTable({ columns, rows, actions, footer, searchLabel = "Search all columns" }: DataTableProps) {
  const [sortColumn, setSortColumn] = useState<string | null>(null);
  const [sortDirection, setSortDirection] = useState<"asc" | "desc">("asc");
  const [filterText, setFilterText] = useState("");
  // Which row is open, as an index into the rows on screen.
  const [openRow, setOpenRow] = useState<number | null>(null);

  const filtered = useMemo(() => {
    if (!filterText) return rows;
    const lower = filterText.toLowerCase();
    return rows.filter((row) => columns.some((col) => cellText(row[col]).toLowerCase().includes(lower)));
  }, [rows, columns, filterText]);

  const sorted = useMemo(() => {
    if (!sortColumn) return filtered;
    const col = sortColumn;
    const dir = sortDirection === "asc" ? 1 : -1;
    return [...filtered].sort((a, b) => {
      const va = a[col];
      const vb = b[col];
      if (va == null && vb == null) return 0;
      if (va == null) return dir;
      if (vb == null) return -dir;
      return compareCells(va, vb) * dir;
    });
  }, [filtered, sortColumn, sortDirection]);

  const displayRows = sorted.slice(0, MAX_DISPLAY_ROWS);

  function handleSort(col: string) {
    if (sortColumn === col) {
      setSortDirection((d) => (d === "asc" ? "desc" : "asc"));
    } else {
      setSortColumn(col);
      setSortDirection("asc");
    }
  }

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between gap-3">
        <SearchInput
          className="max-w-sm flex-1"
          value={filterText}
          onChange={(e) => setFilterText(e.target.value)}
          placeholder={`${searchLabel}...`}
          aria-label={searchLabel}
        />
        {actions}
      </div>

      {/* `overflow-hidden` on the frame, not the scroller: ui/table brings its
          own horizontal scroll container, and without the frame clipping, the
          header fill squares off the rounded corners. */}
      <div className="overflow-hidden rounded-lg border bg-card">
        <Table>
          {/* Lighter than SortableHead's `hover:bg-muted/80`, or hovering a
              sortable header would paint the fill it already carries. */}
          <TableHeader className="bg-muted/40">
            <TableRow>
              {columns.map((col) => (
                <SortableHead
                  key={col}
                  label={col}
                  sortKey={col}
                  sortBy={sortColumn}
                  sortDir={sortDirection}
                  onSort={handleSort}
                />
              ))}
            </TableRow>
          </TableHeader>
          <TableBody>
            {displayRows.map((row, i) => (
              // The row is the control that opens the record, which is how
              // every other list in the portal behaves, and it is focusable
              // and operable from the keyboard because a body of hundreds of
              // rows is otherwise unreachable without a pointer (#1781).
              <TableRow
                key={i}
                className="cursor-pointer even:bg-muted/20 focus-visible:outline focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-ring"
                tabIndex={0}
                role="button"
                aria-label={`Open row ${i + 1}`}
                onClick={() => setOpenRow(i)}
                onKeyDown={(e) => openOnEnterOrSpace(e, () => setOpenRow(i))}
              >
                {columns.map((col) => {
                  // Truncated on purpose: the table is for finding a row, and
                  // the dialog is for reading it. The title is a convenience
                  // for a short value, never the way to read a long one.
                  const text = cellText(row[col]);
                  return (
                    <TableCell key={col} className="max-w-[200px] truncate" title={text}>
                      {text}
                    </TableCell>
                  );
                })}
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>

      <OpenRow columns={columns} rows={displayRows} at={openRow} onGo={setOpenRow} />

      <p className="text-xs text-muted-foreground">
        Showing {displayRows.length} of {rows.length} rows
        {filtered.length < rows.length && ` (${filtered.length} matching filter)`}
      </p>
      {footer}
    </div>
  );
}

/**
 * The row dialog, and the arithmetic of moving between rows.
 *
 * It sits here rather than inline so the table keeps one job: the guard for
 * "no row is open" and the bounds on previous and next are all the same
 * question -- which row, if any -- and they belong together.
 */
function OpenRow({
  columns,
  rows,
  at,
  onGo,
}: {
  columns: string[];
  rows: Record<string, unknown>[];
  at: number | null;
  onGo: (at: number | null) => void;
}) {
  if (at === null || !rows[at]) {
    return null;
  }
  return (
    <RowDetailDialog
      columns={columns}
      row={rows[at]}
      position={at + 1}
      total={rows.length}
      onPrev={at > 0 ? () => onGo(at - 1) : undefined}
      onNext={at < rows.length - 1 ? () => onGo(at + 1) : undefined}
      onClose={() => onGo(null)}
    />
  );
}
