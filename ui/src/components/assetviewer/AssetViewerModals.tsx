import type { Asset } from "@/api/portal/types";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { ChangeSummaryDialog } from "./ChangeSummaryDialog";
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
  contentUpdateMutation?: MutationLike<{
    id: string;
    content: string;
    changeSummary?: string;
  }>;
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

/** The four questions the asset viewer stops to ask before it acts. */
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
  const nextVersion = (asset.current_version ?? 0) + 1;
  return (
    <>
      <ConfirmDialog
        open={deleteModalOpen}
        onOpenChange={(open) => {
          if (!open) onDeleteClose();
        }}
        title="Delete asset"
        description={
          <>
            Deleting <span className="font-medium">{asset.name}</span> cannot be undone.
          </>
        }
        confirmLabel="Delete"
        destructive
        loading={deleteMutation.isPending}
        onConfirm={onConfirmDelete}
      />

      <ConfirmDialog
        open={sharedSaveWarningOpen}
        onOpenChange={(open) => {
          if (!open) onSharedSaveWarningClose();
        }}
        title="Editing a shared asset"
        description={`You are editing a shared asset owned by ${
          asset.owner_email || "another user"
        }. Changes will be visible to the owner and all other recipients.`}
        confirmLabel="Continue"
        loading={contentUpdateMutation?.isPending}
        onConfirm={onSharedSaveWarningContinue}
      />

      <ChangeSummaryDialog
        open={changeSummaryOpen}
        onOpenChange={(open) => {
          if (!open) onChangeSummaryClose();
        }}
        nextVersion={nextVersion}
        value={changeSummary}
        onChange={onChangeSummaryChange}
        onSave={onChangeSummarySave}
        saving={!!contentUpdateMutation?.isPending}
      />

      <ConfirmDialog
        open={revertModalOpen && selectedVersion != null}
        onOpenChange={(open) => {
          if (!open) onRevertClose();
        }}
        title={`Revert to v${selectedVersion}?`}
        description={`A new version (v${nextVersion}) will be created from the content of v${selectedVersion}.`}
        confirmLabel="Revert"
        loading={revertMutation?.isPending}
        onConfirm={onConfirmRevert}
      />
    </>
  );
}
