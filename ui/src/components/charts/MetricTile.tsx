import { ArrowDownRight, ArrowUpRight, Minus } from "lucide-react";
import { Sparkline } from "./Sparkline";

// Delta direction relative to the previous period. "up" is not inherently
// good; callers pass `goodDirection` so the color reflects whether the
// movement is healthy (e.g. rising errors is bad, rising success is good).
export interface MetricTileProps {
  label: string;
  value: string;
  // Series for the inline sparkline (chronological). Optional: omitted hides it.
  spark?: number[];
  // Percentage change vs the previous period, already computed (e.g. 0.12 = +12%).
  delta?: number;
  // Which delta direction is "good" - controls the trend color.
  goodDirection?: "up" | "down" | "neutral";
  // Accent for the sparkline/value (status color for error tiles, etc.).
  accent?: string;
  emphasize?: boolean;
}

function formatDelta(delta: number): string {
  const pct = Math.round(delta * 100);
  if (pct === 0) return "0%";
  return `${pct > 0 ? "+" : ""}${pct}%`;
}

export function MetricTile({
  label,
  value,
  spark,
  delta,
  goodDirection = "neutral",
  accent = "hsl(var(--primary))",
  emphasize = false,
}: MetricTileProps) {
  const hasDelta = delta !== undefined && Number.isFinite(delta);
  const rising = hasDelta && delta! > 0.005;
  const falling = hasDelta && delta! < -0.005;
  const flat = hasDelta && !rising && !falling;

  // Map movement to good/bad color via goodDirection.
  let trendClass = "text-muted-foreground";
  if (goodDirection !== "neutral" && (rising || falling)) {
    const good =
      (goodDirection === "up" && rising) || (goodDirection === "down" && falling);
    trendClass = good ? "text-emerald-400" : "text-red-400";
  }

  const TrendIcon = rising ? ArrowUpRight : falling ? ArrowDownRight : Minus;

  const hasSpark = !!spark && spark.length > 1;
  const valueStyle = emphasize ? { color: accent } : undefined;

  return (
    <div className="flex flex-col gap-1 overflow-hidden rounded-lg border bg-card p-3">
      <div className="flex items-start justify-between gap-2">
        <span className="text-xs font-medium text-muted-foreground">{label}</span>
        {hasDelta && (
          <span className={`flex shrink-0 items-center gap-0.5 text-xs tabular-nums ${trendClass}`}>
            <TrendIcon className="h-3 w-3" />
            {formatDelta(delta!)}
          </span>
        )}
      </div>

      {hasSpark ? (
        // Value sits above a full-width sparkline (which never competes for
        // horizontal space); both pinned to the bottom of the tile.
        <div className="flex flex-1 flex-col justify-end gap-1">
          <span className="tabular-nums text-2xl font-semibold" style={valueStyle}>
            {value}
          </span>
          <div className="h-6 w-full overflow-hidden">
            <Sparkline data={spark} stroke={accent} filled showDot />
          </div>
        </div>
      ) : (
        // No trend series: center a larger number so the tile is not empty.
        <div className="flex flex-1 items-center">
          <span className="tabular-nums text-3xl font-semibold" style={valueStyle}>
            {value}
          </span>
        </div>
      )}
      {flat && <span className="sr-only">no change</span>}
    </div>
  );
}
