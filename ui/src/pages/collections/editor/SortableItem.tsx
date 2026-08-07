import { useState } from "react";
import { Trash2, GripVertical, FileText, Eye } from "lucide-react";
import { useSortable } from "@dnd-kit/sortable";
import { CSS } from "@dnd-kit/utilities";
import { Button } from "@/components/ui/button";
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
  const { attributes, listeners, setNodeRef, transform, transition } = useSortable({
    id: item.id,
  });
  const [confirmDelete, setConfirmDelete] = useState(false);

  const style = {
    transform: CSS.Transform.toString(transform),
    transition,
  };

  return (
    <div
      ref={setNodeRef}
      style={style}
      className="flex items-center gap-2 rounded-md border bg-muted/50 px-3 py-1.5 text-sm"
    >
      <Button
        {...attributes}
        {...listeners}
        variant="ghost"
        size="icon-xs"
        title="Drag to reorder"
        className="cursor-grab text-muted-foreground"
      >
        <GripVertical />
      </Button>
      <FileText className="size-3.5 shrink-0 text-muted-foreground" />
      <span className="flex-1 truncate">{item.assetName || item.asset_id}</span>
      {item.assetContentType && (
        <span className="shrink-0 text-xs text-muted-foreground">{item.assetContentType}</span>
      )}
      <Button
        variant="ghost"
        size="icon-xs"
        onClick={onPreview}
        title="Preview"
        className="text-muted-foreground"
      >
        <Eye />
      </Button>
      {/* Removing a draft item is undone by adding it back, so it confirms in
          place rather than opening a dialog over the section being edited. */}
      {confirmDelete ? (
        <span className="flex shrink-0 items-center gap-1">
          <Button variant="link" size="xs" onClick={onRemove} className="text-destructive">
            Remove
          </Button>
          <Button
            variant="link"
            size="xs"
            onClick={() => setConfirmDelete(false)}
            className="text-muted-foreground"
          >
            Cancel
          </Button>
        </span>
      ) : (
        <Button
          variant="ghost"
          size="icon-xs"
          onClick={() => setConfirmDelete(true)}
          title="Remove"
          className="text-muted-foreground hover:text-destructive"
        >
          <Trash2 />
        </Button>
      )}
    </div>
  );
}
