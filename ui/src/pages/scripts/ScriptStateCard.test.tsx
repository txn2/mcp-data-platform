import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, render, screen, fireEvent, cleanup } from "@testing-library/react";
import type { ScriptContract, ScriptState } from "@/api/portal/hooks/scripts";
import { ScriptStateCard, parseProblem } from "./ScriptStateCard";

// The state a script carries between runs (#1537): what the next run reads,
// who wrote it, and the two resets. The card folds by default, so every
// assertion opens it first.

vi.mock("@/api/portal/hooks/scripts", () => ({
  useScriptState: vi.fn(),
  useSetScriptState: vi.fn(),
  useClearScriptState: vi.fn(),
}));

import {
  useClearScriptState,
  useScriptState,
  useSetScriptState,
} from "@/api/portal/hooks/scripts";

const mockState = vi.mocked(useScriptState);
const mockSet = vi.mocked(useSetScriptState);
const mockClear = vi.mocked(useClearScriptState);

const setState = vi.fn();
const clearState = vi.fn();

function query<T>(data: T, extra: Record<string, unknown> = {}) {
  return { data, isLoading: false, error: null, ...extra } as never;
}

const contract: ScriptContract = {
  id: "script-001",
  name: "incremental-sync",
  status: "active",
  enabled: true,
  params: [],
  version: 2,
  state: { reads_state: true, saves_state: true, revision: 3 },
};

const saved: ScriptState = {
  state: { synced_through: "2026-08-13", rows: 1420 },
  revision: 3,
  updated_at: "2026-08-14T07:00:08Z",
  run_id: "run-001",
};

beforeEach(() => {
  vi.clearAllMocks();
  mockState.mockReturnValue(query(saved));
  mockSet.mockReturnValue({ mutate: setState, isPending: false } as never);
  mockClear.mockReturnValue({ mutate: clearState, isPending: false } as never);
});

afterEach(cleanup);

function renderCard(c: ScriptContract = contract) {
  render(<ScriptStateCard scriptId="script-001" contract={c} />);
  fireEvent.click(screen.getByRole("heading", { name: "State" }));
}

describe("ScriptStateCard", () => {
  it("shows the object, its revision, and the run that wrote it", () => {
    renderCard();
    expect(screen.getByTestId("script-state")).toHaveTextContent('"synced_through": "2026-08-13"');
    expect(screen.getByText("3")).toBeInTheDocument();
    expect(screen.getByText("run run-001")).toBeInTheDocument();
    expect(screen.getByText(/continues from the previous run's save/)).toBeInTheDocument();
  });

  it("says nothing has been saved at revision 0", () => {
    mockState.mockReturnValue(query({ state: {}, revision: 0 }));
    renderCard({ ...contract, state: { reads_state: true, saves_state: true, revision: 0 } });
    expect(screen.getByText("never")).toBeInTheDocument();
    expect(screen.getByText("nobody")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Clear state" })).toBeDisabled();
  });

  it("names a person who reset it", () => {
    mockState.mockReturnValue(query({ ...saved, run_id: undefined, updated_by: "sarah.chen@example.com" }));
    renderCard();
    expect(screen.getByText("sarah.chen@example.com")).toBeInTheDocument();
  });

  it("says a script keeps none when its source neither reads nor saves", () => {
    mockState.mockReturnValue(query({ state: {}, revision: 0 }));
    renderCard({ ...contract, state: { reads_state: false, saves_state: false, revision: 0 } });
    expect(screen.getByText(/neither reads nor saves state/)).toBeInTheDocument();
  });

  it("replaces the whole object from the editor, refusing text that is not an object", () => {
    renderCard();
    fireEvent.click(screen.getByRole("button", { name: "Edit state" }));
    const editor = screen.getByLabelText(/The whole object/);
    fireEvent.change(editor, { target: { value: "[1, 2]" } });
    expect(screen.getByText(/State is a JSON object/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Replace state" })).toBeDisabled();

    fireEvent.change(editor, { target: { value: '{"synced_through": "2026-08-01"}' } });
    fireEvent.click(screen.getByRole("button", { name: "Replace state" }));
    expect(setState).toHaveBeenCalledWith({ synced_through: "2026-08-01" }, expect.anything());
    act(() => setState.mock.calls[0]![1].onSuccess({ ...saved, revision: 4, message: "State replaced." }));
    expect(screen.getByText("State replaced.")).toBeInTheDocument();
  });

  it("clears only after confirming, and reports the reset", () => {
    renderCard();
    fireEvent.click(screen.getByRole("button", { name: "Clear state" }));
    expect(clearState).not.toHaveBeenCalled();
    expect(screen.getByText(/starts from an empty object/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Clear" }));
    expect(clearState).toHaveBeenCalled();
    act(() => clearState.mock.calls[0]![1].onSuccess({ state: {}, revision: 4, message: "State cleared." }));
    expect(screen.getByText("State cleared.")).toBeInTheDocument();
  });

  it("reports a reset the server refused", () => {
    renderCard();
    fireEvent.click(screen.getByRole("button", { name: "Clear state" }));
    fireEvent.click(screen.getByRole("button", { name: "Clear" }));
    act(() => clearState.mock.calls[0]![1].onError(new Error("the state is over the limit")));
    expect(screen.getByText("the state is over the limit")).toBeInTheDocument();
  });

  it("says when this deployment keeps no state", () => {
    mockState.mockReturnValue(query(null));
    renderCard();
    expect(screen.getByText(/keeps no script state/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Edit state" })).not.toBeInTheDocument();
  });

  it("reports a state that could not be loaded", () => {
    mockState.mockReturnValue(query(undefined, { error: new Error("boom") }));
    renderCard();
    expect(screen.getByText(/could not be loaded/)).toBeInTheDocument();
  });
});

describe("parseProblem", () => {
  it("accepts an object and refuses everything else", () => {
    expect(parseProblem("{}")).toBeNull();
    expect(parseProblem('{"a": [1, {"b": null}]}')).toBeNull();
    expect(parseProblem("{")).toMatch(/not valid JSON/);
    expect(parseProblem("null")).toMatch(/JSON object/);
    expect(parseProblem('"x"')).toMatch(/JSON object/);
    expect(parseProblem(`{"blob": "${"x".repeat(64 * 1024)}"}`)).toMatch(/bounded at 64 KiB/);
  });
});
