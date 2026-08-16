import { FileText, Lightbulb } from "lucide-react";
import { EmptyState } from "@/components/patterns/EmptyState";
import { SectionCard } from "@/components/patterns/SectionCard";
import { StatusBadge } from "@/components/cards/StatusBadge";
import type { SessionAssetRef, SessionInsightRef } from "@/api/admin/types";

// SessionOutputs is what the session left behind. Assets open where an admin
// already reads assets; insights are shown with the review status they are
// sitting at, since a captured insight nobody reviewed is the common case
// worth seeing from here.

export function SessionAssets({
  assets,
  onNavigate,
}: {
  assets: SessionAssetRef[];
  onNavigate: (path: string) => void;
}) {
  return (
    <SectionCard title={`Assets (${assets.length})`}>
      {assets.length === 0 ? (
        <EmptyState icon={FileText}>This session saved no assets.</EmptyState>
      ) : (
        <ul className="divide-y">
          {assets.map((asset) => (
            <li key={asset.id}>
              <button
                type="button"
                onClick={() => onNavigate(`/admin/assets/${asset.id}`)}
                className="flex w-full items-center justify-between gap-3 py-2 text-left text-sm hover:text-primary"
              >
                <span className="truncate">{asset.name}</span>
                <span className="shrink-0 text-xs text-muted-foreground">
                  {asset.content_type}
                </span>
              </button>
            </li>
          ))}
        </ul>
      )}
    </SectionCard>
  );
}

export function SessionInsights({ insights }: { insights: SessionInsightRef[] }) {
  return (
    <SectionCard title={`Insights (${insights.length})`}>
      {insights.length === 0 ? (
        <EmptyState icon={Lightbulb}>
          This session captured no insights.
        </EmptyState>
      ) : (
        <ul className="divide-y">
          {insights.map((insight) => (
            <li key={insight.id} className="flex items-start gap-3 py-2 text-sm">
              <span className="min-w-0 flex-1">
                <span className="block break-words">{insight.text}</span>
                <span className="text-xs text-muted-foreground">
                  {insight.category}
                </span>
              </span>
              <StatusBadge
                variant={insight.status === "applied" ? "success" : "neutral"}
              >
                {insight.status}
              </StatusBadge>
            </li>
          ))}
        </ul>
      )}
    </SectionCard>
  );
}
