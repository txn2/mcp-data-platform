import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import { Maximize2, Minus, Plus } from "lucide-react";
import { Button } from "@/components/ui/button";

// PanZoomCanvas is the dot-grid canvas every Flow view draws on (#1906,
// #1972): the reader pans by dragging and zooms with the wheel or the buttons.
// It opens at a readable zoom anchored top-left rather than shrunk to fit: a
// diagram too small to read is not an overview, and Fit is one click away.

const MIN_ZOOM = 0.2;
const MAX_ZOOM = 2.5;
const OPEN_ZOOM = 0.9;
const MIN_HEIGHT = 420;
const MAX_HEIGHT = 720;

export interface View {
  x: number;
  y: number;
  k: number;
}

interface Props {
  /** width and height are the drawing's size at zoom 1. */
  width: number;
  height: number;
  /** fill makes the canvas as tall as its container (full screen). */
  fill?: boolean;
  label: string;
  testId: string;
  legend: ReactNode;
  /** defs are the SVG definitions (arrow markers) the drawing refers to. */
  defs?: ReactNode;
  /** onBackground is a click on the canvas itself rather than on a shape. */
  onBackground: () => void;
  children: ReactNode;
}

export function canvasHeight(drawingHeight: number): number {
  return Math.round(Math.min(MAX_HEIGHT, Math.max(MIN_HEIGHT, drawingHeight * OPEN_ZOOM + 48)));
}

export function PanZoomCanvas({ width, height, fill, label, testId, legend, defs, onBackground, children }: Props) {
  const hostRef = useRef<HTMLDivElement>(null);
  const [view, setView] = useState<View>({ x: 8, y: 8, k: OPEN_ZOOM });
  const drag = useRef<{ x: number; y: number; moved: boolean } | null>(null);
  const boxHeight = canvasHeight(height);

  const zoomAt = useCallback((factor: number, cx: number, cy: number) => {
    setView((v) => {
      const k = Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, v.k * factor));
      return { k, x: cx - ((cx - v.x) * k) / v.k, y: cy - ((cy - v.y) * k) / v.k };
    });
  }, []);

  // The wheel listener is attached by hand because React's is passive, and a
  // passive listener cannot stop the page from scrolling under the zoom.
  useEffect(() => {
    const el = hostRef.current;
    if (!el) return;
    const onWheel = (e: WheelEvent) => {
      e.preventDefault();
      const r = el.getBoundingClientRect();
      zoomAt(Math.exp(-e.deltaY * 0.0015), e.clientX - r.left, e.clientY - r.top);
    };
    el.addEventListener("wheel", onWheel, { passive: false });
    return () => el.removeEventListener("wheel", onWheel);
  }, [zoomAt]);

  const shown = () => hostRef.current?.clientHeight || boxHeight;
  const fit = () => {
    const el = hostRef.current;
    const w = el?.clientWidth || 800;
    const h = shown();
    const k = Math.min(1, w / Math.max(1, width), h / Math.max(1, height)) * 0.98;
    setView({ k, x: Math.max(0, (w - width * k) / 2), y: Math.max(8, (h - height * k) / 2) });
  };
  const zoomCentered = (f: number) => {
    const el = hostRef.current;
    zoomAt(f, (el?.clientWidth || 800) / 2, shown() / 2);
  };

  return (
    <div
      ref={hostRef}
      data-testid={testId}
      className={`relative cursor-grab touch-none overflow-hidden rounded-md border select-none active:cursor-grabbing ${fill ? "h-full" : ""}`}
      style={{
        height: fill ? undefined : boxHeight,
        backgroundColor: "hsl(var(--muted) / 0.35)",
        backgroundImage: "radial-gradient(circle, hsl(var(--border)) 1px, transparent 1.2px)",
        backgroundSize: "22px 22px",
      }}
      onPointerDown={(e) => {
        if (e.button > 0) return;
        drag.current = { x: e.clientX - view.x, y: e.clientY - view.y, moved: false };
      }}
      onPointerMove={(e) => {
        const d = drag.current;
        if (!d) return;
        if (!d.moved && Math.hypot(e.clientX - view.x - d.x, e.clientY - view.y - d.y) < 3) return;
        d.moved = true;
        setView((v) => ({ ...v, x: e.clientX - d.x, y: e.clientY - d.y }));
      }}
      onPointerUp={() => {
        const d = drag.current;
        drag.current = null;
        if (d && !d.moved) onBackground();
      }}
      onPointerLeave={() => {
        drag.current = null;
      }}
    >
      <svg className="absolute inset-0 h-full w-full" role="group" aria-label={label}>
        {defs && <defs>{defs}</defs>}
        <g transform={`translate(${view.x},${view.y}) scale(${view.k})`}>{children}</g>
      </svg>
      {legend}
      <div className="absolute right-3 bottom-3 flex gap-1" onPointerDown={(e) => e.stopPropagation()}>
        <Button type="button" variant="outline" size="icon-sm" aria-label="Zoom in" onClick={() => zoomCentered(1.2)}>
          <Plus />
        </Button>
        <Button type="button" variant="outline" size="icon-sm" aria-label="Zoom out" onClick={() => zoomCentered(1 / 1.2)}>
          <Minus />
        </Button>
        <Button type="button" variant="outline" size="icon-sm" aria-label="Fit the whole diagram" onClick={fit}>
          <Maximize2 />
        </Button>
      </div>
    </div>
  );
}

// Arrow is the marker an edge ends in.
export function Arrow({ id, color }: { id: string; color: string }) {
  return (
    <marker id={id} viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
      <path d="M0,0 L10,5 L0,10 z" fill={color} />
    </marker>
  );
}
