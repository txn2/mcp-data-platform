import { useEffect, useMemo, useRef, useState } from "react";
import { scaleSequential } from "d3-scale";
import { interpolateViridis } from "d3-scale-chromatic";
import { max as d3max } from "d3-array";
import type { IndexKindSummary } from "@/api/admin/indexjobs";

// IndexCoverageHeatmap is the dashboard centerpiece: a kind (rows) by
// job-state (columns) grid where each cell's color intensity encodes how
// many of that kind's units sit in that state. A sparse or failing corner
// (a red-leaning failed column, an all-dark kind) is obvious at a glance,
// which a stack of per-kind stat cards cannot convey. d3 owns the color
// scale and the layout math; React renders the SVG, so there is no
// imperative DOM mutation. The grid measures its container and stretches
// the state columns across the full width.
interface IndexCoverageHeatmapProps {
  kinds: IndexKindSummary[];
  rowHeight?: number;
}

const STATES = ["pending", "running", "succeeded", "failed"] as const;
type State = (typeof STATES)[number];

const KIND_GUTTER = 110;
const HEADER = 18;
const GAP = 4;

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

function cellValue(k: IndexKindSummary, s: State): number {
  return k[s];
}

export function IndexCoverageHeatmap({ kinds, rowHeight = 30 }: IndexCoverageHeatmapProps) {
  const [ref, width] = useElementWidth<HTMLDivElement>();

  const maxVal = useMemo(() => {
    const vals = kinds.flatMap((k) => STATES.map((s) => cellValue(k, s)));
    return d3max(vals) ?? 0;
  }, [kinds]);

  const svgHeight = HEADER + kinds.length * (rowHeight + GAP);

  if (kinds.length === 0) {
    return (
      <div
        ref={ref}
        className="flex items-center justify-center rounded-md border border-dashed text-sm text-muted-foreground"
        style={{ height: 120 }}
      >
        No registered index consumers yet.
      </div>
    );
  }

  const color = scaleSequential(interpolateViridis).domain([0, maxVal || 1]);
  const step = width > KIND_GUTTER ? (width - KIND_GUTTER) / STATES.length : 0;
  const cellW = Math.max(0, step - GAP);

  return (
    <div ref={ref} className="w-full">
      {width > 0 && (
        <svg
          width={width}
          height={svgHeight}
          role="img"
          aria-label="Index job state by kind"
        >
          {STATES.map((s, col) => (
            <text
              key={s}
              x={KIND_GUTTER + col * step + cellW / 2}
              y={HEADER - 6}
              textAnchor="middle"
              fontSize={10}
              fill="hsl(var(--muted-foreground))"
            >
              {s}
            </text>
          ))}
          {kinds.map((k, row) => {
            const y = HEADER + row * (rowHeight + GAP);
            return (
              <g key={k.kind}>
                <text
                  x={0}
                  y={y + rowHeight / 2}
                  dominantBaseline="central"
                  fontSize={11}
                  fill="hsl(var(--foreground))"
                >
                  {k.kind}
                </text>
                {STATES.map((s, col) => {
                  const value = cellValue(k, s);
                  return (
                    <rect
                      key={s}
                      x={KIND_GUTTER + col * step}
                      y={y}
                      width={cellW}
                      height={rowHeight}
                      rx={3}
                      fill={value === 0 ? "hsl(var(--muted))" : color(value)}
                      fillOpacity={value === 0 ? 0.35 : 1}
                      data-state={s}
                      data-kind={k.kind}
                    >
                      <title>{`${k.kind} · ${s}: ${value.toLocaleString()}`}</title>
                    </rect>
                  );
                })}
                {STATES.map((s, col) => {
                  const value = cellValue(k, s);
                  if (value === 0) return null;
                  return (
                    <text
                      key={`label-${s}`}
                      x={KIND_GUTTER + col * step + cellW / 2}
                      y={y + rowHeight / 2}
                      textAnchor="middle"
                      dominantBaseline="central"
                      fontSize={11}
                      fontWeight={600}
                      fill="hsl(var(--background))"
                      pointerEvents="none"
                    >
                      {value.toLocaleString()}
                    </text>
                  );
                })}
              </g>
            );
          })}
        </svg>
      )}
    </div>
  );
}
