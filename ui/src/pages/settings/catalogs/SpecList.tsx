import { FileText, Pencil, RefreshCw, Trash2 } from "lucide-react";

import {
  type APICatalogSpec,
  type APICatalogEmbeddingSpecStatus,
} from "@/api/admin/hooks";
import { EmptyState } from "@/components/patterns/EmptyState";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { cn } from "@/lib/utils";
import { EmbeddingStatusBadge, SourceBadge } from "./badges";

export interface SpecRowActions {
  pendingRetry: Set<string>;
  pendingRefresh: Set<string>;
  onRetry: (specName: string) => void;
  onRefresh: (specName: string) => void;
  onEdit: (specName: string) => void;
  onDelete: (specName: string) => void;
}

// SpecRowButtons renders the per-row action set. Retry appears only for a
// failed embedding job and Refresh only for a URL-sourced spec, so a row
// carries exactly the actions that can act on it.
function SpecRowButtons({
  spec,
  status,
  actions,
}: {
  spec: APICatalogSpec;
  status?: APICatalogEmbeddingSpecStatus;
  actions: SpecRowActions;
}) {
  const retrying = actions.pendingRetry.has(spec.spec_name);
  const refreshing = actions.pendingRefresh.has(spec.spec_name);
  return (
    <div className="flex justify-end gap-1">
      {status?.job_status === "failed" && (
        <Button
          type="button"
          variant="ghost"
          size="xs"
          onClick={() => actions.onRetry(spec.spec_name)}
          disabled={retrying}
          title={`Retry embedding (last error: ${status?.job_last_error ?? "unknown"})`}
          className="text-destructive hover:bg-destructive/10 hover:text-destructive"
        >
          {retrying ? "Retrying…" : "Retry"}
        </Button>
      )}
      {spec.source_kind === "url" && (
        <Button
          type="button"
          variant="ghost"
          size="icon-xs"
          onClick={() => actions.onRefresh(spec.spec_name)}
          disabled={refreshing}
          title={refreshing ? "Refreshing…" : "Refresh from URL"}
          aria-label={`Refresh ${spec.spec_name} from URL`}
        >
          <RefreshCw className={cn(refreshing && "animate-spin")} />
        </Button>
      )}
      <Button
        type="button"
        variant="ghost"
        size="xs"
        onClick={() => actions.onEdit(spec.spec_name)}
      >
        <Pencil /> Edit
      </Button>
      <Button
        type="button"
        variant="ghost"
        size="icon-xs"
        onClick={() => actions.onDelete(spec.spec_name)}
        aria-label={`Delete ${spec.spec_name}`}
        className="text-destructive hover:bg-destructive/10 hover:text-destructive"
      >
        <Trash2 />
      </Button>
    </div>
  );
}

// SpecList renders the loading / empty / populated states of the
// component-spec table. Extracted from SpecsManager so the manager
// function stays within the size budget; the per-row action wiring
// (retry, refresh, edit, delete) is passed in as callbacks.
export function SpecList({
  loading,
  specs,
  statusByName,
  isReadOnly,
  actions,
}: {
  loading: boolean;
  specs: APICatalogSpec[];
  statusByName: Record<string, APICatalogEmbeddingSpecStatus>;
  isReadOnly: boolean;
  actions: SpecRowActions;
}) {
  if (loading) {
    return (
      <div className="space-y-2">
        <Skeleton className="h-8 w-full" />
        <Skeleton className="h-8 w-full" />
      </div>
    );
  }
  if (specs.length === 0) {
    return (
      <EmptyState icon={FileText}>
        No specs yet. Add one to expose endpoints on the connections that
        reference this catalog.
      </EmptyState>
    );
  }
  return (
    <div className="rounded-md border">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Spec</TableHead>
            <TableHead>Source</TableHead>
            <TableHead>Indexing</TableHead>
            <TableHead>Fetched</TableHead>
            {/* Config-file mode has no writable spec, so the column that would
                hold only empty cells is not rendered at all. */}
            {!isReadOnly && <TableHead className="text-right">Actions</TableHead>}
          </TableRow>
        </TableHeader>
        <TableBody>
          {specs.map((s) => {
            const status = statusByName[s.spec_name];
            // Embedded specs are re-seeded from their toolkit at startup, so
            // edits/deletes here do not persist; present them as read-only.
            const specReadOnly = s.source_kind === "embedded";
            return (
              <TableRow key={s.spec_name}>
                <TableCell className="font-mono">{s.spec_name}</TableCell>
                <TableCell>
                  <SourceBadge kind={s.source_kind} url={s.source_url} />
                </TableCell>
                <TableCell>
                  <EmbeddingStatusBadge status={status} />
                </TableCell>
                <TableCell className="text-xs text-muted-foreground">
                  {s.last_fetched_at
                    ? new Date(s.last_fetched_at).toLocaleString()
                    : "—"}
                </TableCell>
                {!isReadOnly && (
                  <TableCell>
                    {!specReadOnly && (
                      <SpecRowButtons spec={s} status={status} actions={actions} />
                    )}
                  </TableCell>
                )}
              </TableRow>
            );
          })}
        </TableBody>
      </Table>
    </div>
  );
}
