import { useId } from "react";
import {
  AreaChart,
  Area,
  XAxis,
  YAxis,
  Tooltip,
  ResponsiveContainer,
} from "recharts";
import type { TrendPoint } from "@/pages/audit/health";

// TrendArea is a compact single-series area chart for a metric over time
// (e.g. one node's CPU or memory). Kept generic: the caller supplies the
// color and value formatter. Renders a "building history" state when there
// are too few points, which is the common case right after a node restarts
// or when Prometheus has little history.
interface TrendAreaProps {
  data: TrendPoint[];
  color: string;
  height?: number;
  format: (v: number) => string;
}

function fmtTime(unixSeconds: number): string {
  return new Date(unixSeconds * 1000).toLocaleTimeString([], {
    hour: "2-digit",
    minute: "2-digit",
  });
}

export function TrendArea({ data, color, height = 130, format }: TrendAreaProps) {
  const gradientId = useId();

  if (data.length < 2) {
    return (
      <div
        className="flex items-center justify-center rounded-md border border-dashed text-xs text-muted-foreground"
        style={{ height }}
      >
        Building history…
      </div>
    );
  }

  return (
    <ResponsiveContainer width="100%" height={height}>
      <AreaChart data={data} margin={{ top: 6, right: 8, left: 0, bottom: 0 }}>
        <defs>
          <linearGradient id={gradientId} x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor={color} stopOpacity={0.5} />
            <stop offset="100%" stopColor={color} stopOpacity={0.05} />
          </linearGradient>
        </defs>
        <XAxis
          dataKey="t"
          tickFormatter={fmtTime}
          className="text-xs"
          tick={{ fill: "hsl(var(--muted-foreground))" }}
          minTickGap={48}
        />
        <YAxis
          tickFormatter={format}
          className="text-xs"
          tick={{ fill: "hsl(var(--muted-foreground))" }}
          width={52}
          domain={[0, "auto"]}
        />
        <Tooltip
          contentStyle={{
            backgroundColor: "hsl(var(--card))",
            border: "1px solid hsl(var(--border))",
            borderRadius: "0.375rem",
            fontSize: "0.75rem",
          }}
          labelFormatter={(t: number) => fmtTime(t)}
          formatter={(v: number) => [format(v), ""]}
        />
        <Area
          type="monotone"
          dataKey="value"
          stroke={color}
          strokeWidth={1.5}
          fill={`url(#${gradientId})`}
          isAnimationActive={false}
        />
      </AreaChart>
    </ResponsiveContainer>
  );
}
