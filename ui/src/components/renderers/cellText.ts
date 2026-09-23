/**
 * How a value in a table cell is written, for every tabular viewer: the CSV
 * table, the JSON-lines table view and the Parquet grid (#1833).
 *
 * A cell holds a scalar most of the time, but a JSON-lines record or a Parquet
 * row can hold an object or a list, and `String()` writes those as
 * "[object Object]". A nested value is its compact JSON in the cell and its
 * indented JSON in the row dialog, where it is read. A Parquet INT64 arrives as
 * a BigInt, which JSON.stringify refuses, so it is written as its digits.
 */

function replacer(_key: string, value: unknown): unknown {
  if (typeof value === "bigint") return value.toString();
  if (value instanceof Uint8Array) return `0x${Array.from(value, (b) => b.toString(16).padStart(2, "0")).join("")}`;
  return value;
}

function isNested(value: unknown): value is object {
  return typeof value === "object" && value !== null && !(value instanceof Date) && !(value instanceof Uint8Array);
}

/** The one-line text of a value, for a table cell. */
export function cellText(value: unknown): string {
  if (value === null || value === undefined) return "";
  if (value instanceof Date) return value.toISOString();
  if (value instanceof Uint8Array) return replacer("", value) as string;
  if (isNested(value)) return JSON.stringify(value, replacer);
  return String(value);
}

/** The full text of a value, for reading it in the row dialog. */
export function detailText(value: unknown): string {
  if (isNested(value)) return JSON.stringify(value, replacer, 2);
  return cellText(value);
}

/** Orders two cell values: numbers and BigInts by magnitude, anything else as text. */
export function compareCells(a: unknown, b: unknown): number {
  const numeric = (v: unknown) => (typeof v === "number" && !Number.isNaN(v)) || typeof v === "bigint";
  if (numeric(a) && numeric(b)) {
    const x = a as number | bigint;
    const y = b as number | bigint;
    return x < y ? -1 : x > y ? 1 : 0;
  }
  return cellText(a).localeCompare(cellText(b));
}
