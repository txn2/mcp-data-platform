import { useState, useMemo, useCallback } from "react";
import Papa from "papaparse";
import { Download } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  Table,
  TableBody,
  TableCell,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { SearchInput } from "@/components/patterns/SearchInput";
import { SortableHead } from "@/components/patterns/SortableHead";
import { RowDetailDialog } from "./RowDetailDialog";

interface Props {
  content: string;
  fileName?: string;
  /**
   * Field delimiter. Defaults to a comma; the registry passes a tab for
   * text/tab-separated-values, which is otherwise the same format and the same
   * viewer.
   */
  delimiter?: "," | "\t";
}

const MAX_DISPLAY_ROWS = 500;

/** Enter and Space open a row, the way they open a button. */
function openOnEnterOrSpace(e: React.KeyboardEvent, open: () => void) {
  if (e.key !== "Enter" && e.key !== " ") {
    return;
  }
  e.preventDefault();
  open();
}

function isNumeric(val: unknown): val is number {
  return typeof val === "number" && !isNaN(val);
}

export function CsvRenderer({ content, fileName, delimiter = "," }: Props) {
  const isTsv = delimiter === "\t";
  const downloadName = fileName || (isTsv ? "data.tsv" : "data.csv");
  const [sortColumn, setSortColumn] = useState<string | null>(null);
  // Which row is open, as an index into the rows on screen.
  const [openRow, setOpenRow] = useState<number | null>(null);
  const [sortDirection, setSortDirection] = useState<"asc" | "desc">("asc");
  const [filterText, setFilterText] = useState("");

  const parsed = useMemo(
    () =>
      Papa.parse<Record<string, unknown>>(content, {
        header: true,
        skipEmptyLines: true,
        dynamicTyping: true,
        delimiter,
      }),
    [content, delimiter],
  );

  const columns = useMemo(() => parsed.meta.fields ?? [], [parsed]);
  const allRows = parsed.data;

  const filtered = useMemo(() => {
    if (!filterText) return allRows;
    const lower = filterText.toLowerCase();
    return allRows.filter((row) =>
      columns.some((col) =>
        String(row[col] ?? "")
          .toLowerCase()
          .includes(lower),
      ),
    );
  }, [allRows, columns, filterText]);

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
      if (isNumeric(va) && isNumeric(vb)) return (va - vb) * dir;
      return String(va).localeCompare(String(vb)) * dir;
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

  const handleDownload = useCallback(() => {
    const blob = new Blob([content], {
      type: isTsv ? "text/tab-separated-values" : "text/csv",
    });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = downloadName;
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    URL.revokeObjectURL(url);
  }, [content, downloadName, isTsv]);

  if (columns.length === 0) {
    return (
      <pre className="rounded-lg border bg-card p-6 text-sm overflow-auto whitespace-pre-wrap">
        {content}
      </pre>
    );
  }

  return (
    <div className="space-y-3">
      {/* Search + Download */}
      <div className="flex items-center justify-between gap-3">
        <SearchInput
          className="max-w-sm flex-1"
          value={filterText}
          onChange={(e) => setFilterText(e.target.value)}
          placeholder="Search all columns..."
          aria-label="Search all columns"
        />
        <Button
          type="button"
          variant="outline"
          onClick={handleDownload}
          className="shrink-0"
          title={isTsv ? "Download TSV" : "Download CSV"}
        >
          <Download />
          Download
        </Button>
      </div>

      {/* Table. `overflow-hidden` on the frame, not the scroller: ui/table
          brings its own horizontal scroll container, and without the frame
          clipping, the header fill squares off the rounded corners. */}
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
                {columns.map((col) => (
                  // Truncated on purpose: the table is for finding a row, and
                  // the dialog is for reading it. The title is a convenience
                  // for a short value, never the way to read a long one.
                  <TableCell
                    key={col}
                    className="max-w-[200px] truncate"
                    title={String(row[col] ?? "")}
                  >
                    {String(row[col] ?? "")}
                  </TableCell>
                ))}
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>

      <OpenRow
        columns={columns}
        rows={displayRows}
        at={openRow}
        onGo={setOpenRow}
      />

      {/* Footer */}
      <p className="text-xs text-muted-foreground">
        Showing {displayRows.length} of {allRows.length} rows
        {filtered.length < allRows.length &&
          ` (${filtered.length} matching filter)`}
      </p>
      <ShapeNotice errors={parsed.errors} />
    </div>
  );
}

/**
 * What the parser found wrong with the file's shape, which the viewer used to
 * throw away.
 *
 * PapaParse reports a record that does not have the header's fields as a
 * `FieldMismatch`, and this component read `parsed.data` and ignored
 * `parsed.errors`. A short record was drawn with its missing columns as empty
 * cells, so a file whose exporter omits trailing fields looked complete here
 * while a table registered over it answered that its records do not all have
 * the header's fields. Nobody looking at the viewer could tell (#1779).
 *
 * It is a note rather than a warning: a short record is ordinary and registers
 * (#1779). What it must not do is go unsaid.
 */
function ShapeNotice({ errors }: { errors: Papa.ParseError[] }) {
  const short = errors.filter((e) => e.code === "TooFewFields").length;
  const long = errors.filter((e) => e.code === "TooManyFields").length;
  if (short === 0 && long === 0) {
    return null;
  }
  const parts: string[] = [];
  if (short > 0) {
    parts.push(
      `${short} ${short === 1 ? "row ends" : "rows end"} before the last column; the columns after it are shown empty`,
    );
  }
  if (long > 0) {
    parts.push(
      `${long} ${long === 1 ? "row has" : "rows have"} more fields than the header names`,
    );
  }
  return (
    <p className="text-xs text-muted-foreground" data-testid="csv-shape-notice">
      {parts.join(". ")}.
    </p>
  );
}

/**
 * The row dialog, and the arithmetic of moving between rows.
 *
 * It sits here rather than inline so the renderer keeps one job: the guard for
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
