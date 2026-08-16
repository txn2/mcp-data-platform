import { Save, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";

interface Props {
  canManage: boolean;
  canSave: boolean;
  isSaving: boolean;
  onDelete: () => void;
  onSave: () => void;
}

/**
 * The collection editor's header actions. Delete is owner authority rather
 * than an editing right, so an Editor gets the form and Save without it.
 */
export function EditorHeaderActions({ canManage, canSave, isSaving, onDelete, onSave }: Props) {
  return (
    <>
      {canManage && (
        <Button
          variant="outline"
          size="sm"
          onClick={onDelete}
          title="Delete collection"
          className="border-destructive/50 text-destructive hover:bg-destructive/10 hover:text-destructive"
        >
          <Trash2 />
          Delete
        </Button>
      )}
      <Button size="sm" onClick={onSave} disabled={isSaving || !canSave}>
        <Save />
        {isSaving ? "Saving..." : "Save"}
      </Button>
    </>
  );
}
