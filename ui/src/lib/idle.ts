import { useEffect, useState } from "react";

/**
 * Scheduling for work that must never compete with what the reader is doing.
 *
 * Thumbnail capture is the case this exists for. It renders a second copy of
 * an asset off-screen and rasterizes it with html2canvas, which is a long
 * synchronous task on the main thread — on the assets home that ran for every
 * asset missing a thumbnail, the moment the list arrived, and the page stopped
 * responding (#1351). The work is still worth doing in the browser; it is not
 * worth doing while someone is waiting for the page.
 */

/** Fallback delay when the browser has no requestIdleCallback (Safari < 17). */
const IDLE_FALLBACK_MS = 1000;

/**
 * How long to wait for genuine idle before running anyway. Without a deadline
 * a page that never goes idle would never capture a thumbnail at all, and the
 * asset would stay a placeholder icon forever.
 */
const IDLE_DEADLINE_MS = 3000;

type IdleWindow = Window & {
  requestIdleCallback?: (cb: () => void, opts?: { timeout: number }) => number;
  cancelIdleCallback?: (handle: number) => void;
};

/**
 * whenIdle runs fn once the browser is idle, or once the deadline passes.
 * Returns a cancel function.
 */
export function whenIdle(fn: () => void): () => void {
  const w = window as IdleWindow;
  if (typeof w.requestIdleCallback === "function") {
    const handle = w.requestIdleCallback(fn, { timeout: IDLE_DEADLINE_MS });
    return () => w.cancelIdleCallback?.(handle);
  }
  const timer = setTimeout(fn, IDLE_FALLBACK_MS);
  return () => clearTimeout(timer);
}

/**
 * useIdleGate reports whether deferred work may start now: the tab is visible
 * and the browser has been idle at least once since the gate opened.
 *
 * Visibility is part of it because a background tab is not idle in a useful
 * sense — its timers are throttled and its rasterization is unreliable — and
 * because work started there lands on the reader as jank the moment they come
 * back.
 *
 * The gate is about when work may *start*, not whether it may continue, so it
 * latches: once open it stays open until the caller says it has nothing left
 * to do. Closing it on every visibility change instead would tear down work
 * already in flight — a capture abandoned halfway is paid for twice and
 * finishes never — so a reader who tabs away mid-capture gets the thumbnail
 * they would otherwise never get, and the next item still waits for them to
 * come back before it starts.
 *
 * Passing `enabled: false` shuts the gate and re-arms it, so each unit of work
 * gets its own idle check.
 */
export function useIdleGate(enabled: boolean): boolean {
  const [open, setOpen] = useState(false);

  useEffect(() => {
    if (!enabled) {
      setOpen(false);
      return;
    }
    let cancelIdle: (() => void) | undefined;
    let cancelled = false;
    let opened = false;

    const arm = () => {
      if (opened) return;
      cancelIdle?.();
      if (document.visibilityState !== "visible") return;
      cancelIdle = whenIdle(() => {
        if (cancelled) return;
        opened = true;
        setOpen(true);
      });
    };

    arm();
    document.addEventListener("visibilitychange", arm);
    return () => {
      cancelled = true;
      cancelIdle?.();
      document.removeEventListener("visibilitychange", arm);
    };
  }, [enabled]);

  return open;
}
