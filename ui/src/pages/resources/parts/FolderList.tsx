import { useState } from "react";
import { Folder, Pencil } from "lucide-react";
import { Button } from "@/components/ui/button";
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
  complete,
  canWrite,
  onOpen,
  onRename,
  onDropResources,
  onDropFolder,
}: {
  folders: FolderEntry[];
  /**
   * True when every resource in this library has been loaded. A count is built
   * from the pages fetched so far, so until it is, it says how many have
   * arrived rather than how many the folder holds.
   */
  complete: boolean;
  /** False hides the rename control and refuses drops, matching the server. */
  canWrite: boolean;
  onOpen: (path: string) => void;
  onRename: (path: string) => void;
  onDropResources: (ids: string[], to: string) => void;
  onDropFolder: (from: string, to: string) => void;
}) {
  const [over, setOver] = useState<string | null>(null);
  if (folders.length === 0) return null;

  return (
    <ul className="divide-y overflow-hidden rounded-lg border bg-card" data-testid="folder-list">
      {folders.map((f) => (
        <li key={f.path}>
          <div
            role="button"
            tabIndex={0}
            data-testid={`folder-row-${f.path}`}
            draggable={canWrite}
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
              else if (dropped.kind === "folder" && dropped.path !== f.path) {
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
            <span
              className="shrink-0 text-xs text-muted-foreground tabular-nums"
              title={complete ? undefined : "At least this many; more load as you scroll"}
            >
              {complete ? f.count : `${f.count}+`}
            </span>
            {/* One action, so it is the control rather than a menu holding
                one item. It stops the click reaching the row, which would
                otherwise open the folder underneath the dialog. */}
            {canWrite && (
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
