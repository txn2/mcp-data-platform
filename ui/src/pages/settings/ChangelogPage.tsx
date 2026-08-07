import { useState } from "react";
import { useInfiniteConfigChangelog } from "@/api/admin/hooks";
import type { ConfigChangelogEntry } from "@/api/admin/types";
import { InfiniteFooter } from "@/components/InfiniteFooter";
import { EmptyState } from "@/components/patterns/EmptyState";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { Clock, ChevronDown } from "lucide-react";
import { PanelShell } from "./panels";
import { ErrorBanner } from "./settingsChrome";

export function ChangelogPage() {
  const { data, isLoading, isError, refetch, hasNextPage, isFetchingNextPage, fetchNextPage } =
    useInfiniteConfigChangelog();
  const entries = data?.data ?? [];

  return (
    <PanelShell
      title="Change Log"
      description="Audit trail of all configuration changes"
      notices={
        isError && (
          <ErrorBanner
            message="Failed to load changelog. The server may be unavailable."
            onRetry={() => void refetch()}
          />
        )
      }
    >
      <div className="flex-1 overflow-auto">
        {isLoading ? (
          <p className="py-16 text-center text-sm text-muted-foreground">
            Loading...
          </p>
        ) : entries.length === 0 && !isError ? (
          <div className="p-5">
            <EmptyState icon={Clock}>
              <p>No configuration changes recorded yet</p>
              <p className="mt-1 text-xs">
                Changes will appear here after saving a config entry
              </p>
            </EmptyState>
          </div>
        ) : (
          <>
            <div className="divide-y">
              {entries.map((e: ConfigChangelogEntry) => (
                <ChangelogRow key={e.id} entry={e} />
              ))}
            </div>
            <div className="p-3">
              <InfiniteFooter
                hasMore={hasNextPage}
                isLoadingMore={isFetchingNextPage}
                onLoadMore={fetchNextPage}
              />
            </div>
          </>
        )}
      </div>
    </PanelShell>
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
        {/* A write and a delete are both normal outcomes; the tint separates
            them at a glance rather than grading one as a failure. */}
        <Badge variant={entry.action === "set" ? "success" : "danger"}>
          {entry.action === "set" ? "Updated" : "Deleted"}
        </Badge>
        <span className="flex-1" />
        <span className="text-xs text-muted-foreground">
          {entry.changed_by || "unknown"}
        </span>
        <span className="text-xs text-muted-foreground">
          {new Date(entry.changed_at).toLocaleString()}
        </span>
        {hasValue && (
          <Button
            type="button"
            variant="ghost"
            size="xs"
            onClick={() => setExpanded((prev) => !prev)}
            className="text-muted-foreground"
          >
            <ChevronDown className={cn("transition-transform", expanded && "rotate-180")} />
            {expanded ? "Hide" : "Show value"}
          </Button>
        )}
      </div>
      {expanded && hasValue && (
        <pre className="mt-2 max-h-60 overflow-auto whitespace-pre-wrap break-words rounded-md border bg-muted/30 p-3 font-mono text-xs">
          {entry.value}
        </pre>
      )}
    </div>
  );
}
