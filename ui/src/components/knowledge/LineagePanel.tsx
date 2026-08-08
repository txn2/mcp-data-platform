import { useKnowledgePageLineage } from "@/api/portal/hooks";
import { SectionCard } from "@/components/patterns/SectionCard";
import { KnowledgeStatusBadge } from "./KnowledgeStatusBadge";

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
    <SectionCard title="Synthesized from">
      <p className="mb-3 text-xs text-muted-foreground">
        The captured insights this page was promoted from.
      </p>
      <ul className="space-y-2">
        {insights.map((ins) => (
          <li key={ins.id} className="rounded-md border bg-card p-2.5">
            <div className="mb-1 flex items-center gap-2">
              <KnowledgeStatusBadge status={ins.status} />
              {ins.category && <span className="text-xs text-muted-foreground">{ins.category}</span>}
            </div>
            <p className="text-sm text-foreground">{ins.text}</p>
          </li>
        ))}
      </ul>
    </SectionCard>
  );
}
