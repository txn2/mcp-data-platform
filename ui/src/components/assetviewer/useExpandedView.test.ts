import { afterEach, describe, expect, it } from "vitest";
import { act, renderHook } from "@testing-library/react";
import { useExpandedView } from "./useExpandedView";

afterEach(() => {
  window.history.replaceState(null, "", "/");
});

describe("useExpandedView (#2390)", () => {
  it("opens with a history entry the back gesture closes, keeping the page's state", () => {
    window.history.replaceState({ appNav: true, from: "/assets" }, "", "/assets/a1?x=1");
    const { result } = renderHook(() => useExpandedView());
    expect(result.current.expanded).toBe(false);

    act(() => result.current.expand());
    expect(result.current.expanded).toBe(true);
    expect(window.location.search).toBe("?x=1&view=full");
    expect(window.history.state).toMatchObject({ appNav: true, from: "/assets", expandedView: true });

    // Back (the gesture, or Close going back over the entry) lands on the
    // page without the parameter, and the hook follows it.
    act(() => {
      window.history.replaceState({ appNav: true }, "", "/assets/a1?x=1");
      window.dispatchEvent(new PopStateEvent("popstate"));
    });
    expect(result.current.expanded).toBe(false);
  });

  it("opens from ?view=full and closes in place when there is no entry under it", () => {
    window.history.replaceState(null, "", "/assets/a1?view=full#top");
    const { result } = renderHook(() => useExpandedView());
    expect(result.current.expanded).toBe(true);

    act(() => result.current.expand());
    expect(window.location.search).toBe("?view=full");

    act(() => result.current.close());
    expect(result.current.expanded).toBe(false);
    expect(window.location.pathname + window.location.search + window.location.hash).toBe("/assets/a1#top");
  });
});
