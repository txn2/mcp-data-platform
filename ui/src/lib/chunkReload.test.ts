import { describe, it, expect, vi, beforeEach } from "vitest";
import { isChunkLoadError, reloadOnceForChunkError, CHUNK_RELOAD_WINDOW_MS } from "./chunkReload";

// What each browser rejects a failed dynamic import with, and what Vite
// rejects a lazy module's failed stylesheet preload with.
const chunkErrors = [
  new TypeError("Failed to fetch dynamically imported module: https://host/portal/assets/AutomationsPage-abc.js"),
  new TypeError("error loading dynamically imported module: https://host/portal/assets/AutomationsPage-abc.js"),
  new TypeError("Importing a module script failed."),
  new Error("Unable to preload CSS for /portal/assets/AutomationsPage-abc.css"),
];

describe("isChunkLoadError", () => {
  it.each(chunkErrors)("recognizes %s", (err) => {
    expect(isChunkLoadError(err)).toBe(true);
  });

  it("does not take a render error for a missing chunk", () => {
    expect(isChunkLoadError(new TypeError("Cannot read properties of undefined (reading 'map')"))).toBe(false);
    expect(isChunkLoadError("dynamically imported module")).toBe(false);
    expect(isChunkLoadError(undefined)).toBe(false);
  });
});

describe("reloadOnceForChunkError", () => {
  beforeEach(() => {
    try {
      sessionStorage.clear();
    } catch {
      // A browser with storage disabled still has to get the one reload.
    }
  });

  it("reloads once on a chunk that did not load", () => {
    const reload = vi.fn();
    expect(reloadOnceForChunkError(chunkErrors[0], 1_000, reload)).toBe(true);
    expect(reload).toHaveBeenCalledTimes(1);
  });

  // The reloaded page can fail the same way (the replica it reached is still
  // on the other build); it shows the error rather than reloading in a loop.
  it("does not reload again inside the window", () => {
    const reload = vi.fn();
    reloadOnceForChunkError(chunkErrors[0], 1_000, reload);
    expect(reloadOnceForChunkError(chunkErrors[0], 1_000 + CHUNK_RELOAD_WINDOW_MS - 1, reload)).toBe(false);
    expect(reload).toHaveBeenCalledTimes(1);
  });

  // A later deploy, long after the first reload, recovers the same way.
  it("reloads again once the window has passed", () => {
    const reload = vi.fn();
    reloadOnceForChunkError(chunkErrors[0], 1_000, reload);
    expect(reloadOnceForChunkError(chunkErrors[0], 1_000 + CHUNK_RELOAD_WINDOW_MS, reload)).toBe(true);
    expect(reload).toHaveBeenCalledTimes(2);
  });

  it("does not reload for any other error", () => {
    const reload = vi.fn();
    expect(reloadOnceForChunkError(new Error("boom"), 1_000, reload)).toBe(false);
    expect(reload).not.toHaveBeenCalled();
  });
});
