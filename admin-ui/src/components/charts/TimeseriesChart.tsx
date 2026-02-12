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

interface TimeseriesChartProps {
  data: TimeseriesBucket[] | undefined;
  isLoading: boolean;
  height?: number;
}

function formatTime(iso: string) {
  const d = new Date(iso);
  return d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}

export function TimeseriesChart({
  data,
  isLoading,
  height = 250,
}: TimeseriesChartProps) {
  if (isLoading || !data) return <ChartSkeleton height={height} />;

  return (
    <ResponsiveContainer width="100%" height={height}>
      <LineChart data={data}>
        <CartesianGrid strokeDasharray="3 3" className="stroke-border" />
        <XAxis
          dataKey="bucket"
          tickFormatter={formatTime}
          className="text-xs"
          tick={{ fill: "hsl(var(--muted-foreground))" }}
        />
        <YAxis
          className="text-xs"
          tick={{ fill: "hsl(var(--muted-foreground))" }}
        />
        <Tooltip
          labelFormatter={(v) => new Date(v as string).toLocaleString()}
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
