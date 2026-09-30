import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import type { ScriptRunFlow } from "@/api/portal/hooks/scriptFlow";
import { ScriptFlowView } from "./ScriptFlowView";
import { sampleGraph } from "./testGraph";
import { layoutStructure } from "./structureLayout";
import { fold } from "./structureModel";

vi.mock("@/api/portal/hooks/scriptFlow", () => ({ useScriptFlow: vi.fn(), useScriptRunFlow: vi.fn() }));
vi.mock("@/api/portal/hooks/scriptRuns", () => ({ useRecentScriptRuns: vi.fn() }));
vi.mock("@/api/portal/hooks/scripts", () => ({ usePortalScriptVersions: vi.fn() }));
import { useScriptFlow, useScriptRunFlow } from "@/api/portal/hooks/scriptFlow";
import { useRecentScriptRuns } from "@/api/portal/hooks/scriptRuns";
import { usePortalScriptVersions } from "@/api/portal/hooks/scripts";
const mockFlow = vi.mocked(useScriptFlow);
const mockRunFlow = vi.mocked(useScriptRunFlow);
const mockRuns = vi.mocked(useRecentScriptRuns);
const mockVersions = vi.mocked(usePortalScriptVersions);

// The Structure view (#1972): the script in the order it runs, the view the
// Flow tab opens on, with the Calls and Timeline views one switch away.

const source = Array.from({ length: 20 }, (_, i) => `line_${i + 1} = ${i + 1}`).join("\n");
const onShowLines = vi.fn();
const LAYOUT_WAIT = { timeout: 4_000 };

function runFlow(over: Partial<ScriptRunFlow> = {}): ScriptRunFlow {
  return {
    script_id: "script-001",
    run_id: "run-2",
    version: 2,
    status: "failed",
    cause: "script",
    error: "Traceback (most recent call last):\n  script:11:16: in main\nError in export: refused",
    graph: sampleGraph(),
    nodes: {
      "op:1": { calls: 3, duration_ms: 1500, response_chars: 10, outputs: 0, rows: 0, failed_calls: 0, reached: true, failed: false },
      "op:2": { calls: 4, duration_ms: 400, response_chars: 10, outputs: 0, rows: 0, failed_calls: 0, reached: true, failed: false },
      "op:3": { calls: 1, duration_ms: 20, response_chars: 0, outputs: 0, rows: 0, failed_calls: 1, reached: true, failed: true, error: "refused" },
    },
    other_calls: [
      { tool: "api_invoke_endpoint", duration_ms: 80, success: true },
      { tool: "api_invoke_endpoint", duration_ms: 70, success: false, error: "Not Found" },
      { tool: "api_invoke_endpoint", duration_ms: 75, success: false, error: "Not Found" },
      { tool: "s3_list", duration_ms: 4, success: true },
    ],
    calls: 12,
    failed_node: "op:3",
    structure_failed: "s:5",
    unplaced: false,
    timeline: [
      { start_ms: 100, duration_ms: 500, tool: "trino_query", success: true, response_chars: 10, call_site: ["8:9", "5:22"], node: "op:1" },
      { start_ms: 700, duration_ms: 100, tool: "api_invoke_endpoint", success: true, response_chars: 10, call_site: ["9:14", "14:20"], node: "op:2" },
      { start_ms: 900, duration_ms: 20, tool: "manage_resource", success: false, error: "refused", response_chars: 0, call_site: ["11:16"], node: "op:3" },
      { start_ms: 950, duration_ms: 10, tool: "s3_list", success: true, response_chars: 3 },
    ],
    run_ms: 1200,
    calls_truncated: false,
    ...over,
  };
}

// runsListed is what the server answers the picker: the newest 25 of a
// script with 60 runs, or its one failed run.
function runsListed(status: string) {
  const newest = [
    { id: "run-2", status: "failed", version: 2, trigger: "portal", fire_time: "2026-09-28T22:57:38Z", duration_ms: 1, output_count: 0 },
    { id: "run-1", status: "succeeded", version: 2, trigger: "schedule", fire_time: "2026-09-28T12:00:00Z", duration_ms: 1, output_count: 0 },
  ];
  if (status === "failed") return { data: newest.slice(0, 1), total: 1, page: 1 };
  const older = Array.from({ length: 23 }, (_, i) => ({
    ...newest[1],
    id: `run-old-${i}`,
    fire_time: `2026-09-${String(27 - i).padStart(2, "0")}T12:00:00Z`,
  }));
  return { data: [...newest, ...older], total: 60, page: 1 };
}

function withRuns(flow: ScriptRunFlow | null) {
  mockRuns.mockImplementation(
    (_id, owned, status) =>
      ({ data: owned ? runsListed(status) : undefined }) as unknown as ReturnType<typeof useRecentScriptRuns>,
  );
  mockRunFlow.mockImplementation(
    (_id, runId) =>
      (runId && flow ? { data: flow, isLoading: false, error: null } : { isLoading: false }) as unknown as ReturnType<
        typeof useScriptRunFlow
      >,
  );
}

function renderView(owned = false) {
  mockFlow.mockReturnValue({ data: sampleGraph(), isLoading: false, error: null } as unknown as ReturnType<
    typeof useScriptFlow
  >);
  return render(
    <ScriptFlowView
      scriptId="script-001"
      version={2}
      owned={owned}
      source={source}
      sourceSelection={null}
      onShowLines={onShowLines}
    />,
  );
}

async function structure() {
  return screen.findByTestId("structure-canvas", {}, LAYOUT_WAIT);
}

function shape(id: string): SVGGElement {
  const el = document.querySelector(`[data-struct="${id}"]`);
  if (!el) throw new Error(`no node ${id}`);
  return el as SVGGElement;
}

beforeAll(async () => {
  const g = sampleGraph();
  await layoutStructure(fold(g.structure, new Set()), g);
}, 30_000);

beforeEach(() => {
  vi.clearAllMocks();
  window.history.replaceState(null, "", "/");
  // A view chosen in one test is kept in browser storage where the runtime
  // has it, and would open the next test on that view.
  try {
    window.localStorage?.removeItem("portal.flow.view");
  } catch {
    // No storage to clear.
  }
  mockRuns.mockReturnValue({ data: undefined } as ReturnType<typeof useRecentScriptRuns>);
  mockVersions.mockReturnValue({ data: undefined } as ReturnType<typeof usePortalScriptVersions>);
  mockRunFlow.mockReturnValue({ isLoading: false } as ReturnType<typeof useScriptRunFlow>);
});
afterEach(cleanup);

describe("ScriptFlowView: the Structure view", () => {
  it("opens on Structure: one Start, the calls in order, the decision, the loop, the helper and the exits", async () => {
    renderView();
    const canvas = await structure();
    expect(screen.getByRole("radio", { name: "Structure" })).toHaveAttribute("aria-checked", "true");
    expect(within(canvas).getByRole("button", { name: "Start" })).toBeInTheDocument();
    expect(within(canvas).getByRole("button", { name: "End" })).toBeInTheDocument();
    expect(within(canvas).getByRole("button", { name: 'If mode == "full"' })).toBeInTheDocument();
    expect(within(canvas).getByRole("button", { name: "Stops: unknown mode" })).toBeInTheDocument();
    expect(within(canvas).getByRole("button", { name: "Repeats: for d in days" })).toBeInTheDocument();
    expect(within(canvas).getByRole("button", { name: "Function load(day)" })).toHaveTextContent("Load one day.");
    expect(shape("s:3").querySelector('[data-node="op:2"]')).toHaveAccessibleName("Reads: API crm");
    const arms = [...document.querySelectorAll('[data-edge^="s:4->"]')].map((e) => e.textContent);
    expect(arms.sort()).toEqual(["no", "yes"]);
    expect(screen.getByTestId("flow-side-panel")).toHaveTextContent("Top to bottom");
    expect(canvas).toHaveTextContent("arrow = runs next");
  });

  it("lights the cards a selected call takes data from and feeds", async () => {
    renderView();
    await structure();
    fireEvent.pointerUp(shape("s:3").querySelector('[data-node="op:2"]')!);
    expect(shape("s:2").querySelector('[data-node="op:1"]')).toHaveAttribute("aria-pressed", "true");
    expect(shape("s:5").querySelector('[data-node="op:3"]')).toHaveAttribute("aria-pressed", "true");
    expect(shape("s:7").querySelector('[data-node="op:4"]')).toHaveAttribute("aria-pressed", "false");
    expect(screen.getByTestId("flow-side-panel")).toHaveTextContent("API crm");
  });

  it("describes a decision and a box, and opens their lines in Source", async () => {
    renderView();
    await structure();
    fireEvent.pointerUp(shape("s:6"));
    const side = within(screen.getByTestId("flow-struct-detail"));
    expect(side.getByText("unknown mode")).toBeInTheDocument();
    expect(side.getByText("the run fails here with this message")).toBeInTheDocument();
    fireEvent.click(side.getByRole("button", { name: "Show in Source" }));
    expect(onShowLines).toHaveBeenLastCalledWith([13]);

    fireEvent.pointerUp(screen.getByRole("button", { name: "Repeats: for d in days" }));
    expect(screen.getByTestId("flow-box-detail")).toHaveTextContent("for d in days");
    fireEvent.pointerUp(screen.getByRole("button", { name: "Function load(day)" }));
    const fn = within(screen.getByTestId("flow-box-detail"));
    expect(fn.getByText("line 4")).toBeInTheDocument();
    fireEvent.click(fn.getByRole("button", { name: "Show in Source" }));
    expect(onShowLines).toHaveBeenLastCalledWith([4]);
  });

  it("folds a helper into one card and opens it again", async () => {
    renderView();
    await structure();
    fireEvent.pointerUp(screen.getByRole("button", { name: "Fold load(day)" }));
    const open = await screen.findByRole("button", { name: "Open load(day)" }, LAYOUT_WAIT);
    expect(document.querySelector('[data-node="op:1"]')).toBeNull();
    expect(shape("b:1")).toHaveAttribute("data-kind", "fn");
    fireEvent.pointerUp(open);
    expect(await screen.findByRole("button", { name: "Fold load(day)" }, LAYOUT_WAIT)).toBeInTheDocument();
    expect(document.querySelector('[data-node="op:1"]')).not.toBeNull();
  });

  it("marks the nodes the version's tests do not reach", async () => {
    mockVersions.mockReturnValue({
      data: {
        data: [
          {
            version: 2,
            tests: { tests: [], passed: 1, failed: 0, coverage: { statements: 10, covered: 9, percent: 90, missed_lines: [13] } },
          },
        ],
      },
    } as unknown as ReturnType<typeof usePortalScriptVersions>);
    renderView();
    const canvas = await structure();
    expect(shape("s:6").querySelector("[data-untested]")).toHaveTextContent("not reached by tests");
    expect(shape("s:4").querySelector("[data-untested]")).toBeNull();
    expect(canvas).toHaveTextContent("not reached by tests");
  });
});

describe("ScriptFlowView: a run on the Structure view", () => {
  it("opens on the latest run: the calls it made, what it did not reach lighter, where it failed in red", async () => {
    withRuns(runFlow());
    renderView(true);
    await structure();
    expect(shape("s:5").querySelector('[data-node="op:3"]')).toHaveAttribute("data-failed", "true");
    expect(shape("s:7").querySelector('[data-node="op:4"]')).toHaveAttribute("opacity", "0.35");
    expect(shape("s:2").querySelector('[data-node="op:1"]')).toHaveAttribute("opacity", "1");
    expect(shape("s:8")).toHaveAttribute("data-run", "skipped");
    expect(shape("s:6")).toHaveAttribute("data-run", "skipped");
    expect(screen.getByRole("button", { name: "Repeats: for d in days" })).toHaveTextContent("×4");
    expect(await structure()).toHaveTextContent("red = where the run failed");
  });

  it("marks a fail() the run stopped at, which no card could show", async () => {
    withRuns(runFlow({ structure_failed: "s:6", failed_node: undefined, error: "Error in fail: unknown mode" }));
    renderView(true);
    await structure();
    expect(shape("s:6")).toHaveAttribute("data-run", "failed");
    fireEvent.pointerUp(shape("s:6"));
    expect(screen.getByTestId("flow-struct-detail")).toHaveTextContent("Error in fail: unknown mode");
  });

  it("says why a run's calls cannot be placed, dims nothing, and groups them by tool", async () => {
    withRuns(runFlow({ unplaced: true, nodes: {}, structure_failed: undefined, status: "succeeded" }));
    renderView(true);
    await structure();
    expect(screen.getByTestId("flow-unplaced")).toHaveTextContent("before the platform recorded which line made each call");
    expect(shape("s:7").querySelector('[data-node="op:4"]')).toHaveAttribute("opacity", "1");
    expect(shape("s:8")).toHaveAttribute("data-run", "plain");
    const other = within(screen.getByTestId("flow-other-calls"));
    expect(other.getByText(/without the line each came from/)).toBeInTheDocument();
    const rows = other.getAllByRole("listitem").filter((li) => li.querySelector("summary"));
    expect(rows[0]).toHaveTextContent("api_invoke_endpoint: 1 succeeded, 2 failed (Not Found 2), median 75 ms");
    expect(rows[1]).toHaveTextContent("s3_list: 1 succeeded, median 4 ms");
  });
});

describe("ScriptFlowView: the toolbar", () => {
  it("lists the newest runs by when and how they ran, with no paging, and narrows them to one status", async () => {
    withRuns(runFlow());
    renderView(true);
    await structure();
    expect(mockRuns).toHaveBeenLastCalledWith("script-001", true, "");
    fireEvent.click(screen.getByRole("combobox", { name: "Run drawn on the diagram" }));
    const listed = await screen.findAllByRole("option");
    // The saved version, then the newest 25 of the script's 60 runs, newest first.
    expect(listed).toHaveLength(26);
    expect(listed[1]).toHaveAccessibleName(/· manual · failed · v2$/);
    expect(listed[2]).toHaveAccessibleName(/· scheduled · succeeded · v2$/);
    fireEvent.keyDown(document.activeElement ?? document.body, { key: "Escape" });

    expect(screen.queryByTestId("run-page")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Newer runs" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Older runs" })).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("combobox", { name: "Runs listed" }));
    fireEvent.click(await screen.findByRole("option", { name: "Failed" }));
    expect(mockRuns).toHaveBeenLastCalledWith("script-001", true, "failed");
    fireEvent.click(screen.getByRole("combobox", { name: "Run drawn on the diagram" }));
    expect(await screen.findAllByRole("option")).toHaveLength(2);
  });

  it("lists a script's few runs with no paging", async () => {
    withRuns(runFlow());
    const three = runsListed("").data.slice(0, 3);
    mockRuns.mockReturnValue({ data: { data: three, total: 3, page: 1 } } as unknown as ReturnType<
      typeof useRecentScriptRuns
    >);
    renderView(true);
    await structure();
    fireEvent.click(screen.getByRole("combobox", { name: "Run drawn on the diagram" }));
    expect(await screen.findAllByRole("option")).toHaveLength(4);
    fireEvent.keyDown(document.activeElement ?? document.body, { key: "Escape" });
    expect(screen.queryByTestId("run-page")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /runs$/ })).not.toBeInTheDocument();
  });

  it("switches to Calls and keeps the choice in the address", async () => {
    renderView();
    await structure();
    fireEvent.click(screen.getByRole("radio", { name: "Calls" }));
    expect(await screen.findByTestId("flow-canvas", {}, LAYOUT_WAIT)).toBeInTheDocument();
    expect(window.location.search).toBe("?view=calls");
    expect(screen.getByTestId("flow-canvas")).toHaveTextContent("arrow = result feeds the next call");
    cleanup();
    renderView();
    expect(await screen.findByTestId("flow-canvas", {}, LAYOUT_WAIT)).toBeInTheDocument();
  });

  it("offers the Timeline only with a run to place in time", async () => {
    renderView();
    await structure();
    expect(screen.getByRole("radio", { name: "Timeline" })).toBeDisabled();
    cleanup();
    window.history.replaceState(null, "", "/?view=timeline");
    renderView();
    expect(await structure()).toBeInTheDocument();
  });

  it("presents the diagram and its panel full screen, and Escape leaves it", async () => {
    renderView();
    await structure();
    fireEvent.click(screen.getByRole("button", { name: "Full screen" }));
    const full = screen.getByTestId("flow-full-screen");
    expect(within(full).getByTestId("structure-canvas")).toBeInTheDocument();
    expect(within(full).getByTestId("flow-side-panel")).toBeInTheDocument();
    fireEvent.pointerUp(shape("s:6"));
    fireEvent.keyDown(window, { key: "Escape" });
    expect(screen.queryByTestId("flow-full-screen")).toBeNull();
    expect(screen.getByTestId("flow-struct-detail")).toHaveTextContent("unknown mode");
    fireEvent.click(screen.getByRole("button", { name: "Full screen" }));
    fireEvent.click(screen.getByRole("button", { name: "Leave full screen" }));
    expect(screen.queryByTestId("flow-full-screen")).toBeNull();
  });
});

describe("ScriptFlowView: the Timeline view", () => {
  it("places every call when it ran, under the helper it was made through", async () => {
    withRuns(runFlow());
    renderView(true);
    await structure();
    fireEvent.click(screen.getByRole("radio", { name: "Timeline" }));
    const tl = await screen.findByTestId("timeline");
    expect(tl).toHaveTextContent("4 calls over 1.2 s");
    expect(tl).toHaveTextContent("calls in flight 630 ms");
    const bars = [...tl.querySelectorAll("[data-bar]")];
    expect(bars.map((b) => b.getAttribute("aria-label"))).toEqual([
      "main()",
      "load()",
      "Query trino, 500 ms",
      "main()",
      "API crm, 100 ms",
      "Export CSV to {dest}, 20 ms",
      "s3_list, 10 ms",
    ]);
    expect(tl.querySelector('[data-bar="c2"]')).toHaveAttribute("data-failed", "true");
    fireEvent.click(tl.querySelector('[data-bar="c1"]')!);
    expect(screen.getByTestId("flow-side-panel")).toHaveTextContent("GET /v1/accounts");
    fireEvent.click(screen.getByRole("button", { name: "Zoom in" }));
    fireEvent.click(screen.getByRole("button", { name: "Fit the whole run" }));
  });

  it("says a run that made no calls has nothing to place", async () => {
    withRuns(runFlow({ timeline: [] }));
    window.history.replaceState(null, "", "/?view=timeline");
    renderView(true);
    expect(await screen.findByTestId("timeline-empty")).toHaveTextContent("made no calls");
  });
});
