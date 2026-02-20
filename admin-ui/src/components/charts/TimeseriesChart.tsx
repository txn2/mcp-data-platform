import {
  LineChart,
  Line,
  XAxis,
  YAxis,
  Tooltip,
  ResponsiveContainer,
  CartesianGrid,
} from "recharts";
import type { TimeseriesBucket } from "@/api/types";
import { ChartSkeleton } from "./ChartSkeleton";
import { formatDuration } from "@/lib/formatDuration";

interface TimeseriesChartProps {
  data: TimeseriesBucket[] | undefined;
  isLoading: boolean;
  height?: number;
  preset?: "1h" | "6h" | "24h" | "7d";
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
}: TimeseriesChartProps) {
  if (isLoading || !data) return <ChartSkeleton height={height} />;

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
        <Line
          type="monotone"
          dataKey="success_count"
          stroke="hsl(142, 76%, 36%)"
          strokeWidth={2}
          dot={false}
          name="Success"
        />
        <Line
          type="monotone"
          dataKey="error_count"
          stroke="hsl(0, 84%, 60%)"
          strokeWidth={2}
          dot={false}
          name="Errors"
        />
      </LineChart>
    </ResponsiveContainer>
  );
}
