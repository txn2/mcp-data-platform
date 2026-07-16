import { describe, it, expect, vi, afterEach } from "vitest";
import { renderHook } from "@testing-library/react";
import { act } from "react";
import type { RefObject } from "react";
import { useInfiniteScroll } from "./useInfiniteScroll";

// Build a scroll container (overflow-auto) with a sentinel child and mocked
// scroll geometry, then render the hook against the sentinel. jsdom reports 0
// for scroll* by default, so each dimension is defined explicitly.
function setup(opts: {
  hasMore: boolean;
  isLoading: boolean;
  scrollTop: number;
  clientHeight: number;
  scrollHeight: number;
}) {
  const scroller = document.createElement("div");
  scroller.style.overflowY = "auto";
  const sentinel = document.createElement("div");
  scroller.appendChild(sentinel);
  document.body.appendChild(scroller);

  Object.defineProperty(scroller, "scrollHeight", { value: opts.scrollHeight, configurable: true });
  Object.defineProperty(scroller, "clientHeight", { value: opts.clientHeight, configurable: true });
  Object.defineProperty(scroller, "scrollTop", { value: opts.scrollTop, writable: true, configurable: true });

  const onLoadMore = vi.fn();
  const ref = { current: sentinel } as RefObject<HTMLElement | null>;
  renderHook(() =>
    useInfiniteScroll(ref, { hasMore: opts.hasMore, isLoading: opts.isLoading, onLoadMore, threshold: 100 }),
  );
  return { scroller, onLoadMore };
}

afterEach(() => {
  document.body.innerHTML = "";
});

describe("useInfiniteScroll", () => {
  it("loads immediately when the list already ends within view (near bottom on mount)", () => {
    const { onLoadMore } = setup({ hasMore: true, isLoading: false, scrollTop: 900, clientHeight: 100, scrollHeight: 1000 });
    expect(onLoadMore).toHaveBeenCalledTimes(1);
  });

  it("loads when a scroll brings the list near the bottom", () => {
    const { scroller, onLoadMore } = setup({ hasMore: true, isLoading: false, scrollTop: 0, clientHeight: 100, scrollHeight: 1000 });
    expect(onLoadMore).not.toHaveBeenCalled(); // top of a long list
    act(() => {
      Object.defineProperty(scroller, "scrollTop", { value: 850, configurable: true });
      scroller.dispatchEvent(new Event("scroll"));
    });
    expect(onLoadMore).toHaveBeenCalledTimes(1);
  });

  it("does nothing when there is no next page", () => {
    const { scroller, onLoadMore } = setup({ hasMore: false, isLoading: false, scrollTop: 950, clientHeight: 100, scrollHeight: 1000 });
    act(() => scroller.dispatchEvent(new Event("scroll")));
    expect(onLoadMore).not.toHaveBeenCalled();
  });

  it("does nothing while a load is already in flight", () => {
    const { scroller, onLoadMore } = setup({ hasMore: true, isLoading: true, scrollTop: 950, clientHeight: 100, scrollHeight: 1000 });
    act(() => scroller.dispatchEvent(new Event("scroll")));
    expect(onLoadMore).not.toHaveBeenCalled();
  });

  it("does not fire on a degenerate zero-height layout (jsdom / unmeasured)", () => {
    const { onLoadMore } = setup({ hasMore: true, isLoading: false, scrollTop: 0, clientHeight: 0, scrollHeight: 0 });
    expect(onLoadMore).not.toHaveBeenCalled();
  });
});
