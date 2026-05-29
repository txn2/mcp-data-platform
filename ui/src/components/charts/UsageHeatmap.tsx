import { useEffect, useMemo, useRef, useState } from "react";
import { scaleSequential } from "d3-scale";
import { interpolateViridis } from "d3-scale-chromatic";
import { max as d3max } from "d3-array";
import type { TimeseriesBucket } from "@/api/admin/types";
import { ChartSkeleton } from "./ChartSkeleton";

// UsageHeatmap renders call volume as a day-of-week x hour-of-day grid,
// surfacing usage rhythm (business hours, weekend lulls, off-hours batch
// jobs) that a single timeseries line hides. Expects hourly buckets
// spanning roughly a week. It measures its container and lays the 24
// hour-columns across the full available width (cells stretch to fill the
// panel) while keeping a fixed row height and crisp, unscaled labels.
interface UsageHeatmapProps {
  data: TimeseriesBucket[] | undefined;
  isLoading: boolean;
  rowHeight?: number;
}

const DAY_LABELS = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];
const DAY_GUTTER = 36;
const HOUR_AXIS = 18;
const GAP = 3;

function useElementWidth<T extends HTMLElement>() {
  const ref = useRef<T>(null);
  const [width, setWidth] = useState(0);
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    const ro = new ResizeObserver((entries) => {
      const w = entries[0]?.contentRect.width ?? 0;
      setWidth(w);
    });
    ro.observe(el);
    return () => ro.disconnect();
  }, []);
  return [ref, width] as const;
}

export function UsageHeatmap({ data, isLoading, rowHeight = 22 }: UsageHeatmapProps) {
  const [ref, width] = useElementWidth<HTMLDivElement>();

  const grid = useMemo(() => {
    if (!data) return null;
    const cells: number[][] = Array.from({ length: 7 }, () => new Array(24).fill(0));
    for (const b of data) {
      const d = new Date(b.bucket);
      if (Number.isNaN(d.getTime())) continue;
      const row = cells[d.getDay()];
      if (row) row[d.getHours()] = (row[d.getHours()] ?? 0) + b.count;
    }
    const flat = cells.flat();
    return { cells, maxVal: d3max(flat) ?? 0, total: flat.reduce((s, v) => s + v, 0) };
  }, [data]);

  const svgHeight = 7 * (rowHeight + GAP) + HOUR_AXIS;

  if (isLoading || !grid) {
    return (
      <div ref={ref}>
        <ChartSkeleton height={svgHeight} />
      </div>
    );
  }

  if (grid.total === 0) {
    return (
      <div
        ref={ref}
        className="flex items-center justify-center rounded-md border border-dashed text-sm text-muted-foreground"
        style={{ height: svgHeight }}
      >
        Building usage history…
      </div>
    );
  }

  const color = scaleSequential(interpolateViridis).domain([0, grid.maxVal || 1]);
  // Lay the 24 hour columns across the full measured width.
  const step = width > DAY_GUTTER ? (width - DAY_GUTTER) / 24 : 0;
  const cellW = Math.max(0, step - GAP);

  return (
    <div ref={ref} className="w-full">
      {width > 0 && (
        <svg width={width} height={svgHeight} role="img" aria-label="Call volume by day of week and hour of day">
          {grid.cells.map((row, day) =>
            row.map((value, hour) => (
              <rect
                key={`${day}-${hour}`}
                x={DAY_GUTTER + hour * step}
                y={day * (rowHeight + GAP)}
                width={cellW}
                height={rowHeight}
                rx={2}
                fill={value === 0 ? "hsl(var(--muted))" : color(value)}
                fillOpacity={value === 0 ? 0.35 : 1}
              >
                <title>{`${DAY_LABELS[day]} ${String(hour).padStart(2, "0")}:00 · ${value.toLocaleString()} calls`}</title>
              </rect>
            )),
          )}
          {DAY_LABELS.map((label, day) => (
            <text
              key={label}
              x={DAY_GUTTER - 8}
              y={day * (rowHeight + GAP) + rowHeight / 2}
              textAnchor="end"
              dominantBaseline="central"
              fontSize={10}
              fill="hsl(var(--muted-foreground))"
            >
              {label}
            </text>
          ))}
          {[0, 4, 8, 12, 16, 20].map((hour) => (
            <text
              key={hour}
              x={DAY_GUTTER + hour * step}
              y={svgHeight - 4}
              textAnchor="start"
              fontSize={10}
              fill="hsl(var(--muted-foreground))"
            >
              {`${String(hour).padStart(2, "0")}h`}
            </text>
          ))}
        </svg>
      )}
    </div>
  );
}
