import { useState, useMemo } from "react";
import Papa from "papaparse";
import { ChevronUp, ChevronDown, Search } from "lucide-react";

interface Props {
  content: string;
}

const MAX_DISPLAY_ROWS = 500;

function isNumeric(val: unknown): val is number {
  return typeof val === "number" && !isNaN(val);
}

export function CsvRenderer({ content }: Props) {
  const [sortColumn, setSortColumn] = useState<string | null>(null);
  const [sortDirection, setSortDirection] = useState<"asc" | "desc">("asc");
  const [filterText, setFilterText] = useState("");

  const parsed = useMemo(
    () =>
      Papa.parse<Record<string, unknown>>(content, {
        header: true,
        skipEmptyLines: true,
        dynamicTyping: true,
      }),
    [content],
  );

  const columns = parsed.meta.fields ?? [];
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

  if (columns.length === 0) {
    return (
      <pre className="rounded-lg border bg-card p-6 text-sm overflow-auto whitespace-pre-wrap">
        {content}
      </pre>
    );
  }

  return (
    <div className="space-y-3">
      {/* Search */}
      <div className="relative max-w-sm">
        <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
        <input
          type="text"
          value={filterText}
          onChange={(e) => setFilterText(e.target.value)}
          placeholder="Search all columns..."
          className="w-full rounded-md border bg-background pl-9 pr-3 py-2 text-sm outline-none ring-ring focus:ring-2"
        />
      </div>

      {/* Table */}
      <div className="rounded-lg border bg-card overflow-auto">
        <table className="w-full text-sm">
          <thead className="sticky top-0 bg-muted/80 backdrop-blur-sm">
            <tr>
              {columns.map((col) => (
                <th
                  key={col}
                  onClick={() => handleSort(col)}
                  className="px-3 py-2 text-left font-medium text-muted-foreground cursor-pointer select-none whitespace-nowrap hover:text-foreground transition-colors"
                >
                  <span className="inline-flex items-center gap-1">
                    {col}
                    {sortColumn === col &&
                      (sortDirection === "asc" ? (
                        <ChevronUp className="h-3.5 w-3.5" />
                      ) : (
                        <ChevronDown className="h-3.5 w-3.5" />
                      ))}
                  </span>
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {displayRows.map((row, i) => (
              <tr
                key={i}
                className="border-t transition-colors hover:bg-accent/30 even:bg-muted/20"
              >
                {columns.map((col) => (
                  <td
                    key={col}
                    className="px-3 py-1.5 max-w-[200px] truncate"
                    title={String(row[col] ?? "")}
                  >
                    {String(row[col] ?? "")}
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      {/* Footer */}
      <p className="text-xs text-muted-foreground">
        Showing {displayRows.length} of {allRows.length} rows
        {filtered.length < allRows.length && ` (${filtered.length} matching filter)`}
      </p>
    </div>
  );
}
