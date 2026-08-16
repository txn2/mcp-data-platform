import { useState } from "react";
import {
  Database,
  Search,
  FileText,
  Info,
  Terminal,
  Copy,
  Check,
  History,
  type LucideIcon,
} from "lucide-react";
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import type { Provenance, ProvenanceToolCall } from "@/api/portal/types";
import { formatToolName } from "@/lib/formatToolName";

interface Props {
  provenance: Provenance;
  /**
   * Opens the session these calls belong to. The panel shows the calls the
   * asset captured at the moment it was saved; the session holds every call
   * that session made, before and after. Omitted where the reader cannot open
   * it — a session refuses anyone but its own caller and an admin (#1319).
   */
  onOpenSession?: () => void;
}

/** Map tool name prefixes to icons for provenance display. */
const TOOL_ICONS: Record<string, LucideIcon> = {
  trino_: Database,
  datahub_: Search,
  s3_: FileText,
  platform_: Info,
};

function getToolIcon(toolName: string): LucideIcon {
  for (const [prefix, icon] of Object.entries(TOOL_ICONS)) {
    if (toolName.startsWith(prefix)) return icon;
  }
  return Terminal;
}

/** Extract a human-readable summary from the tool call parameters. */
function extractSummary(call: ProvenanceToolCall): string | null {
  const params = call.parameters;
  if (!params || Object.keys(params).length === 0) return null;

  // SQL queries
  if (params.sql) {
    const sql = String(params.sql).trim();
    return sql.length > 120 ? sql.slice(0, 120) + "..." : sql;
  }

  // Search queries
  if (params.query) return `"${params.query}"`;

  // URN-based lookups
  if (params.urn) return String(params.urn);

  // Table operations
  if (params.table) {
    const parts = [params.catalog, params.schema, params.table].filter(Boolean);
    return parts.join(".");
  }

  // Bucket/key for S3
  if (params.bucket) {
    return params.key
      ? `${params.bucket}/${params.key}`
      : String(params.bucket);
  }

  // Fall back to first string value
  const firstStr = Object.values(params).find((v) => typeof v === "string");
  if (firstStr) return String(firstStr);

  return null;
}

/** Pretty-print the parameters for the detail modal. */
function formatDetail(params: Record<string, unknown> | undefined): string {
  if (!params || Object.keys(params).length === 0) return "(no parameters)";
  return JSON.stringify(params, null, 2);
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
  const Icon = getToolIcon(call.tool_name);
  const summary = extractSummary(call);

  return (
    <button
      type="button"
      onClick={onClick}
      className="w-full text-left rounded-md border bg-card p-3 transition-colors hover:bg-muted/50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
    >
      <div className="flex items-start gap-2.5">
        <div className="mt-0.5 rounded bg-muted p-1.5">
          <Icon className="h-3.5 w-3.5 text-muted-foreground" />
        </div>
        <div className="min-w-0 flex-1">
          <div className="flex items-center justify-between gap-2">
            <span className="text-sm font-medium">
              {formatToolName(call.tool_name)}
            </span>
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

/** Extract the SQL string from parameters, or null if not a SQL call. */
function extractSQL(call: ProvenanceToolCall): string | null {
  if (!call.tool_name.startsWith("trino_")) return null;
  if (!call.parameters?.sql) return null;
  return String(call.parameters.sql);
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
  const [copied, setCopied] = useState(false);

  if (!call) return null;
  const Icon = getToolIcon(call.tool_name);
  const sql = extractSQL(call);
  const detail = sql ?? formatDetail(call.parameters);

  const handleCopy = () => {
    const writeFallback = () => {
      const el = document.createElement("textarea");
      el.value = detail;
      document.body.appendChild(el);
      el.select();
      document.execCommand("copy");
      document.body.removeChild(el);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    };

    if (navigator.clipboard?.writeText) {
      navigator.clipboard.writeText(detail).then(() => {
        setCopied(true);
        setTimeout(() => setCopied(false), 2000);
      }, writeFallback);
    } else {
      writeFallback();
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent showCloseButton={false}>
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <Icon className="h-4 w-4 text-muted-foreground" />
            {formatToolName(call.tool_name)}
          </DialogTitle>
          <DialogDescription className="text-xs">
            {call.tool_name} &middot;{" "}
            {new Date(call.timestamp).toLocaleString()}
          </DialogDescription>
        </DialogHeader>

        <div>
          <div className="mb-1.5 flex items-center justify-between">
            <p className="text-xs font-medium text-muted-foreground">
              {sql ? "SQL Query" : "Parameters"}
            </p>
            <Button
              type="button"
              variant="ghost"
              size="xs"
              onClick={handleCopy}
              className="text-muted-foreground"
              title={sql ? "Copy SQL query" : "Copy parameters"}
              aria-label={sql ? "Copy SQL query" : "Copy parameters"}
            >
              {copied ? (
                <>
                  <Check className="text-emerald-600 dark:text-emerald-400" />
                  Copied
                </>
              ) : (
                <>
                  <Copy />
                  Copy
                </>
              )}
            </Button>
          </div>
          <pre className="max-h-96 overflow-auto rounded-md bg-muted p-3 text-xs font-mono whitespace-pre-wrap break-words">
            {detail}
          </pre>
        </div>

        <DialogFooter>
          <DialogClose asChild>
            <Button type="button" variant="secondary">
              Close
            </Button>
          </DialogClose>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

export function ProvenancePanel({ provenance, onOpenSession }: Props) {
  const calls = provenance.tool_calls ?? [];
  const [selected, setSelected] = useState<ProvenanceToolCall | null>(null);
  const [showAll, setShowAll] = useState(false);

  if (calls.length === 0) {
    return <NoProvenance onOpenSession={onOpenSession} />;
  }

  const trinoCalls = calls.filter((c) => c.tool_name.startsWith("trino_"));
  const otherCalls = calls.filter((c) => !c.tool_name.startsWith("trino_"));
  const visibleCalls = showAll ? calls : trinoCalls;

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between">
        <h3 className="text-sm font-medium">Provenance</h3>
        <span className="text-xs text-muted-foreground">
          {trinoCalls.length} {trinoCalls.length === 1 ? "query" : "queries"}
          {otherCalls.length > 0 && !showAll && ` + ${otherCalls.length} other`}
        </span>
      </div>

      <div className="space-y-2">
        {visibleCalls.map((call, i) => (
          <ProvenanceCard
            key={i}
            call={call}
            onClick={() => setSelected(call)}
          />
        ))}
      </div>

      {otherCalls.length > 0 && (
        // A disclosure toggle, not an empty state: the dashed outline this
        // used to carry is reserved for EmptyState.
        <Button
          type="button"
          variant="outline"
          size="xs"
          onClick={() => setShowAll((v) => !v)}
          className="w-full text-muted-foreground"
        >
          {showAll ? "Show queries only" : `Show all ${calls.length} calls`}
        </Button>
      )}

      {onOpenSession && <OpenSessionButton onClick={onOpenSession} />}

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

/**
 * An asset that captured no calls. It still came from a session, and that
 * session is where the calls this asset does not carry are recorded — so this
 * is exactly where the walk to it matters most.
 */
function NoProvenance({ onOpenSession }: { onOpenSession?: () => void }) {
  return (
    <div className="space-y-3">
      <p className="text-sm text-muted-foreground">
        No provenance data available.
      </p>
      {onOpenSession && <OpenSessionButton onClick={onOpenSession} />}
    </div>
  );
}

/** Walks from what the asset captured to the whole session that made it. */
function OpenSessionButton({ onClick }: { onClick: () => void }) {
  return (
    <Button
      type="button"
      variant="outline"
      size="xs"
      onClick={onClick}
      className="w-full text-muted-foreground"
    >
      <History />
      Open session
    </Button>
  );
}
