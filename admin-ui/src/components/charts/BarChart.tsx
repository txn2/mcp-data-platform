import { useMemo } from "react";
import {
  BarChart as RechartsBarChart,
  Bar,
  XAxis,
  YAxis,
  Tooltip,
  ResponsiveContainer,
} from "recharts";
import type { BreakdownEntry } from "@/api/types";
import { ChartSkeleton } from "./ChartSkeleton";
import { formatDuration } from "@/lib/formatDuration";

interface BarChartProps {
  data: BreakdownEntry[] | undefined;
  isLoading: boolean;
  height?: number;
  color?: string;
}

/** Shorten an email to just the local part (before @). */
function shortenEmail(label: string): string {
  const at = label.indexOf("@");
  if (at > 0) return label.slice(0, at);
  return label;
}

/** Estimate pixel width for a label at text-xs (12px) in a proportional font. */
function estimateLabelWidth(label: string): number {
  // ~6.5px per character at 12px sans-serif, plus 16px padding for tick/gap.
  return label.length * 6.5 + 16;
}

export function BreakdownBarChart({
  data,
  isLoading,
  height = 250,
  color = "hsl(var(--primary))",
}: BarChartProps) {
  // Pre-process: shorten emails to local part for display.
  const chartData = useMemo(
    () =>
      data?.map((d) => ({
        ...d,
        label: shortenEmail(d.dimension),
      })),
    [data],
  );

  // Compute YAxis width from the longest label — no truncation needed.
  const yAxisWidth = useMemo(() => {
    if (!chartData || chartData.length === 0) return 80;
    const maxLen = Math.max(...chartData.map((d) => estimateLabelWidth(d.label)));
    // Clamp between 80 and 260 to keep bars visible.
    return Math.min(260, Math.max(80, maxLen));
  }, [chartData]);

  if (isLoading || !chartData) return <ChartSkeleton height={height} />;

  return (
    <ResponsiveContainer width="100%" height={height}>
      <RechartsBarChart data={chartData} layout="vertical" margin={{ left: 0, right: 12 }}>
        <XAxis
          type="number"
          className="text-xs"
          tick={{ fill: "hsl(var(--muted-foreground))" }}
        />
        <YAxis
          type="category"
          dataKey="label"
          className="text-xs"
          tick={{ fill: "hsl(var(--muted-foreground))" }}
          width={yAxisWidth}
        />
        <Tooltip
          contentStyle={{
            backgroundColor: "hsl(var(--card))",
            border: "1px solid hsl(var(--border))",
            borderRadius: "0.375rem",
            fontSize: "0.75rem",
          }}
          labelFormatter={(label: string) => {
            // Show the full original dimension in the tooltip.
            const entry = chartData.find((d) => d.label === label);
            return entry?.dimension ?? label;
          }}
          formatter={(value: number, name: string) => {
            if (name === "avg_duration_ms") return [formatDuration(value), "Avg Duration"];
            return [value, "Count"];
          }}
        />
        <Bar dataKey="count" fill={color} radius={[0, 4, 4, 0]} />
      </RechartsBarChart>
    </ResponsiveContainer>
  );
}
