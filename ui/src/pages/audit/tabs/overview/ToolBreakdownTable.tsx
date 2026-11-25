import type { BreakdownEntry } from "@/api/admin/types";
import { ChartSkeleton } from "@/components/charts/ChartSkeleton";

// The dashboard's Top Tools: each tool's calls as a bar, its success rate
// and its holds (#2819). A hold waited for an approval or an answer, so it
// is neither a success nor a failure and the rate leaves it out.
export function ToolBreakdownTable({
  data,
  isLoading,
  labelMap,
}: {
  data: BreakdownEntry[] | undefined;
  isLoading: boolean;
  labelMap?: Record<string, string>;
}) {
  if (isLoading || !data) return <ChartSkeleton height={220} />;
  if (data.length === 0) {
    return <p className="text-sm text-muted-foreground">No tool calls in this range.</p>;
  }
  const most = Math.max(...data.map((d) => d.count), 1);
  return (
    <div className="space-y-3">
      <table className="w-full text-sm">
        <thead>
          <tr className="text-xs text-muted-foreground">
            <th className="pb-2 text-left font-medium">Tool</th>
            <th className="pb-2 text-left font-medium">Calls</th>
            <th className="pb-2 text-right font-medium">Success</th>
            <th className="pb-2 text-right font-medium">Holds</th>
          </tr>
        </thead>
        <tbody>
          {data.map((d) => {
            const holds = d.hold_count ?? 0;
            return (
              <tr key={d.dimension}>
                <td className="max-w-[12rem] truncate py-1 pr-3" title={d.dimension}>
                  {labelMap?.[d.dimension] ?? d.dimension}
                </td>
                <td className="w-full py-1 pr-3">
                  <div className="flex items-center gap-2">
                    <div
                      className="h-2.5 rounded-sm bg-primary"
                      style={{ width: `${(d.count / most) * 100}%` }}
                      aria-hidden="true"
                    />
                    <span className="text-xs tabular-nums text-muted-foreground">
                      {d.count.toLocaleString()}
                    </span>
                  </div>
                </td>
                <td className={`py-1 text-right tabular-nums ${successClass(d.success_rate)}`}>
                  {formatRate(d.success_rate)}
                </td>
                <td
                  className={`py-1 pl-3 text-right tabular-nums ${holds > 0 ? "text-blue-600 dark:text-blue-400" : "text-muted-foreground"}`}
                >
                  {holds.toLocaleString()}
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
      <p className="text-xs text-muted-foreground">
        Success leaves holds out: a call that waits for an approval or an answer is neither a
        success nor a failure.
      </p>
    </div>
  );
}

function formatRate(rate: number): string {
  const pct = rate * 100;
  return pct === 100 ? "100%" : `${pct.toFixed(1)}%`;
}

function successClass(rate: number): string {
  if (rate >= 0.95) return "text-emerald-600 dark:text-emerald-400";
  if (rate >= 0.8) return "text-amber-600 dark:text-amber-400";
  return "text-red-600 dark:text-red-400";
}
