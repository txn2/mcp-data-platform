import { useState, useCallback } from "react";
import { Loader2 } from "lucide-react";
import { useDeleteResource } from "@/api/resources/hooks";
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
      <div className="bg-card rounded-lg border shadow-lg w-full p-6 space-y-4">
        <h2 className="text-lg font-semibold">Delete Resource</h2>
        <p className="text-sm text-muted-foreground">
          Are you sure you want to delete <strong>{r.display_name}</strong>? This will remove both the metadata and the stored file. This action cannot be undone.
        </p>
        {error && <p className="text-sm text-destructive bg-destructive/10 rounded-md px-3 py-2">{error}</p>}
        <div className="flex justify-end gap-2 pt-2">
          <button onClick={onClose} className="rounded-md border px-4 py-2 text-sm hover:bg-muted transition-colors">Cancel</button>
          <button
            onClick={handleDelete}
            disabled={del.isPending}
            className="inline-flex items-center gap-1.5 rounded-md bg-destructive px-4 py-2 text-sm font-medium text-destructive-foreground hover:bg-destructive/90 disabled:opacity-50 transition-colors"
          >
            {del.isPending && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            Delete
          </button>
        </div>
      </div>
    </Overlay>
  );
}
