import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup, act } from "@testing-library/react";
import type { ScriptContract } from "@/api/portal/hooks/scripts";
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

vi.mock("@/api/portal/hooks/scripts", () => ({ useSaveScriptSource: vi.fn() }));

import { useSaveScriptSource } from "@/api/portal/hooks/scripts";

const mockSave = vi.mocked(useSaveScriptSource);
const save = vi.fn();

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
});

afterEach(cleanup);

function renderEditor(contract: ScriptContract = approved) {
  render(<ScriptSourceEditor scriptId="script-001" contract={contract} source={source} />);
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
      <ScriptSourceEditor scriptId="script-001" contract={approved} source={source} />,
    );
    fireEvent.change(screen.getByLabelText("Source"), { target: { value: "print(2)\n" } });
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Revert" })).toBeDisabled();
  });
});
