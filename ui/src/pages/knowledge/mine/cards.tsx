import type { Insight, MemoryRecord } from "@/api/portal/types";
import { KnowledgeStatusBadge } from "@/components/knowledge/KnowledgeStatusBadge";
import { UrnBadge } from "@/components/knowledge/UrnBadge";
import { CollapsibleMarkdown } from "@/components/renderers/CollapsibleMarkdown";
import { MarkdownRenderer } from "@/components/renderers/MarkdownRenderer";
import { Card } from "@/components/ui/card";
import { markdownToPlainText } from "@/lib/markdownText";
import { sinkClassLabel } from "@/lib/sinkClass";

// The capture categories, in the reader's words rather than the stored keys.
const CATEGORY_LABELS: Record<string, string> = {
  correction: "Correction",
  business_context: "Business Context",
  data_quality: "Data Quality",
  usage_guidance: "Usage Guidance",
  relationship: "Relationship",
  enhancement: "Enhancement",
  general: "General",
};

function categoryLabel(category: string): string {
  return CATEGORY_LABELS[category] ?? category;
}

// CardHead is the line every record opens with: its lifecycle status and what
// kind of thing it is on the left, when it was captured on the right.
function CardHead({
  status,
  facets,
  createdAt,
}: {
  status: string;
  facets: string[];
  createdAt: string;
}) {
  return (
    <div className="flex items-start justify-between gap-2">
      <div className="flex flex-wrap items-center gap-2">
        <KnowledgeStatusBadge status={status} />
        {facets.map((f) => (
          <span key={f} className="text-xs text-muted-foreground">
            {f}
          </span>
        ))}
      </div>
      <span
        className="shrink-0 text-[11px] text-muted-foreground"
        title={new Date(createdAt).toLocaleString()}
      >
        {new Date(createdAt).toLocaleDateString()}
      </span>
    </div>
  );
}

// EntityUrns names the catalog entities a record is about, when it names any.
function EntityUrns({ urns }: { urns: string[] | undefined }) {
  if (!urns || urns.length === 0) return null;
  return (
    <div className="flex flex-wrap gap-1">
      {urns.map((urn) => (
        <UrnBadge key={urn} urn={urn} />
      ))}
    </div>
  );
}

export function InsightCard({ insight }: { insight: Insight }) {
  return (
    <Card className="gap-2 p-4">
      <CardHead
        status={insight.status}
        facets={[categoryLabel(insight.category)]}
        createdAt={insight.created_at}
      />
      <MarkdownRenderer content={insight.insight_text} bare />
      <EntityUrns urns={insight.entity_urns} />
      {insight.review_notes && (
        <p className="text-xs italic text-muted-foreground">
          Review: {markdownToPlainText(insight.review_notes)}
        </p>
      )}
    </Card>
  );
}

export function MemoryCard({ record }: { record: MemoryRecord }) {
  const facets = [sinkClassLabel(record.sink_class), categoryLabel(record.category)];
  return (
    <Card className="gap-2 p-4">
      <CardHead
        status={record.status}
        facets={facets.filter(Boolean)}
        createdAt={record.created_at}
      />
      <CollapsibleMarkdown content={record.content} />
      <EntityUrns urns={record.entity_urns} />
      {record.status === "stale" && record.stale_reason && (
        <p className="text-xs italic text-muted-foreground">
          Stale: {record.stale_reason}
        </p>
      )}
    </Card>
  );
}
