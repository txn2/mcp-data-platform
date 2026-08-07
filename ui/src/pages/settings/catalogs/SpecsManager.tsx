import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Plus } from "lucide-react";

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
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { CatalogEmbeddingHealthBanner } from "./badges";
import { SpecList } from "./SpecList";
import { SpecModal } from "./SpecModal";

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
    <SectionCard
      title="Component specs"
      action={
        !isReadOnly && (
          <Button type="button" variant="outline" size="sm" onClick={() => setAdding(true)}>
            <Plus /> Add spec
          </Button>
        )
      }
    >
      <div className="space-y-3">
        {health && <CatalogEmbeddingHealthBanner health={health} />}

        {statusMessage && (
          <Alert
            variant={statusMessage.kind === "error" ? "destructive" : "success"}
            role={statusMessage.kind === "error" ? "alert" : "status"}
          >
            <AlertDescription className="flex w-full items-start justify-between gap-3">
              <span>{statusMessage.text}</span>
              <Button type="button" variant="link" size="xs" onClick={dismissStatus}>
                dismiss
              </Button>
            </AlertDescription>
          </Alert>
        )}

        <SpecList
          loading={loading}
          specs={specs}
          statusByName={statusByName}
          isReadOnly={isReadOnly}
          actions={{
            pendingRetry,
            pendingRefresh,
            onRetry: handleRetry,
            onRefresh: handleRefresh,
            onEdit: setEditing,
            onDelete: setPendingDelete,
          }}
        />
      </div>

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
    </SectionCard>
  );
}
