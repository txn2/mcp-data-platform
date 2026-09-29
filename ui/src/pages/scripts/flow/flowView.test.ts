import { afterEach, describe, expect, it, vi } from "vitest";
import { initialView, rememberView } from "./flowView";

describe("flowView (#1972)", () => {
  afterEach(() => {
    window.history.replaceState(null, "", "/");
    vi.unstubAllGlobals();
  });

  it("opens on the address's view, else the stored one, else Structure", () => {
    window.history.replaceState(null, "", "/automations/x?view=timeline");
    expect(initialView()).toBe("timeline");
    window.history.replaceState(null, "", "/automations/x?view=bogus");
    const store = new Map<string, string>([["portal.flow.view", "calls"]]);
    vi.stubGlobal("localStorage", {
      getItem: (k: string) => store.get(k) ?? null,
      setItem: (k: string, v: string) => store.set(k, v),
    });
    expect(initialView()).toBe("calls");
    store.clear();
    expect(initialView()).toBe("structure");
  });

  it("keeps a choice in the address, the path unchanged, and in storage", () => {
    const store = new Map<string, string>();
    vi.stubGlobal("localStorage", {
      getItem: (k: string) => store.get(k) ?? null,
      setItem: (k: string, v: string) => store.set(k, v),
    });
    window.history.replaceState(null, "", "/automations/x?tab=1");
    rememberView("calls");
    expect(window.location.pathname).toBe("/automations/x");
    expect(window.location.search).toBe("?tab=1&view=calls");
    expect(store.get("portal.flow.view")).toBe("calls");
  });

  it("survives storage that throws", () => {
    vi.stubGlobal("localStorage", {
      getItem: () => {
        throw new Error("blocked");
      },
      setItem: () => {
        throw new Error("blocked");
      },
    });
    expect(initialView()).toBe("structure");
    expect(() => rememberView("timeline")).not.toThrow();
  });
});
