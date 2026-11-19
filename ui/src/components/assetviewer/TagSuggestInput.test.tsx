import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup } from "@testing-library/react";
import { useState } from "react";

vi.mock("@/api/portal/hooks/bulk", () => ({
  usePortalTags: vi.fn(),
}));

import { usePortalTags } from "@/api/portal/hooks/bulk";
import { TagSuggestInput, completeWith, currentFragment, splitTags, suggestionsFor } from "./TagSuggestInput";

const rows = [
  { tag: "finance", count: 14, spellings: [{ tag: "finance", count: 12 }, { tag: "Finance", count: 2 }] },
  { tag: "finance-reports", count: 3, spellings: [{ tag: "finance-reports", count: 3 }] },
  { tag: "q3", count: 2, spellings: [{ tag: "q3", count: 2 }] },
];

beforeEach(() => {
  vi.mocked(usePortalTags).mockReturnValue({ data: { tags: rows, total: 3 } } as unknown as ReturnType<
    typeof usePortalTags
  >);
});

afterEach(cleanup);

function Harness({ initial = "" }: { initial?: string }) {
  const [v, setV] = useState(initial);
  return (
    <>
      <TagSuggestInput id="t" value={v} onChange={setV} />
      <output data-testid="value">{v}</output>
    </>
  );
}

describe("tag field helpers (#2508)", () => {
  it("reads the field as tags and the fragment being typed", () => {
    expect(splitTags(" a, b ,, c")).toEqual(["a", "b", "c"]);
    expect(currentFragment("q3, Fin")).toBe("Fin");
    expect(currentFragment("Fin")).toBe("Fin");
    expect(completeWith("q3, Fin", "finance")).toBe("q3, finance, ");
    expect(completeWith("Fin", "finance")).toBe("finance, ");
  });

  it("suggests tags beginning with the fragment, without case, not already chosen", () => {
    expect(suggestionsFor(rows, "FIN").map((r) => r.tag)).toEqual(["finance", "finance-reports"]);
    expect(suggestionsFor(rows, "finance, fin").map((r) => r.tag)).toEqual(["finance-reports"]);
  });
});

describe("TagSuggestInput (#2508)", () => {
  it("offers existing tags with counts and uses the existing spelling", () => {
    render(<Harness />);
    const input = screen.getByRole("combobox");
    fireEvent.focus(input);
    fireEvent.change(input, { target: { value: "Fin" } });
    expect(screen.getByText("14 assets")).toBeInTheDocument();
    expect(screen.getByText(/Add .Fin. as a new tag/)).toBeInTheDocument();
    fireEvent.mouseDown(screen.getByText("finance"));
    expect(screen.getByTestId("value").textContent).toBe("finance, ");
  });

  it("adds a new tag as typed when chosen", () => {
    render(<Harness />);
    const input = screen.getByRole("combobox");
    fireEvent.focus(input);
    fireEvent.change(input, { target: { value: "board" } });
    fireEvent.mouseDown(screen.getByText(/Add .board. as a new tag/));
    expect(screen.getByTestId("value").textContent).toBe("board, ");
  });

  it("offers no new tag when the fragment is an existing one, and closes on blur", () => {
    render(<Harness />);
    const input = screen.getByRole("combobox");
    fireEvent.focus(input);
    fireEvent.change(input, { target: { value: "q3" } });
    expect(screen.queryByText(/as a new tag/)).not.toBeInTheDocument();
    expect(screen.getByText("2 assets")).toBeInTheDocument();
    fireEvent.blur(input);
    expect(screen.queryByRole("listbox")).not.toBeInTheDocument();
  });
});
