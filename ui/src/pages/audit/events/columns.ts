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
  /** Column id, and the API sort key unless `sortable` is false. */
  key: AuditSortColumn | "purpose";
  label: string;
  className: string;
  /**
   * Purpose is free prose the agent wrote (#1317): alphabetical order over it
   * means nothing, so its header does not sort. Search does cover it, which is
   * how an operator actually finds a purpose.
   */
  sortable?: boolean;
}[] = [
  { key: "timestamp", label: "Timestamp", className: "" },
  { key: "user_id", label: "User", className: "" },
  { key: "tool_name", label: "Tool", className: "" },
  { key: "purpose", label: "Purpose", className: "", sortable: false },
  { key: "toolkit_kind", label: "Toolkit", className: "" },
  { key: "source", label: "Source", className: "" },
  { key: "connection", label: "Connection", className: "" },
  // The header's label is an inline-flex span, so the column's alignment is
  // set on the cell and the span follows it.
  { key: "duration_ms", label: "Duration", className: "text-right" },
  { key: "success", label: "Status", className: "text-center" },
  { key: "enrichment_applied", label: "Enriched", className: "text-center" },
];
