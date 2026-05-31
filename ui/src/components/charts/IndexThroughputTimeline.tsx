import { useEffect, useMemo, useRef, useState } from "react";
import { scaleLinear } from "d3-scale";
import { area as d3area, line as d3line, curveMonotoneX } from "d3-shape";
import { max as d3max } from "d3-array";

// IndexThroughputTimeline draws completed index jobs over time as a d3
// area, so an operator can see indexing keeping up or stalling. It buckets
// the supplied completion timestamps into a fixed number of equal-width
// bins across the observed window; d3 computes the path geometry and React
// renders the SVG. An empty window renders an informative placeholder
// rather than a flat zero line.
interface IndexThroughputTimelineProps {
  // Completion timestamps (ISO strings) of succeeded jobs.
  completedAt: string[];
  bins?: number;
  height?: number;
}

const PAD_X = 4;
const PAD_TOP = 6;
const AXIS = 16;

function useElementWidth<T extends HTMLElement>() {
  const ref = useRef<T>(null);
  const [width, setWidth] = useState(0);
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    const ro = new ResizeObserver((entries) => {
      setWidth(entries[0]?.contentRect.width ?? 0);
    });
    ro.observe(el);
    return () => ro.disconnect();
  }, []);
  return [ref, width] as const;
}

function fmtTick(ms: number): string {
  return new Date(ms).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}

export function IndexThroughputTimeline({
  completedAt,
  bins = 24,
  height = 140,
}: IndexThroughputTimelineProps) {
  const [ref, width] = useElementWidth<HTMLDivElement>();

  const model = useMemo(() => {
    const times = completedAt
      .map((s) => new Date(s).getTime())
      .filter((t) => Number.isFinite(t))
      .sort((a, b) => a - b);
    if (times.length === 0) return null;
    const lo = times[0]!;
    const hi = times[times.length - 1]!;
    const span = Math.max(1, hi - lo);
    const counts = new Array(bins).fill(0);
    for (const t of times) {
      const idx = Math.min(bins - 1, Math.floor(((t - lo) / span) * bins));
      counts[idx] += 1;
    }
    return { counts, lo, hi, total: times.length };
  }, [completedAt, bins]);

  if (!model) {
    return (
      <div
        ref={ref}
        className="flex items-center justify-center rounded-md border border-dashed text-sm text-muted-foreground"
        style={{ height }}
      >
        No completed jobs in this window yet.
      </div>
    );
  }

  const innerH = height - AXIS - PAD_TOP;
  const x = scaleLinear().domain([0, bins - 1]).range([PAD_X, Math.max(PAD_X, width - PAD_X)]);
  const y = scaleLinear()
    .domain([0, d3max(model.counts) || 1])
    .range([PAD_TOP + innerH, PAD_TOP]);
  const indexed = model.counts.map((c: number, i: number) => [i, c] as [number, number]);
  const areaPath = d3area<[number, number]>()
    .x((d) => x(d[0]))
    .y0(PAD_TOP + innerH)
    .y1((d) => y(d[1]))
    .curve(curveMonotoneX)(indexed);
  const linePath = d3line<[number, number]>()
    .x((d) => x(d[0]))
    .y((d) => y(d[1]))
    .curve(curveMonotoneX)(indexed);

  return (
    <div ref={ref} className="w-full">
      {width > 0 && (
        <svg width={width} height={height} role="img" aria-label="Completed index jobs over time">
          {areaPath && (
            <path d={areaPath} fill="hsl(142, 71%, 45%)" fillOpacity={0.18} stroke="none" />
          )}
          {linePath && (
            <path
              d={linePath}
              fill="none"
              stroke="hsl(142, 71%, 45%)"
              strokeWidth={1.5}
              strokeLinejoin="round"
            />
          )}
          <text x={PAD_X} y={height - 3} fontSize={10} fill="hsl(var(--muted-foreground))">
            {fmtTick(model.lo)}
          </text>
          <text
            x={width - PAD_X}
            y={height - 3}
            textAnchor="end"
            fontSize={10}
            fill="hsl(var(--muted-foreground))"
          >
            {fmtTick(model.hi)}
          </text>
        </svg>
      )}
    </div>
  );
}
