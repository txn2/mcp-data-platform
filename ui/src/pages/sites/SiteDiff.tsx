import { diffLines, diffStats, type DiffLine } from "@/lib/textDiff";

// How a change to a site's file reads (#2590): added lines, removed lines and
// a few unchanged ones around each, whether the change comes from the server
// (a unified diff between two releases) or from the editor (what a save would
// write).

const CONTEXT = 3;

type Row = { kind: "same" | "added" | "removed" | "hunk"; text: string };

function rowClass(kind: Row["kind"]): string {
  switch (kind) {
    case "added":
      return "bg-emerald-500/10 text-emerald-700 dark:text-emerald-300";
    case "removed":
      return "bg-red-500/10 text-red-700 dark:text-red-300";
    case "hunk":
      return "bg-muted text-muted-foreground";
    default:
      return "";
  }
}

const SIGN: Record<Row["kind"], string> = { same: " ", added: "+", removed: "−", hunk: "" };

function Rows({ rows, testId }: { rows: Row[]; testId?: string }) {
  if (rows.length === 0) return <p className="text-sm text-muted-foreground">No changes.</p>;
  return (
    <div className="max-h-80 overflow-auto rounded border font-mono text-xs" data-testid={testId}>
      {rows.map((r, i) => (
        <div key={i} className={`flex whitespace-pre ${rowClass(r.kind)}`}>
          <span className="w-5 shrink-0 select-none text-center">{SIGN[r.kind]}</span>
          <span className="pr-3">{r.text}</span>
        </div>
      ))}
    </div>
  );
}

/** UnifiedDiff shows the diff text the server returns, without its file headers. */
export function UnifiedDiff({ text }: { text: string }) {
  const rows: Row[] = [];
  for (const line of text.split("\n")) {
    if (line.startsWith("---") || line.startsWith("+++") || line === "") continue;
    if (line.startsWith("@@")) rows.push({ kind: "hunk", text: line });
    else if (line.startsWith("+")) rows.push({ kind: "added", text: line.slice(1) });
    else if (line.startsWith("-")) rows.push({ kind: "removed", text: line.slice(1) });
    else rows.push({ kind: "same", text: line.slice(1) });
  }
  return <Rows rows={rows} testId="file-diff" />;
}

/** The rows of an edit, unchanged runs cut to a few lines around each change. */
export function editRows(lines: DiffLine[]): Row[] {
  const keep = lines.map((l, i) => l.kind !== "same" || lines.slice(Math.max(0, i - CONTEXT), i + CONTEXT + 1).some((x) => x.kind !== "same"));
  const rows: Row[] = [];
  let skipped = false;
  lines.forEach((l, i) => {
    if (!keep[i]) {
      skipped = true;
      return;
    }
    if (skipped && rows.length > 0) rows.push({ kind: "hunk", text: `@@ line ${i + 1} @@` });
    skipped = false;
    rows.push({ kind: l.kind, text: l.text });
  });
  return rows;
}

/** EditDiff shows what saving from to to would change, with its counts. */
export function EditDiff({ from, to }: { from: string; to: string }) {
  const lines = diffLines(from, to);
  const { added, removed } = diffStats(lines);
  return (
    <div className="space-y-2">
      <p className="text-xs text-muted-foreground" data-testid="edit-counts">
        {added} {added === 1 ? "line" : "lines"} added, {removed} removed
      </p>
      <Rows rows={editRows(lines)} testId="edit-diff" />
    </div>
  );
}
