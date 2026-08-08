import { describe, it, expect, afterEach } from "vitest";
import { render, screen, cleanup, fireEvent } from "@testing-library/react";
import { CsvRenderer } from "./CsvRenderer";

afterEach(cleanup);

// A column's sort key is its own name, which for a CSV is data rather than a
// closed set the component chose. Papa Parse names an empty header cell "", so
// a sentinel standing in for "nothing is sorted" must not be a string a real
// column can hold.
const UNNAMED_COLUMN = "region,,units\nWest,x,10\nEast,y,20\n";

/** The active head shows a direction chevron; an idle one shows the neutral pair. */
function sortState(headerName: string): "asc" | "desc" | "none" {
  const head = screen.getByRole("columnheader", { name: new RegExp(headerName || "^$") });
  if (head.querySelector(".lucide-chevron-up")) return "asc";
  if (head.querySelector(".lucide-chevron-down")) return "desc";
  return "none";
}

describe("CsvRenderer sort indicator", () => {
  it("marks no column sorted when a header is unnamed", () => {
    render(<CsvRenderer content={UNNAMED_COLUMN} />);

    const heads = screen.getAllByRole("columnheader");
    expect(heads).toHaveLength(3);
    for (const head of heads) {
      expect(head.querySelector(".lucide-chevron-up")).toBeNull();
      expect(head.querySelector(".lucide-chevron-down")).toBeNull();
    }
  });

  it("marks only the clicked column, and flips it on a second click", () => {
    render(<CsvRenderer content={UNNAMED_COLUMN} />);

    fireEvent.click(screen.getByRole("columnheader", { name: /region/i }));
    expect(sortState("region")).toBe("asc");
    expect(sortState("units")).toBe("none");

    fireEvent.click(screen.getByRole("columnheader", { name: /region/i }));
    expect(sortState("region")).toBe("desc");
  });
});
