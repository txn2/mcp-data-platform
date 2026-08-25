import { useState, useCallback } from "react";
import { Loader2 } from "lucide-react";
import { useDeleteResource } from "@/api/resources/hooks";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { ModalScroll } from "@/components/ModalShell";
import type { Resource } from "@/api/resources/types";

/**
 * DeleteConfirm stays on the natural-height shape rather than the capped one
 * the other resource modals take: it is one paragraph and two buttons, with no
 * region that grows with the resource, so there is no header to hold in place
 * and nothing for a cap to save.
 */
export function DeleteConfirm({
  resource: r,
  onClose,
  onDeleted,
}: {
  resource: Resource;
  onClose: () => void;
  /** Where the caller goes once the resource is gone. A library dismisses the
   * dialog and stays; a page addressed by this resource has to leave, so it
   * cannot be told about the delete through onClose, which also fires on
   * Cancel. Absent, a successful delete just dismisses. */
  onDeleted?: () => void;
}) {
  const del = useDeleteResource();
  const [error, setError] = useState("");

  const handleDelete = useCallback(async () => {
    try {
      await del.mutateAsync(r.id);
      (onDeleted ?? onClose)();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Delete failed");
    }
  }, [r.id, del, onClose, onDeleted]);

  return (
    <ModalScroll onClose={onClose} label="Delete Resource" busy={del.isPending}>
      <div className="w-full space-y-4 rounded-lg border bg-card p-6 shadow-lg">
        <h2 className="text-lg font-semibold">Delete Resource</h2>
        <p className="text-sm text-muted-foreground">
          Are you sure you want to delete <strong>{r.display_name}</strong>? This will remove both the metadata and the stored file. This action cannot be undone.
        </p>
        {error && (
          <Alert variant="destructive">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}
        <div className="flex justify-end gap-2 pt-2">
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button variant="destructive" onClick={handleDelete} disabled={del.isPending}>
            {del.isPending && <Loader2 className="animate-spin" />}
            Delete
          </Button>
        </div>
      </div>
    </ModalScroll>
  );
}
