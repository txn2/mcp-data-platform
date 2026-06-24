import { useKnowledgePageLineage } from "@/api/portal/hooks";

const STATUS_TONE: Record<string, string> = {
  applied: "border-primary/20 bg-primary/10 text-primary",
  approved: "border-primary/20 bg-primary/10 text-primary",
  pending: "border-border bg-muted text-muted-foreground",
  rejected: "border-border bg-muted text-muted-foreground line-through",
  superseded: "border-border bg-muted text-muted-foreground",
};

/**
 * LineagePanel shows the insights a knowledge page was synthesized from (#678),
 * tracing canonical knowledge back to the captured insights that produced it. It is
 * the reviewer-facing provenance view, so the raw insight text is shown here rather
 * than as agent context.
 */
export function LineagePanel({ pageId }: { pageId: string }) {
  const { data } = useKnowledgePageLineage(pageId);
  const insights = data?.insights ?? [];
  if (insights.length === 0) return null;

  return (
    <aside className="rounded-lg border border-border bg-card p-4">
      <h2 className="mb-1 text-sm font-semibold text-foreground">Synthesized from</h2>
      <p className="mb-3 text-xs text-muted-foreground">
        The captured insights this page was promoted from.
      </p>
      <ul className="space-y-2">
        {insights.map((ins) => (
          <li key={ins.id} className="rounded-md border border-border bg-background p-2.5">
            <div className="mb-1 flex items-center gap-2">
              <span
                className={`inline-flex items-center rounded-full border px-1.5 py-0.5 text-xs ${
                  STATUS_TONE[ins.status] ?? STATUS_TONE.pending
                }`}
              >
                {ins.status}
              </span>
              {ins.category && <span className="text-xs text-muted-foreground">{ins.category}</span>}
            </div>
            <p className="text-sm text-foreground">{ins.text}</p>
          </li>
        ))}
      </ul>
    </aside>
  );
}
