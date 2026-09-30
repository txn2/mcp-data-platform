import { Bar, BarChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import { ChartSkeleton } from "@/components/charts/ChartSkeleton";
import { outcomeLabel, type VolumeBucket } from "./webhookOverview";

// VolumeChart is the webhooks overview's request volume (#1979): one stacked
// bar per bucket, a band per outcome. Accepted is green; every rejection is a
// warm color, so a rise in refusals shows against the accepted traffic.

const COLORS: Record<string, string> = {
  accepted: "hsl(142, 71%, 45%)",
  unauthorized: "hsl(0, 72%, 51%)",
  too_large: "hsl(25, 95%, 53%)",
  rate_limited: "hsl(38, 92%, 50%)",
  buffer_full: "hsl(48, 96%, 53%)",
  write_failed: "hsl(330, 81%, 60%)",
  invalid_body: "hsl(280, 65%, 60%)",
};
const OTHER = "hsl(215, 16%, 57%)";

function fmtTime(iso: string): string {
  return new Date(iso).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}

export function VolumeChart({
  buckets,
  outcomes,
  total,
  isLoading,
  height = 200,
}: {
  buckets: VolumeBucket[];
  outcomes: string[];
  total: number;
  isLoading: boolean;
  height?: number;
}) {
  if (isLoading) return <ChartSkeleton height={height} />;
  if (total === 0) {
    return (
      <div
        className="flex items-center justify-center rounded-md border border-dashed text-sm text-muted-foreground"
        style={{ height }}
      >
        No requests in this range.
      </div>
    );
  }
  return (
    <ResponsiveContainer width="100%" height={height}>
      <BarChart data={buckets} margin={{ top: 4, right: 8, left: 0, bottom: 0 }}>
        <XAxis
          dataKey="at"
          tickFormatter={fmtTime}
          className="text-xs"
          tick={{ fill: "hsl(var(--muted-foreground))" }}
          minTickGap={40}
        />
        <YAxis className="text-xs" tick={{ fill: "hsl(var(--muted-foreground))" }} width={44} allowDecimals={false} />
        <Tooltip
          contentStyle={{
            backgroundColor: "hsl(var(--card))",
            border: "1px solid hsl(var(--border))",
            borderRadius: "0.375rem",
            fontSize: "0.75rem",
          }}
          labelFormatter={(v: string) => fmtTime(v)}
          formatter={(value: number, name: string) => [value.toLocaleString(), outcomeLabel(name)]}
        />
        {outcomes.map((o) => (
          <Bar key={o} dataKey={o} stackId="outcome" fill={COLORS[o] ?? OTHER} isAnimationActive={false} />
        ))}
      </BarChart>
    </ResponsiveContainer>
  );
}
