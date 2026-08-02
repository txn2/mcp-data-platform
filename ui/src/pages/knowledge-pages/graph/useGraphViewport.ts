import { useCallback, useRef, useState } from "react";

/** Transform is the pan/zoom applied to the graph layer. */
export interface Transform {
  k: number;
  x: number;
  y: number;
}

export const IDENTITY: Transform = { k: 1, x: 0, y: 0 };

const MIN_SCALE = 0.25;
const MAX_SCALE = 4;

/** One zoom step for the toolbar buttons, where a press is one discrete step. */
export const ZOOM_IN = 1.2;
export const ZOOM_OUT = 1 / ZOOM_IN;

/**
 * Wheel-delta units per zoom doubling, by the event's deltaMode. A wheel event
 * reports its magnitude in pixels (0), lines (1), or pages (2), and a trackpad
 * emits a stream of small pixel deltas where a mouse wheel emits a few large
 * ones. These are d3-zoom's constants, which is the reference for how this
 * should feel across both devices.
 */
const WHEEL_UNIT = [0.002, 0.05, 1] as const;

/**
 * wheelZoomFactor converts one wheel event into a scale multiplier PROPORTIONAL
 * to how far the wheel actually moved.
 *
 * Applying a fixed step per event instead is what makes trackpad zoom
 * uncontrollable: a single two-finger swipe delivers dozens of events, so a
 * constant 1.2x per event compounds to hundreds of times in one gesture, and the
 * sign flicker at the end of an inertial scroll reads as zooming in and out.
 */
export function wheelZoomFactor(deltaY: number, deltaMode: number): number {
  const unit = WHEEL_UNIT[deltaMode] ?? WHEEL_UNIT[0];
  return Math.pow(2, -deltaY * unit);
}

/** clampScale keeps zoom inside the range where the graph stays legible. */
export function clampScale(k: number): number {
  return Math.min(MAX_SCALE, Math.max(MIN_SCALE, k));
}

/**
 * zoomAbout returns the transform that scales by `factor` while keeping the
 * point (px, py) — in screen coordinates — under the cursor, which is what makes
 * wheel zoom feel anchored rather than jumping to the canvas center.
 */
export function zoomAbout(t: Transform, factor: number, px: number, py: number): Transform {
  const k = clampScale(t.k * factor);
  if (k === t.k) return t;
  return { k, x: px - ((px - t.x) / t.k) * k, y: py - ((py - t.y) / t.k) * k };
}

/** toGraphPoint maps a screen point into the graph's own coordinate space. */
export function toGraphPoint(t: Transform, px: number, py: number): { x: number; y: number } {
  return { x: (px - t.x) / t.k, y: (py - t.y) / t.k };
}

/**
 * useGraphViewport owns pan and zoom for the graph canvas. Panning is a drag on
 * empty canvas; node dragging is the caller's concern and suppresses panning by
 * not starting one.
 */
export function useGraphViewport() {
  const [transform, setTransform] = useState<Transform>(IDENTITY);
  const panFrom = useRef<{ x: number; y: number; t: Transform } | null>(null);

  const startPan = useCallback((px: number, py: number) => {
    panFrom.current = { x: px, y: py, t: transform };
  }, [transform]);

  const panTo = useCallback((px: number, py: number) => {
    const from = panFrom.current;
    if (!from) return false;
    setTransform({ k: from.t.k, x: from.t.x + (px - from.x), y: from.t.y + (py - from.y) });
    return true;
  }, []);

  const endPan = useCallback(() => {
    panFrom.current = null;
  }, []);

  const zoomBy = useCallback((factor: number, px: number, py: number) => {
    setTransform((t) => zoomAbout(t, factor, px, py));
  }, []);

  const reset = useCallback(() => setTransform(IDENTITY), []);

  return { transform, startPan, panTo, endPan, zoomBy, reset, isPanning: () => panFrom.current !== null };
}
