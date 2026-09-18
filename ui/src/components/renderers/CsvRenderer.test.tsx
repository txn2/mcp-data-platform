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
  const head = screen.getByRole("columnheader", {
    name: new RegExp(headerName || "^$"),
  });
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

/** The first row control, which getAllByRole types as possibly undefined. */
function firstRow(): HTMLElement {
  const [first] = screen.getAllByRole("button", { name: /^Open row/ });
  if (!first) throw new Error("no row control rendered");
  return first;
}

describe("reading a row that does not fit its cells", () => {
  // A cell is truncated at 200px so the table can be scanned, and the only way
  // to read a longer value was the browser's own title tooltip -- which cannot
  // be selected or copied, does not wrap, and does not exist on touch (#1781).
  const WIDE = [
    "id,title,note",
    `1,"${"A very long title that will not fit in a two hundred pixel cell ".repeat(3)}","short"`,
    "2,second,another",
  ].join("\n");

  it("opens the record when a row is clicked", async () => {
    render(<CsvRenderer content={WIDE} />);

    fireEvent.click(firstRow());

    const fields = await screen.findByTestId("row-detail-fields");
    expect(fields.textContent).toContain("title");
    expect(fields.textContent).toContain("A very long title that will not fit");
  });

  it("opens the record from the keyboard", async () => {
    render(<CsvRenderer content={WIDE} />);

    fireEvent.keyDown(firstRow(), { key: "Enter" });
    expect(await screen.findByTestId("row-detail-fields")).toBeTruthy();
  });

  it("moves between rows without closing", async () => {
    render(<CsvRenderer content={WIDE} />);
    fireEvent.click(firstRow());
    await screen.findByTestId("row-detail-fields");

    // The first row has no previous; the second does.
    expect(
      (
        screen.getByRole("button", {
          name: "Previous row",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(true);
    fireEvent.click(screen.getByRole("button", { name: "Next row" }));

    expect(screen.getByTestId("row-detail-fields").textContent).toContain(
      "second",
    );
    expect(
      (
        screen.getByRole("button", {
          name: "Previous row",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(false);
  });

  // An absent trailing field and an empty one are different facts about a CSV
  // record, and the reader of a short record needs to tell them apart (#1779).
  it("says whether a field is empty or absent from the row", async () => {
    render(<CsvRenderer content={"a,b,c\n1,,3\n4,5\n"} />);

    fireEvent.click(firstRow());
    expect(
      (await screen.findByTestId("row-detail-fields")).textContent,
    ).toContain("empty");

    fireEvent.click(screen.getByRole("button", { name: "Next row" }));
    expect(screen.getByTestId("row-detail-fields").textContent).toContain(
      "not in this row",
    );
  });
});

describe("what the parser found wrong with the file's shape", () => {
  // The viewer read parsed.data and threw parsed.errors away, so a file whose
  // exporter omits trailing fields looked complete here while a table
  // registered over it named the short records (#1779).
  it("says how many rows end before the last column", () => {
    render(<CsvRenderer content={"a,b,c\n1,2,3\n4,5\n6,7\n"} />);

    const notice = screen.getByTestId("csv-shape-notice");
    expect(notice.textContent).toContain("2 rows end before the last column");
  });

  it("says how many rows carry more fields than the header names", () => {
    render(<CsvRenderer content={"a,b\n1,2\n3,4,5\n"} />);
    expect(screen.getByTestId("csv-shape-notice").textContent).toContain(
      "1 row has more fields than the header names",
    );
  });

  it("says nothing about a file whose records all match", () => {
    render(<CsvRenderer content={"a,b\n1,2\n3,4\n"} />);
    expect(screen.queryByTestId("csv-shape-notice")).toBeNull();
  });
});
