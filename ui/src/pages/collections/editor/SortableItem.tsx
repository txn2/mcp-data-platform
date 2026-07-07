import { useState } from "react";
import { Trash2, GripVertical, FileText, Eye } from "lucide-react";
import { useSortable } from "@dnd-kit/sortable";
import { CSS } from "@dnd-kit/utilities";
import type { ItemDraft } from "./types";

/** Sortable item card within a section. */
export function SortableItem({
  item,
  onRemove,
  onPreview,
}: {
  item: ItemDraft;
  onRemove: () => void;
  onPreview: () => void;
}) {
  const { attributes, listeners, setNodeRef, transform, transition } = useSortable({ id: item.id });
  const [confirmDelete, setConfirmDelete] = useState(false);

  const style = {
    transform: CSS.Transform.toString(transform),
    transition,
  };

  return (
    <div
      ref={setNodeRef}
      style={style}
      className="flex items-center gap-2 rounded border bg-muted/50 px-3 py-1.5 text-sm"
    >
      <button {...attributes} {...listeners} className="cursor-grab text-muted-foreground hover:text-foreground" title="Drag to reorder">
        <GripVertical className="h-3.5 w-3.5" />
      </button>
      <FileText className="h-3.5 w-3.5 text-muted-foreground shrink-0" />
      <span className="flex-1 truncate">{item.assetName || item.asset_id}</span>
      {item.assetContentType && (
        <span className="text-xs text-muted-foreground shrink-0">{item.assetContentType}</span>
      )}
      <button onClick={onPreview} className="text-muted-foreground hover:text-foreground shrink-0" title="Preview">
        <Eye className="h-3 w-3" />
      </button>
      {confirmDelete ? (
        <span className="flex items-center gap-1 shrink-0">
          <button onClick={onRemove} className="text-xs text-destructive font-medium hover:underline">Remove</button>
          <button onClick={() => setConfirmDelete(false)} className="text-xs text-muted-foreground hover:underline">Cancel</button>
        </span>
      ) : (
        <button onClick={() => setConfirmDelete(true)} className="text-muted-foreground hover:text-destructive shrink-0" title="Remove">
          <Trash2 className="h-3 w-3" />
        </button>
      )}
    </div>
  );
}
