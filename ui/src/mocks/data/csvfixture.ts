import Papa from "papaparse";
// A relative path rather than the "@/" alias, for the reason ./resources gives:
// this module is reached from the fixture graph vite.config.ts loads.
import type { TableColumn } from "../../api/tables/types";

// What a registration answers about a CSV, read off the CSV.
//
// Every claim the registered-table panel makes is about the bytes the viewer
// beside it is rendering: the columns come from the file's header, the refusal
// counts the rows the file actually carries a line break inside, and the
// correction is the file rewritten. Answering any of them from a constant puts
// the two halves of one screen in contradiction -- the state #1617 was filed
// over, where a ten-row store list with no `address` column was refused for 94
// torn rows in `address`, and every registration reported the same three
// columns whatever file it was made over.
//
// These mirror `internal/platform/tablecsv`, which is where the real answers
// come from: `ColumnsFrom` (trimmed header fields, every column VARCHAR),
// `Defect.Reason`, `collapseLineBreaks` and `repairSummary`. The sentences are
// reproduced rather than approximated so a capture of this panel is a capture
// of what a deployment says.

/** The type every column of a registered table has; see TableColumn. */
const COLUMN_TYPE = "VARCHAR";

/** records parses a CSV fixture into its rows, quoted line breaks intact. */
function records(csv: string): string[][] {
  return Papa.parse<string[]>(csv, { skipEmptyLines: true }).data;
}

/**
 * fixtureColumns is the columns a table registered over this file declares:
 * its header fields, trimmed, each one VARCHAR.
 */
export function fixtureColumns(csv: string): TableColumn[] {
  const header = records(csv)[0] ?? [];
  return header.map((name, i) => ({
    name: name.trim() || `column_${i + 1}`,
    type: COLUMN_TYPE,
  }));
}

/**
 * CSVDefect is what a line-based reader would get wrong about this file: how
 * many rows carry a line break inside a cell, and which columns those are in.
 */
export interface CSVDefect {
  rows: number;
  columns: string[];
}

/**
 * inspectFixture counts the rows of a fixture that carry a line break inside a
 * cell and names the columns they are in.
 *
 * The header is scanned like any other record, as the Go scan does: a line
 * break in a column name tears the file the same way.
 */
export function inspectFixture(csv: string): CSVDefect {
  const rows = records(csv);
  const names = (rows[0] ?? []).map((n, i) => n.trim() || `column_${i + 1}`);
  const hit = new Set<number>();
  let count = 0;
  for (const record of rows) {
    let broken = false;
    record.forEach((field, i) => {
      if (/[\r\n]/.test(field)) {
        hit.add(i);
        broken = true;
      }
    });
    if (broken) count++;
  }
  const columns = [...hit]
    .sort((a, b) => a - b)
    .map((i) => names[i] ?? `column ${i + 1}`);
  return { rows: count, columns };
}

/**
 * normalizeFixture is the corrected file: every cell back on one line, the
 * lines it was written across joined by a single space. It reports how many
 * rows it changed, which is what the correction's summary counts.
 */
export function normalizeFixture(csv: string): { csv: string; rowsRepaired: number } {
  const rows = records(csv);
  let rowsRepaired = 0;
  const flattened = rows.map((record) => {
    let changed = false;
    const out = record.map((field) => {
      if (!/[\r\n]/.test(field)) return field;
      changed = true;
      return field
        .split(/[\r\n]+/)
        .map((line) => line.trim())
        .filter((line) => line !== "")
        .join(" ");
    });
    if (changed) rowsRepaired++;
    return out;
  });
  // Newlines, explicitly: papaparse emits CRLF by default, and a corrected
  // file whose lines end in a carriage return is the OTHER defect a
  // registration refuses.
  return { csv: `${Papa.unparse(flattened, { newline: "\n" })}\n`, rowsRepaired };
}

/** byteLength is the size the file is stored and reported at. */
export function byteLength(text: string): number {
  return new TextEncoder().encode(text).length;
}

/** joinAnd renders a short list in prose, as tablecsv.JoinAnd does. */
function joinAnd(items: string[]): string {
  if (items.length === 0) return "";
  const head = items.slice(0, -1);
  const last = items[items.length - 1]!;
  if (head.length === 0) return last;
  if (head.length === 1) return `${head[0]} and ${last}`;
  return `${head.join(", ")}, and ${last}`;
}

/** plural agrees a count with its noun, as tablecsv.plural does. */
function plural(n: number, one: string, many: string): string {
  return n === 1 ? `1 ${one}` : `${n} ${many}`;
}

/**
 * defectReason is what the person is told is wrong with the file, in the words
 * `Defect.Reason` writes.
 */
export function defectReason(defect: CSVDefect): string {
  const inColumns = defect.columns.length > 0 ? ` (in ${joinAnd(defect.columns)})` : "";
  return (
    `${plural(defect.rows, "row", "rows")} in this file ` +
    `${defect.rows === 1 ? "has" : "have"} a line break inside a cell${inColumns}, and a table ` +
    "reads a line break as the end of the row, so each of those rows would be torn into fragments."
  );
}

/**
 * repairSummary says what the correction did, in the words `repairSummary`
 * writes -- the same sentence the version trail records, so the panel and the
 * version history agree.
 */
export function repairSummary(rowsRepaired: number): string {
  return `put ${plural(rowsRepaired, "row", "rows")} back onto one line`;
}
