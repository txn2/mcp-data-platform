import { useState, useMemo } from "react";
import { useInsights, useInsightStats, useAuditFilters } from "@/api/admin/hooks";
import { StatCard } from "@/components/cards/StatCard";
import { StatusBadge } from "@/components/cards/StatusBadge";
import type { Insight } from "@/api/admin/types";
import { formatUser } from "@/lib/formatUser";
import { InsightDrawer } from "./InsightDrawer";
import {
  PER_PAGE,
  INSIGHT_CATEGORIES,
  INSIGHT_CONFIDENCES,
  INSIGHT_STATUSES,
  ageBucketVariant,
  ageInDays,
  formatAge,
  formatCategory,
  insightStatusVariant,
} from "./helpers";

export function KnowledgeCaptureTab() {
  const [page, setPage] = useState(1);
  const [statusFilter, setStatusFilter] = useState("");
  const [categoryFilter, setCategoryFilter] = useState("");
  const [confidenceFilter, setConfidenceFilter] = useState("");
  const [order, setOrder] = useState<"newest" | "oldest">("newest");
  const [selectedInsight, setSelectedInsight] = useState<Insight | null>(null);
  const { data: filters } = useAuditFilters();
  const ul = filters?.user_labels ?? {};

  const params = useMemo(
    () => ({
      page,
      perPage: PER_PAGE,
      status: statusFilter || undefined,
      category: categoryFilter || undefined,
      confidence: confidenceFilter || undefined,
      order,
    }),
    [page, statusFilter, categoryFilter, confidenceFilter, order],
  );

  const { data, isLoading } = useInsights(params);
  const { data: stats } = useInsightStats();
  const totalPages = data ? Math.ceil(data.total / PER_PAGE) : 0;

  // Review-queue staleness: how old the oldest pending insight is, and how much
  // debt has crossed the 30-day threshold (#764).
  const oldestPendingDays = stats?.oldest_pending_at
    ? ageInDays(stats.oldest_pending_at)
    : null;
  const pendingOver30d = stats?.pending_over_30d ?? 0;
  const pendingDetail =
    oldestPendingDays !== null
      ? `Oldest ${formatAge(oldestPendingDays)}` +
        (pendingOver30d > 0 ? ` · ${pendingOver30d} over 30d` : "")
      : undefined;

  const topCategory = useMemo(() => {
    if (!stats?.by_category) return "-";
    const entries = Object.entries(stats.by_category);
    if (entries.length === 0) return "-";
    entries.sort((a, b) => b[1] - a[1]);
    return formatCategory(entries[0]![0]);
  }, [stats]);

  return (
    <>
      {/* Stats row */}
      <div className="grid grid-cols-2 gap-4 md:grid-cols-4">
        <StatCard
          label="Pending Review"
          value={stats?.total_pending ?? "-"}
          detail={pendingDetail}
          className={
            pendingOver30d > 0
              ? "border-red-300"
              : stats && stats.total_pending > 0
                ? "border-yellow-200"
                : undefined
          }
        />
        <StatCard
          label="Total Insights"
          value={
            stats?.by_status
              ? Object.values(stats.by_status)
                  .reduce((s, n) => s + n, 0)
                  .toLocaleString()
              : "-"
          }
        />
        <StatCard label="Top Category" value={topCategory} />
        <StatCard
          label="Applied"
          value={stats?.by_status?.["applied"] ?? "-"}
        />
      </div>

      {/* Filters */}
      <InsightFilters
        statusFilter={statusFilter}
        categoryFilter={categoryFilter}
        confidenceFilter={confidenceFilter}
        order={order}
        onStatusChange={(v) => {
          setStatusFilter(v);
          setPage(1);
        }}
        onCategoryChange={(v) => {
          setCategoryFilter(v);
          setPage(1);
        }}
        onConfidenceChange={(v) => {
          setConfidenceFilter(v);
          setPage(1);
        }}
        onOrderChange={(v) => {
          setOrder(v);
          setPage(1);
        }}
      />

      {/* Table */}
      <div className="overflow-auto rounded-lg border bg-card">
        <table className="w-full text-sm">
          <thead>
            <tr className="border-b bg-muted/50">
              <th className="px-3 py-2 text-left font-medium">Created At</th>
              <th className="px-3 py-2 text-center font-medium">Age</th>
              <th className="px-3 py-2 text-left font-medium">Captured By</th>
              <th className="px-3 py-2 text-left font-medium">Category</th>
              <th className="px-3 py-2 text-center font-medium">Confidence</th>
              <th className="px-3 py-2 text-left font-medium">Insight</th>
              <th className="px-3 py-2 text-center font-medium">Status</th>
            </tr>
          </thead>
          <tbody>
            {isLoading && (
              <tr>
                <td
                  colSpan={7}
                  className="px-3 py-8 text-center text-muted-foreground"
                >
                  Loading...
                </td>
              </tr>
            )}
            {data?.data.map((insight) => (
              <tr
                key={insight.id}
                onClick={() => setSelectedInsight(insight)}
                className="cursor-pointer border-b transition-colors hover:bg-muted/50"
              >
                <td className="px-3 py-2 text-xs">
                  {new Date(insight.created_at).toLocaleString()}
                </td>
                <td className="px-3 py-2 text-center">
                  {insight.status === "pending" ? (
                    <StatusBadge
                      variant={ageBucketVariant(ageInDays(insight.created_at))}
                    >
                      {formatAge(ageInDays(insight.created_at))}
                    </StatusBadge>
                  ) : (
                    <span className="text-xs text-muted-foreground">
                      {formatAge(ageInDays(insight.created_at))}
                    </span>
                  )}
                </td>
                <td
                  className="px-3 py-2 text-xs"
                  title={insight.captured_by}
                >
                  {formatUser(insight.captured_by, ul[insight.captured_by])}
                </td>
                <td className="px-3 py-2 text-xs">
                  {formatCategory(insight.category)}
                </td>
                <td className="px-3 py-2 text-center">
                  <StatusBadge
                    variant={
                      insight.confidence === "high"
                        ? "success"
                        : insight.confidence === "medium"
                          ? "warning"
                          : "neutral"
                    }
                  >
                    {insight.confidence}
                  </StatusBadge>
                </td>
                <td className="max-w-xs truncate px-3 py-2 text-xs">
                  {insight.insight_text}
                </td>
                <td className="px-3 py-2 text-center">
                  <StatusBadge variant={insightStatusVariant(insight.status)}>
                    {formatCategory(insight.status)}
                  </StatusBadge>
                </td>
              </tr>
            ))}
            {data?.data.length === 0 && (
              <tr>
                <td
                  colSpan={7}
                  className="px-3 py-8 text-center text-muted-foreground"
                >
                  No insights found
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>

      {/* Pagination */}
      {totalPages > 1 && (
        <div className="flex items-center justify-between text-sm">
          <span className="text-muted-foreground">
            Showing {(page - 1) * PER_PAGE + 1}-
            {Math.min(page * PER_PAGE, data?.total ?? 0)} of{" "}
            {data?.total ?? 0}
          </span>
          <div className="flex gap-2">
            <button
              onClick={() => setPage((p) => Math.max(1, p - 1))}
              disabled={page <= 1}
              className="rounded-md border px-3 py-1 text-xs disabled:opacity-50"
            >
              Previous
            </button>
            <button
              onClick={() => setPage((p) => Math.min(totalPages, p + 1))}
              disabled={page >= totalPages}
              className="rounded-md border px-3 py-1 text-xs disabled:opacity-50"
            >
              Next
            </button>
          </div>
        </div>
      )}

      {/* Detail Drawer */}
      {selectedInsight && (
        <InsightDrawer
          insight={selectedInsight}
          onClose={() => setSelectedInsight(null)}
          userLabels={ul}
        />
      )}
    </>
  );
}

function InsightFilters({
  statusFilter,
  categoryFilter,
  confidenceFilter,
  order,
  onStatusChange,
  onCategoryChange,
  onConfidenceChange,
  onOrderChange,
}: {
  statusFilter: string;
  categoryFilter: string;
  confidenceFilter: string;
  order: "newest" | "oldest";
  onStatusChange: (v: string) => void;
  onCategoryChange: (v: string) => void;
  onConfidenceChange: (v: string) => void;
  onOrderChange: (v: "newest" | "oldest") => void;
}) {
  return (
    <div className="flex flex-wrap items-center gap-3">
      <select
        value={statusFilter}
        onChange={(e) => onStatusChange(e.target.value)}
        className="rounded-md border bg-background px-3 py-1.5 text-sm outline-none ring-ring focus:ring-2"
      >
        <option value="">All Statuses</option>
        {INSIGHT_STATUSES.map((s) => (
          <option key={s} value={s}>
            {formatCategory(s)}
          </option>
        ))}
      </select>
      <select
        value={categoryFilter}
        onChange={(e) => onCategoryChange(e.target.value)}
        className="rounded-md border bg-background px-3 py-1.5 text-sm outline-none ring-ring focus:ring-2"
      >
        <option value="">All Categories</option>
        {INSIGHT_CATEGORIES.map((c) => (
          <option key={c} value={c}>
            {formatCategory(c)}
          </option>
        ))}
      </select>
      <select
        value={confidenceFilter}
        onChange={(e) => onConfidenceChange(e.target.value)}
        className="rounded-md border bg-background px-3 py-1.5 text-sm outline-none ring-ring focus:ring-2"
      >
        <option value="">All Confidence</option>
        {INSIGHT_CONFIDENCES.map((c) => (
          <option key={c} value={c}>
            {c.charAt(0).toUpperCase() + c.slice(1)}
          </option>
        ))}
      </select>
      <select
        value={order}
        onChange={(e) => onOrderChange(e.target.value as "newest" | "oldest")}
        className="rounded-md border bg-background px-3 py-1.5 text-sm outline-none ring-ring focus:ring-2"
        aria-label="Sort by age"
      >
        <option value="newest">Newest First</option>
        <option value="oldest">Oldest First</option>
      </select>
    </div>
  );
}
