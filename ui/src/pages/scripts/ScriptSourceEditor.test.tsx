import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup, act } from "@testing-library/react";
import type { ScriptContract, ScriptParam } from "@/api/portal/hooks/scripts";
import { ScriptSourceEditor } from "./ScriptSourceEditor";

// The editor's own behaviour is what matters here: where a save lands, and
// whether the page says so. CodeMirror is stubbed to a textarea — it is the
// portal's shared editor with its own tests, and driving a contenteditable
// would test that library rather than this page.
vi.mock("@/components/SourceEditor", () => ({
  SourceEditor: ({
    content,
    contentType,
    onChange,
  }: {
    content: string;
    contentType: string;
    onChange: (v: string) => void;
  }) => (
    <textarea
      aria-label="Source"
      data-content-type={contentType}
      value={content}
      onChange={(e) => onChange(e.target.value)}
    />
  ),
}));

vi.mock("@/api/portal/hooks/scripts", () => ({
  SCRIPT_RUN_AUDIENCE: { run: "run", draft: "draft" },
  useSaveScriptSource: vi.fn(),
  useValidateScriptSource: vi.fn(),
  useDryRunScript: vi.fn(),
  useScriptConnections: vi.fn(),
}));

import {
  useDryRunScript,
  useSaveScriptSource,
  useScriptConnections,
  useValidateScriptSource,
} from "@/api/portal/hooks/scripts";

const mockSave = vi.mocked(useSaveScriptSource);
const mockValidate = vi.mocked(useValidateScriptSource);
const mockDryRun = vi.mocked(useDryRunScript);
const mockConnections = vi.mocked(useScriptConnections);
const save = vi.fn();
const validate = vi.fn();
const dryRun = vi.fn();

const source = 'rows = platform.query(connection="acme", sql="SELECT 1")["rows"]\n';

const approved: ScriptContract = {
  id: "script-001",
  name: "daily-sales-report",
  display_name: "Daily Sales Report",
  scope: "global",
  status: "active",
  enabled: true,
  params: [],
  approval: { approved: true, version: 2, approved_by: "admin@acme.example.com" },
};

const unapproved: ScriptContract = {
  ...approved,
  approval: { approved: false, refusal: "the script has no approved version" },
};

beforeEach(() => {
  vi.clearAllMocks();
  mockSave.mockReturnValue({ mutate: save, isPending: false } as never);
  mockValidate.mockReturnValue({ mutate: validate, isPending: false } as never);
  mockDryRun.mockReturnValue({ mutate: dryRun, isPending: false } as never);
  mockConnections.mockReturnValue({ data: undefined, isLoading: false, error: null } as never);
});

afterEach(cleanup);

function renderEditor(contract: ScriptContract = approved, draftParams: ScriptParam[] = []) {
  render(
    <ScriptSourceEditor
      scriptId="script-001"
      contract={contract}
      source={source}
      draftParams={draftParams}
    />,
  );
}

describe("ScriptSourceEditor: what it opens", () => {
  it("shows the live source as Python, which is what Starlark reads as", () => {
    renderEditor();
    const box = screen.getByLabelText("Source");
    expect(box).toHaveValue(source);
    expect(box).toHaveAttribute("data-content-type", "text/x-python");
  });

  // The two outcomes are genuinely different, and which one applies is a
  // property of the script rather than of the edit, so it is stated up front.
  it("says an edit to an approved script goes to review", () => {
    renderEditor();
    expect(screen.getByText(/Version 2 is approved and keeps running/)).toBeInTheDocument();
  });

  it("says an edit to an unapproved script applies directly", () => {
    renderEditor(unapproved);
    expect(screen.getByText(/saving changes it directly/)).toBeInTheDocument();
  });
});

describe("ScriptSourceEditor: saving", () => {
  it("offers nothing to save until the code changes", () => {
    renderEditor();
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Revert" })).toBeDisabled();

    fireEvent.change(screen.getByLabelText("Source"), { target: { value: source + "print(1)\n" } });
    expect(screen.getByRole("button", { name: "Save" })).toBeEnabled();
  });

  it("submits the edited source", () => {
    renderEditor();
    fireEvent.change(screen.getByLabelText("Source"), { target: { value: "print(2)\n" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(save).toHaveBeenCalledWith("print(2)\n", expect.anything());
  });

  it("throws the edit away on revert", () => {
    renderEditor();
    fireEvent.change(screen.getByLabelText("Source"), { target: { value: "print(2)\n" } });
    fireEvent.click(screen.getByRole("button", { name: "Revert" }));
    expect(screen.getByLabelText("Source")).toHaveValue(source);
    expect(save).not.toHaveBeenCalled();
  });

  // "Saved" alone would leave an owner believing a change is running when a
  // reviewer has not looked at it yet.
  it("reports the server's own account of where the edit landed", () => {
    renderEditor();
    fireEvent.change(screen.getByLabelText("Source"), { target: { value: "print(2)\n" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    const onSuccess = save.mock.calls[0]![1].onSuccess as (r: unknown) => void;
    act(() =>
      onSuccess({
        applied: false,
        pending_version: 4,
        message: "saved as a draft awaiting review",
      }),
    );
    expect(screen.getByText(/saved as a draft awaiting review/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
    // The live source is still the approved version's, so resetting the box to
    // it would wipe the change that was just queued.
    expect(screen.getByLabelText("Source")).toHaveValue("print(2)\n");
    expect(screen.getByRole("button", { name: "Revert" })).toBeEnabled();
  });

  it("re-enables saving when the queued edit is edited again", () => {
    renderEditor();
    fireEvent.change(screen.getByLabelText("Source"), { target: { value: "print(2)\n" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    const onSuccess = save.mock.calls[0]![1].onSuccess as (r: unknown) => void;
    act(() => onSuccess({ applied: false, pending_version: 4, message: "queued" }));

    fireEvent.change(screen.getByLabelText("Source"), { target: { value: "print(3)\n" } });
    expect(screen.getByRole("button", { name: "Save" })).toBeEnabled();
  });

  // An applied edit IS the live source, so the editor follows the record.
  it("drops the draft when the edit applied directly", () => {
    renderEditor(unapproved);
    fireEvent.change(screen.getByLabelText("Source"), { target: { value: "print(2)\n" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    const onSuccess = save.mock.calls[0]![1].onSuccess as (r: unknown) => void;
    act(() => onSuccess({ applied: true, message: "Saved." }));
    expect(screen.getByLabelText("Source")).toHaveValue(source);
  });

  it("reports a refusal in place, in the server's words", () => {
    renderEditor();
    fireEvent.change(screen.getByLabelText("Source"), { target: { value: "def broken(:\n" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    const onError = save.mock.calls[0]![1].onError as (e: unknown) => void;
    act(() => onError(new Error("the source does not parse, so it was not saved")));
    expect(screen.getByText(/does not parse/)).toBeInTheDocument();
    // The edit is kept, so nothing typed is lost to a refusal.
    expect(screen.getByLabelText("Source")).toHaveValue("def broken(:\n");
  });

  it("disables both controls while a save is in flight", () => {
    mockSave.mockReturnValue({ mutate: save, isPending: true } as never);
    render(
      <ScriptSourceEditor
        scriptId="script-001"
        contract={approved}
        source={source}
        draftParams={[]}
      />,
    );
    fireEvent.change(screen.getByLabelText("Source"), { target: { value: "print(2)\n" } });
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Revert" })).toBeDisabled();
  });
});

// Checking an edit before asking anyone to approve it (#1364). Both actions are
// on the editor because that is where the author is; neither approves anything.
describe("ScriptSourceEditor: checking an edit", () => {
  it("validates the text on screen and reports what it would reach", () => {
    renderEditor();
    fireEvent.change(screen.getByLabelText("Source"), { target: { value: "x = 2\n" } });
    fireEvent.click(screen.getByRole("button", { name: "Validate" }));

    expect(validate).toHaveBeenCalledWith("x = 2\n", expect.anything());

    act(() =>
      validate.mock.calls[0]![1].onSuccess({
        ok: true,
        findings: [],
        capabilities: ["platform.query"],
        connections: ["warehouse"],
        destinations: [],
        dynamic_connections: false,
        dynamic_destinations: false,
      }),
    );
    expect(screen.getByText("Parses")).toBeInTheDocument();
    expect(screen.getByText("platform.query")).toBeInTheDocument();
    expect(screen.getByText("warehouse")).toBeInTheDocument();
  });

  it("shows each finding with the correction, which is most of its value", () => {
    renderEditor();
    fireEvent.click(screen.getByRole("button", { name: "Validate" }));

    act(() =>
      validate.mock.calls[0]![1].onSuccess({
        ok: false,
        findings: [{ severity: "error", line: 3, message: "while is not available", hint: "Loop over a list." }],
        capabilities: [],
        connections: [],
        destinations: [],
        dynamic_connections: false,
        dynamic_destinations: false,
      }),
    );
    expect(screen.getByText("Does not parse")).toBeInTheDocument();
    expect(screen.getByText(/while is not available/)).toBeInTheDocument();
    expect(screen.getByText("Loop over a list.")).toBeInTheDocument();
  });

  it("dry-runs the text on screen and reports what it would have written", () => {
    renderEditor();
    fireEvent.change(screen.getByLabelText("Source"), { target: { value: "x = 3\n" } });
    fireEvent.click(screen.getByRole("button", { name: "Dry run" }));

    expect(dryRun).toHaveBeenCalledWith(
      { source: "x = 3\n", params: {} },
      expect.anything(),
    );

    act(() =>
      dryRun.mock.calls[0]![1].onSuccess({
        run_id: "run_1",
        status: "succeeded",
        log: "computed 12 rows",
        metrics: { steps: 40, duration_ms: 250, queries: 1, exports: 1 },
        outputs: [{ name: "daily", destination: "portal", format: "csv", row_count: 12, bytes: 300 }],
        message: "Nothing was persisted.",
      }),
    );
    expect(screen.getByText("succeeded")).toBeInTheDocument();
    expect(screen.getByText("Nothing was persisted.")).toBeInTheDocument();
    expect(screen.getByText(/would write 12 rows as csv/)).toBeInTheDocument();
    expect(screen.getByText("computed 12 rows")).toBeInTheDocument();
  });

  it("reports a failed dry run with its log, which is the reason to have run it", () => {
    renderEditor();
    fireEvent.click(screen.getByRole("button", { name: "Dry run" }));

    act(() =>
      dryRun.mock.calls[0]![1].onSuccess({
        run_id: "run_2",
        status: "failed",
        error: "no such column: regoin",
        log: "half way",
        metrics: { steps: 12, duration_ms: 80, queries: 1, exports: 0 },
        outputs: [],
        message: "A script failure is deterministic.",
      }),
    );
    expect(screen.getByText("failed")).toBeInTheDocument();
    expect(screen.getByText(/no such column: regoin/)).toBeInTheDocument();
    expect(screen.getByText("half way")).toBeInTheDocument();
  });

  it("asks for the connections a DRAFT reaches, which are the caller's own", () => {
    renderEditor();

    expect(mockConnections).toHaveBeenCalledWith("script-001", false, "draft");
  });

  it("replaces the previous answer rather than stacking two reports on one editor", () => {
    renderEditor();
    fireEvent.click(screen.getByRole("button", { name: "Validate" }));
    act(() =>
      validate.mock.calls[0]![1].onSuccess({
        ok: true,
        findings: [],
        capabilities: [],
        connections: [],
        destinations: [],
        dynamic_connections: false,
        dynamic_destinations: false,
      }),
    );
    expect(screen.getByText("Parses")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Dry run" }));
    expect(screen.queryByText("Parses")).not.toBeInTheDocument();
  });

  it("reports a refused check in place, in the server's words", () => {
    renderEditor();
    fireEvent.click(screen.getByRole("button", { name: "Validate" }));

    act(() => validate.mock.calls[0]![1].onError(new Error("script not found")));
    expect(screen.getByText("script not found")).toBeInTheDocument();
  });
});

// A dry run binds against the LIVE record's contract, not the approved
// version's: a draft is precisely the code that does not match the approved
// version yet, so a form built from the contract above would offer parameters
// the dry run then refuses.
describe("ScriptSourceEditor: which contract a dry run binds", () => {
  it("builds the form from the live record's parameters", () => {
    renderEditor(
      {
        ...approved,
        params: [{ name: "report_date", type: "date", required: true }],
      },
      [{ name: "region", type: "string", required: false }],
    );

    expect(screen.getByLabelText("region (optional)")).toBeInTheDocument();
    expect(screen.queryByLabelText("report_date")).not.toBeInTheDocument();
  });

  it("sends those values with the source it ran", () => {
    renderEditor(approved, [{ name: "region", type: "string", required: true }]);
    fireEvent.change(screen.getByLabelText("region"), { target: { value: "west" } });
    fireEvent.click(screen.getByRole("button", { name: "Dry run" }));

    expect(dryRun).toHaveBeenCalledWith(
      { source, params: { region: "west" } },
      expect.anything(),
    );
  });

  it("will not dry-run until a required value is supplied", () => {
    renderEditor(approved, [{ name: "region", type: "string", required: true }]);

    expect(screen.getByRole("button", { name: "Dry run" })).toBeDisabled();
    expect(screen.getByText(/region is required before a dry run/)).toBeInTheDocument();
  });
});
