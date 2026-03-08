import { useState } from "react";
import { useMyInsights, useMyInsightStats } from "@/api/portal/hooks";
import { StatCard } from "@/components/cards/StatCard";
import { Lightbulb, ChevronLeft, ChevronRight } from "lucide-react";
import type { Insight } from "@/api/portal/types";

const STATUS_BADGES: Record<string, { label: string; cls: string }> = {
  pending: {
    label: "Pending",
    cls: "bg-yellow-100 text-yellow-800 dark:bg-yellow-900/30 dark:text-yellow-400",
  },
  approved: {
    label: "Approved",
    cls: "bg-blue-100 text-blue-800 dark:bg-blue-900/30 dark:text-blue-400",
  },
  applied: {
    label: "Applied",
    cls: "bg-green-100 text-green-800 dark:bg-green-900/30 dark:text-green-400",
  },
  rejected: {
    label: "Rejected",
    cls: "bg-red-100 text-red-800 dark:bg-red-900/30 dark:text-red-400",
  },
  superseded: {
    label: "Superseded",
    cls: "bg-gray-100 text-gray-600 dark:bg-gray-800 dark:text-gray-400",
  },
};

const CATEGORY_LABELS: Record<string, string> = {
  correction: "Correction",
  business_context: "Business Context",
  data_quality: "Data Quality",
  usage_guidance: "Usage Guidance",
  relationship: "Relationship",
  enhancement: "Enhancement",
};

const PAGE_SIZE = 20;

function StatusBadge({ status }: { status: string }) {
  const badge = STATUS_BADGES[status] ?? {
    label: status,
    cls: "bg-gray-100 text-gray-600",
  };
  return (
    <span
      className={`inline-flex items-center rounded-full px-2 py-0.5 text-[10px] font-medium ${badge.cls}`}
    >
      {badge.label}
    </span>
  );
}

function InsightCard({ insight }: { insight: Insight }) {
  return (
    <div className="rounded-lg border bg-card p-4 space-y-2">
      <div className="flex items-start justify-between gap-2">
        <div className="flex items-center gap-2">
          <StatusBadge status={insight.status} />
          <span className="text-xs text-muted-foreground">
            {CATEGORY_LABELS[insight.category] ?? insight.category}
          </span>
        </div>
        <span
          className="shrink-0 text-[11px] text-muted-foreground"
          title={new Date(insight.created_at).toLocaleString()}
        >
          {new Date(insight.created_at).toLocaleDateString()}
        </span>
      </div>
      <p className="text-sm leading-relaxed">{insight.insight_text}</p>
      {insight.entity_urns.length > 0 && (
        <div className="flex flex-wrap gap-1">
          {insight.entity_urns.map((urn) => (
            <span
              key={urn}
              className="inline-block rounded bg-muted px-1.5 py-0.5 text-[10px] font-mono text-muted-foreground"
            >
              {urn.split(",").pop()?.replace(")", "") ?? urn}
            </span>
          ))}
        </div>
      )}
      {insight.review_notes && (
        <p className="text-xs text-muted-foreground italic">
          Review: {insight.review_notes}
        </p>
      )}
    </div>
  );
}

export function MyKnowledgePage() {
  const [statusFilter, setStatusFilter] = useState("");
  const [offset, setOffset] = useState(0);

  const stats = useMyInsightStats();
  const insights = useMyInsights({
    status: statusFilter || undefined,
    limit: PAGE_SIZE,
    offset,
  });

  const s = stats.data;
  const totalItems = insights.data?.total ?? 0;
  const items = insights.data?.data ?? [];
  const hasNext = offset + PAGE_SIZE < totalItems;
  const hasPrev = offset > 0;

  const total =
    s?.by_status
      ? Object.values(s.by_status).reduce((a, b) => a + b, 0)
      : 0;

  const statusFilters = [
    { value: "", label: "All" },
    { value: "pending", label: "Pending" },
    { value: "approved", label: "Approved" },
    { value: "applied", label: "Applied" },
    { value: "rejected", label: "Rejected" },
  ];

  return (
    <div className="space-y-6">
      {/* Summary Cards */}
      <div className="grid grid-cols-2 gap-4 md:grid-cols-4">
        <StatCard label="Total Insights" value={total} />
        <StatCard label="Pending" value={s?.by_status?.pending ?? 0} />
        <StatCard label="Approved" value={s?.by_status?.approved ?? 0} />
        <StatCard label="Applied" value={s?.by_status?.applied ?? 0} />
      </div>

      {/* Filters */}
      <div className="flex items-center gap-1">
        {statusFilters.map((f) => (
          <button
            key={f.value}
            onClick={() => {
              setStatusFilter(f.value);
              setOffset(0);
            }}
            className={`rounded-md px-3 py-1.5 text-xs font-medium transition-colors ${
              statusFilter === f.value
                ? "bg-primary text-primary-foreground"
                : "text-muted-foreground hover:bg-muted"
            }`}
          >
            {f.label}
          </button>
        ))}
      </div>

      {/* Insights List */}
      {insights.isLoading ? (
        <p className="text-sm text-muted-foreground">Loading...</p>
      ) : items.length === 0 ? (
        <div className="flex flex-col items-center justify-center py-12 text-muted-foreground">
          <Lightbulb className="h-12 w-12 mb-2 opacity-30" />
          <p className="text-sm font-medium">No insights yet</p>
          <div className="mt-3 max-w-md text-center space-y-2">
            <p className="text-xs">
              When you share knowledge about your data &mdash; corrections,
              business context, or quality observations &mdash; it gets captured
              here for review.
            </p>
            <p className="text-xs">
              Try telling your assistant something like{" "}
              <em>"the revenue column excludes returns"</em> or{" "}
              <em>"this table is refreshed weekly"</em>.
            </p>
            <p className="text-xs">
              Approved insights improve the data catalog for everyone.
            </p>
          </div>
        </div>
      ) : (
        <div className="space-y-3">
          {items.map((insight) => (
            <InsightCard key={insight.id} insight={insight} />
          ))}
        </div>
      )}

      {/* Pagination */}
      {totalItems > PAGE_SIZE && (
        <div className="flex items-center justify-between text-xs text-muted-foreground">
          <span>
            {offset + 1}&ndash;{Math.min(offset + PAGE_SIZE, totalItems)} of{" "}
            {totalItems}
          </span>
          <div className="flex gap-1">
            <button
              disabled={!hasPrev}
              onClick={() => setOffset(Math.max(0, offset - PAGE_SIZE))}
              className="rounded p-1 hover:bg-muted disabled:opacity-30"
            >
              <ChevronLeft className="h-4 w-4" />
            </button>
            <button
              disabled={!hasNext}
              onClick={() => setOffset(offset + PAGE_SIZE)}
              className="rounded p-1 hover:bg-muted disabled:opacity-30"
            >
              <ChevronRight className="h-4 w-4" />
            </button>
          </div>
        </div>
      )}
    </div>
  );
}
