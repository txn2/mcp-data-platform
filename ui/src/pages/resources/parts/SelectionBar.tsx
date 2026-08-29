import { FolderInput, Tag, Trash2, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import type { BulkAction } from "../modals/BulkActionModal";

/**
 * What can be done to the files a person has picked.
 *
 * It appears only when something is selected, and it states the count: a
 * selection carried out of the folder it was made in is then visible rather
 * than a surprise on the next action.
 */
export function SelectionBar({
  count,
  onAct,
  onClear,
}: {
  count: number;
  onAct: (action: BulkAction) => void;
  onClear: () => void;
}) {
  if (count === 0) return null;
  return (
    <div
      data-testid="selection-bar"
      className="flex flex-wrap items-center gap-2 rounded-lg border bg-muted/40 px-3 py-2"
    >
      <span className="text-sm font-medium">
        {count} selected
      </span>
      <div className="flex-1" />
      <Button variant="outline" size="sm" onClick={() => onAct("move")}>
        <FolderInput />
        Move
      </Button>
      <Button variant="outline" size="sm" onClick={() => onAct("tag")}>
        <Tag />
        Tag
      </Button>
      <Button
        variant="outline"
        size="sm"
        onClick={() => onAct("delete")}
        className="border-destructive/30 text-destructive hover:bg-destructive/10 hover:text-destructive"
      >
        <Trash2 />
        Delete
      </Button>
      <Button variant="ghost" size="icon-sm" onClick={onClear} aria-label="Clear selection">
        <X />
      </Button>
    </div>
  );
}
