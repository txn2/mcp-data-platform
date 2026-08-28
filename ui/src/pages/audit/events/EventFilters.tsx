import { FilterSelect } from "@/components/patterns/FilterSelect";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import type { AuditFiltersResponse } from "@/api/admin/types";
import { principalOptions } from "@/lib/formatUser";

// EventFilters is the events table's facet bar: free-text search, the five
// dimensions the audit index is queryable on, and the export of whatever the
// filters currently select. Extracted from EventsTab.tsx (#1207).

export interface EventFilterState {
  search: string;
  // sessionId narrows the table to one session's calls. It is free text
  // rather than a facet because the distinct session ids are unbounded; the
  // value arrives from the sessions list or the drawer's session link.
  sessionId: string;
  userId: string;
  toolName: string;
  toolkitKind: string;
  source: string;
  success: string;
}

export function EventFilters({
  filters,
  value,
  onChange,
  onExport,
  canExport,
}: {
  // The distinct values the audit index holds for each dimension.
  filters?: AuditFiltersResponse;
  value: EventFilterState;
  onChange: (patch: Partial<EventFilterState>) => void;
  onExport: (format: "csv" | "json") => void;
  canExport: boolean;
}) {
  return (
    <div className="flex flex-wrap items-center gap-3">
      <Input
        type="text"
        value={value.search}
        onChange={(e) => onChange({ search: e.target.value })}
        placeholder="Search events..."
        aria-label="Search events"
        className="h-8 w-56"
      />
      <FilterSelect
        label="Filter by user"
        className="max-w-56"
        value={value.userId}
        onChange={(userId) => onChange({ userId })}
        options={[
          { value: "", label: "All Users" },
          ...principalOptions(filters?.users, filters?.user_labels),
        ]}
      />
      <FilterSelect
        label="Filter by tool"
        value={value.toolName}
        onChange={(toolName) => onChange({ toolName })}
        options={[
          { value: "", label: "All Tools" },
          ...(filters?.tools ?? []).map((t) => ({ value: t, label: t })),
        ]}
      />
      <FilterSelect
        label="Filter by toolkit"
        title="Filter by toolkit kind (api, trino, datahub, s3, memory)"
        value={value.toolkitKind}
        onChange={(toolkitKind) => onChange({ toolkitKind })}
        options={[
          { value: "", label: "All Toolkits" },
          ...(filters?.toolkit_kinds ?? []).map((k) => ({ value: k, label: k })),
        ]}
      />
      <FilterSelect
        label="Filter by source"
        title="mcp: agents over MCP. rest: NiFi/cronjobs via gateway REST shim. admin: portal-driven tool runs."
        value={value.source}
        onChange={(source) => onChange({ source })}
        options={[
          { value: "", label: "All Sources" },
          ...(filters?.sources ?? []).map((s) => ({ value: s, label: s })),
        ]}
      />
      <Input
        type="text"
        value={value.sessionId}
        onChange={(e) => onChange({ sessionId: e.target.value })}
        placeholder="Session ID"
        aria-label="Filter by session ID"
        title="Show only the calls one session made. Open the session itself from the Sessions page."
        className="h-8 w-48 font-mono text-xs"
      />
      <FilterSelect
        label="Filter by status"
        value={value.success}
        onChange={(success) => onChange({ success })}
        options={[
          { value: "", label: "All Status" },
          { value: "true", label: "Success" },
          { value: "false", label: "Failed" },
        ]}
      />

      <div className="ml-auto flex gap-2">
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={() => onExport("csv")}
          disabled={!canExport}
          className="text-muted-foreground"
        >
          Export CSV
        </Button>
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={() => onExport("json")}
          disabled={!canExport}
          className="text-muted-foreground"
        >
          Export JSON
        </Button>
      </div>
    </div>
  );
}
