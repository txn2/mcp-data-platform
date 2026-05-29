import { useMemo } from "react";
import { scaleLinear } from "d3-scale";
import { line as d3line, area as d3area, curveMonotoneX } from "d3-shape";
import { extent } from "d3-array";

// Sparkline draws a compact inline trend line (optionally area-filled) from
// a numeric series. d3 computes the path geometry; React renders the SVG so
// there is no imperative DOM mutation. Used on KPI cards and the vitals
// strip where a full chart would be too heavy.
//
// The viewBox is fixed and the SVG scales to its container via width/height
// 100%, so callers size it with a wrapping element (e.g. h-8 w-24).
export interface SparklineProps {
  data: number[];
  // stroke/fill default to the primary token; pass a status color for
  // error/latency sparklines.
  stroke?: string;
  fill?: string;
  // Render an area fill under the line.
  filled?: boolean;
  // Highlight the last point with a dot (current value emphasis).
  showDot?: boolean;
  strokeWidth?: number;
  // Logical drawing space; the SVG scales to its container.
  width?: number;
  height?: number;
  className?: string;
}

const PAD = 3;

export function Sparkline({
  data,
  stroke = "hsl(var(--primary))",
  fill,
  filled = false,
  showDot = false,
  strokeWidth = 1.5,
  width = 100,
  height = 28,
  className,
}: SparklineProps) {
  const geom = useMemo(() => {
    const pts = data.filter((d) => Number.isFinite(d));
    if (pts.length < 2) return null;
    const x = scaleLinear()
      .domain([0, pts.length - 1])
      .range([PAD, width - PAD]);
    const [lo, hi] = extent(pts) as [number, number];
    // Avoid a degenerate flat domain (all-equal values) collapsing to the
    // top edge; center a flat line instead.
    const y = scaleLinear()
      .domain(lo === hi ? [lo - 1, hi + 1] : [lo, hi])
      .range([height - PAD, PAD]);
    const indexed = pts.map((d, i) => [i, d] as [number, number]);
    const linePath = d3line<[number, number]>()
      .x((d) => x(d[0]))
      .y((d) => y(d[1]))
      .curve(curveMonotoneX)(indexed);
    const areaPath = filled
      ? d3area<[number, number]>()
          .x((d) => x(d[0]))
          .y0(height - PAD)
          .y1((d) => y(d[1]))
          .curve(curveMonotoneX)(indexed)
      : null;
    const lastX = x(pts.length - 1);
    const lastY = y(pts[pts.length - 1]!);
    return { linePath, areaPath, lastX, lastY };
  }, [data, width, height, filled]);

  if (!geom?.linePath) {
    return <div className={className} aria-hidden />;
  }

  return (
    <svg
      className={className}
      viewBox={`0 0 ${width} ${height}`}
      preserveAspectRatio="none"
      width="100%"
      height="100%"
      role="img"
      aria-hidden
    >
      {geom.areaPath && (
        <path d={geom.areaPath} fill={fill ?? stroke} fillOpacity={0.12} stroke="none" />
      )}
      <path
        d={geom.linePath}
        fill="none"
        stroke={stroke}
        strokeWidth={strokeWidth}
        strokeLinejoin="round"
        strokeLinecap="round"
        vectorEffect="non-scaling-stroke"
      />
      {showDot && (
        <circle cx={geom.lastX} cy={geom.lastY} r={2} fill={stroke} stroke="none" />
      )}
    </svg>
  );
}
