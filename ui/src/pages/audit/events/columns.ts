import type { AuditSortColumn } from "@/api/admin/types";

// The audit events table's column set and the copy that explains the values
// in them. Extracted from EventsTab.tsx (#1207).

// sourceLabel maps the audit Source enum to a human-readable hover tooltip.
// Keep aligned with pkg/middleware/mcp.go: SourceMCP, SourceAdmin, SourceREST.
export function sourceLabel(source?: string): string {
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

export const COLUMNS: readonly {
  key: AuditSortColumn;
  label: string;
  className: string;
}[] = [
  { key: "timestamp", label: "Timestamp", className: "" },
  { key: "user_id", label: "User", className: "" },
  { key: "tool_name", label: "Tool", className: "" },
  { key: "toolkit_kind", label: "Toolkit", className: "" },
  { key: "source", label: "Source", className: "" },
  { key: "connection", label: "Connection", className: "" },
  // The header's label is an inline-flex span, so the column's alignment is
  // set on the cell and the span follows it.
  { key: "duration_ms", label: "Duration", className: "text-right" },
  { key: "success", label: "Status", className: "text-center" },
  { key: "enrichment_applied", label: "Enriched", className: "text-center" },
];
