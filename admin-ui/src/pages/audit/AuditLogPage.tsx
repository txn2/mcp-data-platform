import { useState, useMemo, useCallback } from "react";
import {
  useAuditEvents,
  useAuditFilters,
  useAuditOverview,
  useAuditTimeseries,
  useAuditBreakdown,
  useAuditPerformance,
} from "@/api/hooks";
import { StatCard } from "@/components/cards/StatCard";
import { StatusBadge } from "@/components/cards/StatusBadge";
import { TimeseriesChart } from "@/components/charts/TimeseriesChart";
import { BreakdownBarChart } from "@/components/charts/BarChart";
import { EventDrawer } from "@/components/EventDrawer";
import { RecentErrorsList } from "@/components/RecentErrorsList";
import { useTimeRangeStore, type TimeRangePreset } from "@/stores/timerange";
import type { AuditEvent, AuditSortColumn, SortOrder, Resolution } from "@/api/types";
import { ChevronUp, ChevronDown, ChevronsUpDown } from "lucide-react";
import { formatDuration } from "@/lib/formatDuration";
import { formatUser } from "@/lib/formatUser";

const PER_PAGE = 20;

type Tab = "overview" | "events" | "help";

const TAB_ITEMS: { key: Tab; label: string }[] = [
  { key: "overview", label: "Overview" },
  { key: "events", label: "Events" },
  { key: "help", label: "Help" },
];

export function AuditLogPage({ initialTab }: { initialTab?: string }) {
  const [tab, setTab] = useState<Tab>(
    (["overview", "events", "help"].includes(initialTab ?? "") ? initialTab : "overview") as Tab,
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
      {tab === "events" && <EventsTab />}
      {tab === "help" && <AuditHelpTab />}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Overview Tab — Audit Stats Dashboard
// ---------------------------------------------------------------------------

const presets: { value: TimeRangePreset; label: string }[] = [
  { value: "1h", label: "1h" },
  { value: "6h", label: "6h" },
  { value: "24h", label: "24h" },
  { value: "7d", label: "7d" },
];

function getResolution(preset: TimeRangePreset): Resolution {
  switch (preset) {
    case "1h": return "minute";
    case "6h": return "minute";
    case "24h": return "hour";
    case "7d": return "day";
  }
}

function OverviewTab() {
  const { preset, setPreset, getStartTime, getEndTime } = useTimeRangeStore();
  const { startTime, endTime } = useMemo(
    () => ({ startTime: getStartTime(), endTime: getEndTime() }),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [preset],
  );

  const overview = useAuditOverview({ startTime, endTime });
  const timeseries = useAuditTimeseries({ resolution: getResolution(preset), startTime, endTime });
  const toolBreakdown = useAuditBreakdown({ groupBy: "tool_name", limit: 8, startTime, endTime });
  const userBreakdown = useAuditBreakdown({ groupBy: "user_id", limit: 5, startTime, endTime });
  const recentErrors = useAuditEvents({ perPage: 5, success: false });
  const performance = useAuditPerformance({ startTime, endTime });

  const o = overview.data;

  return (
    <div className="space-y-6">
      {/* Time Range */}
      <div className="flex items-center gap-1">
        {presets.map((p) => (
          <button
            key={p.value}
            onClick={() => setPreset(p.value)}
            className={`rounded-md px-3 py-1.5 text-xs font-medium transition-colors ${
              preset === p.value
                ? "bg-primary text-primary-foreground"
                : "text-muted-foreground hover:bg-muted"
            }`}
          >
            {p.label}
          </button>
        ))}
      </div>

      {/* Summary Cards */}
      <div className="grid grid-cols-2 gap-4 md:grid-cols-4 lg:grid-cols-7">
        <StatCard
          label="Total Calls"
          value={o?.total_calls?.toLocaleString() ?? "-"}
        />
        <StatCard
          label="Success Rate"
          value={o ? `${(o.success_rate * 100).toFixed(1)}%` : "-"}
        />
        <StatCard
          label="Avg Duration"
          value={o ? formatDuration(o.avg_duration_ms) : "-"}
        />
        <StatCard
          label="Unique Users"
          value={o?.unique_users ?? "-"}
        />
        <StatCard
          label="Unique Tools"
          value={o?.unique_tools ?? "-"}
        />
        <StatCard
          label="Enrichment"
          value={o ? `${(o.enrichment_rate * 100).toFixed(0)}%` : "-"}
        />
        <StatCard
          label="Errors"
          value={o?.error_count ?? "-"}
          className={o && o.error_count > 0 ? "border-red-200" : undefined}
        />
      </div>

      {/* Activity Chart */}
      <div className="rounded-lg border bg-card p-4">
        <h2 className="mb-3 text-sm font-medium">Activity</h2>
        <TimeseriesChart data={timeseries.data} isLoading={timeseries.isLoading} />
      </div>

      {/* Charts Grid */}
      <div className="grid gap-6 lg:grid-cols-2">
        <div className="rounded-lg border bg-card p-4">
          <h2 className="mb-3 text-sm font-medium">Top Tools</h2>
          <BreakdownBarChart data={toolBreakdown.data} isLoading={toolBreakdown.isLoading} />
        </div>
        <div className="rounded-lg border bg-card p-4">
          <h2 className="mb-3 text-sm font-medium">Top Users</h2>
          <BreakdownBarChart
            data={userBreakdown.data}
            isLoading={userBreakdown.isLoading}
            color="hsl(221, 83%, 53%)"
          />
        </div>
      </div>

      {/* Bottom Row */}
      <div className="grid gap-6 lg:grid-cols-2">
        {/* Performance */}
        {performance.data && (
          <div className="rounded-lg border bg-card p-4">
            <h2 className="mb-3 text-sm font-medium">Performance</h2>
            <div className="grid grid-cols-3 gap-3">
              <div>
                <p className="text-xs text-muted-foreground">P50</p>
                <p className="text-lg font-semibold">{formatDuration(performance.data.p50_ms)}</p>
              </div>
              <div>
                <p className="text-xs text-muted-foreground">P95</p>
                <p className="text-lg font-semibold">{formatDuration(performance.data.p95_ms)}</p>
              </div>
              <div>
                <p className="text-xs text-muted-foreground">P99</p>
                <p className="text-lg font-semibold">{formatDuration(performance.data.p99_ms)}</p>
              </div>
              <div>
                <p className="text-xs text-muted-foreground">Avg</p>
                <p className="text-lg font-semibold">{formatDuration(performance.data.avg_ms)}</p>
              </div>
              <div>
                <p className="text-xs text-muted-foreground">Max</p>
                <p className="text-lg font-semibold">{formatDuration(performance.data.max_ms)}</p>
              </div>
              <div>
                <p className="text-xs text-muted-foreground">Avg Resp</p>
                <p className="text-lg font-semibold">{performance.data.avg_response_chars.toFixed(0)} chars</p>
              </div>
            </div>
          </div>
        )}

        {/* Recent Errors */}
        <div className="rounded-lg border bg-card p-4">
          <h2 className="mb-3 text-sm font-medium">Recent Errors</h2>
          <RecentErrorsList events={recentErrors.data?.data} />
        </div>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Events Tab — Sortable/Filterable Table
// ---------------------------------------------------------------------------

const COLUMNS: readonly {
  key: AuditSortColumn;
  label: string;
  thClass: string;
  spanClass: string;
}[] = [
  { key: "timestamp",          label: "Timestamp",  thClass: "text-left",   spanClass: "" },
  { key: "user_id",            label: "User",       thClass: "text-left",   spanClass: "" },
  { key: "tool_name",          label: "Tool",       thClass: "text-left",   spanClass: "" },
  { key: "toolkit_kind",       label: "Toolkit",    thClass: "text-left",   spanClass: "" },
  { key: "connection",         label: "Connection", thClass: "text-left",   spanClass: "" },
  { key: "duration_ms",        label: "Duration",   thClass: "text-right",  spanClass: "justify-end" },
  { key: "success",            label: "Status",     thClass: "text-center", spanClass: "justify-center" },
  { key: "enrichment_applied", label: "Enriched",   thClass: "text-center", spanClass: "justify-center" },
];

function EventsTab() {
  const [page, setPage] = useState(1);
  const [userId, setUserId] = useState("");
  const [toolName, setToolName] = useState("");
  const [search, setSearch] = useState("");
  const [successFilter, setSuccessFilter] = useState<string>("");
  const [sortBy, setSortBy] = useState<AuditSortColumn>("timestamp");
  const [sortOrder, setSortOrder] = useState<SortOrder>("desc");
  const [selectedEvent, setSelectedEvent] = useState<AuditEvent | null>(null);

  const { data: filters } = useAuditFilters();

  const handleSort = useCallback(
    (column: AuditSortColumn) => {
      if (sortBy === column) {
        setSortOrder((prev) => (prev === "asc" ? "desc" : "asc"));
      } else {
        setSortBy(column);
        setSortOrder(column === "timestamp" ? "desc" : "asc");
      }
      setPage(1);
    },
    [sortBy],
  );

  const params = useMemo(
    () => ({
      page,
      perPage: PER_PAGE,
      userId: userId || undefined,
      toolName: toolName || undefined,
      search: search || undefined,
      sortBy,
      sortOrder,
      success:
        successFilter === ""
          ? null
          : successFilter === "true",
    }),
    [page, userId, toolName, search, sortBy, sortOrder, successFilter],
  );

  const { data, isLoading } = useAuditEvents(params);
  const totalPages = data ? Math.ceil(data.total / PER_PAGE) : 0;

  const handleExport = useCallback(
    (format: "csv" | "json") => {
      if (!data?.data) return;
      let content: string;
      let mimeType: string;
      let ext: string;

      if (format === "json") {
        content = JSON.stringify(data.data, null, 2);
        mimeType = "application/json";
        ext = "json";
      } else {
        const headers = [
          "timestamp",
          "user_id",
          "tool_name",
          "toolkit_kind",
          "connection",
          "duration_ms",
          "success",
          "enrichment_applied",
          "error_message",
        ];
        const rows = data.data.map((e) =>
          [
            e.timestamp,
            e.user_id,
            e.tool_name,
            e.toolkit_kind,
            e.connection,
            e.duration_ms,
            e.success,
            e.enrichment_applied,
            `"${(e.error_message ?? "").replace(/"/g, '""')}"`,
          ].join(","),
        );
        content = [headers.join(","), ...rows].join("\n");
        mimeType = "text/csv";
        ext = "csv";
      }

      const blob = new Blob([content], { type: mimeType });
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = `audit-events.${ext}`;
      a.click();
      URL.revokeObjectURL(url);
    },
    [data],
  );

  return (
    <>
      {/* Filters */}
      <div className="flex flex-wrap items-center gap-3">
        <input
          type="text"
          value={search}
          onChange={(e) => {
            setSearch(e.target.value);
            setPage(1);
          }}
          placeholder="Search events..."
          className="w-56 rounded-md border bg-background px-3 py-1.5 text-sm outline-none ring-ring focus:ring-2"
        />
        <select
          value={userId}
          onChange={(e) => {
            setUserId(e.target.value);
            setPage(1);
          }}
          className="rounded-md border bg-background px-3 py-1.5 text-sm outline-none ring-ring focus:ring-2"
        >
          <option value="">All Users</option>
          {filters?.users.map((u) => (
            <option key={u} value={u}>
              {filters.user_labels?.[u] || formatUser(u)}
            </option>
          ))}
        </select>
        <select
          value={toolName}
          onChange={(e) => {
            setToolName(e.target.value);
            setPage(1);
          }}
          className="rounded-md border bg-background px-3 py-1.5 text-sm outline-none ring-ring focus:ring-2"
        >
          <option value="">All Tools</option>
          {filters?.tools.map((t) => (
            <option key={t} value={t}>
              {t}
            </option>
          ))}
        </select>
        <select
          value={successFilter}
          onChange={(e) => {
            setSuccessFilter(e.target.value);
            setPage(1);
          }}
          className="rounded-md border bg-background px-3 py-1.5 text-sm outline-none ring-ring focus:ring-2"
        >
          <option value="">All Status</option>
          <option value="true">Success</option>
          <option value="false">Failed</option>
        </select>

        <div className="ml-auto flex gap-2">
          <button
            onClick={() => handleExport("csv")}
            disabled={!data?.data.length}
            className="rounded-md border bg-background px-3 py-1.5 text-xs font-medium text-muted-foreground hover:bg-muted disabled:opacity-50"
          >
            Export CSV
          </button>
          <button
            onClick={() => handleExport("json")}
            disabled={!data?.data.length}
            className="rounded-md border bg-background px-3 py-1.5 text-xs font-medium text-muted-foreground hover:bg-muted disabled:opacity-50"
          >
            Export JSON
          </button>
        </div>
      </div>

      {/* Table */}
      <div className="overflow-auto rounded-lg border bg-card">
        <table className="w-full text-sm">
          <thead>
            <tr className="border-b bg-muted/50">
              {COLUMNS.map((col) => {
                const active = sortBy === col.key;
                return (
                  <th
                    key={col.key}
                    onClick={() => handleSort(col.key)}
                    className={`cursor-pointer select-none px-3 py-2 font-medium ${col.thClass} hover:bg-muted/80`}
                  >
                    <span className={`inline-flex items-center gap-1 ${col.spanClass}`}>
                      {col.label}
                      {active ? (
                        sortOrder === "asc" ? (
                          <ChevronUp className="h-3 w-3 text-foreground" />
                        ) : (
                          <ChevronDown className="h-3 w-3 text-foreground" />
                        )
                      ) : (
                        <ChevronsUpDown className="h-3 w-3 text-muted-foreground/50" />
                      )}
                    </span>
                  </th>
                );
              })}
            </tr>
          </thead>
          <tbody>
            {isLoading && (
              <tr>
                <td colSpan={8} className="px-3 py-8 text-center text-muted-foreground">
                  Loading...
                </td>
              </tr>
            )}
            {data?.data.map((event) => (
              <tr
                key={event.id}
                onClick={() => setSelectedEvent(event)}
                className="cursor-pointer border-b transition-colors hover:bg-muted/50"
              >
                <td className="px-3 py-2 text-xs">
                  {new Date(event.timestamp).toLocaleString()}
                </td>
                <td className="px-3 py-2" title={event.user_id}>
                  {formatUser(event.user_id, event.user_email)}
                </td>
                <td className="px-3 py-2 font-mono text-xs">{event.tool_name}</td>
                <td className="px-3 py-2">{event.toolkit_kind}</td>
                <td className="px-3 py-2 text-xs">{event.connection}</td>
                <td className="px-3 py-2 text-right">{formatDuration(event.duration_ms)}</td>
                <td className="px-3 py-2 text-center">
                  <StatusBadge variant={event.success ? "success" : "error"}>
                    {event.success ? "OK" : "ERR"}
                  </StatusBadge>
                </td>
                <td className="px-3 py-2 text-center">
                  {event.enrichment_applied ? (
                    <StatusBadge variant="success">Yes</StatusBadge>
                  ) : (
                    <StatusBadge variant="neutral">No</StatusBadge>
                  )}
                </td>
              </tr>
            ))}
            {data?.data.length === 0 && (
              <tr>
                <td colSpan={8} className="px-3 py-8 text-center text-muted-foreground">
                  No events found
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
            Showing {((page - 1) * PER_PAGE) + 1}--{Math.min(page * PER_PAGE, data?.total ?? 0)} of{" "}
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
      {selectedEvent && (
        <EventDrawer
          event={selectedEvent}
          onClose={() => setSelectedEvent(null)}
        />
      )}
    </>
  );
}

// ---------------------------------------------------------------------------
// Help Tab — Audit logging documentation
// ---------------------------------------------------------------------------

function AuditHelpTab() {
  return (
    <div className="max-w-3xl space-y-8">
      <section>
        <h2 className="mb-2 text-lg font-semibold">What is Audit Logging?</h2>
        <p className="text-sm leading-relaxed text-muted-foreground">
          The audit system records every MCP tool call made through the platform.
          Each event captures who made the call, which tool was invoked, the
          parameters used, how long it took, whether it succeeded, and whether
          semantic enrichment was applied. This provides a complete trail of all
          AI assistant activity for compliance, debugging, and analytics.
        </p>
      </section>

      <section>
        <h2 className="mb-2 text-lg font-semibold">What Gets Logged</h2>
        <p className="mb-3 text-sm leading-relaxed text-muted-foreground">
          Every tool call generates an audit event with these fields:
        </p>
        <div className="overflow-auto rounded-lg border">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b bg-muted/50">
                <th className="px-3 py-2 text-left font-medium">Field</th>
                <th className="px-3 py-2 text-left font-medium">Description</th>
              </tr>
            </thead>
            <tbody>
              <tr className="border-b">
                <td className="px-3 py-2 font-mono text-xs">timestamp</td>
                <td className="px-3 py-2 text-xs">When the call was made (UTC)</td>
              </tr>
              <tr className="border-b">
                <td className="px-3 py-2 font-mono text-xs">user_id</td>
                <td className="px-3 py-2 text-xs">Authenticated user identity (from token or API key name)</td>
              </tr>
              <tr className="border-b">
                <td className="px-3 py-2 font-mono text-xs">persona</td>
                <td className="px-3 py-2 text-xs">The persona assigned to the user for this call</td>
              </tr>
              <tr className="border-b">
                <td className="px-3 py-2 font-mono text-xs">tool_name</td>
                <td className="px-3 py-2 text-xs">The MCP tool that was called (e.g., trino_query)</td>
              </tr>
              <tr className="border-b">
                <td className="px-3 py-2 font-mono text-xs">toolkit_kind / toolkit_name</td>
                <td className="px-3 py-2 text-xs">The toolkit type and instance name</td>
              </tr>
              <tr className="border-b">
                <td className="px-3 py-2 font-mono text-xs">connection</td>
                <td className="px-3 py-2 text-xs">The connection identifier used for this call</td>
              </tr>
              <tr className="border-b">
                <td className="px-3 py-2 font-mono text-xs">duration_ms</td>
                <td className="px-3 py-2 text-xs">Total execution time in milliseconds</td>
              </tr>
              <tr className="border-b">
                <td className="px-3 py-2 font-mono text-xs">success</td>
                <td className="px-3 py-2 text-xs">Whether the call completed without error</td>
              </tr>
              <tr className="border-b">
                <td className="px-3 py-2 font-mono text-xs">enrichment_applied</td>
                <td className="px-3 py-2 text-xs">Whether semantic enrichment was added to the response</td>
              </tr>
              <tr className="border-b">
                <td className="px-3 py-2 font-mono text-xs">parameters</td>
                <td className="px-3 py-2 text-xs">The parameters passed to the tool (JSON)</td>
              </tr>
              <tr className="border-b">
                <td className="px-3 py-2 font-mono text-xs">request_chars / response_chars</td>
                <td className="px-3 py-2 text-xs">Size of the request and response payloads</td>
              </tr>
              <tr>
                <td className="px-3 py-2 font-mono text-xs">error_message</td>
                <td className="px-3 py-2 text-xs">Error details when the call fails</td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>

      <section>
        <h2 className="mb-2 text-lg font-semibold">Overview Dashboard</h2>
        <p className="text-sm leading-relaxed text-muted-foreground">
          The Overview tab provides a visual summary of audit activity within a
          selected time range (1h, 6h, 24h, 7d). It shows total calls, success
          rate, average duration, unique users/tools, enrichment rate, and error
          count. The activity chart visualizes call volume over time, and
          breakdown charts show the most-used tools and most-active users.
          Performance metrics (P50, P95, P99) help identify latency issues.
        </p>
      </section>

      <section>
        <h2 className="mb-2 text-lg font-semibold">Events Table</h2>
        <p className="mb-3 text-sm leading-relaxed text-muted-foreground">
          The Events tab provides a detailed, sortable table of all audit events
          with the following capabilities:
        </p>
        <ul className="list-inside list-disc space-y-1 text-sm text-muted-foreground">
          <li>
            <strong>Search</strong> &mdash; Full-text search across event fields
          </li>
          <li>
            <strong>Filters</strong> &mdash; Filter by user, tool name, and
            success/failure status
          </li>
          <li>
            <strong>Sorting</strong> &mdash; Click any column header to sort
            ascending or descending
          </li>
          <li>
            <strong>Pagination</strong> &mdash; Navigate through results 20 at a
            time
          </li>
          <li>
            <strong>Detail drawer</strong> &mdash; Click any row to view full
            event details including parameters
          </li>
          <li>
            <strong>Export</strong> &mdash; Download the current filtered view as
            CSV or JSON
          </li>
        </ul>
      </section>

      <section>
        <h2 className="mb-2 text-lg font-semibold">Retention & Storage</h2>
        <p className="text-sm leading-relaxed text-muted-foreground">
          Audit events are stored in PostgreSQL. Retention is configurable via
          the{" "}
          <code className="rounded bg-muted px-1 py-0.5 text-xs">
            audit.retention_days
          </code>{" "}
          setting (default: 90 days). Events older than the retention period are
          automatically purged. Audit logging is enabled via{" "}
          <code className="rounded bg-muted px-1 py-0.5 text-xs">
            audit.enabled: true
          </code>{" "}
          and{" "}
          <code className="rounded bg-muted px-1 py-0.5 text-xs">
            audit.log_tool_calls: true
          </code>{" "}
          in the platform configuration.
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
                <td className="px-3 py-2 font-mono text-xs">GET /audit/events</td>
                <td className="px-3 py-2 text-xs">
                  Paginated event list with filter, sort, and search support
                </td>
              </tr>
              <tr className="border-b">
                <td className="px-3 py-2 font-mono text-xs">GET /audit/overview</td>
                <td className="px-3 py-2 text-xs">
                  Summary statistics for a time range
                </td>
              </tr>
              <tr className="border-b">
                <td className="px-3 py-2 font-mono text-xs">GET /audit/timeseries</td>
                <td className="px-3 py-2 text-xs">
                  Time-bucketed activity data for charting
                </td>
              </tr>
              <tr className="border-b">
                <td className="px-3 py-2 font-mono text-xs">GET /audit/breakdown</td>
                <td className="px-3 py-2 text-xs">
                  Group-by aggregations (tool_name, user_id, etc.)
                </td>
              </tr>
              <tr className="border-b">
                <td className="px-3 py-2 font-mono text-xs">GET /audit/performance</td>
                <td className="px-3 py-2 text-xs">
                  Latency percentiles (P50, P95, P99) and response size stats
                </td>
              </tr>
              <tr>
                <td className="px-3 py-2 font-mono text-xs">GET /audit/filters</td>
                <td className="px-3 py-2 text-xs">
                  Distinct values for filter dropdowns (users, tools)
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>
    </div>
  );
}
