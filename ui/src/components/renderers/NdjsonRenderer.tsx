import { lazy, Suspense, useMemo, useState } from "react";
import { ChevronRight, ChevronDown, AlertTriangle } from "lucide-react";
import { Button } from "@/components/ui/button";
import type { JsonValue } from "./json/model";
import { DataTable } from "./DataTable";

const JsonRenderer = lazy(() => import("./JsonRenderer").then((m) => ({ default: m.JsonRenderer })));

interface NdjsonRendererProps {
  content: string;
  fileName?: string;
}

/** Records parsed at once; beyond this the tail is left for the raw view. */
const MAX_RECORDS = 5000;

export interface Record_ {
  line: number;
  raw: string;
  value: JsonValue | null;
  error: string | null;
}

type View = "table" | "records";

/**
 * Newline-delimited JSON viewer, as a table or as a list of records.
 *
 * A JSON-lines file is two different things depending on who wrote it. An
 * export -- every trino_export and platform.export in format jsonl -- is a
 * table: one record per row, every record with the same keys. An event log
 * is a stream of independent documents of different shapes. The Table view
 * reads the first as the table it is (#1833), with the union of keys as its
 * columns in the order they are first seen; the Records view reads the second,
 * one expandable row per record opening into the JSON viewer for that record
 * alone. Which one opens first is decided by the records themselves.
 */
export function NdjsonRenderer({ content, fileName }: NdjsonRendererProps) {
  const { records, truncated } = useMemo(() => parseRecords(content), [content]);
  const table = useMemo(() => tableOf(records), [records]);
  const [view, setView] = useState<View>(() => (table.tabular ? "table" : "records"));

  if (records.length === 0) {
    return (
      <pre className="overflow-auto whitespace-pre-wrap rounded-lg border bg-card p-6 text-sm" data-feedback-anchorable>
        {content}
      </pre>
    );
  }

  return (
    <div className="space-y-2" data-feedback-anchorable>
      <div className="flex items-center justify-between gap-3">
        <p className="text-xs text-muted-foreground">
          {records.length} record{records.length === 1 ? "" : "s"}
          {truncated && " (first portion of a larger file)"}
        </p>
        {table.columns.length > 0 && (
          <div className="flex gap-1" role="group" aria-label="View">
            {(["table", "records"] as const).map((v) => (
              <Button
                key={v}
                type="button"
                size="sm"
                variant={view === v ? "secondary" : "ghost"}
                aria-pressed={view === v}
                onClick={() => setView(v)}
              >
                {v === "table" ? "Table" : "Records"}
              </Button>
            ))}
          </div>
        )}
      </div>

      {view === "table" && table.columns.length > 0 ? (
        <DataTable
          columns={table.columns}
          rows={table.rows}
          footer={
            table.skipped > 0 ? (
              <p className="text-xs text-muted-foreground">
                {table.skipped} line{table.skipped === 1 ? " is" : "s are"} not a JSON object and{" "}
                {table.skipped === 1 ? "has" : "have"} no row here; see Records.
              </p>
            ) : undefined
          }
        />
      ) : (
        <RecordList records={records} fileName={fileName} />
      )}
    </div>
  );
}

/** The Records view: one expandable row per line. */
function RecordList({ records, fileName }: { records: Record_[]; fileName?: string }) {
  const [expanded, setExpanded] = useState<number | null>(null);
  return (
    <div className="divide-y rounded-lg border bg-card">
      {records.map((rec) => (
        <div key={rec.line}>
          <button
            type="button"
            onClick={() => setExpanded((cur) => (cur === rec.line ? null : rec.line))}
            aria-expanded={expanded === rec.line}
            className="flex w-full items-center gap-2 px-3 py-1.5 text-left font-mono text-xs hover:bg-accent/40"
          >
            {expanded === rec.line ? (
              <ChevronDown className="h-3 w-3 shrink-0" />
            ) : (
              <ChevronRight className="h-3 w-3 shrink-0" />
            )}
            <span className="w-12 shrink-0 tabular-nums text-muted-foreground">{rec.line}</span>
            {rec.error ? (
              <span className="flex items-center gap-1 text-amber-600 dark:text-amber-400">
                <AlertTriangle className="h-3 w-3" />
                {rec.error}
              </span>
            ) : (
              <span className="min-w-0 flex-1 truncate">{rec.raw}</span>
            )}
          </button>

          {expanded === rec.line && !rec.error && (
            <div className="border-t bg-muted/20 p-3">
              <Suspense fallback={<p className="text-xs text-muted-foreground">Loading...</p>}>
                <JsonRenderer content={rec.raw} fileName={fileName} />
              </Suspense>
            </div>
          )}
        </div>
      ))}
    </div>
  );
}

/** What the Table view is drawn from, and whether it is the view to open on. */
export interface NdjsonTable {
  columns: string[];
  rows: Record<string, unknown>[];
  /** True when the records read as one table: see tableOf. */
  tabular: boolean;
  /** Lines that are not a JSON object, so they have no row in the table. */
  skipped: number;
}

/**
 * The share of the columns a record has to carry, on average, for the file to
 * open as a table. An export's records carry every column; an event log's
 * records carry a few keys each out of a wide union, and read better as
 * records.
 */
const TABULAR_COVERAGE = 0.6;

/**
 * Builds the Table view: the union of every object record's keys, in the order
 * first seen, matched without regard to case the way a table registered over
 * the file matches them (tablejsonl), and shown as first spelled.
 *
 * The file opens as a table when every line is an object, no line failed to
 * parse, no value nests deeper than one level (a record of scalars, or of
 * scalars and flat lists or objects), and the records carry most of the
 * columns between them. Anything else opens on Records.
 */
export function tableOf(records: Record_[]): NdjsonTable {
  const columns = new ColumnSet();
  const rows: Record<string, unknown>[] = [];
  let allObjects = records.length > 0;
  let shallow = true;
  let carried = 0;
  for (const rec of records) {
    const obj = rec.error ? null : asObject(rec.value);
    if (!obj) {
      allObjects = false;
      continue;
    }
    const row = columns.row(obj);
    shallow = shallow && Object.values(row).every((v) => depth(v) <= 1);
    carried += Object.keys(row).length;
    rows.push(row);
  }
  const width = columns.names.length;
  const coverage = rows.length > 0 && width > 0 ? carried / (rows.length * width) : 0;
  return {
    columns: columns.names,
    rows,
    tabular: allObjects && shallow && coverage >= TABULAR_COVERAGE,
    skipped: records.length - rows.length,
  };
}

/** The union of the records' keys, matched without regard to case, in first-seen order. */
class ColumnSet {
  names: string[] = [];
  private byLower = new Map<string, string>();

  /** Keys a record by the column each key belongs to, adding the columns it is the first to name. */
  row(obj: Record<string, JsonValue>): Record<string, unknown> {
    const row: Record<string, unknown> = {};
    for (const [key, value] of Object.entries(obj)) {
      const lower = key.toLowerCase();
      let name = this.byLower.get(lower);
      if (name === undefined) {
        name = key;
        this.byLower.set(lower, key);
        this.names.push(key);
      }
      row[name] = value;
    }
    return row;
  }
}

function asObject(value: JsonValue | null): Record<string, JsonValue> | null {
  if (value === null || typeof value !== "object" || Array.isArray(value)) return null;
  return value as Record<string, JsonValue>;
}

/** How deeply a value nests: 0 for a scalar, 1 for a list or object of scalars. */
function depth(value: unknown): number {
  if (value === null || typeof value !== "object") return 0;
  const children = Array.isArray(value) ? value : Object.values(value as Record<string, unknown>);
  let deepest = 0;
  for (const child of children) {
    deepest = Math.max(deepest, depth(child));
    if (deepest > 1) break;
  }
  return deepest + 1;
}

function parseRecords(content: string): { records: Record_[]; truncated: boolean } {
  const lines = content.split("\n");
  const records: Record_[] = [];
  let truncated = false;

  for (let i = 0; i < lines.length; i++) {
    const raw = (lines[i] ?? "").trim();
    if (raw === "") continue;
    if (records.length >= MAX_RECORDS) {
      truncated = true;
      break;
    }
    try {
      records.push({ line: i + 1, raw, value: JSON.parse(raw) as JsonValue, error: null });
    } catch (err) {
      records.push({
        line: i + 1,
        raw,
        value: null,
        error: err instanceof Error ? err.message : "Invalid JSON on this line",
      });
    }
  }
  return { records, truncated };
}
