import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup } from "@testing-library/react";
import type { ScriptContract } from "@/api/portal/hooks/scripts";
import { MARKDOWN_DESCRIPTION, expectRenderedMarkdown } from "@/test/markdownDescription";
import { ScriptDocumentation } from "./ScriptDocumentation";

// The section is the ticket's whole point (#1369): the description is a
// document, it is rendered as one, and the person who owns the script writes it
// here rather than by asking an agent.
//
// CodeMirror is stubbed to a textarea, as it is in the source editor's tests:
// it is the portal's shared markdown editor with its own tests, and driving a
// contenteditable would test that library rather than this section.
vi.mock("@/components/MarkdownEditor", () => ({
  MarkdownEditor: ({
    value,
    label,
    onChange,
  }: {
    value: string;
    label?: string;
    onChange: (v: string) => void;
  }) => (
    <textarea
      aria-label={label ?? "Description"}
      value={value}
      onChange={(e) => onChange(e.target.value)}
    />
  ),
}));

vi.mock("@/api/portal/hooks/scripts", () => ({
  useSaveScriptMetadata: vi.fn(),
}));

import { useSaveScriptMetadata } from "@/api/portal/hooks/scripts";

const mockSave = vi.mocked(useSaveScriptMetadata);
const save = vi.fn();

const contract: ScriptContract = {
  id: "script-001",
  name: "daily-sales-report",
  display_name: "Daily Sales Report",
  description: MARKDOWN_DESCRIPTION,
  owner_email: "sarah.chen@example.com",
  category: "reporting",
  tags: ["sales", "weekly"],
  status: "active",
  enabled: true,
  params: [],
  version: 2,
};

beforeEach(() => {
  vi.clearAllMocks();
  mockSave.mockReturnValue({ mutate: save, isPending: false } as never);
});

afterEach(cleanup);

function renderSection(overrides: Partial<ScriptContract> = {}, owned = true) {
  render(
    <ScriptDocumentation
      scriptId="script-001"
      contract={{ ...contract, ...overrides }}
      owned={owned}
    />,
  );
}

describe("ScriptDocumentation: what a reader gets", () => {
  // The defect this replaces: the description was the page header's subtitle,
  // a one-line slot, so a page of markdown arrived as one run-on line.
  it("renders the description as markdown rather than as literal text", () => {
    renderSection();
    expectRenderedMarkdown();
  });

  it("shows the category and the tags the script is filed under", () => {
    renderSection();
    expect(screen.getByText("reporting")).toBeInTheDocument();
    expect(screen.getByText("sales")).toBeInTheDocument();
    expect(screen.getByText("weekly")).toBeInTheDocument();
  });

  it("shows no facet row for a script nobody has filed", () => {
    renderSection({ category: undefined, tags: [] });
    expect(screen.queryByText("reporting")).not.toBeInTheDocument();
  });

  // An empty description is the state this feature exists to move people out
  // of, so the owner is told what to write and a reader is simply told there is
  // nothing.
  it("tells the owner what to write when there is no description", () => {
    renderSection({ description: "" });
    expect(screen.getByText(/what it produces, what its parameters mean/)).toBeInTheDocument();
  });

  it("tells a reader who does not own it only that there is none", () => {
    renderSection({ description: "" }, false);
    expect(screen.getByText("This script has no description.")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Edit" })).not.toBeInTheDocument();
  });
});

describe("ScriptDocumentation: what the owner writes", () => {
  it("opens the four fields filled in from the script", () => {
    renderSection();
    fireEvent.click(screen.getByRole("button", { name: "Edit" }));

    expect(screen.getByLabelText("Display name")).toHaveValue("Daily Sales Report");
    expect(screen.getByLabelText("Category")).toHaveValue("reporting");
    expect(screen.getByLabelText("Tags")).toHaveValue("sales, weekly");
    expect(screen.getByLabelText("Script description")).toHaveValue(MARKDOWN_DESCRIPTION);
  });

  it("sends all four fields, with the tags split into a list", () => {
    renderSection();
    fireEvent.click(screen.getByRole("button", { name: "Edit" }));
    fireEvent.change(screen.getByLabelText("Display name"), { target: { value: "Daily Sales" } });
    fireEvent.change(screen.getByLabelText("Category"), { target: { value: "finance" } });
    // A trailing comma is what somebody mid-edit leaves behind; it must not
    // become an empty tag.
    fireEvent.change(screen.getByLabelText("Tags"), { target: { value: "sales, margins, " } });
    fireEvent.change(screen.getByLabelText("Script description"), {
      target: { value: "## What it produces\n\nA CSV." },
    });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    expect(save).toHaveBeenCalledWith(
      {
        display_name: "Daily Sales",
        category: "finance",
        tags: ["sales", "margins"],
        description: "## What it produces\n\nA CSV.",
      },
      expect.anything(),
    );
  });

  it("closes the form on a save with nothing more to say", () => {
    save.mockImplementation((_body, opts) => opts.onSuccess({ version: 3, message: "Saved." }));
    renderSection();
    fireEvent.click(screen.getByRole("button", { name: "Edit" }));
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    expect(screen.queryByLabelText("Script description")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Edit" })).toBeInTheDocument();
  });

  // The advisory is not a refusal: the save succeeded, and the form stays open
  // only because the suggestion is about the text still on screen.
  it("keeps the form open to show the long-description advisory", () => {
    save.mockImplementation((_body, opts) =>
      opts.onSuccess({
        version: 3,
        message: "Saved.",
        description_notice: "consider moving the background to a knowledge page",
      }),
    );
    renderSection();
    fireEvent.click(screen.getByRole("button", { name: "Edit" }));
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    expect(screen.getByText(/consider moving the background to a knowledge page/)).toBeInTheDocument();
    expect(screen.getByLabelText("Script description")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Done" })).toBeInTheDocument();
  });

  it("reports a refusal without closing the form", () => {
    save.mockImplementation((_body, opts) =>
      opts.onError(new Error("category must be at most 31 characters")),
    );
    renderSection();
    fireEvent.click(screen.getByRole("button", { name: "Edit" }));
    fireEvent.change(screen.getByLabelText("Category"), { target: { value: "Sales Reports" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    expect(screen.getByText("category must be at most 31 characters")).toBeInTheDocument();
    expect(screen.getByLabelText("Category")).toHaveValue("Sales Reports");
  });

  // The section folds (#1407): a description is a document, and a long one is
  // exactly the case for folding — so the header states what the script is
  // when it is folded, and opens on the document when it is not.
  it("states the first line of the document in the folded header", () => {
    renderSection();
    fireEvent.click(screen.getByRole("button", { name: /^About/ }));

    // The first line of PROSE, not the heading above it: the folded header
    // answers "what is this script", which a section title does not.
    expect(
      screen.getByRole("button", { name: /Recognized revenue only/ }),
    ).toBeInTheDocument();
  });

  it("says an undocumented script has no description, folded", () => {
    renderSection({ description: "" });
    fireEvent.click(screen.getByRole("button", { name: /^About/ }));

    expect(screen.getByRole("button", { name: /No description/ })).toBeInTheDocument();
  });
});
