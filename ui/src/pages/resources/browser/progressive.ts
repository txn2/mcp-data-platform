import { useEffect, useState } from "react";

/** How many more rows a long list draws each time its end comes into view. */
export const PROGRESSIVE_STEP = 200;

/**
 * useProgressive draws a long list a step at a time (#2837): the first
 * PROGRESSIVE_STEP rows, and another step each time the sentinel placed after
 * the last drawn row comes within reach of the screen; sentinel is the ref
 * that element takes. A folder holding 5,000
 * folders opens with 200 rows drawn rather than 5,000. The rows up to `reach`
 * (the row the keyboard is on) are always drawn, so moving down a list never
 * lands on a row that is not there. reset starts again from the first step,
 * when the list in view is another one.
 *
 * Where the browser has no IntersectionObserver every row is drawn.
 */
export function useProgressive(total: number, reach = -1, reset: unknown = undefined) {
  const [shown, setShown] = useState(PROGRESSIVE_STEP);
  // The sentinel is held in state, so the observer follows the element a
  // render puts after the last drawn row.
  const [el, sentinel] = useState<HTMLElement | null>(null);
  const [lastReset, setLastReset] = useState(reset);
  if (lastReset !== reset) {
    setLastReset(reset);
    setShown(PROGRESSIVE_STEP);
  }
  const limit = Math.min(total, Math.max(shown, reach + 1));

  useEffect(() => {
    if (limit >= total) return;
    if (typeof IntersectionObserver === "undefined") {
      setShown(total);
      return;
    }
    if (!el) return;
    const io = new IntersectionObserver(
      (seen) => {
        if (seen.some((e) => e.isIntersecting)) setShown((n) => Math.max(n, limit) + PROGRESSIVE_STEP);
      },
      { rootMargin: "600px" },
    );
    io.observe(el);
    return () => io.disconnect();
  }, [el, limit, total]);

  return { limit, more: limit < total, sentinel };
}
