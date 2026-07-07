import { AlertTriangle, RotateCcw } from "lucide-react";
import type { Asset } from "@/api/portal/types";
import type { MutationLike } from "./types";

interface AssetViewerModalsProps {
  asset: Asset;
  deleteModalOpen: boolean;
  onDeleteClose: () => void;
  onConfirmDelete: () => void;
  deleteMutation: MutationLike<string>;
  sharedSaveWarningOpen: boolean;
  onSharedSaveWarningClose: () => void;
  onSharedSaveWarningContinue: () => void;
  contentUpdateMutation?: MutationLike<{ id: string; content: string; changeSummary?: string }>;
  changeSummaryOpen: boolean;
  onChangeSummaryClose: () => void;
  changeSummary: string;
  onChangeSummaryChange: (v: string) => void;
  onChangeSummarySave: () => void;
  revertModalOpen: boolean;
  selectedVersion?: number | null;
  onRevertClose: () => void;
  onConfirmRevert: () => void;
  revertMutation?: MutationLike<{ assetId: string; version: number }>;
}

export function AssetViewerModals({
  asset,
  deleteModalOpen,
  onDeleteClose,
  onConfirmDelete,
  deleteMutation,
  sharedSaveWarningOpen,
  onSharedSaveWarningClose,
  onSharedSaveWarningContinue,
  contentUpdateMutation,
  changeSummaryOpen,
  onChangeSummaryClose,
  changeSummary,
  onChangeSummaryChange,
  onChangeSummarySave,
  revertModalOpen,
  selectedVersion,
  onRevertClose,
  onConfirmRevert,
  revertMutation,
}: AssetViewerModalsProps) {
  return (
    <>
      {/* Delete confirmation modal */}
      {deleteModalOpen && (
        <div className="fixed inset-0 z-50 flex items-center justify-center">
          <div
            className="absolute inset-0 bg-black/50"
            onClick={onDeleteClose}
            onKeyDown={(e) => { if (e.key === "Escape") onDeleteClose(); }}
            role="button"
            tabIndex={-1}
            aria-label="Close"
          />
          <div className="relative rounded-lg border bg-card p-6 shadow-lg max-w-sm w-full mx-4 space-y-4">
            <div className="flex items-center gap-3">
              <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-full bg-destructive/10">
                <AlertTriangle className="h-5 w-5 text-destructive" />
              </div>
              <div>
                <h3 className="text-sm font-semibold">Delete asset</h3>
                <p className="text-sm text-muted-foreground">This action cannot be undone.</p>
              </div>
            </div>
            <p className="text-sm">
              Are you sure you want to delete <span className="font-medium">{asset.name}</span>?
            </p>
            <div className="flex justify-end gap-2">
              <button
                type="button"
                onClick={onDeleteClose}
                className="rounded-md bg-secondary px-4 py-2 text-sm font-medium text-secondary-foreground hover:bg-secondary/80"
              >
                Cancel
              </button>
              <button
                type="button"
                onClick={onConfirmDelete}
                disabled={deleteMutation.isPending}
                className="rounded-md bg-destructive px-4 py-2 text-sm font-medium text-destructive-foreground hover:bg-destructive/90 disabled:opacity-50"
              >
                {deleteMutation.isPending ? "Deleting..." : "Delete"}
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Shared asset save warning modal */}
      {sharedSaveWarningOpen && (
        <div className="fixed inset-0 z-50 flex items-center justify-center">
          <div
            className="absolute inset-0 bg-black/50"
            onClick={onSharedSaveWarningClose}
            onKeyDown={(e) => { if (e.key === "Escape") onSharedSaveWarningClose(); }}
            role="button"
            tabIndex={-1}
            aria-label="Close"
          />
          <div className="relative rounded-lg border bg-card p-6 shadow-lg max-w-sm w-full mx-4 space-y-4">
            <div className="flex items-center gap-3">
              <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-full bg-amber-100 dark:bg-amber-950">
                <AlertTriangle className="h-5 w-5 text-amber-600 dark:text-amber-400" />
              </div>
              <div>
                <h3 className="text-sm font-semibold">Editing a shared asset</h3>
                <p className="text-sm text-muted-foreground">
                  You are editing a shared asset owned by {asset.owner_email || "another user"}.
                  Changes will be visible to the owner and all other recipients.
                </p>
              </div>
            </div>
            <div className="flex justify-end gap-2">
              <button
                type="button"
                onClick={onSharedSaveWarningClose}
                className="rounded-md bg-secondary px-4 py-2 text-sm font-medium text-secondary-foreground hover:bg-secondary/80"
              >
                Cancel
              </button>
              <button
                type="button"
                onClick={onSharedSaveWarningContinue}
                disabled={contentUpdateMutation?.isPending}
                className="rounded-md bg-primary px-4 py-2 text-sm font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
              >
                Continue
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Change summary dialog */}
      {changeSummaryOpen && (
        <div className="fixed inset-0 z-50 flex items-center justify-center">
          <div
            className="absolute inset-0 bg-black/50"
            onClick={onChangeSummaryClose}
            onKeyDown={(e) => { if (e.key === "Escape") onChangeSummaryClose(); }}
            role="button"
            tabIndex={-1}
            aria-label="Close"
          />
          <div className="relative rounded-lg border bg-card p-6 shadow-lg max-w-sm w-full mx-4 space-y-4">
            <h3 className="text-sm font-semibold">What changed?</h3>
            <p className="text-xs text-muted-foreground">
              Saving will create a new version v{(asset.current_version ?? 0) + 1}.
            </p>
            <textarea
              value={changeSummary}
              onChange={(e) => onChangeSummaryChange(e.target.value)}
              placeholder="Describe your changes (optional)"
              rows={3}
              className="w-full rounded-md border bg-background px-3 py-2 text-sm outline-none ring-ring focus:ring-2 resize-none"
              autoFocus
            />
            <div className="flex justify-end gap-2">
              <button
                type="button"
                onClick={onChangeSummaryClose}
                className="rounded-md bg-secondary px-4 py-2 text-sm font-medium text-secondary-foreground hover:bg-secondary/80"
              >
                Cancel
              </button>
              <button
                type="button"
                onClick={onChangeSummarySave}
                disabled={contentUpdateMutation?.isPending}
                className="rounded-md bg-primary px-4 py-2 text-sm font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
              >
                {contentUpdateMutation?.isPending ? "Saving..." : "Save"}
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Revert confirmation modal */}
      {revertModalOpen && selectedVersion != null && (
        <div className="fixed inset-0 z-50 flex items-center justify-center">
          <div
            className="absolute inset-0 bg-black/50"
            onClick={onRevertClose}
            onKeyDown={(e) => { if (e.key === "Escape") onRevertClose(); }}
            role="button"
            tabIndex={-1}
            aria-label="Close"
          />
          <div className="relative rounded-lg border bg-card p-6 shadow-lg max-w-sm w-full mx-4 space-y-4">
            <div className="flex items-center gap-3">
              <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-full bg-amber-100 dark:bg-amber-950">
                <RotateCcw className="h-5 w-5 text-amber-600 dark:text-amber-400" />
              </div>
              <div>
                <h3 className="text-sm font-semibold">Revert to v{selectedVersion}?</h3>
                <p className="text-sm text-muted-foreground">
                  A new version (v{(asset.current_version ?? 0) + 1}) will be created from the content of v{selectedVersion}.
                </p>
              </div>
            </div>
            <div className="flex justify-end gap-2">
              <button
                type="button"
                onClick={onRevertClose}
                className="rounded-md bg-secondary px-4 py-2 text-sm font-medium text-secondary-foreground hover:bg-secondary/80"
              >
                Cancel
              </button>
              <button
                type="button"
                onClick={onConfirmRevert}
                disabled={revertMutation?.isPending}
                className="rounded-md bg-amber-600 px-4 py-2 text-sm font-medium text-white hover:bg-amber-700 disabled:opacity-50"
              >
                {revertMutation?.isPending ? "Reverting..." : "Revert"}
              </button>
            </div>
          </div>
        </div>
      )}
    </>
  );
}
