import { describe, it, expect, vi, afterEach } from "vitest";
import { renderHook, act, waitFor } from "@testing-library/react";
import { useIdleGate, whenIdle } from "./idle";

// The setup file stubs requestIdleCallback onto a prompt timer, so these
// exercise the same branch a browser takes.

interface IdleGlobal {
  requestIdleCallback?: (cb: () => void, opts?: { timeout: number }) => number;
}

function setVisibility(state: DocumentVisibilityState) {
  Object.defineProperty(document, "visibilityState", {
    configurable: true,
    get: () => state,
  });
  act(() => {
    document.dispatchEvent(new Event("visibilitychange"));
  });
}

afterEach(() => {
  setVisibility("visible");
});

describe("whenIdle", () => {
  it("runs the callback", async () => {
    const fn = vi.fn();
    whenIdle(fn);
    await waitFor(() => expect(fn).toHaveBeenCalled());
  });

  it("does not run a cancelled callback", async () => {
    const fn = vi.fn();
    whenIdle(fn)();
    await new Promise((r) => setTimeout(r, 20));
    expect(fn).not.toHaveBeenCalled();
  });

  // Safari shipped requestIdleCallback late enough that the fallback path is
  // real, not defensive.
  it("falls back to a timer when the browser has no idle callback", async () => {
    const g = globalThis as unknown as IdleGlobal;
    const saved = g.requestIdleCallback;
    g.requestIdleCallback = undefined;
    vi.useFakeTimers();
    try {
      const fn = vi.fn();
      whenIdle(fn);
      expect(fn).not.toHaveBeenCalled();
      vi.advanceTimersByTime(1000);
      expect(fn).toHaveBeenCalled();
    } finally {
      vi.useRealTimers();
      g.requestIdleCallback = saved;
    }
  });
});

describe("useIdleGate", () => {
  it("stays shut while disabled", async () => {
    const { result } = renderHook(() => useIdleGate(false));
    await new Promise((r) => setTimeout(r, 20));
    expect(result.current).toBe(false);
  });

  it("opens once the browser goes idle", async () => {
    const { result } = renderHook(() => useIdleGate(true));
    await waitFor(() => expect(result.current).toBe(true));
  });

  // Work started in a background tab lands on the reader as jank the moment
  // they come back, so a hidden tab holds the gate shut.
  it("stays shut while the tab is hidden, and opens when it returns", async () => {
    setVisibility("hidden");
    const { result } = renderHook(() => useIdleGate(true));
    await new Promise((r) => setTimeout(r, 20));
    expect(result.current).toBe(false);

    setVisibility("visible");
    await waitFor(() => expect(result.current).toBe(true));
  });

  // The gate says when work may start, not whether it may continue. Tearing it
  // down on a tab switch abandons a capture halfway, which is paid for twice
  // and finishes never.
  it("stays open when the tab is hidden after the work started", async () => {
    const { result } = renderHook(() => useIdleGate(true));
    await waitFor(() => expect(result.current).toBe(true));

    setVisibility("hidden");
    await new Promise((r) => setTimeout(r, 20));
    expect(result.current).toBe(true);
  });

  it("closes again when the caller has nothing to do", async () => {
    const { result, rerender } = renderHook(({ on }) => useIdleGate(on), {
      initialProps: { on: true },
    });
    await waitFor(() => expect(result.current).toBe(true));

    rerender({ on: false });
    await waitFor(() => expect(result.current).toBe(false));
  });
});
