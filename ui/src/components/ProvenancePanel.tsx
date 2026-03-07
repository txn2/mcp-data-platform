import { useState } from "react";
import {
  Database,
  Search,
  FileText,
  Info,
  Link2,
  Terminal,
  type LucideIcon,
} from "lucide-react";
import * as Dialog from "@radix-ui/react-dialog";
import type { Provenance, ProvenanceToolCall } from "@/api/portal/types";

interface Props {
  provenance: Provenance;
}

interface ToolMeta {
  label: string;
  icon: LucideIcon;
}

const TOOL_LABELS: Record<string, ToolMeta> = {
  trino_query: { label: "SQL Query", icon: Database },
  trino_execute: { label: "SQL Execute", icon: Database },
  trino_describe_table: { label: "Describe Table", icon: Database },
  trino_list_tables: { label: "List Tables", icon: Database },
  trino_list_schemas: { label: "List Schemas", icon: Database },
  trino_list_catalogs: { label: "List Catalogs", icon: Database },
  trino_explain: { label: "Query Plan", icon: Database },
  datahub_search: { label: "Catalog Search", icon: Search },
  datahub_get_schema: { label: "Schema Lookup", icon: FileText },
  datahub_get_entity: { label: "Entity Details", icon: Info },
  datahub_get_lineage: { label: "Lineage", icon: Link2 },
  datahub_get_column_lineage: { label: "Column Lineage", icon: Link2 },
  datahub_get_queries: { label: "Saved Queries", icon: FileText },
  datahub_get_data_product: { label: "Data Product", icon: Info },
  datahub_get_glossary_term: { label: "Glossary Term", icon: FileText },
  datahub_list_data_products: { label: "Data Products", icon: Search },
  datahub_list_domains: { label: "Domains", icon: Search },
  datahub_list_tags: { label: "Tags", icon: Search },
  platform_info: { label: "Platform Info", icon: Info },
  s3_list_objects: { label: "List Files", icon: FileText },
  s3_get_object: { label: "Get File", icon: FileText },
  s3_list_buckets: { label: "List Buckets", icon: FileText },
};

function getToolMeta(toolName: string): ToolMeta {
  return TOOL_LABELS[toolName] ?? { label: toolName, icon: Terminal };
}

/** Extract a human-readable summary from the raw summary JSON string. */
function extractSummary(call: ProvenanceToolCall): string | null {
  const raw = call.summary;
  if (!raw) return null;

  // Try to parse as JSON to extract useful fields
  try {
    const parsed = JSON.parse(raw);
    if (typeof parsed === "string") return parsed;

    // SQL queries
    if (parsed.sql) {
      const sql = String(parsed.sql).trim();
      return sql.length > 120 ? sql.slice(0, 120) + "..." : sql;
    }

    // Search queries
    if (parsed.query) return `"${parsed.query}"`;

    // URN-based lookups
    if (parsed.urn) return String(parsed.urn);

    // Table operations
    if (parsed.table) {
      const parts = [parsed.catalog, parsed.schema, parsed.table].filter(Boolean);
      return parts.join(".");
    }

    // Bucket/key for S3
    if (parsed.bucket) {
      return parsed.key ? `${parsed.bucket}/${parsed.key}` : parsed.bucket;
    }

    // Fall back to first string value
    const firstStr = Object.values(parsed).find((v) => typeof v === "string");
    if (firstStr) return String(firstStr);
  } catch {
    // Not JSON — use as-is if short enough
    if (raw.length <= 150) return raw;
    return raw.slice(0, 147) + "...";
  }

  return null;
}

/** Pretty-print the raw summary for the detail modal. */
function formatDetail(summary: string | undefined): string {
  if (!summary) return "(no parameters)";
  try {
    return JSON.stringify(JSON.parse(summary), null, 2);
  } catch {
    return summary;
  }
}

function relativeTime(timestamp: string): string {
  const now = Date.now();
  const then = new Date(timestamp).getTime();
  const diff = Math.max(0, now - then);
  const seconds = Math.floor(diff / 1000);
  if (seconds < 60) return "just now";
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes} min ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  const days = Math.floor(hours / 24);
  return `${days}d ago`;
}

function ProvenanceCard({
  call,
  onClick,
}: {
  call: ProvenanceToolCall;
  onClick: () => void;
}) {
  const meta = getToolMeta(call.tool_name);
  const Icon = meta.icon;
  const summary = extractSummary(call);

  return (
    <button
      type="button"
      onClick={onClick}
      className="w-full text-left rounded-md border bg-background p-3 transition-colors hover:bg-muted/50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
    >
      <div className="flex items-start gap-2.5">
        <div className="mt-0.5 rounded bg-muted p-1.5">
          <Icon className="h-3.5 w-3.5 text-muted-foreground" />
        </div>
        <div className="min-w-0 flex-1">
          <div className="flex items-center justify-between gap-2">
            <span className="text-sm font-medium">{meta.label}</span>
            <span
              className="shrink-0 text-[11px] text-muted-foreground"
              title={new Date(call.timestamp).toLocaleString()}
            >
              {relativeTime(call.timestamp)}
            </span>
          </div>
          {summary && (
            <p className="mt-0.5 truncate text-xs text-muted-foreground font-mono">
              {summary}
            </p>
          )}
        </div>
      </div>
    </button>
  );
}

function DetailModal({
  call,
  open,
  onOpenChange,
}: {
  call: ProvenanceToolCall | null;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  if (!call) return null;
  const meta = getToolMeta(call.tool_name);
  const Icon = meta.icon;
  const detail = formatDetail(call.summary);

  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 z-50 bg-black/40" />
        <Dialog.Content className="fixed left-1/2 top-1/2 z-50 w-full max-w-lg -translate-x-1/2 -translate-y-1/2 rounded-lg border bg-card p-6 shadow-lg focus:outline-none">
          <Dialog.Title className="flex items-center gap-2 text-base font-semibold">
            <Icon className="h-4 w-4 text-muted-foreground" />
            {meta.label}
          </Dialog.Title>
          <Dialog.Description className="mt-1 text-xs text-muted-foreground">
            {call.tool_name} &middot; {new Date(call.timestamp).toLocaleString()}
          </Dialog.Description>

          <div className="mt-4">
            <p className="mb-1.5 text-xs font-medium text-muted-foreground">
              {call.tool_name.startsWith("trino_") && detail.includes("SELECT")
                ? "SQL Query"
                : "Parameters"}
            </p>
            <pre className="max-h-72 overflow-auto rounded-md bg-muted p-3 text-xs font-mono whitespace-pre-wrap break-words">
              {detail}
            </pre>
          </div>

          <div className="mt-4 flex justify-end">
            <Dialog.Close asChild>
              <button
                type="button"
                className="rounded-md bg-secondary px-3 py-1.5 text-sm font-medium text-secondary-foreground hover:bg-secondary/80"
              >
                Close
              </button>
            </Dialog.Close>
          </div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}

export function ProvenancePanel({ provenance }: Props) {
  const calls = provenance.tool_calls ?? [];
  const [selected, setSelected] = useState<ProvenanceToolCall | null>(null);

  if (calls.length === 0) {
    return (
      <p className="text-sm text-muted-foreground">No provenance data available.</p>
    );
  }

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between">
        <h3 className="text-sm font-medium">Provenance</h3>
        <span className="text-xs text-muted-foreground">
          {calls.length} {calls.length === 1 ? "call" : "calls"}
        </span>
      </div>

      <div className="space-y-2">
        {calls.map((call, i) => (
          <ProvenanceCard
            key={i}
            call={call}
            onClick={() => setSelected(call)}
          />
        ))}
      </div>

      <DetailModal
        call={selected}
        open={selected !== null}
        onOpenChange={(open) => {
          if (!open) setSelected(null);
        }}
      />
    </div>
  );
}
