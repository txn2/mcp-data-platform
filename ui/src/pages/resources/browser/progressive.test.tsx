import { act, renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { PROGRESSIVE_STEP, useProgressive } from "./progressive";

type Callback = (entries: { isIntersecting: boolean }[]) => void;

describe("useProgressive (#2837)", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("draws a step, the next as the end comes into view, and always the row the keyboard is on", () => {
    let fire: Callback = () => {};
    const observe = vi.fn();
    vi.stubGlobal(
      "IntersectionObserver",
      class {
        constructor(cb: Callback) {
          fire = cb;
        }
        observe = observe;
        disconnect() {}
      },
    );
    const { result, rerender } = renderHook(({ total, reach, reset }) => useProgressive(total, reach, reset), {
      initialProps: { total: 5000, reach: -1, reset: "a" },
    });
    expect(result.current.limit).toBe(PROGRESSIVE_STEP);
    expect(result.current.more).toBe(true);

    act(() => result.current.sentinel(document.createElement("div")));
    expect(observe).toHaveBeenCalledTimes(1);
    act(() => fire([{ isIntersecting: false }]));
    expect(result.current.limit).toBe(PROGRESSIVE_STEP);
    act(() => fire([{ isIntersecting: true }]));
    expect(result.current.limit).toBe(2 * PROGRESSIVE_STEP);

    rerender({ total: 5000, reach: 1200, reset: "a" });
    expect(result.current.limit).toBe(1201);

    rerender({ total: 5000, reach: -1, reset: "b" });
    expect(result.current.limit).toBe(PROGRESSIVE_STEP);

    rerender({ total: 12, reach: -1, reset: "c" });
    expect(result.current.limit).toBe(12);
    expect(result.current.more).toBe(false);
  });

  it("draws every row where the browser cannot observe", () => {
    vi.stubGlobal("IntersectionObserver", undefined);
    const { result } = renderHook(() => useProgressive(900));
    expect(result.current.limit).toBe(900);
  });
});
