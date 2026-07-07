import { useState, useMemo, useCallback } from "react";
import { useAuditEvents, useAuditFilters, useToolTitleMap } from "@/api/admin/hooks";
import { StatusBadge } from "@/components/cards/StatusBadge";
import { EventDrawer } from "@/components/EventDrawer";
import type { AuditEvent, AuditSortColumn, SortOrder } from "@/api/admin/types";
import { ChevronUp, ChevronDown, ChevronsUpDown } from "lucide-react";
import { formatDuration } from "@/lib/formatDuration";
import { formatToolName } from "@/lib/formatToolName";
import { formatUser } from "@/lib/formatUser";

const PER_PAGE = 20;

// sourceLabel maps the audit Source enum to a human-readable hover tooltip.
// Keep aligned with pkg/middleware/mcp.go: SourceMCP, SourceAdmin, SourceREST.
function sourceLabel(source?: string): string {
  switch (source) {
    case "mcp":
      return "Agent via MCP transport";
    case "rest":
      return "External automation via gateway REST shim (e.g. NiFi, cronjobs)";
    case "admin":
      return "Portal-driven tool execution via admin REST API";
    default:
      return source ?? "";
  }
}

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
  { key: "source",             label: "Source",     thClass: "text-left",   spanClass: "" },
  { key: "connection",         label: "Connection", thClass: "text-left",   spanClass: "" },
  { key: "duration_ms",        label: "Duration",   thClass: "text-right",  spanClass: "justify-end" },
  { key: "success",            label: "Status",     thClass: "text-center", spanClass: "justify-center" },
  { key: "enrichment_applied", label: "Enriched",   thClass: "text-center", spanClass: "justify-center" },
];

export function EventsTab({ onNavigate }: { onNavigate?: (path: string) => void }) {
  const [page, setPage] = useState(1);
  const [userId, setUserId] = useState("");
  const [toolName, setToolName] = useState("");
  const [toolkitKind, setToolkitKind] = useState("");
  const [source, setSource] = useState("");
  const [search, setSearch] = useState("");
  const [successFilter, setSuccessFilter] = useState<string>("");
  const [sortBy, setSortBy] = useState<AuditSortColumn>("timestamp");
  const [sortOrder, setSortOrder] = useState<SortOrder>("desc");
  const [selectedEvent, setSelectedEvent] = useState<AuditEvent | null>(null);
  const titleMap = useToolTitleMap();

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
      toolkitKind: toolkitKind || undefined,
      source: source || undefined,
      search: search || undefined,
      sortBy,
      sortOrder,
      success:
        successFilter === ""
          ? null
          : successFilter === "true",
    }),
    [page, userId, toolName, toolkitKind, source, search, sortBy, sortOrder, successFilter],
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
          "source",
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
            e.source,
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
          {filters?.users?.map((u) => (
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
          {filters?.tools?.map((t) => (
            <option key={t} value={t}>
              {t}
            </option>
          ))}
        </select>
        <select
          value={toolkitKind}
          onChange={(e) => {
            setToolkitKind(e.target.value);
            setPage(1);
          }}
          className="rounded-md border bg-background px-3 py-1.5 text-sm outline-none ring-ring focus:ring-2"
          title="Filter by toolkit kind (api, trino, datahub, s3, memory)"
        >
          <option value="">All Toolkits</option>
          {filters?.toolkit_kinds?.map((k) => (
            <option key={k} value={k}>
              {k}
            </option>
          ))}
        </select>
        <select
          value={source}
          onChange={(e) => {
            setSource(e.target.value);
            setPage(1);
          }}
          className="rounded-md border bg-background px-3 py-1.5 text-sm outline-none ring-ring focus:ring-2"
          title="mcp: agents over MCP. rest: NiFi/cronjobs via gateway REST shim. admin: portal-driven tool runs."
        >
          <option value="">All Sources</option>
          {filters?.sources?.map((s) => (
            <option key={s} value={s}>
              {s}
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
                <td className="px-3 py-2 text-xs" title={event.tool_name}>{formatToolName(event.tool_name, titleMap[event.tool_name])}</td>
                <td className="px-3 py-2">{event.toolkit_kind}</td>
                <td className="px-3 py-2 text-xs" title={sourceLabel(event.source)}>{event.source || "-"}</td>
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
                <td colSpan={COLUMNS.length} className="px-3 py-8 text-center text-muted-foreground">
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
          onNavigate={onNavigate}
        />
      )}
    </>
  );
}
