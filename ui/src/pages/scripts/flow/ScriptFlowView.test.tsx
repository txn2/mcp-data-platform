import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import type { ScriptFlow } from "@/api/portal/hooks/scriptFlow";
import { ScriptFlowView } from "./ScriptFlowView";
import { sampleGraph } from "./testGraph";
import { layoutFlow } from "./flowLayout";

vi.mock("@/api/portal/hooks/scriptFlow", () => ({ useScriptFlow: vi.fn(), useScriptRunFlow: vi.fn() }));
vi.mock("@/api/portal/hooks/scriptRuns", () => ({ useScriptRunPage: vi.fn(), RUN_PAGE_SIZE: 25 }));
vi.mock("@/api/portal/hooks/scripts", () => ({ usePortalScriptVersions: vi.fn() }));
import { useScriptFlow, useScriptRunFlow } from "@/api/portal/hooks/scriptFlow";
import { useScriptRunPage } from "@/api/portal/hooks/scriptRuns";
import { usePortalScriptVersions } from "@/api/portal/hooks/scripts";
const mockFlow = vi.mocked(useScriptFlow);
const mockRunFlow = vi.mocked(useScriptRunFlow);
const mockRuns = vi.mocked(useScriptRunPage);
const mockVersions = vi.mocked(usePortalScriptVersions);

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

// LAYOUT_WAIT bounds a wait on the diagram: a layout runs asynchronously, and
// on a loaded machine takes longer than the default second. Loading the layout
// engine itself is paid once, in beforeAll below, not by whichever test draws
// first.
const LAYOUT_WAIT = { timeout: 4_000 };

// card is the drawn card for a node id, once the layout has run.
async function card(id: string) {
  await screen.findByTestId("flow-canvas", {}, LAYOUT_WAIT);
  const el = document.querySelector(`[data-node="${id}"]`);
  if (!el) throw new Error(`no card ${id}`);
  return el as SVGGElement;
}

beforeAll(async () => {
  await layoutFlow(sampleGraph());
}, 30_000);

beforeEach(() => {
  vi.clearAllMocks();
  // These describe the Calls view (#1906); the Structure view, the default
  // since #1972, is described in ScriptFlowView.structure.test.tsx.
  window.history.replaceState(null, "", "/?view=calls");
  // A view chosen in one test is kept in browser storage where the runtime
  // has it, and would open the next test on that view.
  try {
    window.localStorage?.removeItem("portal.flow.view");
  } catch {
    // No storage to clear.
  }
  mockRuns.mockReturnValue({ data: undefined } as ReturnType<typeof useScriptRunPage>);
  mockVersions.mockReturnValue({ data: undefined } as ReturnType<typeof usePortalScriptVersions>);
  mockRunFlow.mockReturnValue({ isLoading: false } as ReturnType<typeof useScriptRunFlow>);
});
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
    answer({
      ...sampleGraph(),
      nodes: [],
      edges: [],
      groups: [],
      params: [],
      structure: {
        nodes: [
          { id: "s:1", kind: "start", line: 0 },
          { id: "s:2", kind: "end", line: 0 },
        ],
        edges: [{ from: "s:1", to: "s:2" }],
        boxes: [],
        functions: [],
        truncated: false,
      },
    });
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
    expect(await screen.findByText(/more steps than one diagram draws/, {}, LAYOUT_WAIT)).toBeInTheDocument();
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

describe("ScriptFlowView: a compared graph (#1908)", () => {
  function compared(): ScriptFlow {
    const g = sampleGraph();
    g.compared_with = 1;
    g.nodes[2] = { ...g.nodes[2]!, change: "changed", was: { title: "API crm-old", detail: [] } };
    g.nodes[3] = { ...g.nodes[3]!, change: "added" };
    g.nodes.push({ ...g.nodes[1]!, id: "was:op:9", title: "Query lake", change: "removed", group: undefined });
    g.edges.push({ from: "was:op:9", to: "op:2", via: [], kind: "data", change: "removed" });
    return g;
  }

  it("counts the changes in place of the reading guide and marks each card", async () => {
    answer(compared());
    render(<ScriptFlowView scriptId="script-001" version={2} compareWith={1} source={source} sourceSelection={null} />);
    expect(mockFlow).toHaveBeenCalledWith("script-001", 2, 1);
    expect(await card("op:2")).toHaveAccessibleName("Reads: API crm (changed)");
    expect(await card("op:3")).toHaveAttribute("data-change", "added");
    expect(await card("was:op:9")).toHaveAttribute("opacity", "0.6");
    expect(document.querySelector('[data-edge="was:op:9->op:2"]')).toHaveAttribute("stroke-dasharray", "6 4");
    const summary = screen.getByTestId("flow-compare-summary");
    expect(summary).toHaveTextContent("v2 compared with v1");
    expect(summary).toHaveTextContent("1 added");
    expect(summary).toHaveTextContent("1 changed");
    expect(summary).toHaveTextContent("1 removed");
  });

  it("says what a changed card was, and offers no Source where there is none", async () => {
    answer(compared());
    render(<ScriptFlowView scriptId="script-001" version={2} compareWith={1} source={source} sourceSelection={null} />);
    fireEvent.pointerUp(await card("op:2"));
    const side = within(screen.getByTestId("flow-side-panel"));
    expect(side.getByText("API crm-old")).toBeInTheDocument();
    expect(side.queryByRole("button", { name: "Show in Source" })).not.toBeInTheDocument();
    fireEvent.pointerUp(await card("was:op:9"));
    expect(screen.getByTestId("flow-side-panel")).toHaveTextContent("only in v1");
    fireEvent.pointerUp(screen.getByRole("button", { name: "Function load(day)" }));
    expect(within(screen.getByTestId("flow-side-panel")).queryByRole("button", { name: "Show in Source" })).toBeNull();
  });

  it("draws no run, even with the run history already read by the Flow tab", async () => {
    // A disabled query still answers from the cache, which is what the
    // owner's Flow tab above the comparison has filled.
    mockRuns.mockReturnValue({
      data: { data: [{ id: "run-001", version: 2, status: "succeeded" }], total: 1 },
    } as unknown as ReturnType<typeof useScriptRunPage>);
    answer(compared());
    render(
      <ScriptFlowView scriptId="script-001" version={2} compareWith={1} owned source={source} sourceSelection={null} />,
    );
    await card("op:1");
    expect(screen.queryByRole("combobox", { name: "Run drawn on the diagram" })).toBeNull();
    expect(mockRunFlow).toHaveBeenCalledWith("script-001", null);
    expect(screen.getByTestId("flow-compare-summary")).toHaveTextContent("1 added");
  });

  it("says when nothing a script reaches changed", async () => {
    const g = sampleGraph();
    g.compared_with = 1;
    answer(g);
    render(<ScriptFlowView scriptId="script-001" version={2} compareWith={1} source={source} sourceSelection={null} />);
    await card("op:1");
    expect(screen.getByTestId("flow-compare-summary")).toHaveTextContent("Nothing this script reads, writes or produces changed");
  });
});

describe("ScriptFlowView: a run drawn on the diagram (#1907)", () => {
  const runFlow = () => ({
    script_id: "script-001",
    run_id: "run-2",
    version: 2,
    status: "failed",
    cause: "upstream",
    error: "Traceback (most recent call last):\n  script:11:20: in <toplevel>\nError in export: refused",
    graph: sampleGraph(),
    nodes: {
      state: { calls: 0, duration_ms: 0, response_chars: 0, outputs: 0, rows: 0, failed_calls: 0, reached: true, failed: false },
      "op:1": { calls: 3, duration_ms: 1500, response_chars: 10, outputs: 0, rows: 0, failed_calls: 1, last_error: "rate limited", reached: true, failed: false },
      "op:3": { calls: 0, duration_ms: 0, response_chars: 0, outputs: 0, rows: 0, failed_calls: 0, reached: true, failed: true, error: "Error in export: refused" },
    },
    other_calls: [{ tool: "s3_list", duration_ms: 4, success: true }],
    calls: 4,
    failed_node: "op:3",
    structure_failed: "s:5",
    unplaced: false,
    timeline: [],
    run_ms: 2000,
    calls_truncated: false,
  });

  function withRuns() {
    mockRuns.mockImplementation((_id, owned) => ({
      data: owned ? {
        data: [
          { id: "run-2", status: "failed", version: 2, trigger: "schedule", fire_time: "2026-09-01T07:00:00Z", duration_ms: 1, output_count: 0 },
          { id: "run-1", status: "succeeded", version: 1, trigger: "schedule", fire_time: "2026-08-31T07:00:00Z", duration_ms: 1, output_count: 0 },
        ],
        total: 2,
      } : undefined,
    }) as unknown as ReturnType<typeof useScriptRunPage>);
    mockRunFlow.mockImplementation(
      (_id, runId) =>
        (runId ? { data: runFlow(), isLoading: false, error: null } : { isLoading: false }) as unknown as ReturnType<
          typeof useScriptRunFlow
        >,
    );
    answer(sampleGraph());
  }

  it("opens on the latest run: its calls on the cards, the failed card, the unreached dimmed", async () => {
    withRuns();
    render(<ScriptFlowView scriptId="script-001" version={2} owned source={source} sourceSelection={null} />);
    expect(mockRunFlow).toHaveBeenLastCalledWith("script-001", "run-2");
    // A call that failed is counted on the chip, its message on hover (#1933).
    expect(await card("op:1")).toHaveTextContent("3 calls · 1 failed · 1.5 s");
    expect((await card("op:1")).querySelector("title")).toHaveTextContent("rate limited");
    expect(await card("op:3")).toHaveAttribute("data-failed", "true");
    expect(await card("op:2")).toHaveAttribute("opacity", "0.35");
    expect(await card("op:1")).toHaveAttribute("opacity", "1");
    const summary = screen.getByTestId("flow-run-summary");
    expect(summary).toHaveTextContent("Run of v2: failed");
    expect(summary).toHaveTextContent("Cause: upstream");
    expect(summary).toHaveTextContent("s3_list");

    fireEvent.pointerUp(await card("op:1"));
    const side = screen.getByTestId("flow-side-panel");
    expect(side).toHaveTextContent("3 calls, 1.5 s");
    expect(side).toHaveTextContent("1: rate limited");
    fireEvent.pointerUp(await card("op:3"));
    expect(screen.getByTestId("flow-side-panel")).toHaveTextContent("Error in export: refused");
    fireEvent.pointerUp(await card("op:2"));
    expect(screen.getByTestId("flow-side-panel")).toHaveTextContent("never reached");
  });

  it("offers no run picker to a reader, and draws the saved version", async () => {
    withRuns();
    render(<ScriptFlowView scriptId="script-001" version={2} source={source} sourceSelection={null} />);
    expect(mockRuns).toHaveBeenCalledWith("script-001", false, 1, "");
    await card("op:1");
    expect(screen.getByTestId("flow-side-panel")).toHaveTextContent("How to read this");
  });

  it("draws no run when the reader picks none", async () => {
    withRuns();
    render(<ScriptFlowView scriptId="script-001" version={2} owned source={source} sourceSelection={null} />);
    await card("op:1");
    fireEvent.click(screen.getByRole("combobox", { name: "Run drawn on the diagram" }));
    fireEvent.click(await screen.findByRole("option", { name: /No run/ }));
    expect(mockRunFlow).toHaveBeenLastCalledWith("script-001", null);
    expect(await screen.findByText("How to read this", {}, LAYOUT_WAIT)).toBeInTheDocument();
  });

  it("says when a run cannot be drawn, and draws a run of a version that no longer parses as its findings", () => {
    withRuns();
    mockRunFlow.mockReturnValue({ isLoading: false, error: new Error("x") } as unknown as ReturnType<typeof useScriptRunFlow>);
    render(<ScriptFlowView scriptId="script-001" version={2} owned source={source} sourceSelection={null} />);
    expect(screen.getByText("This run could not be drawn.")).toBeInTheDocument();
    cleanup();
    mockRunFlow.mockReturnValue({ isLoading: true } as unknown as ReturnType<typeof useScriptRunFlow>);
    render(<ScriptFlowView scriptId="script-001" version={2} owned source={source} sourceSelection={null} />);
    expect(screen.getByText("Reading the run…")).toBeInTheDocument();
    cleanup();
    const bad = { ...runFlow(), graph: { ...sampleGraph(), ok: false, nodes: [], findings: [{ severity: "error", message: "nope" }] } };
    mockRunFlow.mockReturnValue({ data: bad, isLoading: false } as unknown as ReturnType<typeof useScriptRunFlow>);
    render(<ScriptFlowView scriptId="script-001" version={2} owned source={source} sourceSelection={null} />);
    expect(screen.getByTestId("flow-findings")).toHaveTextContent("nope");
  });
});

describe("ScriptFlowView: a library (#1970)", () => {
  const emptyStructure = {
    nodes: [
      { id: "s:1", kind: "start" as const, line: 0 },
      { id: "s:2", kind: "end" as const, line: 0 },
    ],
    edges: [{ from: "s:1", to: "s:2" }],
    boxes: [],
    functions: [],
    truncated: false,
  };
  const libraryGraph = (library: ScriptFlow["library"]): ScriptFlow => ({
    script_id: "lib-001",
    version: 2,
    ok: true,
    findings: [],
    nodes: [],
    edges: [],
    groups: [],
    params: [],
    lines: 9,
    truncated: false,
    structure: emptyStructure,
    library,
  });

  it("lists each function with its parameters and docstring, and the line that loads them", () => {
    answer(
      libraryGraph({
        functions: [
          { name: "last_week", params: ["today", "days=7"], doc: "The seven days before today.", line: 1 },
          { name: "quarter_of", params: ["month"], line: 5 },
        ],
        load: 'load("lib:date-windows@2", "last_week", "quarter_of")',
      }),
    );
    renderView();
    expect(screen.getByTestId("library-function-last_week")).toHaveTextContent("last_week(today, days=7)");
    expect(screen.getByTestId("library-function-last_week")).toHaveTextContent("The seven days before today.");
    expect(screen.getByTestId("library-function-quarter_of")).toHaveTextContent("quarter_of(month)");
    expect(screen.getByTestId("library-load")).toHaveTextContent(
      'load("lib:date-windows@2", "last_week", "quarter_of")',
    );
    expect(screen.getByRole("button", { name: "Copy the load line" })).toBeInTheDocument();
    expect(screen.queryByText(/makes no platform calls/)).not.toBeInTheDocument();
    expect(screen.queryByTestId("flow-toolbar")).not.toBeInTheDocument();
  });

  it("says a library with nothing to load has nothing to load, and offers no load line", () => {
    answer(libraryGraph({ functions: [] }));
    renderView();
    expect(screen.getByTestId("library-functions")).toHaveTextContent("defines no function a script can load");
    expect(screen.queryByTestId("library-load")).not.toBeInTheDocument();
    expect(screen.queryByText(/makes no platform calls/)).not.toBeInTheDocument();
  });
});
