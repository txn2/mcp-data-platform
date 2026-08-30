import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import {
  getStoredViewMode,
  storeViewMode,
  RESOURCE_VIEW_STORAGE_KEY,
} from "./listView";

// The environment has no localStorage of its own, which is also the state a
// private window and a browser set to block site data leave the page in — so
// the absent case below is the real one, not a contrivance.
function withStorage(store: Map<string, string>) {
  vi.stubGlobal("localStorage", {
    getItem: (k: string) => store.get(k) ?? null,
    setItem: (k: string, v: string) => void store.set(k, v),
  });
}

beforeEach(() => {
  withStorage(new Map());
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("the layout a list is drawn in", () => {
  it("is the gallery until somebody says otherwise", () => {
    expect(getStoredViewMode()).toBe("grid");
    expect(getStoredViewMode(RESOURCE_VIEW_STORAGE_KEY)).toBe("grid");
  });

  it("comes back as it was left", () => {
    storeViewMode("table");
    expect(getStoredViewMode()).toBe("table");

    storeViewMode("grid");
    expect(getStoredViewMode()).toBe("grid");
  });

  // A library is a folder tree and the Assets page is a flat gallery, so a
  // reader who wants rows in one and tiles in the other is not confused (#1553).
  it("is remembered per list rather than once for the portal", () => {
    storeViewMode("table", RESOURCE_VIEW_STORAGE_KEY);

    expect(getStoredViewMode(RESOURCE_VIEW_STORAGE_KEY)).toBe("table");
    expect(getStoredViewMode()).toBe("grid");
  });

  it("falls back to the gallery where there is nowhere to remember it", () => {
    vi.stubGlobal("localStorage", undefined);
    expect(getStoredViewMode(RESOURCE_VIEW_STORAGE_KEY)).toBe("grid");
    // Best-effort: a list that cannot persist the choice still redraws on it.
    expect(() => storeViewMode("table", RESOURCE_VIEW_STORAGE_KEY)).not.toThrow();
  });

  it("falls back to the gallery when the store itself refuses", () => {
    vi.stubGlobal("localStorage", {
      getItem: () => {
        throw new Error("site data is blocked");
      },
      setItem: () => {
        throw new Error("site data is blocked");
      },
    });
    expect(getStoredViewMode(RESOURCE_VIEW_STORAGE_KEY)).toBe("grid");
    expect(() => storeViewMode("table", RESOURCE_VIEW_STORAGE_KEY)).not.toThrow();
  });
});
