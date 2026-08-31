import { useState } from "react";
import { Folder, Pencil } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import type { ViewMode } from "@/components/listView";
import { cn } from "@/lib/utils";
import type { FolderEntry } from "./tree";
import { DRAG_FOLDER, readDrag } from "./drag";

/**
 * The folders at one level of a library.
 *
 * A folder row is the same shape as a file row and opens on click, which is how
 * every other portal list behaves. It is also the one drop target the tree has:
 * a file dragged onto it is refiled into it, and a folder dragged onto it is
 * nested inside it.
 */
export function FolderList({
  folders,
  viewMode,
  canWrite,
  canRename,
  onOpen,
  onRename,
  onDropResources,
  onDropFolder,
}: {
  folders: FolderEntry[];
  /**
   * Tiles or rows. A library root is mostly folders, so a switch the folders
   * ignored was a switch that appeared to do nothing there (#1553).
   */
  viewMode: ViewMode;
  /** False refuses drops and hides the drag handle, matching the server. */
  canWrite: boolean;
  /**
   * False hides the rename control. It is separate from canWrite because a
   * rename rewrites the path of every file under the folder in ONE library, and
   * a view spanning several names none to rewrite in (#1553).
   */
  canRename: boolean;
  onOpen: (path: string) => void;
  onRename: (path: string) => void;
  onDropResources: (ids: string[], to: string) => void;
  onDropFolder: (from: string, to: string) => void;
}) {
  const [over, setOver] = useState<string | null>(null);
  if (folders.length === 0) return null;

  // What a folder does when something is dragged onto it, stated once so a
  // tile and a row are the same drop target.
  const dropProps = (f: FolderEntry) => ({
    draggable: canRename,
    onDragStart: (e: React.DragEvent) => {
      e.dataTransfer.setData(DRAG_FOLDER, f.path);
      e.dataTransfer.effectAllowed = "move";
    },
    onDragOver: (e: React.DragEvent) => {
      if (!canWrite) return;
      e.preventDefault();
      setOver(f.path);
    },
    onDragLeave: () => setOver((o) => (o === f.path ? null : o)),
    onDrop: (e: React.DragEvent) => {
      setOver(null);
      if (!canWrite) return;
      e.preventDefault();
      const dropped = readDrag(e.dataTransfer);
      if (dropped.kind === "resources") onDropResources(dropped.ids, f.path);
      else if (canRename && dropped.kind === "folder" && dropped.path !== f.path) {
        onDropFolder(dropped.path, f.path);
      }
    },
  });

  if (viewMode === "grid") {
    return (
      <div
        className="grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-4"
        data-testid="folder-list"
      >
        {folders.map((f) => (
          <div key={f.path} className="relative" {...dropProps(f)}>
            {/* The whole face is the target, the way every other portal tile
                is, so the card rides a button rather than wrapping one. */}
            <Card
              asChild
              className={cn(
                "w-full gap-0 p-0 transition-colors hover:border-primary/50 hover:bg-muted/50",
                over === f.path && "border-primary bg-accent",
              )}
            >
              <button
                type="button"
                data-testid={`folder-row-${f.path}`}
                onClick={() => onOpen(f.path)}
                className="flex w-full items-center gap-2 px-4 py-3 text-left text-sm"
              >
                <Folder className="size-4 shrink-0 text-muted-foreground" />
                <span className="min-w-0 flex-1 truncate font-medium">{f.name}</span>
                <span className="shrink-0 text-xs text-muted-foreground tabular-nums">
                  {f.count}
                </span>
              </button>
            </Card>
            {/* Outside the button rather than inside it: a control nested in a
                button is neither clickable on its own nor valid markup. */}
            {canRename && (
              <Button
                variant="ghost"
                size="icon-xs"
                title={`Rename or move ${f.name}`}
                aria-label={`Rename or move ${f.name}`}
                onClick={() => onRename(f.path)}
                className="absolute top-1 right-1"
              >
                <Pencil />
              </Button>
            )}
          </div>
        ))}
      </div>
    );
  }

  return (
    <ul className="divide-y overflow-hidden rounded-lg border bg-card" data-testid="folder-list">
      {folders.map((f) => (
        <li key={f.path}>
          <div
            role="button"
            tabIndex={0}
            data-testid={`folder-row-${f.path}`}
            // Dragging a folder is a folder move, so it follows the rename gate
            // rather than the drop gate.
            draggable={canRename}
            onDragStart={(e) => {
              e.dataTransfer.setData(DRAG_FOLDER, f.path);
              e.dataTransfer.effectAllowed = "move";
            }}
            onDragOver={(e) => {
              if (!canWrite) return;
              e.preventDefault();
              setOver(f.path);
            }}
            onDragLeave={() => setOver((o) => (o === f.path ? null : o))}
            onDrop={(e) => {
              setOver(null);
              if (!canWrite) return;
              e.preventDefault();
              const dropped = readDrag(e.dataTransfer);
              if (dropped.kind === "resources") onDropResources(dropped.ids, f.path);
              else if (canRename && dropped.kind === "folder" && dropped.path !== f.path) {
                onDropFolder(dropped.path, f.path);
              }
            }}
            onClick={() => onOpen(f.path)}
            onKeyDown={(e) => {
              if (e.key === "Enter" || e.key === " ") {
                e.preventDefault();
                onOpen(f.path);
              }
            }}
            className={cn(
              "flex cursor-pointer items-center gap-2 px-4 py-2.5 text-sm hover:bg-muted/50",
              over === f.path && "bg-accent ring-1 ring-inset ring-primary",
            )}
          >
            <Folder className="size-4 shrink-0 text-muted-foreground" />
            <span className="min-w-0 flex-1 truncate font-medium">{f.name}</span>
            <span className="shrink-0 text-xs text-muted-foreground tabular-nums">
              {f.count}
            </span>
            {/* One action, so it is the control rather than a menu holding
                one item. It stops the click reaching the row, which would
                otherwise open the folder underneath the dialog. */}
            {canRename && (
              <Button
                variant="ghost"
                size="icon-xs"
                title={`Rename or move ${f.name}`}
                aria-label={`Rename or move ${f.name}`}
                onClick={(e) => {
                  e.stopPropagation();
                  onRename(f.path);
                }}
              >
                <Pencil />
              </Button>
            )}
          </div>
        </li>
      ))}
    </ul>
  );
}
