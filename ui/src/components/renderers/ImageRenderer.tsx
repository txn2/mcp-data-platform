import { useCallback, useRef, useState } from "react";
import { Download, Maximize2, Minus, Plus, RotateCcw } from "lucide-react";
import { formatBytes } from "@/lib/format";

interface ImageRendererProps {
  /** URL the image is loaded from. Binary content is never embedded in the page. */
  contentUrl: string;
  fileName?: string;
  sizeBytes?: number;
  contentType: string;
}

const ZOOM_STEPS = [0.25, 0.5, 0.75, 1, 1.5, 2, 3, 4, 6, 8];

/**
 * Image viewer with zoom and pan.
 *
 * The checkerboard backing is not decoration: without it a transparent PNG is
 * indistinguishable from a white one on a light theme and from a black one on a
 * dark theme, which is exactly the thing someone opens an exported chart to
 * check. Natural dimensions and file size are shown for the same reason.
 */
export function ImageRenderer({ contentUrl, fileName, sizeBytes, contentType }: ImageRendererProps) {
  const [zoomIndex, setZoomIndex] = useState(3); // 1x
  const [fit, setFit] = useState(true);
  const [dimensions, setDimensions] = useState<{ w: number; h: number } | null>(null);
  const [failed, setFailed] = useState(false);
  const [offset, setOffset] = useState({ x: 0, y: 0 });
  const dragRef = useRef<{ x: number; y: number; ox: number; oy: number } | null>(null);

  const zoom = ZOOM_STEPS[zoomIndex] ?? 1;

  const reset = useCallback(() => {
    setZoomIndex(3);
    setOffset({ x: 0, y: 0 });
    setFit(true);
  }, []);

  const stepZoom = (delta: number) => {
    setFit(false);
    setZoomIndex((i) => Math.min(ZOOM_STEPS.length - 1, Math.max(0, i + delta)));
  };

  const onPointerDown = (e: React.PointerEvent<HTMLDivElement>) => {
    if (fit) return;
    dragRef.current = { x: e.clientX, y: e.clientY, ox: offset.x, oy: offset.y };
    e.currentTarget.setPointerCapture(e.pointerId);
  };

  const onPointerMove = (e: React.PointerEvent<HTMLDivElement>) => {
    const drag = dragRef.current;
    if (!drag) return;
    setOffset({ x: drag.ox + (e.clientX - drag.x), y: drag.oy + (e.clientY - drag.y) });
  };

  const onPointerUp = (e: React.PointerEvent<HTMLDivElement>) => {
    dragRef.current = null;
    e.currentTarget.releasePointerCapture(e.pointerId);
  };

  if (failed) {
    return (
      <div className="rounded-lg border bg-card p-8 text-center text-sm text-muted-foreground" data-feedback-anchorable>
        <p className="font-medium text-foreground">This image could not be loaded</p>
        <p className="mt-1 text-xs">The content may have been removed from storage, or the format is not supported by this browser.</p>
        <a
          href={contentUrl}
          download={fileName}
          className="mt-4 inline-flex items-center gap-2 rounded-md border px-3 py-2 text-sm hover:bg-accent"
        >
          <Download className="h-3.5 w-3.5" />
          Download
        </a>
      </div>
    );
  }

  return (
    <div className="space-y-2" data-feedback-anchorable>
      <ImageToolbar
        zoomIndex={zoomIndex}
        zoom={zoom}
        fit={fit}
        dimensions={dimensions}
        sizeBytes={sizeBytes}
        contentType={contentType}
        contentUrl={contentUrl}
        fileName={fileName}
        onStepZoom={stepZoom}
        onToggleFit={() => (fit ? (setFit(false), setZoomIndex(3)) : reset())}
        onReset={reset}
      />

      <div
        className="checkerboard flex items-center justify-center overflow-hidden rounded-lg border"
        style={{ height: "min(70vh, 640px)", cursor: fit ? "default" : "grab" }}
        onPointerDown={onPointerDown}
        onPointerMove={onPointerMove}
        onPointerUp={onPointerUp}
      >
        <img
          src={contentUrl}
          alt={fileName || "Asset image"}
          onLoad={(e) => setDimensions({ w: e.currentTarget.naturalWidth, h: e.currentTarget.naturalHeight })}
          onError={() => setFailed(true)}
          draggable={false}
          style={
            fit
              ? { maxWidth: "100%", maxHeight: "100%", objectFit: "contain" }
              : {
                  transform: `translate(${offset.x}px, ${offset.y}px) scale(${zoom})`,
                  transformOrigin: "center",
                  imageRendering: zoom >= 3 ? "pixelated" : "auto",
                  maxWidth: "none",
                }
          }
        />
      </div>
    </div>
  );
}

interface ImageToolbarProps {
  zoomIndex: number;
  zoom: number;
  fit: boolean;
  dimensions: { w: number; h: number } | null;
  sizeBytes?: number;
  contentType: string;
  contentUrl: string;
  fileName?: string;
  onStepZoom: (delta: number) => void;
  onToggleFit: () => void;
  onReset: () => void;
}

/** Zoom controls, the natural-size readout, and the download action. */
function ImageToolbar({
  zoomIndex,
  zoom,
  fit,
  dimensions,
  sizeBytes,
  contentType,
  contentUrl,
  fileName,
  onStepZoom,
  onToggleFit,
  onReset,
}: ImageToolbarProps) {
  return (
    <div className="flex flex-wrap items-center gap-2 text-xs">
      <button
        type="button"
        onClick={() => onStepZoom(-1)}
        disabled={zoomIndex === 0}
        aria-label="Zoom out"
        className="rounded-md border p-1.5 hover:bg-accent disabled:opacity-40"
      >
        <Minus className="h-3 w-3" />
      </button>
      <span className="w-12 text-center tabular-nums text-muted-foreground">
        {fit ? "Fit" : `${Math.round(zoom * 100)}%`}
      </span>
      <button
        type="button"
        onClick={() => onStepZoom(1)}
        disabled={zoomIndex === ZOOM_STEPS.length - 1}
        aria-label="Zoom in"
        className="rounded-md border p-1.5 hover:bg-accent disabled:opacity-40"
      >
        <Plus className="h-3 w-3" />
      </button>
      <button
        type="button"
        onClick={onToggleFit}
        className="inline-flex items-center gap-1.5 rounded-md border px-2 py-1.5 hover:bg-accent"
      >
        <Maximize2 className="h-3 w-3" />
        {fit ? "Actual size" : "Fit"}
      </button>
      <button type="button" onClick={onReset} aria-label="Reset view" className="rounded-md border p-1.5 hover:bg-accent">
        <RotateCcw className="h-3 w-3" />
      </button>

      <span className="ml-auto text-muted-foreground">
        {dimensions ? `${dimensions.w} x ${dimensions.h}` : ""}
        {sizeBytes ? ` · ${formatBytes(sizeBytes)}` : ""}
        {` · ${contentType}`}
      </span>
      <a
        href={contentUrl}
        download={fileName}
        className="inline-flex items-center gap-1.5 rounded-md border px-2 py-1.5 hover:bg-accent"
      >
        <Download className="h-3 w-3" />
        Download
      </a>
    </div>
  );
}
