import { useState, useCallback } from "react";
import { AlertTriangle, Globe, Loader2 } from "lucide-react";
import { useAssetsUsingResource } from "@/api/portal/hooks/assetResources";
import { useDeleteResource } from "@/api/resources/hooks";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ModalScroll } from "@/components/ModalShell";
import type { Resource } from "@/api/resources/types";
import type { ReferencingAsset } from "@/api/portal/hooks/assetResources";

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
  // What this delete would break. A reference survives the delete of the file
  // it names, so the assets below keep rendering and keep reporting a picture
  // missing; naming them here is the difference between an owner deciding that
  // and finding out from a broken report (#1475).
  //
  // Delete waits on this. The whole point of the check is to be read before the
  // click, and a dialog that armed its destructive button while the answer was
  // still in flight would let the fastest click skip it entirely.
  const { data: usedBy, isPending: checking, isError: checkFailed } = useAssetsUsingResource(r.id);
  const busy = del.isPending || checking;

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
        <ReferencingAssetsWarning
          assets={usedBy?.data ?? []}
          hidden={usedBy?.hidden ?? 0}
          truncated={usedBy?.truncated ?? false}
        />
        <CheckFailedNotice failed={checkFailed} />
        {error && (
          <Alert variant="destructive">
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}
        <div className="flex justify-end gap-2 pt-2">
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button variant="destructive" onClick={handleDelete} disabled={busy}>
            {busy && <Loader2 className="animate-spin" />}
            Delete
          </Button>
        </div>
      </div>
    </ModalScroll>
  );
}

// ReferencingAssetsWarning names what this delete would break. The reference
// row survives the delete of the file it names, so those assets keep rendering
// and keep reporting a picture missing; naming them here is the difference
// between an owner deciding that and finding out from a broken report.
// CheckFailedNotice stands in for the list when the check itself could not run.
// Silence there would read as "nothing references this file", which is the one
// wrong answer this dialog exists to prevent someone acting on.
function CheckFailedNotice({ failed }: { failed: boolean }) {
  if (!failed) return null;
  return (
    <Alert variant="destructive" data-testid="delete-resource-check-failed">
      <AlertTriangle />
      <AlertDescription>
        Could not check which assets reference this file. Deleting it now may leave one
        rendering without it.
      </AlertDescription>
    </Alert>
  );
}

function ReferencingAssetsWarning({
  assets,
  hidden,
  truncated,
}: {
  assets: ReferencingAsset[];
  hidden: number;
  /** The server cut the answer at its bound; there are more than these. */
  truncated: boolean;
}) {
  const referenced = assets.length + hidden;
  if (referenced === 0) return null;
  return (
    <Alert data-testid="delete-resource-used-by-assets">
      <AlertTriangle />
      <AlertDescription className="space-y-2">
        <p>
          {truncated ? "At least " : ""}
          {referenced} {referenced === 1 ? "asset references" : "assets reference"} this file.
          Deleting it leaves them rendering without it.
        </p>
        <ul className="space-y-1">
          {assets.map((a) => (
            <li key={a.id} className="flex items-center gap-2 text-xs">
              <span className="truncate">{a.name}</span>
              {a.public && (
                <Badge variant="warning" className="rounded px-1.5">
                  <Globe className="h-3 w-3" />
                  public link
                </Badge>
              )}
            </li>
          ))}
        </ul>
        {hidden > 0 && (
          <p className="text-xs">
            {hidden} of them {hidden === 1 ? "is" : "are"} an asset you cannot open.
          </p>
        )}
      </AlertDescription>
    </Alert>
  );
}
