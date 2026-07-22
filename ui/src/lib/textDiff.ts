// Line-based text diff for the prompt version history view (#1010). A plain
// LCS over lines is sufficient at prompt scale (bodies are short documents),
// avoiding a diff library dependency for one view.

export interface DiffLine {
  kind: "same" | "added" | "removed";
  text: string;
}

// diffLines returns the line-level edit script transforming `from` into `to`:
// unchanged lines once as "same", removals from `from` and additions in `to`
// in document order.
export function diffLines(from: string, to: string): DiffLine[] {
  const a = from.split("\n");
  const b = to.split("\n");

  // LCS length table (a.length+1 x b.length+1).
  const m = a.length;
  const n = b.length;
  const table: number[][] = Array.from({ length: m + 1 }, () => new Array<number>(n + 1).fill(0));
  for (let i = m - 1; i >= 0; i--) {
    for (let j = n - 1; j >= 0; j--) {
      table[i]![j] =
        a[i] === b[j]
          ? table[i + 1]![j + 1]! + 1
          : Math.max(table[i + 1]![j]!, table[i]![j + 1]!);
    }
  }

  // Walk the table to emit the edit script.
  const out: DiffLine[] = [];
  let i = 0;
  let j = 0;
  while (i < m && j < n) {
    if (a[i] === b[j]) {
      out.push({ kind: "same", text: a[i]! });
      i++;
      j++;
    } else if (table[i + 1]![j]! >= table[i]![j + 1]!) {
      out.push({ kind: "removed", text: a[i]! });
      i++;
    } else {
      out.push({ kind: "added", text: b[j]! });
      j++;
    }
  }
  for (; i < m; i++) out.push({ kind: "removed", text: a[i]! });
  for (; j < n; j++) out.push({ kind: "added", text: b[j]! });
  return out;
}

// diffStats counts added and removed lines in an edit script.
export function diffStats(lines: DiffLine[]): { added: number; removed: number } {
  let added = 0;
  let removed = 0;
  for (const l of lines) {
    if (l.kind === "added") added++;
    else if (l.kind === "removed") removed++;
  }
  return { added, removed };
}
