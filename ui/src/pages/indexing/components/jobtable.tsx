import { useEffect, useMemo, useState } from "react";
import { ChevronLeft, ChevronRight } from "lucide-react";
import { type IndexJob } from "@/api/admin/indexjobs";
import { relTime, failureKey } from "./helpers";
import { JobStatusChip } from "./badges";

// JobRow is a normalised job-table entry: either a single notable job or
// a collapsed summary of routine reconciler heartbeats for one unit.
interface JobRow {
  key: string;
  sourceKind: string;
  sourceID: string;
  status: string;
  trigger: string;
  attempts: number;
  updated?: string;
  error?: string;
  // routineCount > 0 marks a collapsed heartbeat summary; the value is
  // how many succeeded reconciler runs (across replicas) it stands for.
  routineCount: number;
}

// collapseJobs folds the job firehose into table rows. Timer-driven
// reconciler successes for the same unit (which every replica re-runs on
// its own schedule, producing duplicate rows) collapse to one summary
// row "synced ×N"; everything else stays an individual row. This keeps a
// kind that re-syncs on a timer from drowning the table.
function collapseJobs(jobs: IndexJob[]): JobRow[] {
  const routine = new Map<string, JobRow>();
  const rows: JobRow[] = [];
  for (const j of jobs) {
    const updated = j.completed_at ?? j.started_at ?? j.created_at;
    if (j.status === "succeeded" && j.trigger === "reconciler") {
      const k = failureKey(j.source_kind, j.source_id);
      const cur = routine.get(k);
      if (!cur) {
        routine.set(k, {
          key: `routine:${k}`,
          sourceKind: j.source_kind,
          sourceID: j.source_id,
          status: "succeeded",
          trigger: "reconciler",
          attempts: j.attempts,
          updated,
          routineCount: 1,
        });
      } else {
        cur.routineCount += 1;
        // Keep the most recent timestamp as the row's "last synced".
        if (updated && (!cur.updated || new Date(updated) > new Date(cur.updated))) {
          cur.updated = updated;
        }
      }
      continue;
    }
    rows.push({
      key: `job:${j.id}`,
      sourceKind: j.source_kind,
      sourceID: j.source_id,
      status: j.status,
      trigger: j.trigger,
      attempts: j.attempts,
      updated,
      error: j.last_error,
      routineCount: 0,
    });
  }
  const all = [...rows, ...routine.values()];
  all.sort((a, b) => {
    const ta = a.updated ? new Date(a.updated).getTime() : 0;
    const tb = b.updated ? new Date(b.updated).getTime() : 0;
    return tb - ta;
  });
  return all;
}

// JOB_TABLE_PAGE_SIZE bounds how many collapsed job rows render at once.
// The fetch is capped at 500 server-side and routine reconciler syncs
// collapse before paging, so a page is well under this; the cap keeps the
// table from growing into a multi-hundred-row DOM block as history fills
// the window. See #523.
const JOB_TABLE_PAGE_SIZE = 25;

// JobTable renders the collapsed drill-down rows one page at a time.
// collapseJobs runs over the whole filtered set first (so the
// reconciler-heartbeat "synced ×N" counts stay global, not per-page),
// then the page slice is taken from the result. resetKey changes when the
// kind/status filter changes, snapping back to the first page so a filter
// switch never strands the operator on a now-empty page.
export function JobTable({ jobs, resetKey }: { jobs: IndexJob[]; resetKey: string }) {
  const rows = useMemo(() => collapseJobs(jobs), [jobs]);
  const [page, setPage] = useState(0);
  useEffect(() => {
    setPage(0);
  }, [resetKey]);

  const pageCount = Math.max(1, Math.ceil(rows.length / JOB_TABLE_PAGE_SIZE));
  // Clamp on render so live polling that shrinks the set below the current
  // page boundary lands on a valid page instead of a blank table.
  const current = Math.min(page, pageCount - 1);
  const start = current * JOB_TABLE_PAGE_SIZE;
  const visible = rows.slice(start, start + JOB_TABLE_PAGE_SIZE);

  if (rows.length === 0) {
    return <p className="py-6 text-center text-sm text-muted-foreground">No jobs match this filter.</p>;
  }
  return (
    <div className="space-y-3">
      <div className="overflow-x-auto">
        <table className="w-full text-left text-xs">
          <thead className="text-muted-foreground">
            <tr className="border-b">
              <th className="py-1.5 pr-3 font-medium">Kind</th>
              <th className="py-1.5 pr-3 font-medium">Unit</th>
              <th className="py-1.5 pr-3 font-medium">Status</th>
              <th className="py-1.5 pr-3 font-medium">Trigger</th>
              <th className="py-1.5 pr-3 font-medium">Attempts</th>
              <th className="py-1.5 pr-3 font-medium">Updated</th>
              <th className="py-1.5 font-medium">Error</th>
            </tr>
          </thead>
          <tbody>
            {visible.map((r) => (
              <tr key={r.key} className="border-b last:border-0">
                <td className="py-1.5 pr-3 font-mono">{r.sourceKind}</td>
                <td className="max-w-[200px] truncate py-1.5 pr-3 font-mono">{r.sourceID}</td>
                <td className="py-1.5 pr-3">
                  <JobStatusChip status={r.status} />
                </td>
                <td className="py-1.5 pr-3 text-muted-foreground">
                  {r.routineCount > 0 ? `reconciler · synced ×${r.routineCount}` : r.trigger}
                </td>
                <td className="py-1.5 pr-3 tabular-nums">{r.routineCount > 0 ? "—" : r.attempts}</td>
                <td className="py-1.5 pr-3 text-muted-foreground">{relTime(r.updated)}</td>
                <td className="max-w-[260px] truncate py-1.5 text-red-600 dark:text-red-400">
                  {r.error ?? ""}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {pageCount > 1 && (
        <div className="flex items-center justify-between text-xs text-muted-foreground">
          <span className="tabular-nums">
            {start + 1}–{start + visible.length} of {rows.length}
          </span>
          <div className="flex items-center gap-1">
            <button
              type="button"
              onClick={() => setPage(current - 1)}
              disabled={current === 0}
              className="flex items-center gap-1 rounded-md border px-2 py-1 transition-colors hover:bg-accent disabled:opacity-40"
              aria-label="Previous page"
            >
              <ChevronLeft className="h-3 w-3" /> Prev
            </button>
            <span className="tabular-nums">
              Page {current + 1} of {pageCount}
            </span>
            <button
              type="button"
              onClick={() => setPage(current + 1)}
              disabled={current >= pageCount - 1}
              className="flex items-center gap-1 rounded-md border px-2 py-1 transition-colors hover:bg-accent disabled:opacity-40"
              aria-label="Next page"
            >
              Next <ChevronRight className="h-3 w-3" />
            </button>
          </div>
        </div>
      )}
    </div>
  );
}
