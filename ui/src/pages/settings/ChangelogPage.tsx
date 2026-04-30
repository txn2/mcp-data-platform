import { useState } from "react";
import { useConfigChangelog } from "@/api/admin/hooks";
import type { ConfigChangelogEntry } from "@/api/admin/types";
import { cn } from "@/lib/utils";
import {
  Clock,
  ChevronDown,
  RefreshCw,
  XCircle,
} from "lucide-react";

// ---------------------------------------------------------------------------
// Shared error banner
// ---------------------------------------------------------------------------

function ErrorBanner({ message, onRetry }: { message: string; onRetry?: () => void }) {
  return (
    <div className="flex items-center gap-2 border-b bg-red-50 px-5 py-2.5 text-xs text-red-700 dark:bg-red-950/30 dark:text-red-400">
      <XCircle className="h-3.5 w-3.5 shrink-0" />
      <span className="flex-1">{message}</span>
      {onRetry && (
        <button
          type="button"
          onClick={onRetry}
          className="inline-flex items-center gap-1 rounded px-2 py-1 text-xs font-medium hover:bg-red-100 dark:hover:bg-red-900/30"
        >
          <RefreshCw className="h-3 w-3" />
          Retry
        </button>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// ChangelogPage
// ---------------------------------------------------------------------------

export function ChangelogPage() {
  const { data: changelog, isLoading, error, refetch } = useConfigChangelog();
  const entries = changelog ?? [];

  return (
    <div className="flex h-[calc(100vh-8rem)] flex-col overflow-hidden rounded-lg border bg-card">
      <div className="border-b px-5 py-3">
        <h3 className="text-sm font-semibold leading-none">Change Log</h3>
        <p className="mt-1 text-xs text-muted-foreground">
          Audit trail of all configuration changes
        </p>
      </div>

      {error && (
        <ErrorBanner
          message="Failed to load changelog. The server may be unavailable."
          onRetry={() => void refetch()}
        />
      )}

      <div className="flex-1 overflow-auto">
        {isLoading ? (
          <div className="flex items-center justify-center py-16 text-sm text-muted-foreground">
            Loading...
          </div>
        ) : entries.length === 0 && !error ? (
          <div className="flex flex-col items-center justify-center py-16 text-muted-foreground">
            <Clock className="mb-3 h-8 w-8 opacity-30" />
            <p className="text-sm">No configuration changes recorded yet</p>
            <p className="mt-1 text-xs opacity-60">
              Changes will appear here after saving a config entry
            </p>
          </div>
        ) : (
          <div className="divide-y">
            {entries.map((e: ConfigChangelogEntry) => (
              <ChangelogRow key={e.id} entry={e} />
            ))}
          </div>
        )}
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// ChangelogRow
// ---------------------------------------------------------------------------

function ChangelogRow({ entry }: { entry: ConfigChangelogEntry }) {
  const [expanded, setExpanded] = useState(false);
  const hasValue = entry.action === "set" && entry.value != null;

  return (
    <div className="px-5 py-3 transition-colors hover:bg-muted/30">
      <div className="flex items-center gap-3">
        <span className="font-mono text-xs text-foreground">{entry.key}</span>
        <span
          className={cn(
            "inline-flex items-center rounded-full px-2 py-0.5 text-xs font-medium",
            entry.action === "set"
              ? "bg-green-100 text-green-700 dark:bg-green-900/30 dark:text-green-400"
              : "bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-400",
          )}
        >
          {entry.action === "set" ? "Updated" : "Deleted"}
        </span>
        <span className="flex-1" />
        <span className="text-xs text-muted-foreground">
          {entry.changed_by || "unknown"}
        </span>
        <span className="text-xs text-muted-foreground">
          {new Date(entry.changed_at).toLocaleString()}
        </span>
        {hasValue && (
          <button
            type="button"
            onClick={() => setExpanded((prev) => !prev)}
            className="inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-xs font-medium text-muted-foreground hover:bg-muted hover:text-foreground"
          >
            <ChevronDown className={cn("h-3 w-3 transition-transform", expanded && "rotate-180")} />
            {expanded ? "Hide" : "Show value"}
          </button>
        )}
      </div>
      {expanded && hasValue && (
        <pre className="mt-2 max-h-60 overflow-auto rounded-md border bg-muted/30 p-3 text-xs font-mono whitespace-pre-wrap break-words">
          {entry.value}
        </pre>
      )}
    </div>
  );
}
