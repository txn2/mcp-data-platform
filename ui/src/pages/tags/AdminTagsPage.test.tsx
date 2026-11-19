import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup, within } from "@testing-library/react";

const rename = vi.fn();
const merge = vi.fn();
let rows = [
  { tag: "finance", count: 121, spellings: [{ tag: "finance", count: 112 }, { tag: "Finance", count: 9 }] },
  { tag: "fin", count: 14, spellings: [{ tag: "fin", count: 14 }] },
  { tag: "finance-reports", count: 6, spellings: [{ tag: "finance-reports", count: 6 }] },
];
vi.mock("@/api/admin/hooks/tags", () => ({
  useAdminTags: () => ({ data: { tags: rows, total: rows.length }, isLoading: false, isError: false }),
  useRenameTag: () => ({ mutate: rename, isPending: false, error: null }),
  useMergeTags: () => ({ mutate: merge, isPending: false, error: null }),
}));

import { AdminTagsPage, existingTarget, impact, spellingsLine } from "./AdminTagsPage";

afterEach(cleanup);
beforeEach(() => {
  rename.mockReset();
  merge.mockReset();
});

function row(tag: string): HTMLElement {
  return screen.getByText(tag, { selector: "td" }).closest("tr")!;
}

describe("AdminTagsPage (#2508)", () => {
  it("lists every tag with its spellings and count", () => {
    render(<AdminTagsPage />);
    const finance = row("finance");
    expect(within(finance).getByText("finance (112), Finance (9)")).toBeInTheDocument();
    expect(within(finance).getByText("121")).toBeInTheDocument();
  });

  it("merges a tag into another, every spelling of it", () => {
    render(<AdminTagsPage />);
    fireEvent.click(within(row("fin")).getByRole("button", { name: "Merge into…" }));
    fireEvent.change(screen.getByLabelText("Into"), { target: { value: "Finance" } });
    expect(screen.getByText(/Merge fin into finance: 14 assets move/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Merge" }));
    expect(merge).toHaveBeenCalledWith({ from: ["fin"], into: "finance" }, expect.anything());
  });

  it("renames, and becomes a merge when the new name is a tag", () => {
    render(<AdminTagsPage />);
    fireEvent.click(within(row("finance-reports")).getByRole("button", { name: "Rename" }));
    const input = screen.getByLabelText("New name");
    fireEvent.change(input, { target: { value: "finance reports" } });
    fireEvent.click(screen.getByRole("button", { name: "Rename" }));
    expect(rename).toHaveBeenCalledWith({ from: "finance-reports", to: "finance reports" }, expect.anything());
    fireEvent.change(input, { target: { value: "FINANCE" } });
    fireEvent.click(screen.getByRole("button", { name: "Merge" }));
    expect(merge).toHaveBeenCalledWith({ from: ["finance-reports"], into: "finance" }, expect.anything());
  });

  it("says so when no asset carries a tag", () => {
    const saved = rows;
    rows = [];
    render(<AdminTagsPage />);
    expect(screen.getByText("No asset carries a tag yet.")).toBeInTheDocument();
    rows = saved;
  });
});

describe("tag helpers (#2508)", () => {
  it("phrases spellings, targets and what a change will do", () => {
    const [finance, fin] = rows as [(typeof rows)[0], (typeof rows)[0]];
    expect(spellingsLine(fin)).toBe("fin (14)");
    expect(existingTarget(rows, "FINANCE", fin)?.tag).toBe("finance");
    expect(existingTarget(rows, "fin", fin)).toBeUndefined();
    expect(existingTarget(rows, "", fin)).toBeUndefined();
    expect(impact("merge", fin, "finance", finance)).toContain("at most 135 assets");
    expect(impact("merge", fin, "", undefined)).toBe("Name the tag to merge into.");
    expect(impact("merge", fin, "money", undefined)).toContain("not a tag yet");
    expect(impact("rename", fin, "x", undefined)).toContain("14 assets change");
  });
});
