import { useState, useMemo, useCallback } from "react";
import {
  useInsights,
  useInsightStats,
  useUpdateInsightStatus,
  useChangesets,
  useRollbackChangeset,
  useMemoryRecords,
  useMemoryStats,
  useArchiveMemory,
  useAuditFilters,
} from "@/api/admin/hooks";
import { StatCard } from "@/components/cards/StatCard";
import { StatusBadge } from "@/components/cards/StatusBadge";
import type { Insight, Changeset, MemoryRecord } from "@/api/admin/types";
import { formatUser } from "@/lib/formatUser";
import {
  PieChart,
  Pie,
  Cell,
  Tooltip,
  ResponsiveContainer,
} from "recharts";

const PER_PAGE = 20;

type Tab = "overview" | "knowledge" | "memory" | "changesets" | "help";

const TAB_ITEMS: { key: Tab; label: string }[] = [
  { key: "overview", label: "Overview" },
  { key: "knowledge", label: "Knowledge Capture" },
  { key: "memory", label: "All Memory" },
  { key: "changesets", label: "Changesets" },
  { key: "help", label: "Help" },
];

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

const MEMORY_DIMENSIONS = [
  "knowledge",
  "event",
  "entity",
  "relationship",
  "preference",
];

const MEMORY_CATEGORIES = [
  "correction",
  "business_context",
  "data_quality",
  "usage_guidance",
  "relationship",
  "enhancement",
  "general",
];

const MEMORY_STATUSES = ["active", "stale", "superseded", "archived"];

const MEMORY_SOURCES = [
  "user",
  "agent_discovery",
  "enrichment_gap",
  "automation",
  "lineage_event",
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

function memoryStatusVariant(status: string): BadgeVariant {
  switch (status) {
    case "active":
      return "success";
    case "stale":
      return "warning";
    case "superseded":
      return "neutral";
    case "archived":
      return "error";
    default:
      return "neutral";
  }
}

function confidenceVariant(c: string): BadgeVariant {
  switch (c) {
    case "high":
      return "success";
    case "medium":
      return "warning";
    case "low":
      return "neutral";
    default:
      return "neutral";
  }
}

function formatCategory(cat: string): string {
  return cat.replace(/_/g, " ").replace(/\b\w/g, (c) => c.toUpperCase());
}

export function KnowledgePage({ initialTab }: { initialTab?: string }) {
  const [tab, setTab] = useState<Tab>(
    (["overview", "knowledge", "memory", "changesets", "help"].includes(
      initialTab ?? "",
    )
      ? initialTab
      : "overview") as Tab,
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

      {tab === "overview" && <OverviewTab />}
      {tab === "knowledge" && <KnowledgeCaptureTab />}
      {tab === "memory" && <AllMemoryTab />}
      {tab === "changesets" && <ChangesetsTab />}
      {tab === "help" && <HelpTab />}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Overview Tab
// ---------------------------------------------------------------------------

const INSIGHT_STATUS_COLORS: Record<string, string> = {
  pending: "hsl(45, 93%, 47%)",
  approved: "hsl(142, 76%, 36%)",
  rejected: "hsl(0, 84%, 60%)",
  applied: "hsl(142, 76%, 46%)",
  superseded: "hsl(220, 9%, 46%)",
  rolled_back: "hsl(0, 72%, 51%)",
};

const MEMORY_STATUS_COLORS: Record<string, string> = {
  active: "hsl(142, 76%, 36%)",
  stale: "hsl(45, 93%, 47%)",
  superseded: "hsl(220, 9%, 46%)",
  archived: "hsl(0, 84%, 60%)",
};

const DIMENSION_COLORS: Record<string, string> = {
  knowledge: "hsl(221, 83%, 53%)",
  event: "hsl(262, 83%, 58%)",
  entity: "hsl(330, 81%, 60%)",
  relationship: "hsl(24, 94%, 50%)",
  preference: "hsl(142, 76%, 36%)",
};

function OverviewTab() {
  const { data: insightStats } = useInsightStats();
  const { data: memoryStats } = useMemoryStats();

  const totalInsights = useMemo(() => {
    if (!insightStats?.by_status) return 0;
    return Object.values(insightStats.by_status).reduce((s, n) => s + n, 0);
  }, [insightStats]);

  const approvalRate = useMemo(() => {
    if (!insightStats?.by_status) return null;
    const approved =
      (insightStats.by_status["approved"] ?? 0) +
      (insightStats.by_status["applied"] ?? 0);
    const reviewed = approved + (insightStats.by_status["rejected"] ?? 0);
    if (reviewed === 0) return null;
    return ((approved / reviewed) * 100).toFixed(0);
  }, [insightStats]);

  const insightStatusData = useMemo(() => {
    if (!insightStats?.by_status) return [];
    return Object.entries(insightStats.by_status).map(([name, value]) => ({
      name: formatCategory(name),
      value,
      key: name,
    }));
  }, [insightStats]);

  const memoryStatusData = useMemo(() => {
    if (!memoryStats?.by_status) return [];
    return Object.entries(memoryStats.by_status).map(([name, value]) => ({
      name: formatCategory(name),
      value,
      key: name,
    }));
  }, [memoryStats]);

  const dimensionData = useMemo(() => {
    if (!memoryStats?.by_dimension) return [];
    return Object.entries(memoryStats.by_dimension).map(([name, value]) => ({
      name: formatCategory(name),
      value,
      key: name,
    }));
  }, [memoryStats]);

  return (
    <div className="space-y-6">
      <p className="text-sm text-muted-foreground mb-6">
        Agents remember what happens during sessions. Corrections, business
        rules, preferences, and context accumulate here instead of disappearing
        when a session ends. Knowledge capture is the review process where admins
        decide which observations belong in the DataHub catalog.
      </p>

      {/* Knowledge stats */}
      <h2 className="text-sm font-medium">Knowledge Capture</h2>
      <div className="grid grid-cols-2 gap-4 md:grid-cols-3 lg:grid-cols-6">
        <StatCard label="Total Insights" value={totalInsights} />
        <StatCard
          label="Pending Review"
          value={insightStats?.total_pending ?? "-"}
          className={
            insightStats && insightStats.total_pending > 0
              ? "border-yellow-200"
              : undefined
          }
        />
        <StatCard
          label="Approved"
          value={insightStats?.by_status?.["approved"] ?? "-"}
        />
        <StatCard
          label="Applied"
          value={insightStats?.by_status?.["applied"] ?? "-"}
        />
        <StatCard
          label="Rejected"
          value={insightStats?.by_status?.["rejected"] ?? "-"}
        />
        <StatCard
          label="Approval Rate"
          value={approvalRate ? `${approvalRate}%` : "-"}
        />
      </div>

      {/* Insight Status chart */}
      <div className="rounded-lg border bg-card p-4">
        <h2 className="mb-3 text-sm font-medium">
          Insight Status Distribution
        </h2>
        {insightStatusData.length > 0 ? (
          <div className="flex items-center gap-4">
            <ResponsiveContainer width="50%" height={200}>
              <PieChart>
                <Pie
                  data={insightStatusData}
                  cx="50%"
                  cy="50%"
                  innerRadius={50}
                  outerRadius={80}
                  dataKey="value"
                  nameKey="name"
                >
                  {insightStatusData.map((entry) => (
                    <Cell
                      key={entry.key}
                      fill={
                        INSIGHT_STATUS_COLORS[entry.key] ??
                        "hsl(220, 9%, 46%)"
                      }
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
              {insightStatusData.map((entry) => (
                <div
                  key={entry.key}
                  className="flex items-center gap-2 text-xs"
                >
                  <span
                    className="inline-block h-3 w-3 rounded-full"
                    style={{
                      backgroundColor:
                        INSIGHT_STATUS_COLORS[entry.key] ??
                        "hsl(220, 9%, 46%)",
                    }}
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

      {/* Memory stats */}
      <h2 className="text-sm font-medium">Memory</h2>
      <div className="grid grid-cols-2 gap-4 md:grid-cols-4">
        <StatCard label="Total Memories" value={memoryStats?.total ?? "-"} />
        <StatCard
          label="Active"
          value={memoryStats?.by_status?.["active"] ?? "-"}
        />
        <StatCard
          label="Stale"
          value={memoryStats?.by_status?.["stale"] ?? "-"}
          className={
            memoryStats && (memoryStats.by_status?.["stale"] ?? 0) > 0
              ? "border-yellow-200"
              : undefined
          }
        />
        <StatCard
          label="Dimensions"
          value={
            memoryStats?.by_dimension
              ? Object.keys(memoryStats.by_dimension).length
              : "-"
          }
        />
      </div>

      {/* Memory charts: Status + Dimension */}
      <div className="grid gap-6 lg:grid-cols-2">
        {/* Memory Status */}
        <div className="rounded-lg border bg-card p-4">
          <h2 className="mb-3 text-sm font-medium">
            Memory Status Distribution
          </h2>
          {memoryStatusData.length > 0 ? (
            <div className="flex items-center gap-4">
              <ResponsiveContainer width="50%" height={200}>
                <PieChart>
                  <Pie
                    data={memoryStatusData}
                    cx="50%"
                    cy="50%"
                    innerRadius={50}
                    outerRadius={80}
                    dataKey="value"
                    nameKey="name"
                  >
                    {memoryStatusData.map((entry) => (
                      <Cell
                        key={entry.key}
                        fill={
                          MEMORY_STATUS_COLORS[entry.key] ??
                          "hsl(220, 9%, 46%)"
                        }
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
                {memoryStatusData.map((entry) => (
                  <div
                    key={entry.key}
                    className="flex items-center gap-2 text-xs"
                  >
                    <span
                      className="inline-block h-3 w-3 rounded-full"
                      style={{
                        backgroundColor:
                          MEMORY_STATUS_COLORS[entry.key] ??
                          "hsl(220, 9%, 46%)",
                      }}
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

        {/* Dimension Distribution */}
        <div className="rounded-lg border bg-card p-4">
          <h2 className="mb-3 text-sm font-medium">Memory by Dimension</h2>
          {dimensionData.length > 0 ? (
            <div className="flex items-center gap-4">
              <ResponsiveContainer width="50%" height={200}>
                <PieChart>
                  <Pie
                    data={dimensionData}
                    cx="50%"
                    cy="50%"
                    innerRadius={50}
                    outerRadius={80}
                    dataKey="value"
                    nameKey="name"
                  >
                    {dimensionData.map((entry) => (
                      <Cell
                        key={entry.key}
                        fill={
                          DIMENSION_COLORS[entry.key] ?? "hsl(220, 9%, 46%)"
                        }
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
                {dimensionData.map((entry) => (
                  <div
                    key={entry.key}
                    className="flex items-center gap-2 text-xs"
                  >
                    <span
                      className="inline-block h-3 w-3 rounded-full"
                      style={{
                        backgroundColor:
                          DIMENSION_COLORS[entry.key] ?? "hsl(220, 9%, 46%)",
                      }}
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
    </div>
  );
}

// ---------------------------------------------------------------------------
// Knowledge Capture Tab
// ---------------------------------------------------------------------------

function KnowledgeCaptureTab() {
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

  const topCategory = useMemo(() => {
    if (!stats?.by_category) return "-";
    const entries = Object.entries(stats.by_category);
    if (entries.length === 0) return "-";
    entries.sort((a, b) => b[1] - a[1]);
    return formatCategory(entries[0]![0]);
  }, [stats]);

  return (
    <>
      <p className="text-sm text-muted-foreground mb-6">
        Domain knowledge shared during sessions. Someone mentions that stores
        close at 9pm, or that the revenue column excludes returns. It gets
        recorded here. Admins review each insight and decide whether to write it
        into DataHub as a description, tag, glossary term, or context document.
      </p>

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
              <p title={insight.captured_by}>
                {formatUser(
                  insight.captured_by,
                  userLabels[insight.captured_by],
                )}
              </p>
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
            <p className="rounded bg-muted p-3 text-sm">
              {insight.insight_text}
            </p>
          </div>

          {/* Entity URNs */}
          {insight.entity_urns.length > 0 && (
            <div>
              <p className="mb-1 text-xs text-muted-foreground">Entity URNs</p>
              <div className="space-y-1">
                {insight.entity_urns.map((urn, i) => (
                  <p
                    key={i}
                    className="font-mono text-xs text-muted-foreground"
                  >
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
                        <td className="px-2 py-1 font-mono">
                          {a.action_type}
                        </td>
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
                <p title={insight.reviewed_by}>
                  {formatUser(
                    insight.reviewed_by!,
                    userLabels[insight.reviewed_by!],
                  )}
                </p>
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
                <p title={insight.applied_by}>
                  {formatUser(
                    insight.applied_by!,
                    userLabels[insight.applied_by!],
                  )}
                </p>
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
                  <p className="text-xs text-muted-foreground">
                    Changeset Ref
                  </p>
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
// All Memory Tab
// ---------------------------------------------------------------------------

function AllMemoryTab() {
  const [page, setPage] = useState(1);
  const [dimensionFilter, setDimensionFilter] = useState("");
  const [categoryFilter, setCategoryFilter] = useState("");
  const [statusFilter, setStatusFilter] = useState("");
  const [personaFilter, setPersonaFilter] = useState("");
  const [sourceFilter, setSourceFilter] = useState("");
  const [selectedRecord, setSelectedRecord] = useState<MemoryRecord | null>(
    null,
  );
  const { data: filters } = useAuditFilters();
  const ul = filters?.user_labels ?? {};

  const params = useMemo(
    () => ({
      page,
      perPage: PER_PAGE,
      dimension: dimensionFilter || undefined,
      category: categoryFilter || undefined,
      status: statusFilter || undefined,
      persona: personaFilter || undefined,
      source: sourceFilter || undefined,
    }),
    [
      page,
      dimensionFilter,
      categoryFilter,
      statusFilter,
      personaFilter,
      sourceFilter,
    ],
  );

  const { data, isLoading } = useMemoryRecords(params);
  const { data: stats } = useMemoryStats();
  const totalPages = data ? Math.ceil(data.total / PER_PAGE) : 0;

  return (
    <>
      <p className="text-sm text-muted-foreground mb-6">
        Every memory record across all users and sessions. Active records get
        attached to query results automatically, so agents have context without
        being told. When a referenced dataset changes in DataHub, the staleness
        watcher flags affected memories for review.
      </p>

      {/* Stats row */}
      <div className="grid grid-cols-2 gap-4 md:grid-cols-4">
        <StatCard label="Total" value={stats?.total ?? "-"} />
        <StatCard
          label="Active"
          value={stats?.by_status?.["active"] ?? "-"}
        />
        <StatCard
          label="Stale"
          value={stats?.by_status?.["stale"] ?? "-"}
          className={
            stats && (stats.by_status?.["stale"] ?? 0) > 0
              ? "border-yellow-200"
              : undefined
          }
        />
        <StatCard
          label="Archived"
          value={stats?.by_status?.["archived"] ?? "-"}
        />
      </div>

      {/* Filters */}
      <div className="flex flex-wrap items-center gap-3">
        <select
          value={dimensionFilter}
          onChange={(e) => {
            setDimensionFilter(e.target.value);
            setPage(1);
          }}
          className="rounded-md border bg-background px-3 py-1.5 text-sm outline-none ring-ring focus:ring-2"
        >
          <option value="">All Dimensions</option>
          {MEMORY_DIMENSIONS.map((d) => (
            <option key={d} value={d}>
              {formatCategory(d)}
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
          {MEMORY_CATEGORIES.map((c) => (
            <option key={c} value={c}>
              {formatCategory(c)}
            </option>
          ))}
        </select>
        <select
          value={statusFilter}
          onChange={(e) => {
            setStatusFilter(e.target.value);
            setPage(1);
          }}
          className="rounded-md border bg-background px-3 py-1.5 text-sm outline-none ring-ring focus:ring-2"
        >
          <option value="">All Statuses</option>
          {MEMORY_STATUSES.map((s) => (
            <option key={s} value={s}>
              {formatCategory(s)}
            </option>
          ))}
        </select>
        <select
          value={sourceFilter}
          onChange={(e) => {
            setSourceFilter(e.target.value);
            setPage(1);
          }}
          className="rounded-md border bg-background px-3 py-1.5 text-sm outline-none ring-ring focus:ring-2"
        >
          <option value="">All Sources</option>
          {MEMORY_SOURCES.map((s) => (
            <option key={s} value={s}>
              {formatCategory(s)}
            </option>
          ))}
        </select>
        {personaFilter && (
          <button
            onClick={() => {
              setPersonaFilter("");
              setPage(1);
            }}
            className="rounded-md border px-3 py-1.5 text-xs hover:bg-muted"
          >
            Clear persona: {personaFilter}
          </button>
        )}
      </div>

      {/* Table */}
      <div className="overflow-auto rounded-lg border bg-card">
        <table className="w-full text-sm">
          <thead>
            <tr className="border-b bg-muted/50">
              <th className="px-3 py-2 text-left font-medium">Created</th>
              <th className="px-3 py-2 text-left font-medium">User</th>
              <th className="px-3 py-2 text-left font-medium">Persona</th>
              <th className="px-3 py-2 text-left font-medium">Dimension</th>
              <th className="px-3 py-2 text-left font-medium">Category</th>
              <th className="px-3 py-2 text-left font-medium">Content</th>
              <th className="px-3 py-2 text-center font-medium">Status</th>
              <th className="px-3 py-2 text-center font-medium">Confidence</th>
            </tr>
          </thead>
          <tbody>
            {isLoading && (
              <tr>
                <td
                  colSpan={8}
                  className="px-3 py-8 text-center text-muted-foreground"
                >
                  Loading...
                </td>
              </tr>
            )}
            {data?.data.map((record) => (
              <tr
                key={record.id}
                onClick={() => setSelectedRecord(record)}
                className="cursor-pointer border-b transition-colors hover:bg-muted/50"
              >
                <td className="px-3 py-2 text-xs whitespace-nowrap">
                  {new Date(record.created_at).toLocaleString()}
                </td>
                <td className="px-3 py-2 text-xs" title={record.created_by}>
                  {formatUser(record.created_by, ul[record.created_by])}
                </td>
                <td className="px-3 py-2 text-xs">{record.persona}</td>
                <td className="px-3 py-2 text-xs">
                  {formatCategory(record.dimension)}
                </td>
                <td className="px-3 py-2 text-xs">
                  {formatCategory(record.category)}
                </td>
                <td className="max-w-xs truncate px-3 py-2 text-xs">
                  {record.content}
                </td>
                <td className="px-3 py-2 text-center">
                  <StatusBadge variant={memoryStatusVariant(record.status)}>
                    {formatCategory(record.status)}
                  </StatusBadge>
                </td>
                <td className="px-3 py-2 text-center">
                  <StatusBadge variant={confidenceVariant(record.confidence)}>
                    {record.confidence}
                  </StatusBadge>
                </td>
              </tr>
            ))}
            {data?.data.length === 0 && (
              <tr>
                <td
                  colSpan={8}
                  className="px-3 py-8 text-center text-muted-foreground"
                >
                  No memory records found
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
      {selectedRecord && (
        <MemoryDrawer
          record={selectedRecord}
          onClose={() => setSelectedRecord(null)}
          userLabels={ul}
        />
      )}
    </>
  );
}

// ---------------------------------------------------------------------------
// Memory Detail Drawer
// ---------------------------------------------------------------------------

function MemoryDrawer({
  record,
  onClose,
  userLabels,
}: {
  record: MemoryRecord;
  onClose: () => void;
  userLabels: Record<string, string>;
}) {
  const archiveMutation = useArchiveMemory();
  const [metadataExpanded, setMetadataExpanded] = useState(false);

  const handleArchive = useCallback(() => {
    archiveMutation.mutate(record.id, { onSuccess: () => onClose() });
  }, [record.id, archiveMutation, onClose]);

  return (
    <div className="fixed inset-0 z-50 flex justify-end">
      <div className="absolute inset-0 bg-black/50" onClick={onClose} />
      <div className="relative w-full max-w-lg overflow-auto bg-card p-6 shadow-xl">
        <div className="mb-4 flex items-center justify-between">
          <h2 className="text-lg font-semibold">Memory Detail</h2>
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
              <p className="font-mono text-xs break-all">{record.id}</p>
            </div>
            <div>
              <p className="text-xs text-muted-foreground">Created At</p>
              <p>{new Date(record.created_at).toLocaleString()}</p>
            </div>
            <div>
              <p className="text-xs text-muted-foreground">Updated At</p>
              <p>{new Date(record.updated_at).toLocaleString()}</p>
            </div>
            <div>
              <p className="text-xs text-muted-foreground">Created By</p>
              <p title={record.created_by}>
                {formatUser(record.created_by, userLabels[record.created_by])}
              </p>
            </div>
            <div>
              <p className="text-xs text-muted-foreground">Persona</p>
              <p>{record.persona}</p>
            </div>
            <div>
              <p className="text-xs text-muted-foreground">Dimension</p>
              <p>{formatCategory(record.dimension)}</p>
            </div>
            <div>
              <p className="text-xs text-muted-foreground">Category</p>
              <p>{formatCategory(record.category)}</p>
            </div>
            <div>
              <p className="text-xs text-muted-foreground">Confidence</p>
              <StatusBadge variant={confidenceVariant(record.confidence)}>
                {record.confidence}
              </StatusBadge>
            </div>
            <div>
              <p className="text-xs text-muted-foreground">Source</p>
              <p>{formatCategory(record.source)}</p>
            </div>
            <div>
              <p className="text-xs text-muted-foreground">Status</p>
              <StatusBadge variant={memoryStatusVariant(record.status)}>
                {formatCategory(record.status)}
              </StatusBadge>
            </div>
          </div>

          {/* Full content */}
          <div>
            <p className="mb-1 text-xs text-muted-foreground">Content</p>
            <p className="rounded bg-muted p-3 text-sm whitespace-pre-wrap">
              {record.content}
            </p>
          </div>

          {/* Entity URNs */}
          {record.entity_urns && record.entity_urns.length > 0 && (
            <div>
              <p className="mb-1 text-xs text-muted-foreground">Entity URNs</p>
              <div className="space-y-1">
                {record.entity_urns.map((urn, i) => (
                  <p
                    key={i}
                    className="font-mono text-xs text-muted-foreground break-all"
                  >
                    {urn}
                  </p>
                ))}
              </div>
            </div>
          )}

          {/* Related Columns */}
          {record.related_columns && record.related_columns.length > 0 && (
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
                    {record.related_columns.map((c, i) => (
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

          {/* Stale info */}
          {record.status === "stale" && (
            <div className="grid grid-cols-2 gap-3 border-t pt-3 text-sm">
              {record.stale_reason && (
                <div className="col-span-2">
                  <p className="text-xs text-muted-foreground">Stale Reason</p>
                  <p className="text-sm">{record.stale_reason}</p>
                </div>
              )}
              {record.stale_at && (
                <div>
                  <p className="text-xs text-muted-foreground">Stale At</p>
                  <p>{new Date(record.stale_at).toLocaleString()}</p>
                </div>
              )}
            </div>
          )}

          {/* Last verified */}
          {record.last_verified && (
            <div className="border-t pt-3 text-sm">
              <p className="text-xs text-muted-foreground">Last Verified</p>
              <p>{new Date(record.last_verified).toLocaleString()}</p>
            </div>
          )}

          {/* Metadata JSON */}
          {record.metadata &&
            Object.keys(record.metadata).length > 0 && (
              <div>
                <button
                  onClick={() => setMetadataExpanded(!metadataExpanded)}
                  className="mb-1 text-xs text-muted-foreground hover:text-foreground"
                >
                  Metadata {metadataExpanded ? "(collapse)" : "(expand)"}
                </button>
                {metadataExpanded && (
                  <pre className="max-h-48 overflow-auto rounded bg-muted p-3 text-xs">
                    {JSON.stringify(record.metadata, null, 2)}
                  </pre>
                )}
              </div>
            )}

          {/* Archive button */}
          {record.status !== "archived" && (
            <div className="border-t pt-4">
              <button
                onClick={handleArchive}
                disabled={archiveMutation.isPending}
                className="rounded-md bg-red-600 px-4 py-2 text-sm font-medium text-white hover:bg-red-700 disabled:opacity-50"
              >
                {archiveMutation.isPending ? "Archiving..." : "Archive"}
              </button>
            </div>
          )}
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
      <p className="text-sm text-muted-foreground mb-6">
        Catalog changes that came from approved knowledge. Each changeset
        records what was changed, the previous value, and who approved it.
        Roll back any change that needs to be undone.
      </p>

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
                <td
                  className="px-3 py-2 text-xs"
                  title={changeset.applied_by}
                >
                  {formatUser(changeset.applied_by, ul[changeset.applied_by])}
                </td>
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
              <p title={changeset.approved_by}>
                {formatUser(
                  changeset.approved_by,
                  userLabels[changeset.approved_by],
                )}
              </p>
            </div>
            <div>
              <p className="text-xs text-muted-foreground">Applied By</p>
              <p title={changeset.applied_by}>
                {formatUser(
                  changeset.applied_by,
                  userLabels[changeset.applied_by],
                )}
              </p>
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
                  <p
                    key={i}
                    className="font-mono text-xs text-muted-foreground"
                  >
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
                <p title={changeset.rolled_back_by}>
                  {formatUser(
                    changeset.rolled_back_by ?? "",
                    userLabels[changeset.rolled_back_by ?? ""],
                  )}
                </p>
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
// Help Tab
// ---------------------------------------------------------------------------

function HelpTab() {
  return (
    <div className="max-w-3xl space-y-8">
      <section>
        <h2 className="mb-2 text-lg font-semibold">The Memory System</h2>
        <p className="text-sm leading-relaxed text-muted-foreground">
          Memory is the first-class system. Everything lives in memory records.
          As users interact with AI agents, the platform accumulates knowledge
          across sessions: corrections to data descriptions, user preferences,
          domain context, business rules, and episodic observations about data
          behavior.
        </p>
        <p className="mt-2 text-sm leading-relaxed text-muted-foreground">
          Active memories are automatically attached to toolkit responses so
          agents have the right context without asking the same questions twice.
          When the underlying data changes in DataHub, memories that reference
          it are flagged as stale for review.
        </p>
      </section>

      <section>
        <h2 className="mb-2 text-lg font-semibold">Knowledge Capture</h2>
        <p className="text-sm leading-relaxed text-muted-foreground">
          Knowledge capture is a governance workflow within memory. When a piece
          of domain knowledge is important enough to be reviewed and potentially
          written back to the DataHub catalog, it goes through the knowledge
          capture pipeline.
        </p>
        <p className="mt-2 text-sm leading-relaxed text-muted-foreground">
          The pipeline works like this: an agent calls{" "}
          <code className="rounded bg-muted px-1 py-0.5 text-xs">
            capture_insight
          </code>{" "}
          when a user shares something worth preserving. An admin reviews the
          insight and approves or rejects it. Approved insights can then be
          applied via{" "}
          <code className="rounded bg-muted px-1 py-0.5 text-xs">
            apply_knowledge
          </code>{" "}
          to write changes back to DataHub as descriptions, tags, glossary
          terms, or context documents.
        </p>
        <p className="mt-2 text-sm leading-relaxed text-muted-foreground">
          Knowledge is not limited to catalog metadata. It includes things
          like &quot;we have two distinct selling seasons&quot; or
          &quot;stores close at 9pm so after-hours data is stale.&quot; The
          catalog write-back is just one possible outcome of the review process.
        </p>
      </section>

      <section>
        <h2 className="mb-2 text-lg font-semibold">
          How Memory and Knowledge Relate
        </h2>
        <p className="text-sm leading-relaxed text-muted-foreground">
          Every insight is a memory record. Memory is the storage layer.
          Knowledge capture is the review process that sits on top. An agent
          remembers hundreds of things. A few of those are important enough
          to put in the catalog. That is what knowledge capture is for.
        </p>
      </section>

      <section>
        <h2 className="mb-2 text-lg font-semibold">What You Can Do Here</h2>
        <ul className="list-inside list-disc space-y-1 text-sm text-muted-foreground">
          <li>
            <strong>Overview</strong>: See combined statistics for both
            knowledge capture and memory. A quick view of how much the platform
            has learned and what needs attention.
          </li>
          <li>
            <strong>Knowledge Capture</strong>: Browse, filter, and
            review captured insights. Approve or reject them. Applied insights
            become changesets.
          </li>
          <li>
            <strong>All Memory</strong>: Browse every memory record
            across all users and sessions. Filter by dimension, category,
            status, or source. Archive records that are no longer useful.
          </li>
          <li>
            <strong>Changesets</strong>: See the history of changes
            applied to the DataHub catalog and roll back any that need to be
            reverted.
          </li>
        </ul>
      </section>

      <section>
        <h2 className="mb-2 text-lg font-semibold">Types of Knowledge</h2>
        <div className="overflow-auto rounded-lg border">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b bg-muted/50">
                <th className="px-3 py-2 text-left font-medium">Type</th>
                <th className="px-3 py-2 text-left font-medium">Example</th>
              </tr>
            </thead>
            <tbody>
              <tr className="border-b">
                <td className="px-3 py-2 font-medium text-xs">Correction</td>
                <td className="px-3 py-2 text-xs">
                  &quot;The description says daily but this table is updated
                  hourly&quot;
                </td>
              </tr>
              <tr className="border-b">
                <td className="px-3 py-2 font-medium text-xs">
                  Business Context
                </td>
                <td className="px-3 py-2 text-xs">
                  &quot;Revenue excludes returns and store credits&quot;
                </td>
              </tr>
              <tr className="border-b">
                <td className="px-3 py-2 font-medium text-xs">Data Quality</td>
                <td className="px-3 py-2 text-xs">
                  &quot;Store 47 has missing inventory data for Q3&quot;
                </td>
              </tr>
              <tr className="border-b">
                <td className="px-3 py-2 font-medium text-xs">
                  Usage Guidance
                </td>
                <td className="px-3 py-2 text-xs">
                  &quot;Always filter by status=active for current
                  inventory&quot;
                </td>
              </tr>
              <tr className="border-b">
                <td className="px-3 py-2 font-medium text-xs">Relationship</td>
                <td className="px-3 py-2 text-xs">
                  &quot;Join transactions with products on sku, not
                  product_id&quot;
                </td>
              </tr>
              <tr>
                <td className="px-3 py-2 font-medium text-xs">Enhancement</td>
                <td className="px-3 py-2 text-xs">
                  &quot;This table should be tagged as PII-containing&quot;
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>

      <section>
        <h2 className="mb-2 text-lg font-semibold">The Review Pipeline</h2>
        <div className="space-y-2">
          {[
            {
              status: "Pending",
              detail: "A new insight arrives and awaits admin review.",
            },
            {
              status: "Approved",
              detail:
                "Reviewed and confirmed as correct. Ready to apply to the catalog.",
            },
            {
              status: "Applied",
              detail:
                "The change has been written to DataHub. A changeset tracks the before and after.",
            },
            {
              status: "Rejected",
              detail:
                "Reviewed and determined not accurate or not useful. Preserved for the audit trail.",
            },
          ].map((step, i) => (
            <div
              key={step.status}
              className="flex gap-3 rounded-lg border p-3"
            >
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
        <h2 className="mb-2 text-lg font-semibold">Memory Dimensions</h2>
        <p className="text-sm leading-relaxed text-muted-foreground">
          Memory records are organized by dimension, which describes what kind
          of information they hold:
        </p>
        <ul className="mt-2 list-inside list-disc space-y-1 text-sm text-muted-foreground">
          <li>
            <strong>Knowledge</strong>: Domain facts, business rules,
            and data definitions.
          </li>
          <li>
            <strong>Event</strong>: Observations about data changes or
            incidents.
          </li>
          <li>
            <strong>Entity</strong>: Information about specific datasets,
            tables, or columns.
          </li>
          <li>
            <strong>Relationship</strong>: How datasets connect to each
            other (joins, lineage, dependencies).
          </li>
          <li>
            <strong>Preference</strong>: User preferences for how data
            should be queried or displayed.
          </li>
        </ul>
      </section>
    </div>
  );
}
