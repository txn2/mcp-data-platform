import { useState, useCallback } from "react";
import { Loader2 } from "lucide-react";
import { useDeleteResource } from "@/api/resources/hooks";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import type { Resource } from "@/api/resources/types";
import { Overlay } from "./Overlay";

export function DeleteConfirm({ resource: r, onClose }: { resource: Resource; onClose: () => void }) {
  const del = useDeleteResource();
  const [error, setError] = useState("");

  const handleDelete = useCallback(async () => {
    try {
      await del.mutateAsync(r.id);
      onClose();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Delete failed");
    }
  }, [r.id, del, onClose]);

  return (
    <Overlay onClose={onClose}>
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
    </Overlay>
  );
}
