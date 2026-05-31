import { scaleLinear } from "d3-scale";
import { max as d3max } from "d3-array";

// IndexLatencyTrack renders per-kind embed-pass duration (started ->
// completed) as horizontal bars on a shared scale, with a p95 tick marker,
// surfacing slow passes (the CPU-only embedder case) that a single average
// would hide. d3 owns the scale; React renders the SVG bars.
export interface KindLatency {
  kind: string;
  p50Ms: number;
  p95Ms: number;
  maxMs: number;
  count: number;
}

interface IndexLatencyTrackProps {
  rows: KindLatency[];
}

const ROW_H = 26;
const LABEL_W = 110;
const BAR_H = 12;

function fmtDuration(ms: number): string {
  if (ms < 1000) return `${Math.round(ms)}ms`;
  const s = ms / 1000;
  if (s < 60) return `${s.toFixed(1)}s`;
  return `${Math.floor(s / 60)}m${String(Math.round(s % 60)).padStart(2, "0")}s`;
}

export function IndexLatencyTrack({ rows }: IndexLatencyTrackProps) {
  if (rows.length === 0) {
    return (
      <div className="flex items-center justify-center rounded-md border border-dashed py-6 text-sm text-muted-foreground">
        No completed passes to measure yet.
      </div>
    );
  }
  const domainMax = d3max(rows, (r) => r.maxMs) || 1;
  const x = scaleLinear().domain([0, domainMax]).range([0, 100]);

  return (
    <div className="space-y-1">
      {rows.map((r) => (
        <div key={r.kind} className="flex items-center gap-2 text-xs" data-kind={r.kind}>
          <span className="shrink-0 truncate text-muted-foreground" style={{ width: LABEL_W }}>
            {r.kind}
          </span>
          <div className="relative h-6 flex-1">
            <svg width="100%" height={ROW_H} role="img" aria-label={`${r.kind} embed duration`}>
              <rect x="0" y={(ROW_H - BAR_H) / 2} width="100%" height={BAR_H} rx={3} fill="hsl(var(--muted))" fillOpacity={0.4} />
              <rect
                x="0"
                y={(ROW_H - BAR_H) / 2}
                width={`${x(r.p50Ms)}%`}
                height={BAR_H}
                rx={3}
                fill="hsl(217, 91%, 60%)"
              >
                <title>{`${r.kind} p50 ${fmtDuration(r.p50Ms)}`}</title>
              </rect>
              <line
                x1={`${x(r.p95Ms)}%`}
                x2={`${x(r.p95Ms)}%`}
                y1={(ROW_H - BAR_H) / 2 - 2}
                y2={(ROW_H + BAR_H) / 2 + 2}
                stroke="hsl(38, 92%, 50%)"
                strokeWidth={2}
              >
                <title>{`${r.kind} p95 ${fmtDuration(r.p95Ms)}`}</title>
              </line>
            </svg>
          </div>
          <span className="w-28 shrink-0 text-right tabular-nums text-muted-foreground">
            p50 {fmtDuration(r.p50Ms)} · p95 {fmtDuration(r.p95Ms)}
          </span>
        </div>
      ))}
    </div>
  );
}
