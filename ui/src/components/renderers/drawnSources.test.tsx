import { describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { GraphvizRenderer } from "./GraphvizRenderer";
import { VegaLiteRenderer } from "./VegaLiteRenderer";

describe("GraphvizRenderer", () => {
  it("draws a source, with no link a node's URL names", async () => {
    const onDrawn = vi.fn();
    render(
      <GraphvizRenderer
        content={'digraph { a [URL="javascript:alert(1)" tooltip="tip"]; a -> b }'}
        onDrawn={onDrawn}
      />,
    );
    await waitFor(() => expect(onDrawn).toHaveBeenCalledWith(""), { timeout: 20000 });
    const drawing = screen.getByTestId("graphviz-drawing");
    expect(drawing.querySelectorAll("g.node")).toHaveLength(2);
    expect(drawing.innerHTML).not.toMatch(/javascript:|<a\b/);
  }, 30000);

  it("says why a source cannot be drawn", async () => {
    const onDrawn = vi.fn();
    render(<GraphvizRenderer content="digraph { a -> -> b }" onDrawn={onDrawn} />);
    await waitFor(() => expect(onDrawn).toHaveBeenCalledWith(expect.stringMatching(/could not be drawn/)), {
      timeout: 20000,
    });
    expect(screen.getByRole("alert").textContent).toMatch(/could not be drawn/);
  }, 30000);
});

describe("VegaLiteRenderer", () => {
  it("draws an inline spec", async () => {
    const onDrawn = vi.fn();
    render(
      <VegaLiteRenderer
        content={JSON.stringify({
          data: { values: [{ a: "x", b: 1 }, { a: "y", b: 3 }] },
          mark: "bar",
          encoding: { x: { field: "a", type: "nominal" }, y: { field: "b", type: "quantitative" } },
        })}
        onDrawn={onDrawn}
      />,
    );
    await waitFor(() => expect(onDrawn).toHaveBeenCalledWith(""), { timeout: 20000 });
    expect(screen.getByTestId("vegalite-drawing").querySelector("svg")).not.toBeNull();
  }, 30000);

  it("says why a spec that is not JSON cannot be drawn", async () => {
    const onDrawn = vi.fn();
    render(<VegaLiteRenderer content="{" onDrawn={onDrawn} />);
    await waitFor(() => expect(onDrawn).toHaveBeenCalledWith(expect.stringMatching(/not valid JSON/)));
    expect(screen.getByRole("alert")).toBeTruthy();
  });

  it("refuses data from anywhere but a reference", async () => {
    const onDrawn = vi.fn();
    render(
      <VegaLiteRenderer
        content={JSON.stringify({ data: { url: "https://example.com/d.csv" }, mark: "bar" })}
        onDrawn={onDrawn}
      />,
    );
    await waitFor(() => expect(onDrawn).toHaveBeenCalledWith(expect.stringMatching(/refused/)), { timeout: 20000 });
  }, 30000);
});
