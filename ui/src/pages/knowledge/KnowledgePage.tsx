import { useState, useMemo, useCallback } from "react";
import {
  useInsights,
  useInsightStats,
  useUpdateInsightStatus,
  useChangesets,
  useRollbackChangeset,
  useAuditFilters,
} from "@/api/admin/hooks";
import { StatCard } from "@/components/cards/StatCard";
import { StatusBadge } from "@/components/cards/StatusBadge";
import type { Insight, Changeset } from "@/api/admin/types";
import { formatUser } from "@/lib/formatUser";
import { MarkdownRenderer } from "@/components/renderers/MarkdownRenderer";

const PER_PAGE = 20;

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

// ---------------------------------------------------------------------------
// Knowledge Capture Tab
// ---------------------------------------------------------------------------

export function KnowledgeCaptureTab() {
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
            <div className="rounded bg-muted p-3">
              <MarkdownRenderer content={insight.insight_text} bare />
            </div>
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
// Changesets Tab
// ---------------------------------------------------------------------------

export function ChangesetsTab() {
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
