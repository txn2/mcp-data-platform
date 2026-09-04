import { describe, it, expect } from "vitest";
import {
  byteLength,
  defectReason,
  fixtureColumns,
  inspectFixture,
  normalizeFixture,
  repairSummary,
} from "./csvfixture";

const TORN = [
  'name,address,city',
  'Acme,"1200 SW Morrison St',
  'Suite 300",Portland',
  "Globex,900 Main St,Eugene",
  'Initech,"5150 Mae Anne Ave',
  'Unit 12",Reno',
  "",
].join("\n");

describe("fixtureColumns", () => {
  it("names the columns the file's header names, in its order", () => {
    expect(fixtureColumns("term, definition ,owner\na,b,c\n").map((c) => c.name)).toEqual([
      "term",
      "definition",
      "owner",
    ]);
  });

  it("reads a header through the quoted line breaks below it", () => {
    expect(fixtureColumns(TORN).map((c) => c.name)).toEqual(["name", "address", "city"]);
  });

  it("types every column VARCHAR, which is the CSV connector's rule", () => {
    expect(fixtureColumns("a,b\n1,2\n").map((c) => c.type)).toEqual(["VARCHAR", "VARCHAR"]);
  });

  it("has nothing to declare about a file it was given none of", () => {
    expect(fixtureColumns("")).toEqual([]);
  });
});

describe("inspectFixture", () => {
  it("counts the rows a line break is inside a cell of and names their column", () => {
    expect(inspectFixture(TORN)).toEqual({ rows: 2, columns: ["address"] });
  });

  it("finds nothing in a file whose every cell is on one line", () => {
    expect(inspectFixture("a,b\n1,2\n")).toEqual({ rows: 0, columns: [] });
  });
});

describe("normalizeFixture", () => {
  it("puts each broken cell back on one line and counts the rows it changed", () => {
    const { csv, rowsRepaired } = normalizeFixture(TORN);
    expect(rowsRepaired).toBe(2);
    expect(csv).toContain("1200 SW Morrison St Suite 300");
    expect(csv).toContain("5150 Mae Anne Ave Unit 12");
  });

  it("leaves the corrected file one record per newline, with no carriage return", () => {
    const { csv } = normalizeFixture(TORN);
    expect(csv).not.toContain("\r");
    expect(csv.trimEnd().split("\n")).toHaveLength(4);
    expect(inspectFixture(csv)).toEqual({ rows: 0, columns: [] });
  });

  it("shortens the file, the quotes and the breaks being what it takes out", () => {
    expect(byteLength(normalizeFixture(TORN).csv)).toBeLessThan(byteLength(TORN));
  });

  it("changes nothing in a file that was already readable", () => {
    const clean = "a,b\n1,2\n";
    expect(normalizeFixture(clean)).toEqual({ csv: clean, rowsRepaired: 0 });
  });
});

describe("the sentences a person reads", () => {
  it("says how many rows and which column, agreeing the verb with the count", () => {
    expect(defectReason({ rows: 1, columns: ["address"] })).toContain(
      "1 row in this file has a line break inside a cell (in address)",
    );
    expect(defectReason({ rows: 4, columns: ["address", "notes"] })).toContain(
      "4 rows in this file have a line break inside a cell (in address and notes)",
    );
  });

  it("names three columns as a list, as tablecsv.JoinAnd renders one", () => {
    expect(defectReason({ rows: 2, columns: ["a", "b", "c"] })).toContain("(in a, b, and c)");
  });

  it("says what the correction did in the same count", () => {
    expect(repairSummary(1)).toBe("put 1 row back onto one line");
    expect(repairSummary(4)).toBe("put 4 rows back onto one line");
  });
});
