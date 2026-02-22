import { useState, useMemo, useCallback } from "react";
import {
  useInsights,
  useInsightStats,
  useUpdateInsightStatus,
  useChangesets,
  useRollbackChangeset,
  useAuditFilters,
} from "@/api/hooks";
import { StatCard } from "@/components/cards/StatCard";
import { StatusBadge } from "@/components/cards/StatusBadge";
import type { Insight, Changeset } from "@/api/types";
import { formatUser } from "@/lib/formatUser";
import {
  PieChart,
  Pie,
  Cell,
  BarChart as RechartsBarChart,
  Bar,
  XAxis,
  YAxis,
  Tooltip,
  ResponsiveContainer,
} from "recharts";

const PER_PAGE = 20;

type Tab = "stats" | "insights" | "changesets" | "help";

const INSIGHT_CATEGORIES = [
  "correction",
  "business_context",
  "data_quality",
  "usage_guidance",
  "relationship",
  "enhancement",
];

const INSIGHT_CONFIDENCES = ["high", "medium", "low"];

const INSIGHT_STATUSES = [
  "pending",
  "approved",
  "rejected",
  "applied",
  "superseded",
  "rolled_back",
];

type BadgeVariant = "success" | "error" | "warning" | "neutral";

function insightStatusVariant(status: string): BadgeVariant {
  switch (status) {
    case "pending":
      return "warning";
    case "approved":
    case "applied":
      return "success";
    case "rejected":
    case "rolled_back":
      return "error";
    case "superseded":
      return "neutral";
    default:
      return "neutral";
  }
}

function formatCategory(cat: string): string {
  return cat.replace(/_/g, " ").replace(/\b\w/g, (c) => c.toUpperCase());
}

const TAB_ITEMS: { key: Tab; label: string }[] = [
  { key: "stats", label: "Overview" },
  { key: "insights", label: "Insights" },
  { key: "changesets", label: "Changesets" },
  { key: "help", label: "Help" },
];

export function KnowledgePage({ initialTab }: { initialTab?: string }) {
  const [tab, setTab] = useState<Tab>(
    (["stats", "insights", "changesets", "help"].includes(initialTab ?? "")
      ? initialTab
      : "stats") as Tab,
  );

  return (
    <div className="space-y-4">
      {/* Tab bar */}
      <div className="flex gap-1 border-b">
        {TAB_ITEMS.map((t) => (
          <button
            key={t.key}
            onClick={() => setTab(t.key)}
            className={`px-4 py-2 text-sm font-medium transition-colors ${
              tab === t.key
                ? "border-b-2 border-primary text-primary"
                : "text-muted-foreground hover:text-foreground"
            }`}
          >
            {t.label}
          </button>
        ))}
      </div>

      {tab === "stats" && <StatsTab />}
      {tab === "insights" && <InsightsTab />}
      {tab === "changesets" && <ChangesetsTab />}
      {tab === "help" && <KnowledgeHelpTab />}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Stats Tab — Knowledge Dashboard
// ---------------------------------------------------------------------------

const STATUS_COLORS: Record<string, string> = {
  pending: "hsl(45, 93%, 47%)",
  approved: "hsl(142, 76%, 36%)",
  rejected: "hsl(0, 84%, 60%)",
  applied: "hsl(142, 76%, 46%)",
  superseded: "hsl(220, 9%, 46%)",
  rolled_back: "hsl(0, 72%, 51%)",
};

const CATEGORY_COLORS = [
  "hsl(221, 83%, 53%)",
  "hsl(262, 83%, 58%)",
  "hsl(330, 81%, 60%)",
  "hsl(24, 94%, 50%)",
  "hsl(142, 76%, 36%)",
  "hsl(45, 93%, 47%)",
];

const CONFIDENCE_COLORS: Record<string, string> = {
  high: "hsl(142, 76%, 36%)",
  medium: "hsl(45, 93%, 47%)",
  low: "hsl(220, 9%, 46%)",
};

function StatsTab() {
  const { data: stats } = useInsightStats();
  const { data: pendingData } = useInsights({ perPage: 5, status: "pending" });
  const { data: changesetData } = useChangesets({ perPage: 5 });

  const totalInsights = useMemo(() => {
    if (!stats?.by_status) return 0;
    return Object.values(stats.by_status).reduce((s, n) => s + n, 0);
  }, [stats]);

  const statusChartData = useMemo(() => {
    if (!stats?.by_status) return [];
    return Object.entries(stats.by_status).map(([name, value]) => ({
      name: formatCategory(name),
      value,
      key: name,
    }));
  }, [stats]);

  const categoryChartData = useMemo(() => {
    if (!stats?.by_category) return [];
    return Object.entries(stats.by_category)
      .map(([name, value]) => ({ name: formatCategory(name), value }))
      .sort((a, b) => b.value - a.value);
  }, [stats]);

  const confidenceChartData = useMemo(() => {
    if (!stats?.by_confidence) return [];
    return Object.entries(stats.by_confidence).map(([name, value]) => ({
      name: name.charAt(0).toUpperCase() + name.slice(1),
      value,
      key: name,
    }));
  }, [stats]);

  const topEntities = useMemo(() => {
    if (!stats?.by_entity) return [];
    return stats.by_entity.slice(0, 5);
  }, [stats]);

  // Compute approval rate
  const approvalRate = useMemo(() => {
    if (!stats?.by_status) return null;
    const approved = (stats.by_status["approved"] ?? 0) + (stats.by_status["applied"] ?? 0);
    const reviewed = approved + (stats.by_status["rejected"] ?? 0);
    if (reviewed === 0) return null;
    return ((approved / reviewed) * 100).toFixed(0);
  }, [stats]);

  return (
    <div className="space-y-6">
      {/* Summary cards */}
      <div className="grid grid-cols-2 gap-4 md:grid-cols-3 lg:grid-cols-6">
        <StatCard label="Total Insights" value={totalInsights} />
        <StatCard
          label="Pending Review"
          value={stats?.total_pending ?? "-"}
          className={stats && stats.total_pending > 0 ? "border-yellow-200" : undefined}
        />
        <StatCard
          label="Approved"
          value={stats?.by_status?.["approved"] ?? "-"}
        />
        <StatCard
          label="Applied"
          value={stats?.by_status?.["applied"] ?? "-"}
        />
        <StatCard
          label="Rejected"
          value={stats?.by_status?.["rejected"] ?? "-"}
        />
        <StatCard
          label="Approval Rate"
          value={approvalRate ? `${approvalRate}%` : "-"}
        />
      </div>

      {/* Charts row 1: Status pipeline + Confidence */}
      <div className="grid gap-6 lg:grid-cols-2">
        {/* Status Distribution */}
        <div className="rounded-lg border bg-card p-4">
          <h2 className="mb-3 text-sm font-medium">Status Distribution</h2>
          {statusChartData.length > 0 ? (
            <div className="flex items-center gap-4">
              <ResponsiveContainer width="50%" height={200}>
                <PieChart>
                  <Pie
                    data={statusChartData}
                    cx="50%"
                    cy="50%"
                    innerRadius={50}
                    outerRadius={80}
                    dataKey="value"
                    nameKey="name"
                  >
                    {statusChartData.map((entry) => (
                      <Cell
                        key={entry.key}
                        fill={STATUS_COLORS[entry.key] ?? "hsl(220, 9%, 46%)"}
                      />
                    ))}
                  </Pie>
                  <Tooltip
                    contentStyle={{
                      backgroundColor: "hsl(var(--card))",
                      border: "1px solid hsl(var(--border))",
                      borderRadius: "0.375rem",
                      fontSize: "0.75rem",
                    }}
                  />
                </PieChart>
              </ResponsiveContainer>
              <div className="space-y-2">
                {statusChartData.map((entry) => (
                  <div key={entry.key} className="flex items-center gap-2 text-xs">
                    <span
                      className="inline-block h-3 w-3 rounded-full"
                      style={{ backgroundColor: STATUS_COLORS[entry.key] ?? "hsl(220, 9%, 46%)" }}
                    />
                    <span className="text-muted-foreground">{entry.name}</span>
                    <span className="font-medium">{entry.value}</span>
                  </div>
                ))}
              </div>
            </div>
          ) : (
            <div className="flex h-[200px] items-center justify-center text-sm text-muted-foreground">
              No data
            </div>
          )}
        </div>

        {/* Confidence Breakdown */}
        <div className="rounded-lg border bg-card p-4">
          <h2 className="mb-3 text-sm font-medium">Confidence Levels</h2>
          {confidenceChartData.length > 0 ? (
            <div className="flex items-center gap-4">
              <ResponsiveContainer width="50%" height={200}>
                <PieChart>
                  <Pie
                    data={confidenceChartData}
                    cx="50%"
                    cy="50%"
                    innerRadius={50}
                    outerRadius={80}
                    dataKey="value"
                    nameKey="name"
                  >
                    {confidenceChartData.map((entry) => (
                      <Cell
                        key={entry.key}
                        fill={CONFIDENCE_COLORS[entry.key] ?? "hsl(220, 9%, 46%)"}
                      />
                    ))}
                  </Pie>
                  <Tooltip
                    contentStyle={{
                      backgroundColor: "hsl(var(--card))",
                      border: "1px solid hsl(var(--border))",
                      borderRadius: "0.375rem",
                      fontSize: "0.75rem",
                    }}
                  />
                </PieChart>
              </ResponsiveContainer>
              <div className="space-y-2">
                {confidenceChartData.map((entry) => (
                  <div key={entry.key} className="flex items-center gap-2 text-xs">
                    <span
                      className="inline-block h-3 w-3 rounded-full"
                      style={{ backgroundColor: CONFIDENCE_COLORS[entry.key] ?? "hsl(220, 9%, 46%)" }}
                    />
                    <span className="text-muted-foreground">{entry.name}</span>
                    <span className="font-medium">{entry.value}</span>
                  </div>
                ))}
              </div>
            </div>
          ) : (
            <div className="flex h-[200px] items-center justify-center text-sm text-muted-foreground">
              No data
            </div>
          )}
        </div>
      </div>

      {/* Charts row 2: Category breakdown */}
      <div className="rounded-lg border bg-card p-4">
        <h2 className="mb-3 text-sm font-medium">Insights by Category</h2>
        {categoryChartData.length > 0 ? (
          <ResponsiveContainer width="100%" height={250}>
            <RechartsBarChart data={categoryChartData} layout="vertical" margin={{ left: 100 }}>
              <XAxis
                type="number"
                className="text-xs"
                tick={{ fill: "hsl(var(--muted-foreground))" }}
              />
              <YAxis
                type="category"
                dataKey="name"
                className="text-xs"
                tick={{ fill: "hsl(var(--muted-foreground))" }}
                width={100}
              />
              <Tooltip
                contentStyle={{
                  backgroundColor: "hsl(var(--card))",
                  border: "1px solid hsl(var(--border))",
                  borderRadius: "0.375rem",
                  fontSize: "0.75rem",
                }}
                formatter={(value: number) => [value, "Insights"]}
              />
              <Bar dataKey="value" radius={[0, 4, 4, 0]}>
                {categoryChartData.map((_, idx) => (
                  <Cell key={idx} fill={CATEGORY_COLORS[idx % CATEGORY_COLORS.length]} />
                ))}
              </Bar>
            </RechartsBarChart>
          </ResponsiveContainer>
        ) : (
          <div className="flex h-[250px] items-center justify-center text-sm text-muted-foreground">
            No data
          </div>
        )}
      </div>

      {/* Bottom row: Top entities + Recent pending */}
      <div className="grid gap-6 lg:grid-cols-2">
        {/* Top Entities */}
        <div className="rounded-lg border bg-card p-4">
          <h2 className="mb-3 text-sm font-medium">Top Entities by Insights</h2>
          {topEntities.length > 0 ? (
            <div className="space-y-3">
              {topEntities.map((entity) => {
                const pct = totalInsights > 0 ? (entity.count / totalInsights) * 100 : 0;
                // Extract just the table path from the URN for display
                const match = entity.entity_urn.match(/trino,([^,]+),/);
                const tablePath = match?.[1] ?? entity.entity_urn;
                return (
                  <div key={entity.entity_urn}>
                    <div className="mb-1 flex items-center justify-between text-xs">
                      <span className="font-mono text-muted-foreground" title={entity.entity_urn}>
                        {tablePath}
                      </span>
                      <span className="font-medium">{entity.count}</span>
                    </div>
                    <div className="h-2 overflow-hidden rounded-full bg-muted">
                      <div
                        className="h-full rounded-full bg-primary transition-all"
                        style={{ width: `${pct}%` }}
                      />
                    </div>
                    <div className="mt-1 flex gap-1">
                      {entity.categories.map((cat) => (
                        <span
                          key={cat}
                          className="rounded bg-muted px-1.5 py-0.5 text-[10px] text-muted-foreground"
                        >
                          {formatCategory(cat)}
                        </span>
                      ))}
                    </div>
                  </div>
                );
              })}
            </div>
          ) : (
            <p className="text-sm text-muted-foreground">No entity data</p>
          )}
        </div>

        {/* Recent Pending */}
        <div className="rounded-lg border bg-card p-4">
          <h2 className="mb-3 text-sm font-medium">Recent Pending Insights</h2>
          {pendingData?.data && pendingData.data.length > 0 ? (
            <div className="space-y-3">
              {pendingData.data.map((insight) => (
                <div key={insight.id} className="flex items-start gap-2 text-xs">
                  <StatusBadge variant="warning">Pending</StatusBadge>
                  <div className="min-w-0 flex-1">
                    <p className="font-medium">{formatCategory(insight.category)}</p>
                    <p className="truncate text-muted-foreground">{insight.insight_text}</p>
                  </div>
                  <span className="shrink-0 text-muted-foreground">
                    {new Date(insight.created_at).toLocaleDateString()}
                  </span>
                </div>
              ))}
            </div>
          ) : (
            <p className="text-sm text-muted-foreground">No pending insights</p>
          )}
        </div>
      </div>

      {/* Recent Changesets */}
      {changesetData?.data && changesetData.data.length > 0 && (
        <div className="rounded-lg border bg-card p-4">
          <h2 className="mb-3 text-sm font-medium">Recent Changesets</h2>
          <div className="space-y-2">
            {changesetData.data.map((cs) => {
              const match = cs.target_urn.match(/trino,([^,]+),/);
              const tablePath = match?.[1] ?? cs.target_urn;
              return (
                <div key={cs.id} className="flex items-center gap-3 text-xs">
                  <StatusBadge variant={cs.rolled_back ? "error" : "success"}>
                    {cs.rolled_back ? "Rolled Back" : "Active"}
                  </StatusBadge>
                  <span className="font-medium">{formatCategory(cs.change_type)}</span>
                  <span className="font-mono text-muted-foreground" title={cs.target_urn}>
                    {tablePath}
                  </span>
                  <span className="ml-auto shrink-0 text-muted-foreground">
                    {new Date(cs.created_at).toLocaleDateString()}
                  </span>
                </div>
              );
            })}
          </div>
        </div>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Insights Tab
// ---------------------------------------------------------------------------

function InsightsTab() {
  const [page, setPage] = useState(1);
  const [statusFilter, setStatusFilter] = useState("");
  const [categoryFilter, setCategoryFilter] = useState("");
  const [confidenceFilter, setConfidenceFilter] = useState("");
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
    }),
    [page, statusFilter, categoryFilter, confidenceFilter],
  );

  const { data, isLoading } = useInsights(params);
  const { data: stats } = useInsightStats();
  const totalPages = data ? Math.ceil(data.total / PER_PAGE) : 0;

  // Find top category
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
          className={
            stats && stats.total_pending > 0 ? "border-yellow-200" : undefined
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
      <div className="flex flex-wrap items-center gap-3">
        <select
          value={statusFilter}
          onChange={(e) => {
            setStatusFilter(e.target.value);
            setPage(1);
          }}
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
          onChange={(e) => {
            setCategoryFilter(e.target.value);
            setPage(1);
          }}
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
          onChange={(e) => {
            setConfidenceFilter(e.target.value);
            setPage(1);
          }}
          className="rounded-md border bg-background px-3 py-1.5 text-sm outline-none ring-ring focus:ring-2"
        >
          <option value="">All Confidence</option>
          {INSIGHT_CONFIDENCES.map((c) => (
            <option key={c} value={c}>
              {c.charAt(0).toUpperCase() + c.slice(1)}
            </option>
          ))}
        </select>
      </div>

      {/* Table */}
      <div className="overflow-auto rounded-lg border bg-card">
        <table className="w-full text-sm">
          <thead>
            <tr className="border-b bg-muted/50">
              <th className="px-3 py-2 text-left font-medium">Created At</th>
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
                  colSpan={6}
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
                <td className="px-3 py-2 text-xs" title={insight.captured_by}>{formatUser(insight.captured_by, ul[insight.captured_by])}</td>
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
                  colSpan={6}
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
            Showing {(page - 1) * PER_PAGE + 1}--
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

// ---------------------------------------------------------------------------
// Insight Detail Drawer
// ---------------------------------------------------------------------------

function InsightDrawer({
  insight,
  onClose,
  userLabels,
}: {
  insight: Insight;
  onClose: () => void;
  userLabels: Record<string, string>;
}) {
  const [reviewNotes, setReviewNotes] = useState(insight.review_notes ?? "");
  const updateStatus = useUpdateInsightStatus();

  const handleAction = useCallback(
    (status: string) => {
      updateStatus.mutate(
        { id: insight.id, status, reviewNotes: reviewNotes || undefined },
        { onSuccess: () => onClose() },
      );
    },
    [insight.id, reviewNotes, updateStatus, onClose],
  );

  return (
    <div className="fixed inset-0 z-50 flex justify-end">
      <div className="absolute inset-0 bg-black/50" onClick={onClose} />
      <div className="relative w-full max-w-lg overflow-auto bg-card p-6 shadow-xl">
        <div className="mb-4 flex items-center justify-between">
          <h2 className="text-lg font-semibold">Insight Detail</h2>
          <button
            onClick={onClose}
            className="rounded-md px-2 py-1 text-sm hover:bg-muted"
          >
            Close
          </button>
        </div>

        <div className="space-y-4">
          {/* Metadata grid */}
          <div className="grid grid-cols-2 gap-3 text-sm">
            <div>
              <p className="text-xs text-muted-foreground">ID</p>
              <p className="font-mono text-xs">{insight.id}</p>
            </div>
            <div>
              <p className="text-xs text-muted-foreground">Created At</p>
              <p>{new Date(insight.created_at).toLocaleString()}</p>
            </div>
            <div>
              <p className="text-xs text-muted-foreground">Captured By</p>
              <p title={insight.captured_by}>{formatUser(insight.captured_by, userLabels[insight.captured_by])}</p>
            </div>
            <div>
              <p className="text-xs text-muted-foreground">Persona</p>
              <p>{insight.persona}</p>
            </div>
            <div>
              <p className="text-xs text-muted-foreground">Category</p>
              <p>{formatCategory(insight.category)}</p>
            </div>
            <div>
              <p className="text-xs text-muted-foreground">Confidence</p>
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
            </div>
            <div>
              <p className="text-xs text-muted-foreground">Status</p>
              <StatusBadge variant={insightStatusVariant(insight.status)}>
                {formatCategory(insight.status)}
              </StatusBadge>
            </div>
            <div>
              <p className="text-xs text-muted-foreground">Session ID</p>
              <p className="font-mono text-xs">{insight.session_id}</p>
            </div>
          </div>

          {/* Full insight text */}
          <div>
            <p className="mb-1 text-xs text-muted-foreground">Insight</p>
            <p className="rounded bg-muted p-3 text-sm">{insight.insight_text}</p>
          </div>

          {/* Entity URNs */}
          {insight.entity_urns.length > 0 && (
            <div>
              <p className="mb-1 text-xs text-muted-foreground">Entity URNs</p>
              <div className="space-y-1">
                {insight.entity_urns.map((urn, i) => (
                  <p key={i} className="font-mono text-xs text-muted-foreground">
                    {urn}
                  </p>
                ))}
              </div>
            </div>
          )}

          {/* Suggested Actions */}
          {insight.suggested_actions.length > 0 && (
            <div>
              <p className="mb-1 text-xs text-muted-foreground">
                Suggested Actions
              </p>
              <div className="overflow-auto rounded border">
                <table className="w-full text-xs">
                  <thead>
                    <tr className="border-b bg-muted/50">
                      <th className="px-2 py-1 text-left font-medium">Type</th>
                      <th className="px-2 py-1 text-left font-medium">
                        Target
                      </th>
                      <th className="px-2 py-1 text-left font-medium">
                        Detail
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {insight.suggested_actions.map((a, i) => (
                      <tr key={i} className="border-b">
                        <td className="px-2 py-1 font-mono">{a.action_type}</td>
                        <td className="max-w-[120px] truncate px-2 py-1 font-mono">
                          {a.target}
                        </td>
                        <td className="px-2 py-1">{a.detail}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </div>
          )}

          {/* Related Columns */}
          {insight.related_columns.length > 0 && (
            <div>
              <p className="mb-1 text-xs text-muted-foreground">
                Related Columns
              </p>
              <div className="overflow-auto rounded border">
                <table className="w-full text-xs">
                  <thead>
                    <tr className="border-b bg-muted/50">
                      <th className="px-2 py-1 text-left font-medium">URN</th>
                      <th className="px-2 py-1 text-left font-medium">
                        Column
                      </th>
                      <th className="px-2 py-1 text-left font-medium">
                        Relevance
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {insight.related_columns.map((c, i) => (
                      <tr key={i} className="border-b">
                        <td className="max-w-[120px] truncate px-2 py-1 font-mono">
                          {c.urn}
                        </td>
                        <td className="px-2 py-1 font-mono">{c.column}</td>
                        <td className="px-2 py-1">{c.relevance}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </div>
          )}

          {/* Lifecycle section */}
          {insight.reviewed_by && (
            <div className="grid grid-cols-2 gap-3 border-t pt-3 text-sm">
              <div>
                <p className="text-xs text-muted-foreground">Reviewed By</p>
                <p title={insight.reviewed_by}>{formatUser(insight.reviewed_by!, userLabels[insight.reviewed_by!])}</p>
              </div>
              <div>
                <p className="text-xs text-muted-foreground">Reviewed At</p>
                <p>
                  {insight.reviewed_at
                    ? new Date(insight.reviewed_at).toLocaleString()
                    : "-"}
                </p>
              </div>
            </div>
          )}

          {insight.applied_by && (
            <div className="grid grid-cols-2 gap-3 border-t pt-3 text-sm">
              <div>
                <p className="text-xs text-muted-foreground">Applied By</p>
                <p title={insight.applied_by}>{formatUser(insight.applied_by!, userLabels[insight.applied_by!])}</p>
              </div>
              <div>
                <p className="text-xs text-muted-foreground">Applied At</p>
                <p>
                  {insight.applied_at
                    ? new Date(insight.applied_at).toLocaleString()
                    : "-"}
                </p>
              </div>
              {insight.changeset_ref && (
                <div>
                  <p className="text-xs text-muted-foreground">Changeset Ref</p>
                  <p className="font-mono text-xs">{insight.changeset_ref}</p>
                </div>
              )}
            </div>
          )}

          {/* Action buttons */}
          <div className="space-y-3 border-t pt-3">
            <div>
              <label className="mb-1 block text-xs text-muted-foreground">
                Review Notes
              </label>
              <textarea
                value={reviewNotes}
                onChange={(e) => setReviewNotes(e.target.value)}
                placeholder="Optional review notes..."
                rows={3}
                className="w-full rounded-md border bg-background px-3 py-2 text-sm outline-none ring-ring focus:ring-2"
              />
            </div>
            <div className="flex gap-2">
              <button
                onClick={() => handleAction("approved")}
                disabled={updateStatus.isPending}
                className="rounded-md bg-green-600 px-4 py-2 text-sm font-medium text-white hover:bg-green-700 disabled:opacity-50"
              >
                Approve
              </button>
              <button
                onClick={() => handleAction("rejected")}
                disabled={updateStatus.isPending}
                className="rounded-md bg-red-600 px-4 py-2 text-sm font-medium text-white hover:bg-red-700 disabled:opacity-50"
              >
                Reject
              </button>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Changesets Tab
// ---------------------------------------------------------------------------

function ChangesetsTab() {
  const [page, setPage] = useState(1);
  const [entityUrnFilter, setEntityUrnFilter] = useState("");
  const [rolledBackFilter, setRolledBackFilter] = useState("");
  const [selectedChangeset, setSelectedChangeset] = useState<Changeset | null>(
    null,
  );
  const { data: filters } = useAuditFilters();
  const ul = filters?.user_labels ?? {};

  const params = useMemo(
    () => ({
      page,
      perPage: PER_PAGE,
      entityUrn: entityUrnFilter || undefined,
      rolledBack: rolledBackFilter || undefined,
    }),
    [page, entityUrnFilter, rolledBackFilter],
  );

  const { data, isLoading } = useChangesets(params);
  const totalPages = data ? Math.ceil(data.total / PER_PAGE) : 0;

  return (
    <>
      {/* Filters */}
      <div className="flex flex-wrap items-center gap-3">
        <input
          type="text"
          value={entityUrnFilter}
          onChange={(e) => {
            setEntityUrnFilter(e.target.value);
            setPage(1);
          }}
          placeholder="Filter by Entity URN..."
          className="w-64 rounded-md border bg-background px-3 py-1.5 text-sm outline-none ring-ring focus:ring-2"
        />
        <select
          value={rolledBackFilter}
          onChange={(e) => {
            setRolledBackFilter(e.target.value);
            setPage(1);
          }}
          className="rounded-md border bg-background px-3 py-1.5 text-sm outline-none ring-ring focus:ring-2"
        >
          <option value="">All</option>
          <option value="false">Active</option>
          <option value="true">Rolled Back</option>
        </select>
      </div>

      {/* Table */}
      <div className="overflow-auto rounded-lg border bg-card">
        <table className="w-full text-sm">
          <thead>
            <tr className="border-b bg-muted/50">
              <th className="px-3 py-2 text-left font-medium">Created At</th>
              <th className="px-3 py-2 text-left font-medium">Target URN</th>
              <th className="px-3 py-2 text-left font-medium">Change Type</th>
              <th className="px-3 py-2 text-left font-medium">Applied By</th>
              <th className="px-3 py-2 text-center font-medium">Status</th>
            </tr>
          </thead>
          <tbody>
            {isLoading && (
              <tr>
                <td
                  colSpan={5}
                  className="px-3 py-8 text-center text-muted-foreground"
                >
                  Loading...
                </td>
              </tr>
            )}
            {data?.data.map((changeset) => (
              <tr
                key={changeset.id}
                onClick={() => setSelectedChangeset(changeset)}
                className="cursor-pointer border-b transition-colors hover:bg-muted/50"
              >
                <td className="px-3 py-2 text-xs">
                  {new Date(changeset.created_at).toLocaleString()}
                </td>
                <td className="max-w-xs truncate px-3 py-2 font-mono text-xs">
                  {changeset.target_urn}
                </td>
                <td className="px-3 py-2 text-xs">
                  {formatCategory(changeset.change_type)}
                </td>
                <td className="px-3 py-2 text-xs" title={changeset.applied_by}>{formatUser(changeset.applied_by, ul[changeset.applied_by])}</td>
                <td className="px-3 py-2 text-center">
                  <StatusBadge
                    variant={changeset.rolled_back ? "error" : "success"}
                  >
                    {changeset.rolled_back ? "Rolled Back" : "Active"}
                  </StatusBadge>
                </td>
              </tr>
            ))}
            {data?.data.length === 0 && (
              <tr>
                <td
                  colSpan={5}
                  className="px-3 py-8 text-center text-muted-foreground"
                >
                  No changesets found
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
            Showing {(page - 1) * PER_PAGE + 1}--
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
      {selectedChangeset && (
        <ChangesetDrawer
          changeset={selectedChangeset}
          onClose={() => setSelectedChangeset(null)}
          userLabels={ul}
        />
      )}
    </>
  );
}

// ---------------------------------------------------------------------------
// Changeset Detail Drawer
// ---------------------------------------------------------------------------

function ChangesetDrawer({
  changeset,
  onClose,
  userLabels,
}: {
  changeset: Changeset;
  onClose: () => void;
  userLabels: Record<string, string>;
}) {
  const rollback = useRollbackChangeset();

  const handleRollback = useCallback(() => {
    if (!window.confirm("Are you sure you want to rollback this changeset?"))
      return;
    rollback.mutate(changeset.id, { onSuccess: () => onClose() });
  }, [changeset.id, rollback, onClose]);

  return (
    <div className="fixed inset-0 z-50 flex justify-end">
      <div className="absolute inset-0 bg-black/50" onClick={onClose} />
      <div className="relative w-full max-w-lg overflow-auto bg-card p-6 shadow-xl">
        <div className="mb-4 flex items-center justify-between">
          <h2 className="text-lg font-semibold">Changeset Detail</h2>
          <button
            onClick={onClose}
            className="rounded-md px-2 py-1 text-sm hover:bg-muted"
          >
            Close
          </button>
        </div>

        <div className="space-y-4">
          {/* Metadata grid */}
          <div className="grid grid-cols-2 gap-3 text-sm">
            <div>
              <p className="text-xs text-muted-foreground">ID</p>
              <p className="font-mono text-xs">{changeset.id}</p>
            </div>
            <div>
              <p className="text-xs text-muted-foreground">Created At</p>
              <p>{new Date(changeset.created_at).toLocaleString()}</p>
            </div>
            <div className="col-span-2">
              <p className="text-xs text-muted-foreground">Target URN</p>
              <p className="font-mono text-xs">{changeset.target_urn}</p>
            </div>
            <div>
              <p className="text-xs text-muted-foreground">Change Type</p>
              <p>{formatCategory(changeset.change_type)}</p>
            </div>
            <div>
              <p className="text-xs text-muted-foreground">Status</p>
              <StatusBadge
                variant={changeset.rolled_back ? "error" : "success"}
              >
                {changeset.rolled_back ? "Rolled Back" : "Active"}
              </StatusBadge>
            </div>
            <div>
              <p className="text-xs text-muted-foreground">Approved By</p>
              <p title={changeset.approved_by}>{formatUser(changeset.approved_by, userLabels[changeset.approved_by])}</p>
            </div>
            <div>
              <p className="text-xs text-muted-foreground">Applied By</p>
              <p title={changeset.applied_by}>{formatUser(changeset.applied_by, userLabels[changeset.applied_by])}</p>
            </div>
          </div>

          {/* Previous Value */}
          <div>
            <p className="mb-1 text-xs text-muted-foreground">
              Previous Value
            </p>
            <pre className="overflow-auto rounded bg-muted p-3 text-xs">
              {JSON.stringify(changeset.previous_value, null, 2)}
            </pre>
          </div>

          {/* New Value */}
          <div>
            <p className="mb-1 text-xs text-muted-foreground">New Value</p>
            <pre className="overflow-auto rounded bg-muted p-3 text-xs">
              {JSON.stringify(changeset.new_value, null, 2)}
            </pre>
          </div>

          {/* Source Insight IDs */}
          {changeset.source_insight_ids.length > 0 && (
            <div>
              <p className="mb-1 text-xs text-muted-foreground">
                Source Insight IDs
              </p>
              <div className="space-y-1">
                {changeset.source_insight_ids.map((id, i) => (
                  <p key={i} className="font-mono text-xs text-muted-foreground">
                    {id}
                  </p>
                ))}
              </div>
            </div>
          )}

          {/* Rollback info */}
          {changeset.rolled_back && (
            <div className="grid grid-cols-2 gap-3 border-t pt-3 text-sm">
              <div>
                <p className="text-xs text-muted-foreground">Rolled Back By</p>
                <p title={changeset.rolled_back_by}>{formatUser(changeset.rolled_back_by ?? "", userLabels[changeset.rolled_back_by ?? ""])}</p>
              </div>
              <div>
                <p className="text-xs text-muted-foreground">Rolled Back At</p>
                <p>
                  {changeset.rolled_back_at
                    ? new Date(changeset.rolled_back_at).toLocaleString()
                    : "-"}
                </p>
              </div>
            </div>
          )}

          {/* Rollback button */}
          {!changeset.rolled_back && (
            <div className="border-t pt-3">
              <button
                onClick={handleRollback}
                disabled={rollback.isPending}
                className="rounded-md bg-red-600 px-4 py-2 text-sm font-medium text-white hover:bg-red-700 disabled:opacity-50"
              >
                Rollback Changeset
              </button>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Help Tab — Knowledge system documentation
// ---------------------------------------------------------------------------

function KnowledgeHelpTab() {
  return (
    <div className="max-w-3xl space-y-8">
      <section>
        <h2 className="mb-2 text-lg font-semibold">
          What is the Knowledge System?
        </h2>
        <p className="text-sm leading-relaxed text-muted-foreground">
          The knowledge system captures domain knowledge shared during AI
          assistant sessions and integrates it back into the data catalog. When
          users provide corrections to metadata, share business context about
          data meaning, report data quality observations, or describe
          relationships between datasets, the platform records these as{" "}
          <strong>insights</strong>. Insights are then reviewed by admins and
          applied to the catalog as <strong>changesets</strong>.
        </p>
      </section>

      <section>
        <h2 className="mb-2 text-lg font-semibold">Insight Categories</h2>
        <p className="mb-3 text-sm leading-relaxed text-muted-foreground">
          Insights are categorized by the type of knowledge they represent:
        </p>
        <div className="overflow-auto rounded-lg border">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b bg-muted/50">
                <th className="px-3 py-2 text-left font-medium">Category</th>
                <th className="px-3 py-2 text-left font-medium">Description</th>
                <th className="px-3 py-2 text-left font-medium">Example</th>
              </tr>
            </thead>
            <tbody>
              <tr className="border-b">
                <td className="px-3 py-2 font-medium text-xs">Correction</td>
                <td className="px-3 py-2 text-xs">
                  Fixes to existing metadata that is incorrect
                </td>
                <td className="px-3 py-2 text-xs">
                  &quot;The description says daily but this table is updated hourly&quot;
                </td>
              </tr>
              <tr className="border-b">
                <td className="px-3 py-2 font-medium text-xs">Business Context</td>
                <td className="px-3 py-2 text-xs">
                  Information about what data means in business terms
                </td>
                <td className="px-3 py-2 text-xs">
                  &quot;Revenue excludes returns and store credits&quot;
                </td>
              </tr>
              <tr className="border-b">
                <td className="px-3 py-2 font-medium text-xs">Data Quality</td>
                <td className="px-3 py-2 text-xs">
                  Observations about data completeness, accuracy, or freshness
                </td>
                <td className="px-3 py-2 text-xs">
                  &quot;Store 47 has missing inventory data for Q3 2024&quot;
                </td>
              </tr>
              <tr className="border-b">
                <td className="px-3 py-2 font-medium text-xs">Usage Guidance</td>
                <td className="px-3 py-2 text-xs">
                  Tips for querying or interpreting data correctly
                </td>
                <td className="px-3 py-2 text-xs">
                  &quot;Always filter by status=active for current inventory&quot;
                </td>
              </tr>
              <tr className="border-b">
                <td className="px-3 py-2 font-medium text-xs">Relationship</td>
                <td className="px-3 py-2 text-xs">
                  Connections between datasets not captured in lineage
                </td>
                <td className="px-3 py-2 text-xs">
                  &quot;Join transactions with products on sku, not product_id&quot;
                </td>
              </tr>
              <tr>
                <td className="px-3 py-2 font-medium text-xs">Enhancement</td>
                <td className="px-3 py-2 text-xs">
                  Suggestions for adding new metadata (tags, glossary terms)
                </td>
                <td className="px-3 py-2 text-xs">
                  &quot;This table should be tagged as PII-containing&quot;
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>

      <section>
        <h2 className="mb-2 text-lg font-semibold">Confidence Levels</h2>
        <p className="mb-3 text-sm leading-relaxed text-muted-foreground">
          Each insight is assigned a confidence level indicating how certain the
          capture is:
        </p>
        <div className="grid gap-3 sm:grid-cols-3">
          <div className="rounded-lg border p-3">
            <h3 className="mb-1 text-sm font-medium">High</h3>
            <p className="text-xs leading-relaxed text-muted-foreground">
              Explicitly stated by the user with clear intent. Can typically be
              applied directly.
            </p>
          </div>
          <div className="rounded-lg border p-3">
            <h3 className="mb-1 text-sm font-medium">Medium</h3>
            <p className="text-xs leading-relaxed text-muted-foreground">
              Inferred from user context or conversation. Should be reviewed
              before applying.
            </p>
          </div>
          <div className="rounded-lg border p-3">
            <h3 className="mb-1 text-sm font-medium">Low</h3>
            <p className="text-xs leading-relaxed text-muted-foreground">
              Speculative or derived from indirect signals. Requires careful
              review and may need verification.
            </p>
          </div>
        </div>
      </section>

      <section>
        <h2 className="mb-2 text-lg font-semibold">Insight Lifecycle</h2>
        <p className="mb-3 text-sm leading-relaxed text-muted-foreground">
          Insights progress through a defined lifecycle:
        </p>
        <div className="space-y-2">
          {[
            {
              status: "Pending",
              detail: "Newly captured, awaiting admin review.",
            },
            {
              status: "Approved",
              detail:
                "Reviewed and accepted. Ready to be applied to the catalog.",
            },
            {
              status: "Applied",
              detail:
                "Changes have been made to the data catalog based on this insight.",
            },
            {
              status: "Rejected",
              detail:
                "Reviewed and declined. The insight was incorrect or not actionable.",
            },
            {
              status: "Superseded",
              detail:
                "Replaced by a newer insight that covers the same correction.",
            },
            {
              status: "Rolled Back",
              detail:
                "Applied changes were reverted. The insight remains for audit trail.",
            },
          ].map((step, i) => (
            <div key={step.status} className="flex gap-3 rounded-lg border p-3">
              <span className="flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-primary/10 text-xs font-semibold text-primary">
                {i + 1}
              </span>
              <div>
                <p className="text-sm font-medium">{step.status}</p>
                <p className="text-xs text-muted-foreground">{step.detail}</p>
              </div>
            </div>
          ))}
        </div>
      </section>

      <section>
        <h2 className="mb-2 text-lg font-semibold">Suggested Actions</h2>
        <p className="text-sm leading-relaxed text-muted-foreground">
          When an insight is captured, the system may suggest specific actions
          to take on the data catalog. Actions include adding descriptions,
          updating tags, adding glossary terms, modifying lineage, and other
          catalog operations. Each action specifies a type, target entity, and
          detailed change description. Admins review these suggestions when
          deciding whether to approve or reject an insight.
        </p>
      </section>

      <section>
        <h2 className="mb-2 text-lg font-semibold">Changesets</h2>
        <p className="mb-3 text-sm leading-relaxed text-muted-foreground">
          When approved insights are applied to the catalog, each change creates
          a <strong>changeset</strong> that records:
        </p>
        <ul className="list-inside list-disc space-y-1 text-sm text-muted-foreground">
          <li>
            <strong>Target URN</strong> &mdash; The data catalog entity that was
            modified
          </li>
          <li>
            <strong>Change type</strong> &mdash; The kind of modification (add
            description, update tag, etc.)
          </li>
          <li>
            <strong>Previous value</strong> &mdash; What the field contained
            before the change
          </li>
          <li>
            <strong>New value</strong> &mdash; What the field was changed to
          </li>
          <li>
            <strong>Source insights</strong> &mdash; The insight(s) that led to
            this change
          </li>
        </ul>
        <p className="mt-3 text-sm leading-relaxed text-muted-foreground">
          Changesets can be <strong>rolled back</strong> to revert catalog
          modifications. This creates a full audit trail of all changes made
          through the knowledge system.
        </p>
      </section>

      <section>
        <h2 className="mb-2 text-lg font-semibold">Admin API Endpoints</h2>
        <div className="overflow-auto rounded-lg border">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b bg-muted/50">
                <th className="px-3 py-2 text-left font-medium">Endpoint</th>
                <th className="px-3 py-2 text-left font-medium">Description</th>
              </tr>
            </thead>
            <tbody>
              <tr className="border-b">
                <td className="px-3 py-2 font-mono text-xs">GET /knowledge/insights</td>
                <td className="px-3 py-2 text-xs">
                  List insights with filter/pagination
                </td>
              </tr>
              <tr className="border-b">
                <td className="px-3 py-2 font-mono text-xs">GET /knowledge/insights/stats</td>
                <td className="px-3 py-2 text-xs">
                  Aggregated statistics by status, category, confidence, entity
                </td>
              </tr>
              <tr className="border-b">
                <td className="px-3 py-2 font-mono text-xs">PUT /knowledge/insights/:id/status</td>
                <td className="px-3 py-2 text-xs">
                  Update insight status (approve, reject, etc.)
                </td>
              </tr>
              <tr className="border-b">
                <td className="px-3 py-2 font-mono text-xs">GET /knowledge/changesets</td>
                <td className="px-3 py-2 text-xs">
                  List changesets with filter/pagination
                </td>
              </tr>
              <tr>
                <td className="px-3 py-2 font-mono text-xs">POST /knowledge/changesets/:id/rollback</td>
                <td className="px-3 py-2 text-xs">
                  Rollback a changeset, reverting catalog changes
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>
    </div>
  );
}
