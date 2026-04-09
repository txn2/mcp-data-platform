import { useState, useMemo, useCallback } from "react";
import {
  useMemoryRecords,
  useMemoryStats,
  useArchiveMemory,
  useAuditFilters,
} from "@/api/admin/hooks";
import { StatCard } from "@/components/cards/StatCard";
import { StatusBadge } from "@/components/cards/StatusBadge";
import type { MemoryRecord } from "@/api/admin/types";
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

type Tab = "overview" | "records";

const TAB_ITEMS: { key: Tab; label: string }[] = [
  { key: "overview", label: "Overview" },
  { key: "records", label: "Records" },
];

const DIMENSIONS = [
  "knowledge",
  "event",
  "entity",
  "relationship",
  "preference",
];

const CATEGORIES = [
  "correction",
  "business_context",
  "data_quality",
  "usage_guidance",
  "relationship",
  "enhancement",
  "general",
];

const STATUSES = ["active", "stale", "superseded", "archived"];

const SOURCES = [
  "user",
  "agent_discovery",
  "enrichment_gap",
  "automation",
  "lineage_event",
];

type BadgeVariant = "success" | "error" | "warning" | "neutral";

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

function formatCategory(cat: string): string {
  return cat.replace(/_/g, " ").replace(/\b\w/g, (c) => c.toUpperCase());
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

export function MemoryPage({ initialTab }: { initialTab?: string }) {
  const [tab, setTab] = useState<Tab>(
    (["overview", "records"].includes(initialTab ?? "")
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
      {tab === "records" && <RecordsTab />}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Overview Tab
// ---------------------------------------------------------------------------

const STATUS_COLORS: Record<string, string> = {
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

const CATEGORY_COLORS = [
  "hsl(221, 83%, 53%)",
  "hsl(262, 83%, 58%)",
  "hsl(330, 81%, 60%)",
  "hsl(24, 94%, 50%)",
  "hsl(142, 76%, 36%)",
  "hsl(45, 93%, 47%)",
  "hsl(200, 70%, 50%)",
];

function OverviewTab() {
  const { data: stats } = useMemoryStats();

  const activeCount = stats?.by_status?.["active"] ?? 0;
  const staleCount = stats?.by_status?.["stale"] ?? 0;

  const statusChartData = useMemo(() => {
    if (!stats?.by_status) return [];
    return Object.entries(stats.by_status).map(([name, value]) => ({
      name: formatCategory(name),
      value,
      key: name,
    }));
  }, [stats]);

  const dimensionChartData = useMemo(() => {
    if (!stats?.by_dimension) return [];
    return Object.entries(stats.by_dimension).map(([name, value]) => ({
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

  return (
    <div className="space-y-6">
      {/* Summary cards */}
      <div className="grid grid-cols-2 gap-4 md:grid-cols-4">
        <StatCard label="Total Memories" value={stats?.total ?? "-"} />
        <StatCard label="Active" value={activeCount} />
        <StatCard
          label="Stale"
          value={staleCount}
          className={staleCount > 0 ? "border-yellow-200" : undefined}
        />
        <StatCard
          label="Dimensions"
          value={
            stats?.by_dimension
              ? Object.keys(stats.by_dimension).length
              : "-"
          }
        />
      </div>

      {/* Charts row: Status + Dimension */}
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
                  <div
                    key={entry.key}
                    className="flex items-center gap-2 text-xs"
                  >
                    <span
                      className="inline-block h-3 w-3 rounded-full"
                      style={{
                        backgroundColor:
                          STATUS_COLORS[entry.key] ?? "hsl(220, 9%, 46%)",
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
          <h2 className="mb-3 text-sm font-medium">By Dimension</h2>
          {dimensionChartData.length > 0 ? (
            <div className="flex items-center gap-4">
              <ResponsiveContainer width="50%" height={200}>
                <PieChart>
                  <Pie
                    data={dimensionChartData}
                    cx="50%"
                    cy="50%"
                    innerRadius={50}
                    outerRadius={80}
                    dataKey="value"
                    nameKey="name"
                  >
                    {dimensionChartData.map((entry) => (
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
                {dimensionChartData.map((entry) => (
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

      {/* Category breakdown bar chart */}
      <div className="rounded-lg border bg-card p-4">
        <h2 className="mb-3 text-sm font-medium">Memories by Category</h2>
        {categoryChartData.length > 0 ? (
          <ResponsiveContainer width="100%" height={250}>
            <RechartsBarChart
              data={categoryChartData}
              layout="vertical"
              margin={{ left: 100 }}
            >
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
                formatter={(value: number) => [value, "Memories"]}
              />
              <Bar dataKey="value" radius={[0, 4, 4, 0]}>
                {categoryChartData.map((_, idx) => (
                  <Cell
                    key={idx}
                    fill={CATEGORY_COLORS[idx % CATEGORY_COLORS.length]}
                  />
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
    </div>
  );
}

// ---------------------------------------------------------------------------
// Records Tab
// ---------------------------------------------------------------------------

function RecordsTab() {
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
    [page, dimensionFilter, categoryFilter, statusFilter, personaFilter, sourceFilter],
  );

  const { data, isLoading } = useMemoryRecords(params);
  const { data: stats } = useMemoryStats();
  const totalPages = data ? Math.ceil(data.total / PER_PAGE) : 0;

  return (
    <>
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
          {DIMENSIONS.map((d) => (
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
          {CATEGORIES.map((c) => (
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
          {STATUSES.map((s) => (
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
          {SOURCES.map((s) => (
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
