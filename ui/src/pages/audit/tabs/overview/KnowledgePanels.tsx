import { SectionCard } from "@/components/patterns/SectionCard";
import { StatusBadge } from "@/components/cards/StatusBadge";
import type { Insight, InsightStats } from "@/api/admin/types";

// The two knowledge panels at the foot of the MCP dashboard: the shape of the
// insight backlog, and the head of the review queue. Extracted from
// OverviewTab.tsx (#1207).

// titleCase renders a snake_case category ("query_pattern") as prose.
function titleCase(s: string): string {
  return s.replace(/_/g, " ").replace(/\b\w/g, (c) => c.toUpperCase());
}

export function InsightStatsPanel({
  stats,
  total,
  topCategory,
}: {
  stats?: InsightStats;
  total: number;
  topCategory: string;
}) {
  const byCategory = stats?.by_category;
  return (
    <SectionCard title="Knowledge Insights">
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
        <Figure label="Total" value={total || "-"} />
        <Figure label="Pending" value={stats?.total_pending ?? "-"} />
        <Figure label="Applied" value={stats?.by_status?.["applied"] ?? "-"} />
        <Figure label="Top Category" value={topCategory} />
      </div>
      {byCategory && Object.keys(byCategory).length > 0 && (
        <div className="mt-4 space-y-2">
          {Object.entries(byCategory)
            .sort((a, b) => b[1] - a[1])
            .map(([cat, count]) => (
              <CategoryBar
                key={cat}
                label={titleCase(cat)}
                count={count}
                pct={total > 0 ? (count / total) * 100 : 0}
              />
            ))}
        </div>
      )}
    </SectionCard>
  );
}

function Figure({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <div>
      <p className="text-xs text-muted-foreground">{label}</p>
      <p className="text-lg font-semibold">{value}</p>
    </div>
  );
}

function CategoryBar({ label, count, pct }: { label: string; count: number; pct: number }) {
  return (
    <div>
      <div className="mb-0.5 flex items-center justify-between text-xs">
        <span className="text-muted-foreground">{label}</span>
        <span className="font-medium">{count}</span>
      </div>
      <div className="h-1.5 overflow-hidden rounded-full bg-muted">
        <div className="h-full rounded-full bg-primary/70 transition-all" style={{ width: `${pct}%` }} />
      </div>
    </div>
  );
}

export function PendingReviewPanel({ insights }: { insights?: Insight[] }) {
  return (
    <SectionCard title="Pending Review">
      {insights && insights.length > 0 ? (
        <div className="space-y-2">
          {insights.map((ins) => (
            <div key={ins.id} className="flex items-start gap-2 text-xs">
              <StatusBadge variant="warning">{ins.confidence}</StatusBadge>
              <div className="min-w-0 flex-1">
                <p className="font-medium">{titleCase(ins.category)}</p>
                <p className="truncate text-muted-foreground">{ins.insight_text}</p>
              </div>
              <span className="shrink-0 text-muted-foreground">
                {new Date(ins.created_at).toLocaleDateString()}
              </span>
            </div>
          ))}
        </div>
      ) : (
        <p className="text-sm text-muted-foreground">No pending insights</p>
      )}
    </SectionCard>
  );
}
