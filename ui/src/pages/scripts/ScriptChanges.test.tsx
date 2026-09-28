import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { ScriptChanges } from "./ScriptChanges";

const versions = vi.fn();
vi.mock("@/api/portal/hooks/scripts", () => ({
  usePortalScriptVersions: () => versions(),
}));

afterEach(() => {
  cleanup();
  versions.mockReset();
});

function history(list: object[]) {
  versions.mockReturnValue({ data: { data: list, total: list.length }, isLoading: false, error: null });
}

describe("ScriptChanges (#1943)", () => {
  it("shows each version's agreed summary, who saved it and when it was agreed", () => {
    history([
      {
        id: "v3", version: 3, author: "jane@example.com", created_at: "2026-09-20T10:00:00Z",
        source: "def main():\n    pass\n",
        change_summary: "The weekly file no longer carries the count.",
        change_agreed_by: "jane@example.com", change_agreed_at: "2026-09-20T10:00:00Z",
      },
      { id: "v2", version: 2, author: "jane@example.com", created_at: "2026-09-10T10:00:00Z", source: "x" },
      {
        id: "v1", version: 1, author: "bob@example.com", created_at: "2026-09-01T10:00:00Z", source: "y",
        change_summary: "Adds the region.", change_agreed_by: "carol@example.com",
        change_agreed_at: "2026-09-01T11:00:00Z",
      },
    ]);
    render(<ScriptChanges scriptId="s1" />);
    const card = screen.getByTestId("script-changes");
    expect(card).toHaveTextContent("What changed");
    expect(card).toHaveTextContent("The weekly file no longer carries the count.");
    expect(card).toHaveTextContent("v3, saved by jane@example.com");
    expect(card).toHaveTextContent("agreed on");
    expect(card).toHaveTextContent("Adds the region.");
    expect(card).toHaveTextContent("(confirmed by carol@example.com)");
    expect(screen.getAllByRole("listitem")).toHaveLength(2);
    // What changed is read without the code.
    expect(card).not.toHaveTextContent("def main");
  });

  it("shows nothing when no version changed behavior", () => {
    history([{ id: "v1", version: 1, author: "a", created_at: "", source: "x" }]);
    render(<ScriptChanges scriptId="s1" />);
    expect(screen.queryByTestId("script-changes")).not.toBeInTheDocument();
  });

  it("shows nothing while the history loads", () => {
    versions.mockReturnValue({ data: undefined, isLoading: true, error: null });
    render(<ScriptChanges scriptId="s1" />);
    expect(screen.queryByTestId("script-changes")).not.toBeInTheDocument();
  });

  it("names an unknown author and a summary with no agreement time", () => {
    history([{ id: "v1", version: 1, author: "", created_at: "", source: "x", change_summary: "Something." }]);
    render(<ScriptChanges scriptId="s1" />);
    expect(screen.getByTestId("script-changes")).toHaveTextContent("saved by unknown");
    expect(screen.getByTestId("script-changes")).not.toHaveTextContent("agreed on");
  });
});
