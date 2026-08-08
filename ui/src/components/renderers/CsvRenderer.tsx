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

function isNumeric(val: unknown): val is number {
  return typeof val === "number" && !isNaN(val);
}

export function CsvRenderer({ content, fileName, delimiter = "," }: Props) {
  const isTsv = delimiter === "\t";
  const downloadName = fileName || (isTsv ? "data.tsv" : "data.csv");
  const [sortColumn, setSortColumn] = useState<string | null>(null);
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
      columns.some((col) => String(row[col] ?? "").toLowerCase().includes(lower)),
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
              <TableRow key={i} className="even:bg-muted/20">
                {columns.map((col) => (
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

      {/* Footer */}
      <p className="text-xs text-muted-foreground">
        Showing {displayRows.length} of {allRows.length} rows
        {filtered.length < allRows.length && ` (${filtered.length} matching filter)`}
      </p>
    </div>
  );
}
