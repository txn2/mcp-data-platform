import { useCallback, useState } from "react";
import { useRollbackChangeset } from "@/api/admin/hooks";
import type { Changeset } from "@/api/admin/types";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { KnowledgeStatusBadge } from "@/components/knowledge/KnowledgeStatusBadge";
import { DrawerShell } from "@/components/patterns/DrawerShell";
import { Button } from "@/components/ui/button";
import { formatUser } from "@/lib/formatUser";
import { errorMessage } from "@/lib/utils";
import { JsonBlock, LabeledBlock, MetaField, MetaGrid } from "./fields";
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
  const [confirming, setConfirming] = useState(false);
  const rollback = useRollbackChangeset();

  // Rolling back rewrites the catalog, so it is confirmed in a real dialog and
  // the drawer stays open until the write lands: a failure has to be readable
  // where it was triggered.
  const handleRollback = useCallback(async () => {
    await rollback.mutateAsync(changeset.id);
    setConfirming(false);
    onClose();
  }, [changeset.id, rollback, onClose]);

  return (
    <DrawerShell
      title="Changeset Detail"
      onClose={onClose}
      footer={
        !changeset.rolled_back && (
          <Button
            variant="destructive"
            onClick={() => setConfirming(true)}
            disabled={rollback.isPending}
          >
            Rollback Changeset
          </Button>
        )
      }
    >
      <MetaGrid>
        <MetaField label="ID" mono>
          {changeset.id}
        </MetaField>
        <MetaField label="Created At">
          {new Date(changeset.created_at).toLocaleString()}
        </MetaField>
        <MetaField label="Target URN" mono wide>
          {changeset.target_urn}
        </MetaField>
        <MetaField label="Change Type">{formatCategory(changeset.change_type)}</MetaField>
        <MetaField label="Status">
          <KnowledgeStatusBadge status={changeset.rolled_back ? "rolled_back" : "active"} />
        </MetaField>
        <MetaField label="Approved By" title={changeset.approved_by}>
          {formatUser(changeset.approved_by, userLabels[changeset.approved_by])}
        </MetaField>
        <MetaField label="Applied By" title={changeset.applied_by}>
          {formatUser(changeset.applied_by, userLabels[changeset.applied_by])}
        </MetaField>
      </MetaGrid>

      <LabeledBlock label="Previous Value">
        <JsonBlock value={changeset.previous_value} />
      </LabeledBlock>

      <LabeledBlock label="New Value">
        <JsonBlock value={changeset.new_value} />
      </LabeledBlock>

      {changeset.source_insight_ids.length > 0 && (
        <LabeledBlock label="Source Insight IDs">
          <div className="space-y-1">
            {changeset.source_insight_ids.map((id) => (
              <p key={id} className="break-all font-mono text-xs text-muted-foreground">
                {id}
              </p>
            ))}
          </div>
        </LabeledBlock>
      )}

      {changeset.rolled_back && (
        <MetaGrid className="border-t pt-3">
          <MetaField label="Rolled Back By" title={changeset.rolled_back_by}>
            {formatUser(
              changeset.rolled_back_by ?? "",
              userLabels[changeset.rolled_back_by ?? ""],
            )}
          </MetaField>
          <MetaField label="Rolled Back At">
            {changeset.rolled_back_at
              ? new Date(changeset.rolled_back_at).toLocaleString()
              : "-"}
          </MetaField>
        </MetaGrid>
      )}

      <ConfirmDialog
        open={confirming}
        onOpenChange={setConfirming}
        title="Roll back this changeset?"
        description="The values it wrote are restored to what they were before it was applied."
        confirmLabel="Roll back"
        destructive
        loading={rollback.isPending}
        error={rollback.error ? errorMessage(rollback.error) : undefined}
        onConfirm={handleRollback}
      />
    </DrawerShell>
  );
}
