import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Plus, RefreshCw, Trash2 } from "lucide-react";

import {
  type APICatalogSpec,
  type APICatalogEmbeddingSpecStatus,
  useAPICatalogEmbeddingHealth,
  useAPICatalogEmbeddingStatuses,
  useDeleteAPICatalogSpec,
  useManualRetryEmbedding,
  useRefreshAPICatalogSpec,
} from "@/api/admin/hooks";
import { apiFetch } from "@/api/admin/client";
import { cn } from "@/lib/utils";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import {
  CatalogEmbeddingHealthBanner,
  EmbeddingStatusBadge,
  SourceBadge,
} from "./badges";
import { SpecModal } from "./SpecModal";

// SpecList renders the loading / empty / populated states of the
// component-spec table. Extracted from SpecsManager so the manager
// function stays within the size budget; the per-row action wiring
// (retry, refresh, edit, delete) is passed in as callbacks.
function SpecList({
  loading,
  specs,
  statusByName,
  isReadOnly,
  pendingRetry,
  pendingRefresh,
  onRetry,
  onRefresh,
  onEdit,
  onDelete,
}: {
  loading: boolean;
  specs: APICatalogSpec[];
  statusByName: Record<string, APICatalogEmbeddingSpecStatus>;
  isReadOnly: boolean;
  pendingRetry: Set<string>;
  pendingRefresh: Set<string>;
  onRetry: (specName: string) => void;
  onRefresh: (specName: string) => void;
  onEdit: (specName: string) => void;
  onDelete: (specName: string) => void;
}) {
  if (loading) {
    return <div className="text-sm text-muted-foreground">Loading…</div>;
  }
  if (specs.length === 0) {
    return (
      <div className="rounded-md border bg-muted/30 p-4 text-sm text-muted-foreground">
        No specs yet. Add one to expose endpoints on the connections that reference this catalog.
      </div>
    );
  }
  return (
    <ul className="divide-y rounded-md border">
      {specs.map((s) => {
        const status = statusByName[s.spec_name];
        const failed = status?.job_status === "failed";
        // Embedded specs are re-seeded from their toolkit at startup, so
        // edits/deletes here do not persist; present them as read-only.
        const specReadOnly = isReadOnly || s.source_kind === "embedded";
        return (
          <li key={s.spec_name} className="flex items-center gap-3 px-3 py-2 text-sm">
            <span className="flex-1 truncate font-mono">{s.spec_name}</span>
            <SourceBadge kind={s.source_kind} url={s.source_url} />
            <EmbeddingStatusBadge status={status} />
            {s.last_fetched_at && (
              <span className="text-xs text-muted-foreground">
                fetched {new Date(s.last_fetched_at).toLocaleString()}
              </span>
            )}
            {!specReadOnly && (
              <div className="flex gap-1">
                {failed && (
                  <button
                    type="button"
                    onClick={() => onRetry(s.spec_name)}
                    disabled={pendingRetry.has(s.spec_name)}
                    title={`Retry embedding (last error: ${status?.job_last_error ?? "unknown"})`}
                    className="rounded px-2 py-1 text-xs text-destructive hover:bg-destructive/10 disabled:cursor-not-allowed disabled:opacity-60"
                  >
                    {pendingRetry.has(s.spec_name) ? "Retrying…" : "Retry"}
                  </button>
                )}
                {s.source_kind === "url" && (
                  <button
                    type="button"
                    onClick={() => onRefresh(s.spec_name)}
                    disabled={pendingRefresh.has(s.spec_name)}
                    title={pendingRefresh.has(s.spec_name) ? "Refreshing…" : "Refresh from URL"}
                    className="rounded p-1 hover:bg-muted disabled:cursor-not-allowed disabled:opacity-60"
                  >
                    <RefreshCw
                      className={cn("h-4 w-4", pendingRefresh.has(s.spec_name) && "animate-spin")}
                    />
                  </button>
                )}
                <button
                  type="button"
                  onClick={() => onEdit(s.spec_name)}
                  className="rounded p-1 hover:bg-muted"
                >
                  Edit
                </button>
                <button
                  type="button"
                  onClick={() => onDelete(s.spec_name)}
                  className="rounded p-1 text-destructive hover:bg-destructive/10"
                >
                  <Trash2 className="h-4 w-4" />
                </button>
              </div>
            )}
          </li>
        );
      })}
    </ul>
  );
}

export function SpecsManager({ catalogID, isReadOnly }: { catalogID: string; isReadOnly: boolean }) {
  const [specs, setSpecs] = useState<APICatalogSpec[]>([]);
  const [loading, setLoading] = useState(false);
  const [refreshCounter, setRefreshCounter] = useState(0);
  const [editing, setEditing] = useState<string | null>(null);
  const [adding, setAdding] = useState(false);
  const [pendingDelete, setPendingDelete] = useState<string | null>(null);
  const [deleteError, setDeleteError] = useState<string | null>(null);

  const refresh = useRefreshAPICatalogSpec();
  const manualRetry = useManualRetryEmbedding();
  const del = useDeleteAPICatalogSpec();
  // Per-spec in-flight tracking. TanStack's mutation.isPending is a
  // single bit shared across every spec row, so it cannot say "this
  // row is the one mid-mutation". We track pending spec names locally
  // so each row's button disables + changes label without affecting
  // its siblings. The ref pair holds the synchronous truth: a rapid
  // click that arrives before React commits the disabled state still
  // hits the ref guard at the top of the handler and short-circuits,
  // instead of firing a duplicate mutation against the same spec.
  const inFlightRetryRef = useRef<Set<string>>(new Set());
  const inFlightRefreshRef = useRef<Set<string>>(new Set());
  const [pendingRetry, setPendingRetry] = useState<Set<string>>(new Set());
  const [pendingRefresh, setPendingRefresh] = useState<Set<string>>(new Set());
  // Inline status banner. Auto-clears after a short delay on success;
  // stays until dismissed on error so the operator can read the
  // failure (previously a failed retry/refresh was silent).
  const [statusMessage, setStatusMessage] = useState<{
    kind: "success" | "error";
    text: string;
  } | null>(null);
  // The auto-clear timer is held in a ref so a newer action can cancel
  // a still-pending clear before scheduling its own; otherwise an old
  // success timer could clear a newer banner ahead of its window.
  const statusTimerRef = useRef<number | null>(null);

  const clearStatusTimer = useCallback(() => {
    if (statusTimerRef.current !== null) {
      window.clearTimeout(statusTimerRef.current);
      statusTimerRef.current = null;
    }
  }, []);

  const showStatus = useCallback(
    (msg: { kind: "success" | "error"; text: string }, autoClearMs?: number) => {
      clearStatusTimer();
      setStatusMessage(msg);
      if (autoClearMs !== undefined) {
        statusTimerRef.current = window.setTimeout(() => {
          setStatusMessage(null);
          statusTimerRef.current = null;
        }, autoClearMs);
      }
    },
    [clearStatusTimer],
  );

  const dismissStatus = useCallback(() => {
    clearStatusTimer();
    setStatusMessage(null);
  }, [clearStatusTimer]);

  // Unmount cleanup: a timer that fires after the component is gone
  // would setState on an unmounted component.
  useEffect(() => clearStatusTimer, [clearStatusTimer]);

  const handleRetry = (specName: string) => {
    if (inFlightRetryRef.current.has(specName)) {
      return; // synchronous debounce against rapid double-clicks
    }
    inFlightRetryRef.current.add(specName);
    setPendingRetry(new Set(inFlightRetryRef.current));
    dismissStatus();
    manualRetry.mutate(
      { catalogID, specName },
      {
        onSuccess: (data) => {
          setRefreshCounter((n) => n + 1);
          showStatus(
            {
              kind: "success",
              text: data.created
                ? `Re-embedding queued for "${specName}".`
                : `"${specName}" is already queued for re-embedding.`,
            },
            4000,
          );
        },
        onError: (err) => {
          showStatus({
            kind: "error",
            text: `Retry "${specName}" failed: ${err.message}`,
          });
        },
        onSettled: () => {
          inFlightRetryRef.current.delete(specName);
          setPendingRetry(new Set(inFlightRetryRef.current));
        },
      },
    );
  };

  const handleRefresh = (specName: string) => {
    if (inFlightRefreshRef.current.has(specName)) {
      return; // synchronous debounce against rapid double-clicks
    }
    inFlightRefreshRef.current.add(specName);
    setPendingRefresh(new Set(inFlightRefreshRef.current));
    dismissStatus();
    refresh.mutate(
      { catalogID, specName },
      {
        onSuccess: () => {
          setRefreshCounter((n) => n + 1);
          showStatus(
            {
              kind: "success",
              text: `Refreshed "${specName}" from upstream URL.`,
            },
            4000,
          );
        },
        onError: (err) => {
          showStatus({
            kind: "error",
            text: `Refresh "${specName}" failed: ${err.message}`,
          });
        },
        onSettled: () => {
          inFlightRefreshRef.current.delete(specName);
          setPendingRefresh(new Set(inFlightRefreshRef.current));
        },
      },
    );
  };
  // The job-queue-backed embedding state polls every 5s while
  // the panel is mounted. The badge updates as the worker
  // progresses; the catalog header summary reflects pending /
  // failed counts. Operators do not need to take any action.
  const { data: health } = useAPICatalogEmbeddingHealth(catalogID);
  const { data: statusList } = useAPICatalogEmbeddingStatuses(catalogID);
  const statusByName = useMemo(() => {
    const map: Record<string, APICatalogEmbeddingSpecStatus> = {};
    for (const s of statusList?.specs ?? []) {
      map[s.spec_name] = s;
    }
    return map;
  }, [statusList]);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    void apiFetch<{ specs?: APICatalogSpec[] }>(`/api-catalogs/${catalogID}/specs`)
      .then((res) => {
        if (!cancelled) setSpecs(res.specs ?? []);
      })
      .catch(() => {
        if (!cancelled) setSpecs([]);
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [catalogID, refreshCounter]);

  return (
    <div className="space-y-3">
      <div className="flex items-center justify-between">
        <h3 className="text-sm font-medium">Component specs</h3>
        {!isReadOnly && (
          <button
            type="button"
            onClick={() => setAdding(true)}
            className="inline-flex items-center gap-1 rounded-md border bg-background px-2 py-1 text-xs hover:bg-muted"
          >
            <Plus className="h-3.5 w-3.5" /> Add spec
          </button>
        )}
      </div>

      {health && <CatalogEmbeddingHealthBanner health={health} />}

      {statusMessage && (
        <div
          role={statusMessage.kind === "error" ? "alert" : "status"}
          className={cn(
            "flex items-start justify-between gap-3 rounded-md border px-3 py-2 text-xs",
            statusMessage.kind === "success" &&
              "border-emerald-500/30 bg-emerald-50 text-emerald-900 dark:bg-emerald-500/10 dark:text-emerald-200",
            statusMessage.kind === "error" &&
              "border-destructive/40 bg-destructive/10 text-destructive",
          )}
        >
          <span>{statusMessage.text}</span>
          <button
            type="button"
            onClick={dismissStatus}
            className="text-xs underline-offset-2 hover:underline"
          >
            dismiss
          </button>
        </div>
      )}

      <SpecList
        loading={loading}
        specs={specs}
        statusByName={statusByName}
        isReadOnly={isReadOnly}
        pendingRetry={pendingRetry}
        pendingRefresh={pendingRefresh}
        onRetry={handleRetry}
        onRefresh={handleRefresh}
        onEdit={setEditing}
        onDelete={setPendingDelete}
      />

      {(adding || editing) && (
        <SpecModal
          catalogID={catalogID}
          existingSpecName={editing ?? undefined}
          onClose={() => {
            setAdding(false);
            setEditing(null);
          }}
          onSaved={() => {
            setAdding(false);
            setEditing(null);
            setRefreshCounter((n) => n + 1);
          }}
        />
      )}

      <ConfirmDialog
        open={pendingDelete !== null}
        onOpenChange={(open) => {
          if (!open) {
            setPendingDelete(null);
            setDeleteError(null);
          }
        }}
        destructive
        title="Delete component spec?"
        description={
          pendingDelete ? (
            <>
              The spec <code className="font-mono">{pendingDelete}</code> will
              be removed from this catalog. Connections referencing the
              catalog reload immediately and stop seeing operations from
              this spec.
            </>
          ) : null
        }
        confirmLabel="Delete"
        loading={del.isPending}
        error={deleteError}
        onConfirm={async () => {
          if (!pendingDelete) return;
          setDeleteError(null);
          try {
            await del.mutateAsync({ catalogID, specName: pendingDelete });
            setRefreshCounter((n) => n + 1);
            setPendingDelete(null);
          } catch (e) {
            setDeleteError(e instanceof Error ? e.message : "delete failed");
          }
        }}
      />
    </div>
  );
}
