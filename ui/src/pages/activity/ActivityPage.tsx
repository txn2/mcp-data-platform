import { useMemo } from "react";
import { useTimeRangeStore, type TimeRangePreset } from "@/stores/timerange";
import {
  useMyActivityOverview,
  useMyActivityTimeseries,
  useMyActivityBreakdown,
} from "@/api/portal/hooks";
import { StatCard } from "@/components/cards/StatCard";
import { SectionCard } from "@/components/patterns/SectionCard";
import { TimeseriesChart } from "@/components/charts/TimeseriesChart";
import { BreakdownBarChart } from "@/components/charts/BarChart";
import { TimeRangePicker } from "@/pages/audit/TimeRangePicker";
import { formatDuration } from "@/lib/formatDuration";
import { formatToolName } from "@/lib/formatToolName";
import type { TimeseriesBucket, BreakdownEntry } from "@/api/admin/types";

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
  const { preset, getStartTime, getEndTime } = useTimeRangeStore();
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
      {/* The dashboard's own window chooser, over the store both pages share:
          a range picked here is the range the dashboard opens on. */}
      <TimeRangePicker />

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

      <SectionCard title="My Activity">
        <TimeseriesChart
          data={timeseries.data as TimeseriesBucket[] | undefined}
          isLoading={timeseries.isLoading}
          preset={preset}
        />
      </SectionCard>

      <SectionCard title="Top Tools">
        <BreakdownBarChart
          data={toolBreakdown.data as BreakdownEntry[] | undefined}
          isLoading={toolBreakdown.isLoading}
          labelMap={toolLabelMap}
        />
      </SectionCard>
    </div>
  );
}
