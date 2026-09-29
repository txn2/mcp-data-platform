import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { ScriptTestsView } from "./ScriptTestsView";

vi.mock("@/api/portal/hooks/scripts", () => ({ usePortalScriptVersions: vi.fn() }));
vi.mock("@/api/portal/hooks/scriptDrafts", () => ({ useValidateScriptSource: vi.fn() }));
import { usePortalScriptVersions } from "@/api/portal/hooks/scripts";
import { useValidateScriptSource } from "@/api/portal/hooks/scriptDrafts";
const mockVersions = vi.mocked(usePortalScriptVersions);
const mockValidate = vi.mocked(useValidateScriptSource);
const mutate = vi.fn();
const onShowLines = vi.fn();

const report = {
  tests: [
    { name: "test_full_window", passed: true, line: 40 },
    { name: "test_empty_changelog", passed: false, line: 52, failure: "want 0 rows, got 3" },
  ],
  passed: 1,
  failed: 1,
  coverage: { statements: 140, covered: 121, percent: 86.4, missed_lines: [88, 91] },
};

function versions(tests?: typeof report) {
  mockVersions.mockReturnValue({
    data: { data: [{ version: 3, tests }, { version: 2 }] },
    isLoading: false,
    error: null,
  } as unknown as ReturnType<typeof usePortalScriptVersions>);
}

function validate(over: Record<string, unknown> = {}) {
  mockValidate.mockReturnValue({ mutate, isPending: false, data: undefined, error: null, ...over } as unknown as ReturnType<
    typeof useValidateScriptSource
  >);
}

beforeEach(() => {
  vi.clearAllMocks();
  validate();
});
afterEach(cleanup);

describe("ScriptTestsView (#1972)", () => {
  it("lists each test with its outcome and the coverage the save kept", () => {
    versions(report);
    render(<ScriptTestsView scriptId="s" version={3} source="src" owned onShowLines={onShowLines} />);
    expect(mockVersions).toHaveBeenCalledWith("s", true);
    expect(screen.getByTestId("script-tests-coverage")).toHaveTextContent("86%");
    const box = screen.getByTestId("script-tests");
    expect(box).toHaveTextContent("of statements reached by the tests (121 of 140)");
    expect(box).toHaveTextContent("2 tests: 1 passed, 1 failed");
    expect(box).toHaveTextContent("As they ran when version 3 was saved.");
    const rows = within(box).getAllByRole("listitem");
    expect(rows[0]).toHaveTextContent("test_full_window");
    expect(within(rows[0]!).getByLabelText("passed")).toBeInTheDocument();
    expect(rows[1]).toHaveTextContent("want 0 rows, got 3");
    expect(within(rows[1]!).getByLabelText("failed")).toBeInTheDocument();
    fireEvent.click(within(rows[1]!).getByRole("button", { name: "line 52" }));
    expect(onShowLines).toHaveBeenLastCalledWith([52]);
    expect(box).toHaveTextContent("2 lines no test reaches");
    fireEvent.click(within(box).getByRole("button", { name: "91" }));
    expect(onShowLines).toHaveBeenLastCalledWith([91]);
    expect(screen.queryByRole("button", { name: "Run the tests" })).toBeNull();
  });

  it("names a line without a link where there is no Source", () => {
    versions({ ...report, coverage: { ...report.coverage, missed_lines: [7] } });
    render(<ScriptTestsView scriptId="s" version={3} source="src" owned={false} />);
    expect(screen.getByText("line 40")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "line 40" })).toBeNull();
    expect(screen.getByTestId("script-tests")).toHaveTextContent("1 line no test reaches");
  });

  it("says a version saved before reports were kept, and runs its tests for the owner", () => {
    versions();
    render(<ScriptTestsView scriptId="s" version={2} source="saved source" owned />);
    expect(screen.getByTestId("script-tests-none")).toHaveTextContent(
      "Version 2 was saved before test results were kept with each version.",
    );
    fireEvent.click(screen.getByRole("button", { name: "Run the tests" }));
    expect(mutate).toHaveBeenCalledWith("saved source");

    cleanup();
    validate({ data: { tests: report } });
    render(<ScriptTestsView scriptId="s" version={2} source="saved source" owned />);
    expect(screen.getByTestId("script-tests")).toHaveTextContent("Run just now against version 2.");

    cleanup();
    validate({ data: { save_refusal: "the source has no tests" } });
    render(<ScriptTestsView scriptId="s" version={2} source="saved source" owned />);
    expect(screen.getByTestId("script-tests-none")).toHaveTextContent("the source has no tests");

    cleanup();
    validate({ isPending: true });
    render(<ScriptTestsView scriptId="s" version={2} source="saved source" owned />);
    expect(screen.getByRole("button", { name: "Running the tests…" })).toBeDisabled();

    cleanup();
    validate({ error: new Error("refused") });
    render(<ScriptTestsView scriptId="s" version={2} source="saved source" owned />);
    expect(screen.getByText("refused")).toBeInTheDocument();
  });

  it("offers a reader no run, and says when a version has no tests", () => {
    versions();
    render(<ScriptTestsView scriptId="s" version={2} source="x" owned={false} />);
    expect(screen.queryByRole("button", { name: "Run the tests" })).toBeNull();
    cleanup();
    versions({ ...report, tests: [], passed: 0, failed: 0 });
    render(<ScriptTestsView scriptId="s" version={3} source="x" owned={false} />);
    expect(screen.getByTestId("script-tests")).toHaveTextContent("This version has no tests.");
  });

  it("says it is reading, and when it cannot", () => {
    mockVersions.mockReturnValue({ isLoading: true } as unknown as ReturnType<typeof usePortalScriptVersions>);
    render(<ScriptTestsView scriptId="s" version={2} source="x" owned />);
    expect(screen.getByText("Reading the tests…")).toBeInTheDocument();
    cleanup();
    mockVersions.mockReturnValue({ isLoading: false, error: new Error("x") } as unknown as ReturnType<
      typeof usePortalScriptVersions
    >);
    render(<ScriptTestsView scriptId="s" version={2} source="x" owned />);
    expect(screen.getByText("The tests of this script could not be read.")).toBeInTheDocument();
  });
});
