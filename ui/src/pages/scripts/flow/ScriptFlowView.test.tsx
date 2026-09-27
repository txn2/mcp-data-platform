import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import type { ScriptFlow } from "@/api/portal/hooks/scriptFlow";
import { ScriptFlowView } from "./ScriptFlowView";
import { sampleGraph } from "./testGraph";

vi.mock("@/api/portal/hooks/scriptFlow", () => ({ useScriptFlow: vi.fn() }));
import { useScriptFlow } from "@/api/portal/hooks/scriptFlow";
const mockFlow = vi.mocked(useScriptFlow);

function answer(data: ScriptFlow | undefined, extra: Record<string, unknown> = {}) {
  mockFlow.mockReturnValue({ data, isLoading: false, error: null, ...extra } as unknown as ReturnType<
    typeof useScriptFlow
  >);
}

const source = Array.from({ length: 20 }, (_, i) => `line_${i + 1} = ${i + 1}`).join("\n");
const onShowLines = vi.fn();

function renderView(selection: { from: number; to: number } | null = null) {
  return render(
    <ScriptFlowView
      scriptId="script-001"
      version={2}
      source={source}
      sourceSelection={selection}
      onShowLines={onShowLines}
    />,
  );
}

// card is the drawn card for a node id, once the layout has run.
async function card(id: string) {
  await screen.findByTestId("flow-canvas");
  const el = document.querySelector(`[data-node="${id}"]`);
  if (!el) throw new Error(`no card ${id}`);
  return el as SVGGElement;
}

beforeEach(() => vi.clearAllMocks());
afterEach(cleanup);

describe("ScriptFlowView: the states before a diagram", () => {
  it("says it is reading, and says so when it cannot", () => {
    mockFlow.mockReturnValue({ isLoading: true } as ReturnType<typeof useScriptFlow>);
    renderView();
    expect(screen.getByText(/Reading the script/)).toBeInTheDocument();
    cleanup();
    answer(undefined, { error: new Error("boom") });
    renderView();
    expect(screen.getByText(/could not be read/)).toBeInTheDocument();
  });

  it("shows a source that does not parse as its findings, not an empty canvas", () => {
    answer({
      ...sampleGraph(),
      ok: false,
      nodes: [],
      edges: [],
      groups: [],
      params: [],
      findings: [{ severity: "error", line: 3, message: "got class, want primary", hint: "There are no classes." }],
    });
    renderView();
    const box = screen.getByTestId("flow-findings");
    expect(box).toHaveTextContent("Version 2 does not parse");
    expect(box).toHaveTextContent("line 3: got class, want primary");
    expect(box).toHaveTextContent("There are no classes.");
    expect(screen.queryByTestId("flow-canvas")).not.toBeInTheDocument();
  });

  it("says a script with no platform calls has nothing to draw", () => {
    answer({ ...sampleGraph(), nodes: [], edges: [], groups: [], params: [] });
    renderView();
    expect(screen.getByTestId("flow-empty")).toHaveTextContent("makes no platform calls");
  });
});

describe("ScriptFlowView: the diagram", () => {
  it("draws every card, the box and the arrows, and explains how to read them", async () => {
    answer(sampleGraph());
    renderView();
    expect(await card("op:2")).toHaveAccessibleName("Reads: API crm");
    expect(await card("op:3")).toHaveTextContent("Export CSV to {dest}");
    expect(screen.getByRole("button", { name: "Function load(day)" })).toHaveTextContent("Load one day.");
    expect(document.querySelector('[data-edge="op:4->state"]')).toHaveAttribute("stroke-dasharray", "6 4");
    expect(document.querySelector('[data-edge="op:1->op:2"] title')).toHaveTextContent("reshaped by clean()");
    const side = screen.getByTestId("flow-side-panel");
    expect(side).toHaveTextContent("How to read this");
    expect(side).toHaveTextContent("20 lines");
  });

  it("notes a diagram cut at its step bound", async () => {
    answer({ ...sampleGraph(), truncated: true });
    renderView();
    expect(await screen.findByText(/more steps than one diagram draws/)).toBeInTheDocument();
  });

  it("fills the side panel from the selected card and opens its lines in Source", async () => {
    answer(sampleGraph());
    renderView();
    fireEvent.pointerUp(await card("op:2"));
    const side = within(screen.getByTestId("flow-side-panel"));
    expect(side.getByText("API crm")).toBeInTheDocument();
    expect(side.getByText("Daily load: look up each account.")).toBeInTheDocument();
    expect(side.getByText("fetch(), called on line 9")).toBeInTheDocument();
    expect(side.getByText("Query trino")).toBeInTheDocument();
    expect(side.getByText("Export CSV to {dest}")).toBeInTheDocument();
    expect(side.getByText(/14\s+line_14 = 14/)).toBeInTheDocument();
    expect(await card("op:2")).toHaveAttribute("aria-pressed", "true");

    fireEvent.click(side.getByRole("button", { name: "Show in Source" }));
    expect(onShowLines).toHaveBeenCalledWith([14, 9]);

    fireEvent.doubleClick(await card("op:1"));
    expect(onShowLines).toHaveBeenLastCalledWith([5, 6]);
  });

  it("describes a computed card, a box and the state input", async () => {
    answer(sampleGraph());
    renderView();
    fireEvent.pointerUp(await card("op:3"));
    expect(screen.getByTestId("flow-side-panel")).toHaveTextContent("computed when the script runs");

    fireEvent.pointerUp(screen.getByRole("button", { name: "Function load(day)" }));
    const side = within(screen.getByTestId("flow-side-panel"));
    expect(side.getByText("load(day)")).toBeInTheDocument();
    expect(side.getByText("line 8")).toBeInTheDocument();
    fireEvent.click(side.getByRole("button", { name: "Show in Source" }));
    expect(onShowLines).toHaveBeenCalledWith([4]);

    fireEvent.keyDown(await card("state"), { key: "Enter" });
    expect(screen.getByTestId("flow-side-panel")).toHaveTextContent("what the last run saved");
  });

  it("lights exactly the steps a parameter reaches, and dims the rest", async () => {
    answer(sampleGraph());
    renderView();
    await card("op:1");
    fireEvent.click(screen.getByRole("button", { name: "day" }));
    expect(await card("op:1")).toHaveAttribute("aria-pressed", "true");
    expect(await card("op:2")).toHaveAttribute("opacity", "0.35");
    const side = screen.getByTestId("flow-side-panel");
    expect(side).toHaveTextContent("Parameter");
    expect(side).toHaveTextContent("line 3");

    fireEvent.click(screen.getByRole("button", { name: /mode/ }));
    expect(screen.getByTestId("flow-side-panel")).toHaveTextContent("which steps run: a condition reads it");
    expect(screen.getByTestId("flow-side-panel")).toHaveTextContent("no step's arguments");
    fireEvent.click(screen.getByRole("button", { name: /mode/ }));
    expect(screen.getByTestId("flow-side-panel")).toHaveTextContent("How to read this");
  });

  it("marks the cards the lines selected in Source produced", async () => {
    answer(sampleGraph());
    renderView({ from: 9, to: 9 });
    expect(await card("op:2")).toHaveAttribute("aria-pressed", "true");
    expect(await card("op:1")).toHaveAttribute("opacity", "0.35");
  });

  it("lets a new selection in Source replace the card picked on the diagram", async () => {
    answer(sampleGraph());
    const { rerender } = renderView();
    fireEvent.pointerUp(await card("op:1"));
    rerender(
      <ScriptFlowView
        scriptId="script-001"
        version={2}
        source={source}
        sourceSelection={{ from: 11, to: 12 }}
        onShowLines={onShowLines}
      />,
    );
    expect(await card("op:1")).toHaveAttribute("aria-pressed", "false");
    expect(await card("op:3")).toHaveAttribute("aria-pressed", "true");
    expect(await card("op:4")).toHaveAttribute("aria-pressed", "true");
  });

  it("clears the selection on a click on the canvas, and pans and zooms", async () => {
    answer(sampleGraph());
    renderView();
    fireEvent.pointerUp(await card("op:2"));
    const canvas = screen.getByTestId("flow-canvas");
    fireEvent.pointerDown(canvas, { button: 0, clientX: 10, clientY: 10 });
    fireEvent.pointerUp(canvas);
    expect(screen.getByTestId("flow-side-panel")).toHaveTextContent("How to read this");

    const g = canvas.querySelector("svg > g")!;
    const before = g.getAttribute("transform");
    fireEvent.pointerDown(canvas, { button: 0, clientX: 10, clientY: 10 });
    fireEvent.pointerMove(canvas, { clientX: 60, clientY: 40 });
    fireEvent.pointerUp(canvas);
    expect(g.getAttribute("transform")).not.toBe(before);

    const moved = g.getAttribute("transform");
    fireEvent.click(screen.getByRole("button", { name: "Zoom in" }));
    fireEvent.click(screen.getByRole("button", { name: "Zoom out" }));
    fireEvent.wheel(canvas, { deltaY: -100 });
    expect(g.getAttribute("transform")).not.toBe(moved);
    fireEvent.click(screen.getByRole("button", { name: "Fit the whole diagram" }));
    expect(g.getAttribute("transform")).toMatch(/scale\(/);
    fireEvent.pointerLeave(canvas);
  });
});
