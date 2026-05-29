import { useMemo } from "react";
import { scaleLog } from "d3-scale";
import type { PerformanceStats } from "@/api/admin/types";
import { formatDuration } from "@/lib/formatDuration";
import { ChartSkeleton } from "./ChartSkeleton";

// LatencyPanel visualizes the latency distribution as a set of horizontal
// bars (one per statistic) on a shared log scale, since latencies span
// orders of magnitude. The big p50/p95/p99 readouts sit on top; the bars
// fill the panel's vertical space and make the tail (p99 -> max) obvious
// without the markers colliding. Uses the audit performance percentiles,
// which are populated in both mock and live data.
interface LatencyPanelProps {
  data: PerformanceStats | undefined;
  isLoading: boolean;
}

const READOUTS: { key: keyof PerformanceStats; label: string; color: string }[] = [
  { key: "p50_ms", label: "p50", color: "hsl(142, 71%, 45%)" },
  { key: "p95_ms", label: "p95", color: "hsl(38, 92%, 50%)" },
  { key: "p99_ms", label: "p99", color: "hsl(0, 72%, 51%)" },
];

const BARS: { key: keyof PerformanceStats; label: string; color: string }[] = [
  { key: "p50_ms", label: "p50", color: "hsl(142, 71%, 45%)" },
  { key: "avg_ms", label: "avg", color: "hsl(199, 89%, 48%)" },
  { key: "p95_ms", label: "p95", color: "hsl(38, 92%, 50%)" },
  { key: "p99_ms", label: "p99", color: "hsl(0, 72%, 51%)" },
  { key: "max_ms", label: "max", color: "hsl(0, 62%, 38%)" },
];

export function LatencyPanel({ data, isLoading }: LatencyPanelProps) {
  const scale = useMemo(() => {
    if (!data) return null;
    const hi = Math.max(data.max_ms, data.p99_ms, 10);
    return scaleLog().domain([1, hi]).range([2, 100]).clamp(true);
  }, [data]);

  if (isLoading || !data || !scale) return <ChartSkeleton height={220} />;

  return (
    <div className="flex h-full flex-col gap-5">
      {/* Headline percentiles */}
      <div className="grid grid-cols-3 gap-3">
        {READOUTS.map((m) => (
          <div key={m.key}>
            <p className="text-xs text-muted-foreground">{m.label}</p>
            <p className="tabular-nums text-2xl font-semibold" style={{ color: m.color }}>
              {formatDuration(data[m.key])}
            </p>
          </div>
        ))}
      </div>

      {/* Log-scaled bar per statistic; fills the remaining vertical space. */}
      <div className="flex flex-1 flex-col justify-center gap-3">
        {BARS.map((b) => (
          <div key={b.key} className="flex items-center gap-3 text-xs">
            <span className="w-8 shrink-0 text-muted-foreground">{b.label}</span>
            <div className="relative h-5 flex-1 overflow-hidden rounded bg-muted/30">
              <div
                className="absolute inset-y-0 left-0 rounded"
                style={{ width: `${scale(Math.max(1, data[b.key]))}%`, backgroundColor: b.color }}
              />
            </div>
            <span
              className="w-16 shrink-0 text-right tabular-nums font-medium"
              style={{ color: b.color }}
            >
              {formatDuration(data[b.key])}
            </span>
          </div>
        ))}
      </div>
    </div>
  );
}
