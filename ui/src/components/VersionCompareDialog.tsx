import { useMemo, useState } from "react";
import { useVersionContent } from "@/api/portal/hooks/assets";
import type { AssetVersion } from "@/api/portal/types";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { diffLines, diffStats } from "@/lib/textDiff";
import { truncationOf } from "@/lib/truncation";
import { cn } from "@/lib/utils";

// Comparing two versions of an asset (#2600): the line diff of their source,
// or for HTML, JSX and SVG of the words a reader sees with the markup left out.
// A version that holds only part of its content (an export cut at a limit) is
// never compared: an empty or partial diff of it would mislead.

/** comparable reports whether an asset's versions are text a line diff reads. */
export function comparable(contentType: string): boolean {
  const ct = contentType.toLowerCase();
  return (
    ct.startsWith("text/") ||
    ct.includes("json") ||
    ct.includes("xml") ||
    ct.includes("svg") ||
    ct.includes("javascript") ||
    ct.includes("jsx") ||
    ct.includes("markdown") ||
    ct.includes("yaml")
  );
}

/** hasRenderedText reports whether an asset's versions have words apart from their markup. */
export function hasRenderedText(contentType: string): boolean {
  const ct = contentType.toLowerCase();
  return ct.includes("html") || ct.includes("jsx") || ct.includes("svg");
}

/** completeVersion reports whether a version holds all of its content. */
export function completeVersion(v: AssetVersion): boolean {
  return !truncationOf(v.metadata);
}

const BLOCK_END = /<\/(p|div|li|h[1-6]|tr|td|th|section|article|header|footer|ul|ol|table|blockquote|pre|text|tspan|title)>/gi;

/** renderedText is the words a reader sees in markup, one block a line, the markup left out. */
export function renderedText(markup: string): string {
  const flattened = markup
    .replace(/<(script|style)\b[^>]*>[\s\S]*?<\/\1>/gi, "")
    .replace(/<br\s*\/?>/gi, "\n")
    .replace(BLOCK_END, "\n");
  const text = new DOMParser().parseFromString(flattened, "text/html").body.textContent ?? "";
  return text
    .split("\n")
    .map((l) => l.replace(/\s+/g, " ").trim())
    .filter(Boolean)
    .join("\n");
}

export interface ComparePair {
  from: number;
  to: number;
}

export function VersionCompareDialog({
  assetId,
  assetName,
  contentType,
  versions,
  currentVersion,
  pair,
  onClose,
}: {
  assetId: string;
  assetName: string;
  contentType: string;
  versions: AssetVersion[];
  currentVersion: number;
  pair: ComparePair;
  onClose: () => void;
}) {
  const choices = versions.filter(completeVersion).sort((a, b) => a.version - b.version);
  const [from, setFrom] = useState(pair.from);
  const [to, setTo] = useState(pair.to);
  const [mode, setMode] = useState<"source" | "text">("source");
  const label = (v: AssetVersion) =>
    `v${v.version} · ${v.version === currentVersion ? "current" : new Date(v.created_at).toLocaleDateString()}`;
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="max-h-[90vh] overflow-hidden sm:max-w-5xl" data-testid="version-compare">
        <DialogHeader>
          <DialogTitle>Compare versions · {assetName}</DialogTitle>
          <DialogDescription className="sr-only">The lines added and removed between two versions of this asset.</DialogDescription>
        </DialogHeader>
        <div className="flex flex-wrap items-center gap-3 text-sm">
          <VersionSelect id="compare-from" label="From" value={from} onChange={setFrom} choices={choices} labelOf={label} />
          <VersionSelect id="compare-to" label="To" value={to} onChange={setTo} choices={choices} labelOf={label} />
          {hasRenderedText(contentType) && (
            <div role="group" aria-label="Show" className="flex overflow-hidden rounded-md border">
              {(["source", "text"] as const).map((m) => (
                <Button
                  key={m}
                  type="button"
                  size="sm"
                  variant={mode === m ? "default" : "ghost"}
                  aria-pressed={mode === m}
                  className="rounded-none"
                  onClick={() => setMode(m)}
                >
                  {m === "source" ? "Source" : "Text"}
                </Button>
              ))}
            </div>
          )}
        </div>
        <CompareBody assetId={assetId} from={from} to={to} mode={mode} summary={versions.find((v) => v.version === to)?.change_summary} />
      </DialogContent>
    </Dialog>
  );
}

function VersionSelect({
  id,
  label,
  value,
  onChange,
  choices,
  labelOf,
}: {
  id: string;
  label: string;
  value: number;
  onChange: (v: number) => void;
  choices: AssetVersion[];
  labelOf: (v: AssetVersion) => string;
}) {
  return (
    <label htmlFor={id} className="flex items-center gap-2">
      {label}
      <select
        id={id}
        className="h-8 rounded-md border bg-background px-2 text-sm"
        value={value}
        onChange={(e) => onChange(Number(e.target.value))}
      >
        {choices.map((v) => (
          <option key={v.version} value={v.version}>
            {labelOf(v)}
          </option>
        ))}
      </select>
    </label>
  );
}

function CompareBody({
  assetId,
  from,
  to,
  mode,
  summary,
}: {
  assetId: string;
  from: number;
  to: number;
  mode: "source" | "text";
  summary?: string;
}) {
  const a = useVersionContent(assetId, from);
  const b = useVersionContent(assetId, to);
  const diff = useMemo(() => {
    if (a.data === undefined || b.data === undefined) return null;
    const read = mode === "text" ? renderedText : (s: string) => s;
    const lines = diffLines(read(a.data), read(b.data));
    return { lines, stats: diffStats(lines) };
  }, [a.data, b.data, mode]);
  if (a.isError || b.isError) {
    return <p className="text-sm text-destructive">A version could not be read, so the two cannot be compared.</p>;
  }
  if (!diff) return <p className="text-sm text-muted-foreground">Loading the two versions...</p>;
  return (
    <div className="min-h-0 space-y-2">
      <div className="flex flex-wrap gap-3 text-sm" data-testid="version-compare-stats">
        <span className="font-medium text-emerald-700 dark:text-emerald-400">+{diff.stats.added} lines</span>
        <span className="font-medium text-destructive">−{diff.stats.removed} lines</span>
        {diff.stats.added === 0 && diff.stats.removed === 0 && <span className="text-muted-foreground">No difference</span>}
        {mode === "text" ? (
          <span className="text-muted-foreground">The words a reader sees, with the markup left out</span>
        ) : (
          summary && <span className="text-muted-foreground">{summary}</span>
        )}
      </div>
      <pre className="max-h-[60vh] overflow-auto rounded-md border bg-muted/20 py-2 font-mono text-xs leading-5" data-testid="version-compare-lines">
        {diff.lines.map((l, i) => (
          <div
            key={i}
            className={cn(
              "whitespace-pre-wrap break-words px-4",
              l.kind === "added" && "bg-green-500/10 text-green-700 dark:text-green-400",
              l.kind === "removed" && "bg-red-500/10 text-red-600 dark:text-red-400",
            )}
          >
            {l.kind === "added" ? "+ " : l.kind === "removed" ? "- " : "  "}
            {l.text}
          </div>
        ))}
      </pre>
    </div>
  );
}
