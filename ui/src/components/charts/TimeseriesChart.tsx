import {
  LineChart,
  Line,
  XAxis,
  YAxis,
  Tooltip,
  ResponsiveContainer,
  CartesianGrid,
} from "recharts";
import type { TimeseriesBucket } from "@/api/admin/types";
import { ChartSkeleton } from "./ChartSkeleton";
import { formatDuration } from "@/lib/formatDuration";

// TimeseriesSeries describes one line to plot. Callers that want
// something other than the default success/error split (e.g. a single
// request-rate line) pass their own series config.
export interface TimeseriesSeries {
  dataKey: keyof TimeseriesBucket;
  name: string;
  stroke: string;
}

// DEFAULT_SERIES is the success/error split used by every existing
// caller (MCP activity dashboards). Kept as the default so this change
// is backward-compatible.
const DEFAULT_SERIES: TimeseriesSeries[] = [
  { dataKey: "success_count", name: "Success", stroke: "hsl(142, 76%, 36%)" },
  { dataKey: "error_count", name: "Errors", stroke: "hsl(0, 84%, 60%)" },
];

interface TimeseriesChartProps {
  data: TimeseriesBucket[] | undefined;
  isLoading: boolean;
  height?: number;
  preset?: "1h" | "6h" | "24h" | "7d";
  /** Lines to plot. Defaults to the success/error split. */
  series?: TimeseriesSeries[];
}

function formatTick(iso: string, preset?: string) {
  const d = new Date(iso);
  switch (preset) {
    case "7d":
      return d.toLocaleDateString([], { month: "short", day: "numeric" });
    case "24h":
      return d.toLocaleTimeString([], { hour: "numeric" });
    default:
      return d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  }
}

export function TimeseriesChart({
  data,
  isLoading,
  height = 250,
  preset,
  series = DEFAULT_SERIES,
}: TimeseriesChartProps) {
  if (isLoading || !data) return <ChartSkeleton height={height} />;

  const nonZeroCount = data.filter((d) => d.count > 0).length;
  const showDots = nonZeroCount > 0 && nonZeroCount <= 10;

  return (
    <ResponsiveContainer width="100%" height={height}>
      <LineChart data={data}>
        <CartesianGrid strokeDasharray="3 3" className="stroke-border" />
        <XAxis
          dataKey="bucket"
          tickFormatter={(v) => formatTick(v as string, preset)}
          className="text-xs"
          tick={{ fill: "hsl(var(--muted-foreground))" }}
        />
        <YAxis
          className="text-xs"
          tick={{ fill: "hsl(var(--muted-foreground))" }}
        />
        <Tooltip
          labelFormatter={(v) => new Date(v as string).toLocaleString()}
          formatter={(value: number, name: string) => {
            if (name === "avg_duration_ms") return [formatDuration(value), "Avg Duration"];
            return [value, name];
          }}
          contentStyle={{
            backgroundColor: "hsl(var(--card))",
            border: "1px solid hsl(var(--border))",
            borderRadius: "0.375rem",
            fontSize: "0.75rem",
          }}
        />
        {series.map((s) => (
          <Line
            key={s.dataKey}
            type="monotone"
            dataKey={s.dataKey}
            stroke={s.stroke}
            strokeWidth={2}
            dot={showDots ? { r: 3 } : false}
            name={s.name}
          />
        ))}
      </LineChart>
    </ResponsiveContainer>
  );
}
