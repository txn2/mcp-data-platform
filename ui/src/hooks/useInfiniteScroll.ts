import { useEffect, useRef, type RefObject } from "react";

interface InfiniteScrollOptions {
  /** Whether there is another page to fetch. */
  hasMore: boolean;
  /** Whether a page fetch is currently in flight. */
  isLoading: boolean;
  /** Invoked when the list is scrolled near the bottom and a load is warranted. */
  onLoadMore: () => void;
  /** How far (px) from the bottom to pre-fetch (default 600). */
  threshold?: number;
}

/**
 * nearestScrollParent walks up from `el` to the first ancestor that scrolls
 * vertically, or null if none. The portal renders its list inside a scrolling
 * `<main overflow-auto>`, so the scroll happens on that container rather than
 * the window — the listener must attach there.
 */
function nearestScrollParent(el: HTMLElement | null): HTMLElement | null {
  let node = el?.parentElement ?? null;
  while (node) {
    const oy = getComputedStyle(node).overflowY;
    if (oy === "auto" || oy === "scroll" || oy === "overlay") return node;
    node = node.parentElement;
  }
  return null;
}

/**
 * useInfiniteScroll auto-invokes `onLoadMore` when the list scrolls within
 * `threshold` px of the bottom of its scroll container. A scroll listener is
 * used rather than IntersectionObserver because the portal's list lives inside
 * a nested `overflow-auto` container where an observer proved unreliable. It is
 * a no-op when there is nothing more to load or a load is already in flight, and
 * degenerate layouts (zero scrollHeight, e.g. jsdom in tests) never trigger a
 * load — callers keep a manual "Load more" control as the fallback/indicator.
 *
 * The latest `onLoadMore` is read through a ref so its closure (which may depend
 * on the active scope/filters) stays current without re-attaching the listener.
 */
export function useInfiniteScroll(
  ref: RefObject<HTMLElement | null>,
  { hasMore, isLoading, onLoadMore, threshold = 600 }: InfiniteScrollOptions,
): void {
  const onLoadMoreRef = useRef(onLoadMore);
  onLoadMoreRef.current = onLoadMore;

  useEffect(() => {
    if (!hasMore || isLoading) return;
    const scroller = nearestScrollParent(ref.current);

    const nearBottom = (): boolean => {
      const st = scroller ? scroller.scrollTop : window.scrollY;
      const ch = scroller ? scroller.clientHeight : window.innerHeight;
      const sh = scroller
        ? scroller.scrollHeight
        : document.documentElement.scrollHeight;
      // sh > 0 guards degenerate layouts (jsdom reports 0) so tests do not
      // auto-fire; ch < sh ensures there is actually something to scroll to.
      return sh > 0 && ch < sh && st + ch >= sh - threshold;
    };

    const check = (): void => {
      if (nearBottom()) onLoadMoreRef.current();
    };

    const target: HTMLElement | Window = scroller ?? window;
    target.addEventListener("scroll", check, { passive: true });
    // Fire once in case the list already ends within the viewport (short page).
    check();
    return () => target.removeEventListener("scroll", check);
  }, [ref, hasMore, isLoading, threshold]);
}
