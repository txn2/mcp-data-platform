import { useMemo } from "react";
import { useKnowledgePageRefs, type PageEntityRef } from "@/api/portal/hooks";
import { EntityChip } from "./EntityChip";

const TYPE_ORDER = ["asset", "prompt", "collection", "connection", "knowledge_page", "datahub"];
const TYPE_LABELS: Record<string, string> = {
  asset: "Assets",
  prompt: "Prompts",
  collection: "Collections",
  connection: "Connections",
  knowledge_page: "Pages",
  datahub: "DataHub",
};

/**
 * RelatedPanel lists the entities a knowledge page references, grouped by type.
 * The list is already access-filtered and resolved by the server, so it only
 * contains references the viewer can reach, each with its display label.
 */
export function RelatedPanel({ pageId, onNavigate }: { pageId: string; onNavigate?: (path: string) => void }) {
  const { data } = useKnowledgePageRefs(pageId);
  const refs = useMemo(() => data?.refs ?? [], [data]);

  const groups = useMemo(() => {
    const byType = new Map<string, PageEntityRef[]>();
    for (const ref of refs) {
      const list = byType.get(ref.type) ?? [];
      list.push(ref);
      byType.set(ref.type, list);
    }
    return byType;
  }, [refs]);

  if (refs.length === 0) return null;

  return (
    <aside className="rounded-lg border border-border bg-card p-4">
      <h2 className="mb-3 text-sm font-semibold text-foreground">Related</h2>
      <div className="space-y-3">
        {TYPE_ORDER.filter((t) => groups.has(t)).map((t) => (
          <div key={t}>
            <p className="mb-1.5 text-xs uppercase tracking-wide text-muted-foreground">
              {TYPE_LABELS[t] ?? t}
            </p>
            <div className="flex flex-wrap gap-1.5">
              {(groups.get(t) ?? []).map((ref) => (
                <EntityChip
                  key={ref.urn}
                  urn={ref.urn}
                  resolved={{ urn: ref.urn, type: ref.type, label: ref.label, exists: ref.exists, accessible: true }}
                  onNavigate={onNavigate}
                />
              ))}
            </div>
          </div>
        ))}
      </div>
    </aside>
  );
}
