import {
  AreaChart,
  Area,
  XAxis,
  YAxis,
  Tooltip,
  ResponsiveContainer,
} from "recharts";
import type { StatusStackBucket } from "@/pages/audit/promql";
import { ChartSkeleton } from "./ChartSkeleton";

// StatusStackChart shows the request-rate mix by HTTP status class over
// time as a stacked area. Healthy 2xx dominates; the amber 4xx and red 5xx
// bands make error spikes obvious at a glance, which a single rate line
// cannot convey. Renders an empty state when there is no data in the window.
interface StatusStackChartProps {
  data: StatusStackBucket[];
  isLoading: boolean;
  height?: number;
}

const COLORS = {
  "2xx": "hsl(142, 71%, 45%)",
  "4xx": "hsl(38, 92%, 50%)",
  "5xx": "hsl(0, 72%, 51%)",
} as const;

function fmtTime(iso: string): string {
  const d = new Date(iso);
  return d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}

export function StatusStackChart({ data, isLoading, height = 220 }: StatusStackChartProps) {
  if (isLoading) return <ChartSkeleton height={height} />;

  if (data.length === 0) {
    return (
      <div
        className="flex items-center justify-center rounded-md border border-dashed text-sm text-muted-foreground"
        style={{ height }}
      >
        No traffic in this window.
      </div>
    );
  }

  return (
    <ResponsiveContainer width="100%" height={height}>
      <AreaChart data={data} margin={{ top: 4, right: 8, left: 0, bottom: 0 }}>
        <defs>
          {(["2xx", "4xx", "5xx"] as const).map((k) => (
            <linearGradient key={k} id={`stk-${k}`} x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor={COLORS[k]} stopOpacity={0.7} />
              <stop offset="100%" stopColor={COLORS[k]} stopOpacity={0.1} />
            </linearGradient>
          ))}
        </defs>
        <XAxis
          dataKey="bucket"
          tickFormatter={fmtTime}
          className="text-xs"
          tick={{ fill: "hsl(var(--muted-foreground))" }}
          minTickGap={40}
        />
        <YAxis
          className="text-xs"
          tick={{ fill: "hsl(var(--muted-foreground))" }}
          width={36}
          allowDecimals={false}
        />
        <Tooltip
          contentStyle={{
            backgroundColor: "hsl(var(--card))",
            border: "1px solid hsl(var(--border))",
            borderRadius: "0.375rem",
            fontSize: "0.75rem",
          }}
          labelFormatter={(v: string) => fmtTime(v)}
          formatter={(value: number, name: string) => [value.toFixed(2) + "/s", name]}
        />
        {(["2xx", "4xx", "5xx"] as const).map((k) => (
          <Area
            key={k}
            type="monotone"
            dataKey={k}
            stackId="status"
            stroke={COLORS[k]}
            strokeWidth={1.5}
            fill={`url(#stk-${k})`}
            isAnimationActive={false}
          />
        ))}
      </AreaChart>
    </ResponsiveContainer>
  );
}
