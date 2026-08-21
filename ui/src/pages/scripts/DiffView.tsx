import { cn } from "@/lib/utils";

// DiffView renders a unified diff the server computed. The diff is produced
// once, next to the grant it belongs to, so the code change and the authority
// change a reviewer reads always describe the same pair of versions.
//
// Colour is not the only signal: every line keeps its +/-/@ marker, so the
// change is still readable where colour is not (a printed review, a colour
// vision difference, a copied paste).
export function DiffView({ diff }: { diff: string }) {
  const lines = diff.replace(/\n$/, "").split("\n");
  return (
    <pre className="overflow-x-auto rounded-md border bg-muted/30 p-3 font-mono text-xs leading-relaxed">
      {lines.map((line, i) => (
        <div
          // Diff lines have no identity of their own and never reorder; the
          // index is the line number here.
          key={i}
          className={cn("whitespace-pre", diffLineClass(line))}
        >
          {line || " "}
        </div>
      ))}
    </pre>
  );
}

// diffLineClass colours one unified-diff line by its marker.
function diffLineClass(line: string): string {
  if (line.startsWith("+++") || line.startsWith("---")) {
    return "text-muted-foreground";
  }
  if (line.startsWith("@@")) {
    return "text-blue-600 dark:text-blue-300";
  }
  if (line.startsWith("+")) {
    return "bg-emerald-500/10 text-emerald-700 dark:text-emerald-300";
  }
  if (line.startsWith("-")) {
    return "bg-red-500/10 text-red-700 dark:text-red-300";
  }
  return "text-muted-foreground";
}

// SourceView renders a version's whole source, which is what a first reading
// is: there is no earlier version to diff against, so the change is the file.
export function SourceView({ source }: { source: string }) {
  return (
    <pre className="overflow-x-auto rounded-md border bg-muted/30 p-3 font-mono text-xs leading-relaxed whitespace-pre">
      {source || "(this version has no source)"}
    </pre>
  );
}
