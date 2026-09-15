import { describe, it, expect, vi, afterEach } from "vitest";
import { captureFailure, failCapture, failureSentence } from "./captureFailure";

// A capture happens in the reader's browser and writes nothing to a server, so
// the console line and the callback are the only records a failure leaves
// anywhere. Before this every path discarded both (#1752).

afterEach(() => vi.restoreAllMocks());

describe("failCapture", () => {
  it("names what the capture was of, and why it failed", () => {
    const error = vi.spyOn(console, "error").mockImplementation(() => {});

    failCapture({ kind: "resource", id: "res-7" }, undefined, "render", new Error("boom"));

    expect(error).toHaveBeenCalledTimes(1);
    const line = String(error.mock.calls[0]![0]);
    expect(line).toContain("resource res-7");
    expect(line).toContain("render");
    expect(line).toContain("boom");
  });

  it("hands the reason to the caller as well as the console", () => {
    vi.spyOn(console, "error").mockImplementation(() => {});
    const onFailed = vi.fn();

    failCapture({ kind: "asset", id: "a1" }, onFailed, "upload", "the route answered 503");

    expect(onFailed).toHaveBeenCalledWith({
      code: "upload",
      detail: "the route answered 503",
      transient: true,
    });
  });
});

describe("captureFailure", () => {
  it("reduces an exception to a line", () => {
    expect(captureFailure("render", new TypeError("not a function")).detail).toBe(
      "TypeError: not a function",
    );
  });

  it("reduces a response-shaped object to a line", () => {
    expect(captureFailure("upload", { status: 503 }).detail).toBe('{"status":503}');
  });

  // A document that threw in the rasterizer throws every time, and a linked
  // file that 404s keeps 404ing. A timeout or a refused upload is the other
  // thing entirely.
  it("separates the reasons another attempt could change from the ones it cannot", () => {
    expect(captureFailure("render", "x").transient).toBe(false);
    expect(captureFailure("references", "x").transient).toBe(false);
    expect(captureFailure("unsupported", "x").transient).toBe(false);
    expect(captureFailure("timeout", "x").transient).toBe(true);
    expect(captureFailure("upload", "x").transient).toBe(true);
    expect(captureFailure("content", "x").transient).toBe(true);
  });
});

describe("failureSentence", () => {
  it("says what happened and whether waiting will help", () => {
    expect(failureSentence(captureFailure("render", "x"))).toContain("could not be drawn");
    expect(failureSentence(captureFailure("render", "x"))).toContain("same result");
    expect(failureSentence(captureFailure("timeout", "x"))).toContain("may work");
  });

  it("has a sentence for every reason a capture can fail for", () => {
    for (const code of [
      "references",
      "render",
      "upload",
      "timeout",
      "content",
      "unsupported",
    ] as const) {
      expect(failureSentence(captureFailure(code, "x")).length).toBeGreaterThan(30);
    }
  });
});
