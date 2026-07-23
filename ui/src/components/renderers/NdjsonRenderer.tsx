import { lazy, Suspense, useMemo, useState } from "react";
import { ChevronRight, ChevronDown, AlertTriangle } from "lucide-react";
import type { JsonValue } from "./json/model";

const JsonRenderer = lazy(() => import("./JsonRenderer").then((m) => ({ default: m.JsonRenderer })));

interface NdjsonRendererProps {
  content: string;
  fileName?: string;
}

/** Records parsed at once; beyond this the tail is left for the raw view. */
const MAX_RECORDS = 5000;

interface Record_ {
  line: number;
  raw: string;
  value: JsonValue | null;
  error: string | null;
}

/**
 * Newline-delimited JSON viewer: one expandable row per record.
 *
 * NDJSON is a stream of independent documents, so it reads as a list rather
 * than as one tree. Each row shows a one-line summary of its record and expands
 * into the full JSON viewer for that record alone, which keeps a file of
 * thousands of events navigable.
 */
export function NdjsonRenderer({ content, fileName }: NdjsonRendererProps) {
  const { records, truncated } = useMemo(() => parseRecords(content), [content]);
  const [expanded, setExpanded] = useState<number | null>(null);

  if (records.length === 0) {
    return (
      <pre className="overflow-auto whitespace-pre-wrap rounded-lg border bg-card p-6 text-sm" data-feedback-anchorable>
        {content}
      </pre>
    );
  }

  return (
    <div className="space-y-2" data-feedback-anchorable>
      <p className="text-xs text-muted-foreground">
        {records.length} record{records.length === 1 ? "" : "s"}
        {truncated && " (first portion of a larger file)"}
      </p>

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
    </div>
  );
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
