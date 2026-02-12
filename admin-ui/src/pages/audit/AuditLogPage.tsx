import { useState, useMemo, useCallback } from "react";
import { useAuditEvents } from "@/api/hooks";
import { useTimeRangeStore } from "@/stores/timerange";
import { StatusBadge } from "@/components/cards/StatusBadge";
import type { AuditEvent } from "@/api/types";

const PER_PAGE = 20;

export function AuditLogPage() {
  const { getStartTime, getEndTime } = useTimeRangeStore();
  const [page, setPage] = useState(1);
  const [userId, setUserId] = useState("");
  const [toolName, setToolName] = useState("");
  const [successFilter, setSuccessFilter] = useState<string>("");
  const [selectedEvent, setSelectedEvent] = useState<AuditEvent | null>(null);

  const params = useMemo(
    () => ({
      page,
      perPage: PER_PAGE,
      userId: userId || undefined,
      toolName: toolName || undefined,
      success:
        successFilter === ""
          ? null
          : successFilter === "true",
      startTime: getStartTime(),
      endTime: getEndTime(),
    }),
    [page, userId, toolName, successFilter, getStartTime, getEndTime],
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
            `"${e.error_message.replace(/"/g, '""')}"`,
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
    <div className="space-y-4">
      {/* Filters */}
      <div className="flex flex-wrap items-center gap-3">
        <input
          type="text"
          value={userId}
          onChange={(e) => {
            setUserId(e.target.value);
            setPage(1);
          }}
          placeholder="Filter by user..."
          className="rounded-md border bg-background px-3 py-1.5 text-sm outline-none ring-ring focus:ring-2"
        />
        <input
          type="text"
          value={toolName}
          onChange={(e) => {
            setToolName(e.target.value);
            setPage(1);
          }}
          placeholder="Filter by tool..."
          className="rounded-md border bg-background px-3 py-1.5 text-sm outline-none ring-ring focus:ring-2"
        />
        <select
          value={successFilter}
          onChange={(e) => {
            setSuccessFilter(e.target.value);
            setPage(1);
          }}
          className="rounded-md border bg-background px-3 py-1.5 text-sm outline-none ring-ring focus:ring-2"
        >
          <option value="">All</option>
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
              <th className="px-3 py-2 text-left font-medium">Timestamp</th>
              <th className="px-3 py-2 text-left font-medium">User</th>
              <th className="px-3 py-2 text-left font-medium">Tool</th>
              <th className="px-3 py-2 text-left font-medium">Toolkit</th>
              <th className="px-3 py-2 text-left font-medium">Connection</th>
              <th className="px-3 py-2 text-right font-medium">Duration</th>
              <th className="px-3 py-2 text-center font-medium">Status</th>
              <th className="px-3 py-2 text-center font-medium">Enriched</th>
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
                <td className="px-3 py-2">{event.user_id}</td>
                <td className="px-3 py-2 font-mono text-xs">{event.tool_name}</td>
                <td className="px-3 py-2">{event.toolkit_kind}</td>
                <td className="px-3 py-2 text-xs">{event.connection}</td>
                <td className="px-3 py-2 text-right">{event.duration_ms}ms</td>
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
        <div className="fixed inset-0 z-50 flex justify-end">
          <div
            className="absolute inset-0 bg-black/50"
            onClick={() => setSelectedEvent(null)}
          />
          <div className="relative w-full max-w-lg overflow-auto bg-card p-6 shadow-xl">
            <div className="mb-4 flex items-center justify-between">
              <h2 className="text-lg font-semibold">Event Detail</h2>
              <button
                onClick={() => setSelectedEvent(null)}
                className="rounded-md px-2 py-1 text-sm hover:bg-muted"
              >
                Close
              </button>
            </div>

            <div className="space-y-4">
              <div className="grid grid-cols-2 gap-3 text-sm">
                <div>
                  <p className="text-xs text-muted-foreground">Event ID</p>
                  <p className="font-mono text-xs">{selectedEvent.id}</p>
                </div>
                <div>
                  <p className="text-xs text-muted-foreground">Timestamp</p>
                  <p>{new Date(selectedEvent.timestamp).toLocaleString()}</p>
                </div>
                <div>
                  <p className="text-xs text-muted-foreground">User</p>
                  <p>{selectedEvent.user_id}</p>
                </div>
                <div>
                  <p className="text-xs text-muted-foreground">Persona</p>
                  <p>{selectedEvent.persona || "-"}</p>
                </div>
                <div>
                  <p className="text-xs text-muted-foreground">Tool</p>
                  <p className="font-mono text-xs">{selectedEvent.tool_name}</p>
                </div>
                <div>
                  <p className="text-xs text-muted-foreground">Toolkit</p>
                  <p>
                    {selectedEvent.toolkit_kind} / {selectedEvent.toolkit_name}
                  </p>
                </div>
                <div>
                  <p className="text-xs text-muted-foreground">Connection</p>
                  <p>{selectedEvent.connection}</p>
                </div>
                <div>
                  <p className="text-xs text-muted-foreground">Duration</p>
                  <p>{selectedEvent.duration_ms}ms</p>
                </div>
                <div>
                  <p className="text-xs text-muted-foreground">Status</p>
                  <StatusBadge
                    variant={selectedEvent.success ? "success" : "error"}
                  >
                    {selectedEvent.success ? "Success" : "Error"}
                  </StatusBadge>
                </div>
                <div>
                  <p className="text-xs text-muted-foreground">Enriched</p>
                  <StatusBadge
                    variant={
                      selectedEvent.enrichment_applied ? "success" : "neutral"
                    }
                  >
                    {selectedEvent.enrichment_applied ? "Yes" : "No"}
                  </StatusBadge>
                </div>
                <div>
                  <p className="text-xs text-muted-foreground">Transport</p>
                  <p>{selectedEvent.transport}</p>
                </div>
                <div>
                  <p className="text-xs text-muted-foreground">Session</p>
                  <p className="font-mono text-xs">{selectedEvent.session_id}</p>
                </div>
              </div>

              <div className="grid grid-cols-3 gap-3 text-sm">
                <div>
                  <p className="text-xs text-muted-foreground">Request Chars</p>
                  <p>{selectedEvent.request_chars.toLocaleString()}</p>
                </div>
                <div>
                  <p className="text-xs text-muted-foreground">Response Chars</p>
                  <p>{selectedEvent.response_chars.toLocaleString()}</p>
                </div>
                <div>
                  <p className="text-xs text-muted-foreground">Content Blocks</p>
                  <p>{selectedEvent.content_blocks}</p>
                </div>
              </div>

              {selectedEvent.error_message && (
                <div>
                  <p className="text-xs text-muted-foreground">Error Message</p>
                  <p className="mt-1 rounded bg-red-50 p-2 text-sm text-red-800">
                    {selectedEvent.error_message}
                  </p>
                </div>
              )}

              {selectedEvent.parameters &&
                Object.keys(selectedEvent.parameters).length > 0 && (
                  <div>
                    <p className="mb-1 text-xs text-muted-foreground">
                      Parameters
                    </p>
                    <pre className="overflow-auto rounded bg-muted p-3 text-xs">
                      {JSON.stringify(selectedEvent.parameters, null, 2)}
                    </pre>
                  </div>
                )}
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
