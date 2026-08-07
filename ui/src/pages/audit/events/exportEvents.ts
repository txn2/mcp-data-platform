import type { AuditEvent } from "@/api/admin/types";

// Download of the events currently on screen, in the two shapes an analyst
// asks for. Extracted from EventsTab.tsx (#1207) so the serialization is a
// pure function the component only has to hand a list to.

const CSV_HEADERS = [
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
] as const;

// toCSV renders the events as RFC 4180-ish CSV: only the free-text error is
// quoted, since it is the one field that can carry a comma or a quote.
export function toCSV(events: AuditEvent[]): string {
  const rows = events.map((e) =>
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
  return [CSV_HEADERS.join(","), ...rows].join("\n");
}

// downloadEvents serializes the events and hands them to the browser as a
// file. A no-op on an empty list: the callers disable their buttons then, and
// an empty download would look like a failure.
export function downloadEvents(events: AuditEvent[], format: "csv" | "json"): void {
  if (events.length === 0) return;
  const json = format === "json";
  const content = json ? JSON.stringify(events, null, 2) : toCSV(events);
  const blob = new Blob([content], { type: json ? "application/json" : "text/csv" });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = `audit-events.${json ? "json" : "csv"}`;
  a.click();
  URL.revokeObjectURL(url);
}
