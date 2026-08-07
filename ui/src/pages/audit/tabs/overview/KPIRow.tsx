import { MetricTile } from "@/components/charts/MetricTile";
import type { Overview, TimeseriesBucket } from "@/api/admin/types";
import { formatDuration } from "@/lib/formatDuration";

// The MCP dashboard's headline numbers. Extracted from OverviewTab.tsx
// (#1207) so the page above is a layout of panels and the arithmetic behind
// each tile lives next to the tile.

// halfDelta estimates a period-over-period change from a single timeseries:
// it compares the newer half of the buckets to the older half. Returns
// undefined when there are too few buckets or the older half is empty (so
// the tile shows no misleading trend). agg is "sum" for counts, "avg" for
// rates/durations.
function halfDelta(vals: number[], agg: "sum" | "avg"): number | undefined {
  if (vals.length < 4) return undefined;
  const mid = Math.floor(vals.length / 2);
  const reduce = (a: number[]) => {
    const sum = a.reduce((s, v) => s + v, 0);
    return agg === "sum" ? sum : a.length ? sum / a.length : 0;
  };
  const older = reduce(vals.slice(0, mid));
  const newer = reduce(vals.slice(mid));
  if (older === 0) return undefined;
  return (newer - older) / older;
}

// stat renders one overview figure, or "-" until the overview has loaded.
// Every tile reads through it so the loading placeholder is stated once
// rather than once per tile.
function stat(overview: Overview | undefined, of: (o: Overview) => string | number): string {
  return overview === undefined ? "-" : String(of(overview));
}

export function KPIRow({
  overview,
  buckets,
}: {
  overview?: Overview;
  // The activity timeseries each tile draws its own sparkline and trend from.
  buckets: TimeseriesBucket[];
}) {
  const callsSpark = buckets.map((b) => b.count);
  const errorSpark = buckets.map((b) => b.error_count);
  const durationSpark = buckets.map((b) => b.avg_duration_ms);
  const successSpark = buckets.map((b) => (b.count > 0 ? (b.success_count / b.count) * 100 : 0));

  return (
    <div className="grid grid-cols-2 gap-3 md:grid-cols-4 lg:grid-cols-7">
      <MetricTile
        label="Total Calls"
        value={stat(overview, (o) => o.total_calls.toLocaleString())}
        spark={callsSpark}
        delta={halfDelta(callsSpark, "sum")}
        goodDirection="neutral"
        emphasize
      />
      <MetricTile
        label="Success Rate"
        value={stat(overview, (o) => `${(o.success_rate * 100).toFixed(1)}%`)}
        spark={successSpark}
        delta={halfDelta(successSpark, "avg")}
        goodDirection="up"
        accent="hsl(142, 71%, 45%)"
      />
      <MetricTile
        label="Avg Duration"
        value={stat(overview, (o) => formatDuration(o.avg_duration_ms))}
        spark={durationSpark}
        delta={halfDelta(durationSpark, "avg")}
        goodDirection="down"
      />
      <MetricTile label="Unique Users" value={stat(overview, (o) => o.unique_users)} />
      <MetricTile label="Unique Tools" value={stat(overview, (o) => o.unique_tools)} />
      <MetricTile
        label="Enrichment"
        value={stat(overview, (o) => `${(o.enrichment_rate * 100).toFixed(0)}%`)}
        goodDirection="up"
      />
      <MetricTile
        label="Errors"
        value={stat(overview, (o) => o.error_count.toLocaleString())}
        spark={errorSpark}
        delta={halfDelta(errorSpark, "sum")}
        goodDirection="down"
        accent="hsl(0, 72%, 51%)"
      />
    </div>
  );
}
