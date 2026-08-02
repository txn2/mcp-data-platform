import { useCallback, useRef, useState } from "react";

/**
 * useElementSize measures a container so the graph fills its available width.
 * Until the observer reports (the first paint, and any environment without a
 * live ResizeObserver), the fallback width is used so the graph draws its first
 * frame at a sensible size instead of collapsing to nothing.
 *
 * The returned ref is a CALLBACK ref, not an object ref with a mount effect: a
 * component that renders its loading, empty, and ready states as different
 * elements swaps the measured node after the first commit, and a mount-only
 * effect would be left observing a detached element — the width would then never
 * update and the fallback would be permanent. A callback ref is invoked with each
 * new element, so the observer follows the node that is actually on screen.
 */
export function useElementSize<T extends HTMLElement>(fallbackWidth: number) {
  const [width, setWidth] = useState(0);
  const observer = useRef<ResizeObserver | null>(null);

  const ref = useCallback((el: T | null) => {
    observer.current?.disconnect();
    observer.current = null;
    if (!el) return;
    const ro = new ResizeObserver((entries) => setWidth(entries[0]?.contentRect.width ?? 0));
    ro.observe(el);
    observer.current = ro;
    setWidth(el.clientWidth);
  }, []);

  return [ref, width > 0 ? width : fallbackWidth] as const;
}
