import { useMemo } from "react";
import { useTimeRangeStore, type TimeRangePreset } from "@/stores/timerange";
import {
  useMyActivityOverview,
  useMyActivityTimeseries,
  useMyActivityBreakdown,
} from "@/api/portal/hooks";
import { StatCard } from "@/components/cards/StatCard";
import { TimeseriesChart } from "@/components/charts/TimeseriesChart";
import { BreakdownBarChart } from "@/components/charts/BarChart";
import { formatDuration } from "@/lib/formatDuration";
import { formatToolName } from "@/lib/formatToolName";
import type { TimeseriesBucket, BreakdownEntry } from "@/api/admin/types";

const presets: { value: TimeRangePreset; label: string }[] = [
  { value: "1h", label: "1h" },
  { value: "6h", label: "6h" },
  { value: "24h", label: "24h" },
  { value: "7d", label: "7d" },
];

function getResolution(preset: TimeRangePreset): string {
  switch (preset) {
    case "1h":
      return "minute";
    case "6h":
      return "minute";
    case "24h":
      return "hour";
    case "7d":
      return "day";
  }
}

export function ActivityPage() {
  const { preset, setPreset, getStartTime, getEndTime } = useTimeRangeStore();
  const { startTime, endTime } = useMemo(
    () => ({ startTime: getStartTime(), endTime: getEndTime() }),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [preset],
  );

  const overview = useMyActivityOverview({ startTime, endTime });
  const timeseries = useMyActivityTimeseries({
    resolution: getResolution(preset),
    startTime,
    endTime,
  });
  const toolBreakdown = useMyActivityBreakdown({
    groupBy: "tool_name",
    limit: 8,
    startTime,
    endTime,
  });

  const o = overview.data;

  const toolLabelMap = useMemo(() => {
    const map: Record<string, string> = {};
    for (const entry of toolBreakdown.data ?? []) {
      map[entry.dimension] = formatToolName(entry.dimension);
    }
    return map;
  }, [toolBreakdown.data]);

  return (
    <div className="space-y-6">
      {/* Time Range */}
      <div className="flex items-center gap-1">
        {presets.map((p) => (
          <button
            key={p.value}
            onClick={() => setPreset(p.value)}
            className={`rounded-md px-3 py-1.5 text-xs font-medium transition-colors ${
              preset === p.value
                ? "bg-primary text-primary-foreground"
                : "text-muted-foreground hover:bg-muted"
            }`}
          >
            {p.label}
          </button>
        ))}
      </div>

      {/* Summary Cards */}
      <div className="grid grid-cols-3 gap-4">
        <StatCard
          label="Total Calls"
          value={o?.total_calls?.toLocaleString() ?? "-"}
        />
        <StatCard
          label="Avg Duration"
          value={o ? formatDuration(o.avg_duration_ms) : "-"}
        />
        <StatCard
          label="Tools Used"
          value={o?.unique_tools ?? "-"}
        />
      </div>

      {/* Activity Timeline */}
      <div className="rounded-lg border bg-card p-4">
        <h2 className="mb-3 text-sm font-medium">My Activity</h2>
        <TimeseriesChart
          data={timeseries.data as TimeseriesBucket[] | undefined}
          isLoading={timeseries.isLoading}
          preset={preset}
        />
      </div>

      {/* Top Tools */}
      <div className="rounded-lg border bg-card p-4">
        <h2 className="mb-3 text-sm font-medium">Top Tools</h2>
        <BreakdownBarChart
          data={toolBreakdown.data as BreakdownEntry[] | undefined}
          isLoading={toolBreakdown.isLoading}
          labelMap={toolLabelMap}
        />
      </div>
    </div>
  );
}
