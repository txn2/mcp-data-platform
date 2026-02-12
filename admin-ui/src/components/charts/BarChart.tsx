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

interface BarChartProps {
  data: BreakdownEntry[] | undefined;
  isLoading: boolean;
  height?: number;
  color?: string;
}

export function BreakdownBarChart({
  data,
  isLoading,
  height = 250,
  color = "hsl(var(--primary))",
}: BarChartProps) {
  if (isLoading || !data) return <ChartSkeleton height={height} />;

  return (
    <ResponsiveContainer width="100%" height={height}>
      <RechartsBarChart data={data} layout="vertical" margin={{ left: 80 }}>
        <XAxis
          type="number"
          className="text-xs"
          tick={{ fill: "hsl(var(--muted-foreground))" }}
        />
        <YAxis
          type="category"
          dataKey="dimension"
          className="text-xs"
          tick={{ fill: "hsl(var(--muted-foreground))" }}
          width={80}
        />
        <Tooltip
          contentStyle={{
            backgroundColor: "hsl(var(--card))",
            border: "1px solid hsl(var(--border))",
            borderRadius: "0.375rem",
            fontSize: "0.75rem",
          }}
          formatter={(value: number) => [value, "Count"]}
        />
        <Bar dataKey="count" fill={color} radius={[0, 4, 4, 0]} />
      </RechartsBarChart>
    </ResponsiveContainer>
  );
}
