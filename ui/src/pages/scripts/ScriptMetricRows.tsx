import type { BreakdownEntry } from "@/api/admin/types";
import type { PortalScriptRow } from "@/api/portal/hooks/scripts";
import { Button } from "@/components/ui/button";

// ScriptMetricRows is a per-script metric a reader can act on (#1407).
//
// The two panels it draws — the busiest scripts, and the schedules missing
// fires — name a script and then leave the reader to find it. A metric that
// names something links to it: the name opens the script, and the count opens
// the runs behind it. The bar is kept because the ranking is the point of the
// panel, and it is drawn as the row's own background rather than by a chart
// library, because a chart cannot hold a link.
//
// A row naming a script this listing does not hold is still drawn, with no
// links: the series outlives the record, so a deleted script keeps its history
// in Prometheus, and dropping the row would understate the platform's load.

interface Props {
  data: BreakdownEntry[] | undefined;
  isLoading: boolean;
  /** scripts resolves the metric's script label — a name — to the record. */
  scripts: PortalScriptRow[];
  /** unit names what the count counts, in the row's own words. */
  unit: string;
  onOpenScript: (scriptId: string) => void;
  onShowRuns: (scriptId: string, name: string) => void;
}

export function ScriptMetricRows({
  data,
  isLoading,
  scripts,
  unit,
  onOpenScript,
  onShowRuns,
}: Props) {
  if (isLoading) {
    return <p className="text-sm text-muted-foreground">Loading...</p>;
  }
  const rows = data ?? [];
  if (rows.length === 0) {
    return <p className="text-sm text-muted-foreground">Nothing in this window.</p>;
  }
  const top = Math.max(...rows.map((row) => row.count));
  return (
    <ul className="space-y-1">
      {rows.map((row) => (
        <MetricRow
          key={row.dimension}
          row={row}
          share={top > 0 ? row.count / top : 0}
          script={scriptFor(scripts, row.dimension)}
          unit={unit}
          onOpenScript={onOpenScript}
          onShowRuns={onShowRuns}
        />
      ))}
    </ul>
  );
}

// scriptFor resolves the series label to the record it names. The label is the
// script's NAME rather than its id (pkg/observability/metrics.go), because a
// metric is read by a person; the link needs the id, and this listing is what
// the page already holds to resolve it.
function scriptFor(scripts: PortalScriptRow[], name: string): PortalScriptRow | undefined {
  return scripts.find((row) => row.script.name === name);
}

function MetricRow({
  row,
  share,
  script,
  unit,
  onOpenScript,
  onShowRuns,
}: {
  row: BreakdownEntry;
  share: number;
  script?: PortalScriptRow;
  unit: string;
  onOpenScript: (scriptId: string) => void;
  onShowRuns: (scriptId: string, name: string) => void;
}) {
  const id = script?.script.id;
  const label = script?.script.display_name || row.dimension;
  return (
    <li className="relative overflow-hidden rounded-md border px-2 py-1.5">
      {/* The bar is the row's background, so the ranking reads at a glance and
          the controls stay in front of it. */}
      <div
        aria-hidden
        className="bg-primary/10 absolute inset-y-0 left-0"
        style={{ width: `${Math.round(share * 100)}%` }}
      />
      <div className="relative flex items-center justify-between gap-2">
        <div className="min-w-0">
          {id ? (
            <button
              type="button"
              className="max-w-full truncate text-left text-sm font-medium hover:underline"
              onClick={() => onOpenScript(id)}
            >
              {label}
            </button>
          ) : (
            <span className="block max-w-full truncate text-sm font-medium">{label}</span>
          )}
          {label !== row.dimension && (
            // The series is labelled by the script's name, and the link is
            // labelled by what a person calls it. The name is kept underneath
            // when the two differ, and dropped when it would be the same
            // string twice.
            <span className="block truncate font-mono text-xs text-muted-foreground">
              {row.dimension}
            </span>
          )}
        </div>
        <div className="flex shrink-0 items-center gap-2">
          <span className="text-sm tabular-nums">
            {row.count.toLocaleString()}{" "}
            <span className="text-xs text-muted-foreground">{unit}</span>
          </span>
          {id && (
            <Button size="xs" variant="ghost" onClick={() => onShowRuns(id, label)}>
              Runs
            </Button>
          )}
        </div>
      </div>
    </li>
  );
}
