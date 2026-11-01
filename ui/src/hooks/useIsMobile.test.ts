import { afterEach, describe, expect, it, vi } from "vitest";
import { act, renderHook } from "@testing-library/react";
import { MOBILE_BREAKPOINT, useIsMobile } from "./useIsMobile";

function setWidth(w: number) {
  Object.defineProperty(window, "innerWidth", { configurable: true, value: w });
}

describe("useIsMobile", () => {
  const original = window.matchMedia;
  afterEach(() => {
    window.matchMedia = original;
    setWidth(1024);
  });

  it("reads the width at mount when there are no media queries", () => {
    // @ts-expect-error jsdom has none; the hook must not throw
    window.matchMedia = undefined;
    setWidth(MOBILE_BREAKPOINT - 1);
    expect(renderHook(() => useIsMobile()).result.current).toBe(true);
    setWidth(MOBILE_BREAKPOINT);
    expect(renderHook(() => useIsMobile()).result.current).toBe(false);
  });

  it("follows the media query as the viewport changes", () => {
    let listener: ((e: MediaQueryListEvent) => void) | undefined;
    const remove = vi.fn();
    window.matchMedia = vi.fn().mockReturnValue({
      addEventListener: (_: string, fn: (e: MediaQueryListEvent) => void) => {
        listener = fn;
      },
      removeEventListener: remove,
    });
    setWidth(1024);
    const { result, unmount } = renderHook(() => useIsMobile());
    expect(result.current).toBe(false);
    expect(window.matchMedia).toHaveBeenCalledWith(`(max-width: ${MOBILE_BREAKPOINT - 1}px)`);
    act(() => listener?.({ matches: true } as MediaQueryListEvent));
    expect(result.current).toBe(true);
    unmount();
    expect(remove).toHaveBeenCalled();
  });
});
