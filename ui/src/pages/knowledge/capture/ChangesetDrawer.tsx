import { useCallback } from "react";
import { useRollbackChangeset } from "@/api/admin/hooks";
import { StatusBadge } from "@/components/cards/StatusBadge";
import type { Changeset } from "@/api/admin/types";
import { formatUser } from "@/lib/formatUser";
import { formatCategory } from "./helpers";

export function ChangesetDrawer({
  changeset,
  onClose,
  userLabels,
}: {
  changeset: Changeset;
  onClose: () => void;
  userLabels: Record<string, string>;
}) {
  const rollback = useRollbackChangeset();

  const handleRollback = useCallback(() => {
    if (!window.confirm("Are you sure you want to rollback this changeset?"))
      return;
    rollback.mutate(changeset.id, { onSuccess: () => onClose() });
  }, [changeset.id, rollback, onClose]);

  return (
    <div className="fixed inset-0 z-50 flex justify-end">
      <div className="absolute inset-0 bg-black/50" onClick={onClose} />
      <div className="relative w-full max-w-lg overflow-auto bg-card p-6 shadow-xl">
        <div className="mb-4 flex items-center justify-between">
          <h2 className="text-lg font-semibold">Changeset Detail</h2>
          <button
            onClick={onClose}
            className="rounded-md px-2 py-1 text-sm hover:bg-muted"
          >
            Close
          </button>
        </div>

        <div className="space-y-4">
          {/* Metadata grid */}
          <div className="grid grid-cols-2 gap-3 text-sm">
            <div>
              <p className="text-xs text-muted-foreground">ID</p>
              <p className="font-mono text-xs">{changeset.id}</p>
            </div>
            <div>
              <p className="text-xs text-muted-foreground">Created At</p>
              <p>{new Date(changeset.created_at).toLocaleString()}</p>
            </div>
            <div className="col-span-2">
              <p className="text-xs text-muted-foreground">Target URN</p>
              <p className="font-mono text-xs">{changeset.target_urn}</p>
            </div>
            <div>
              <p className="text-xs text-muted-foreground">Change Type</p>
              <p>{formatCategory(changeset.change_type)}</p>
            </div>
            <div>
              <p className="text-xs text-muted-foreground">Status</p>
              <StatusBadge
                variant={changeset.rolled_back ? "error" : "success"}
              >
                {changeset.rolled_back ? "Rolled Back" : "Active"}
              </StatusBadge>
            </div>
            <div>
              <p className="text-xs text-muted-foreground">Approved By</p>
              <p title={changeset.approved_by}>
                {formatUser(
                  changeset.approved_by,
                  userLabels[changeset.approved_by],
                )}
              </p>
            </div>
            <div>
              <p className="text-xs text-muted-foreground">Applied By</p>
              <p title={changeset.applied_by}>
                {formatUser(
                  changeset.applied_by,
                  userLabels[changeset.applied_by],
                )}
              </p>
            </div>
          </div>

          {/* Previous Value */}
          <div>
            <p className="mb-1 text-xs text-muted-foreground">
              Previous Value
            </p>
            <pre className="overflow-auto rounded bg-muted p-3 text-xs">
              {JSON.stringify(changeset.previous_value, null, 2)}
            </pre>
          </div>

          {/* New Value */}
          <div>
            <p className="mb-1 text-xs text-muted-foreground">New Value</p>
            <pre className="overflow-auto rounded bg-muted p-3 text-xs">
              {JSON.stringify(changeset.new_value, null, 2)}
            </pre>
          </div>

          {/* Source Insight IDs */}
          {changeset.source_insight_ids.length > 0 && (
            <div>
              <p className="mb-1 text-xs text-muted-foreground">
                Source Insight IDs
              </p>
              <div className="space-y-1">
                {changeset.source_insight_ids.map((id, i) => (
                  <p
                    key={i}
                    className="font-mono text-xs text-muted-foreground"
                  >
                    {id}
                  </p>
                ))}
              </div>
            </div>
          )}

          {/* Rollback info */}
          {changeset.rolled_back && (
            <div className="grid grid-cols-2 gap-3 border-t pt-3 text-sm">
              <div>
                <p className="text-xs text-muted-foreground">Rolled Back By</p>
                <p title={changeset.rolled_back_by}>
                  {formatUser(
                    changeset.rolled_back_by ?? "",
                    userLabels[changeset.rolled_back_by ?? ""],
                  )}
                </p>
              </div>
              <div>
                <p className="text-xs text-muted-foreground">Rolled Back At</p>
                <p>
                  {changeset.rolled_back_at
                    ? new Date(changeset.rolled_back_at).toLocaleString()
                    : "-"}
                </p>
              </div>
            </div>
          )}

          {/* Rollback button */}
          {!changeset.rolled_back && (
            <div className="border-t pt-3">
              <button
                onClick={handleRollback}
                disabled={rollback.isPending}
                className="rounded-md bg-red-600 px-4 py-2 text-sm font-medium text-white hover:bg-red-700 disabled:opacity-50"
              >
                Rollback Changeset
              </button>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
