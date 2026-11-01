// The one ordering every list of named things in the portal uses (#2389):
// case-insensitive, accents next to their base letter, and numbers compared
// by value, so `conn-2` comes before `conn-10`. Pickers sort on the label
// they display, not on the key the server sorted by.
//
// The lint rule `local/no-bare-name-sort` (ui/eslint-rules) refuses a bare
// `.sort()` and `localeCompare` elsewhere in src, so a new picker reaches for
// these helpers instead of the code-unit order a bare sort gives.

const collator = new Intl.Collator(undefined, { numeric: true, sensitivity: "base" });

/** Compares two strings by code unit: for keys, ids and ISO timestamps, never for names. */
export function compareOrdinal(a: string, b: string): number {
  if (a === b) return 0;
  return a < b ? -1 : 1;
}

/**
 * Compares two names for display order. Names the collator holds equal
 * ("a" and "A") fall back to code-unit order, so the order is total and two
 * sorts of the same list always agree.
 */
export function compareNames(a: string, b: string): number {
  return collator.compare(a, b) || compareOrdinal(a, b);
}

/**
 * Compares two slash-separated paths folder by folder, so a folder's children
 * follow it directly ("a", "a/b", "a-c") and each level is in name order.
 */
export function comparePaths(a: string, b: string): number {
  const as = a.split("/");
  const bs = b.split("/");
  const n = Math.min(as.length, bs.length);
  for (let i = 0; i < n; i++) {
    const cmp = compareNames(as[i] ?? "", bs[i] ?? "");
    if (cmp !== 0) return cmp;
  }
  return as.length - bs.length;
}

/** A comparator ordering items by the name `key` reads from each. */
export function byName<T>(key: (item: T) => string): (a: T, b: T) => number {
  return (a, b) => compareNames(key(a), key(b));
}

/** A new array of the names in display order; the input is not changed. */
export function sortNames(names: Iterable<string>): string[] {
  return Array.from(names).sort(compareNames);
}

export interface SortOptionsConfig<T> {
  /** Entries kept first, in their given order ("All ...", "None", "Choose ..."). */
  pinFirst?: (option: T) => boolean;
  /** Entries kept last, in their given order ("New folder", a stale value). */
  pinLast?: (option: T) => boolean;
}

/**
 * A new array of options ordered by label, with the deliberate leading and
 * trailing entries left where they were put.
 */
export function sortOptions<T extends { label: string }>(
  options: readonly T[],
  config: SortOptionsConfig<T> = {},
): T[] {
  const { pinFirst, pinLast } = config;
  const first: T[] = [];
  const middle: T[] = [];
  const last: T[] = [];
  for (const option of options) {
    if (pinFirst?.(option)) first.push(option);
    else if (pinLast?.(option)) last.push(option);
    else middle.push(option);
  }
  middle.sort(byName((o) => o.label));
  return [...first, ...middle, ...last];
}
