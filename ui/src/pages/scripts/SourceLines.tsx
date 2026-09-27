import { useEffect, useRef } from "react";
import type { SelectedLines } from "@/lib/codemirrorLines";
import { cn } from "@/lib/utils";

// SourceLines is a script's source for a reader who cannot edit it: numbered
// lines, the lines the Flow tab asked to see marked and scrolled to, and the
// lines the reader selects reported back so the Flow tab can mark the cards
// they produced (#1906).
export function SourceLines({
  source,
  markedLines,
  onSelectLines,
}: {
  source: string;
  markedLines?: number[];
  onSelectLines?: (lines: SelectedLines | null) => void;
}) {
  const hostRef = useRef<HTMLDivElement>(null);
  const marked = new Set(markedLines ?? []);
  const lines = source ? source.split("\n") : [];

  useEffect(() => {
    const first = [...(markedLines ?? [])].sort((a, b) => a - b)[0];
    if (!first) return;
    const row = hostRef.current?.querySelector<HTMLElement>(`[data-line="${first}"]`);
    row?.scrollIntoView?.({ block: "center" });
  }, [markedLines]);

  if (lines.length === 0) {
    return (
      <p className="rounded-md border bg-muted/30 p-3 text-xs text-muted-foreground">
        (this version has no source)
      </p>
    );
  }

  const reportSelection = () => {
    const sel = window.getSelection();
    if (!sel || sel.isCollapsed) {
      onSelectLines?.(null);
      return;
    }
    const a = lineOf(sel.anchorNode);
    const b = lineOf(sel.focusNode);
    if (!a || !b) return;
    onSelectLines?.({ from: Math.min(a, b), to: Math.max(a, b) });
  };

  return (
    <div
      ref={hostRef}
      className="max-h-[calc(100vh-200px)] overflow-auto rounded-md border bg-muted/30 py-2 font-mono text-xs leading-relaxed"
      onMouseUp={reportSelection}
      onKeyUp={reportSelection}
      data-testid="script-source-lines"
    >
      {lines.map((text, i) => {
        const n = i + 1;
        return (
          <div
            key={n}
            data-line={n}
            data-marked={marked.has(n) || undefined}
            className={cn("flex whitespace-pre", marked.has(n) && "bg-[hsl(var(--chart-4)/0.2)]")}
          >
            <span className="w-12 shrink-0 pr-3 text-right text-muted-foreground select-none">{n}</span>
            <span className="pr-3">{text || " "}</span>
          </div>
        );
      })}
    </div>
  );
}

// lineOf is the source line a DOM node sits on.
function lineOf(node: Node | null): number | null {
  const el = node instanceof Element ? node : node?.parentElement;
  const row = el?.closest<HTMLElement>("[data-line]");
  return row ? Number(row.dataset.line) : null;
}
