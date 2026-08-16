import { useState, useEffect } from "react";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";

interface Props {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  name: string;
  description: string;
  saving: boolean;
  error?: string;
  onSave: (values: { name: string; description: string }) => void | Promise<void>;
}

/**
 * Renames and re-describes a collection the admin does not own. The admin route
 * updates the same two fields the owner's own editor does; the sections stay
 * with the owner's editor, so this dialog offers exactly what the route accepts.
 */
export function CollectionDetailsDialog({
  open,
  onOpenChange,
  name,
  description,
  saving,
  error,
  onSave,
}: Props) {
  const [draftName, setDraftName] = useState(name);
  const [draftDescription, setDraftDescription] = useState(description);

  // Reopening on a collection whose stored values changed (or a different
  // collection entirely) must show what is stored now, not the last draft.
  useEffect(() => {
    if (open) {
      setDraftName(name);
      setDraftDescription(description);
    }
  }, [open, name, description]);

  const trimmed = draftName.trim();

  return (
    <Dialog open={open} onOpenChange={saving ? undefined : onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Edit collection details</DialogTitle>
          <DialogDescription>
            The name and description shown wherever this collection appears.
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-4">
          <div className="space-y-2">
            <Label htmlFor="admin-collection-name">Name</Label>
            <Input
              id="admin-collection-name"
              value={draftName}
              onChange={(e) => setDraftName(e.target.value)}
            />
          </div>
          <div className="space-y-2">
            <Label htmlFor="admin-collection-description">Description</Label>
            <Textarea
              id="admin-collection-description"
              rows={4}
              value={draftDescription}
              onChange={(e) => setDraftDescription(e.target.value)}
            />
          </div>
          {error && <p className="text-sm text-destructive">{error}</p>}
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)} disabled={saving}>
            Cancel
          </Button>
          <Button
            onClick={() => void onSave({ name: trimmed, description: draftDescription })}
            // The server refuses an empty name; refusing it here keeps the
            // reader out of a round trip that can only fail.
            disabled={saving || trimmed === ""}
          >
            {saving ? "Saving..." : "Save"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
