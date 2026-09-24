import { X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { formatBytes } from "@/lib/format";
import { RESOURCE_POSITIONING } from "@/lib/positioning";
import { displayPath, libraryCopy, type ResourceRoot } from "../scopes";
import { plural, type Entry } from "./model";

/** Move to, Tag and Delete over the selection, shown only while there is one. */
export function SelectionBar({
  count,
  canAct,
  onMove,
  onTag,
  onDelete,
  onClear,
}: {
  count: number;
  canAct: boolean;
  onMove: () => void;
  onTag: () => void;
  onDelete: () => void;
  onClear: () => void;
}) {
  if (count === 0) return null;
  return (
    // It floats over the foot of the listing rather than sitting above it: a
    // bar that pushed the rows down on the first click of a double-click moved
    // a different row under the second.
    <div
      data-testid="selection-bar"
      className="absolute bottom-3 left-1/2 z-10 flex -translate-x-1/2 items-center gap-2 rounded-lg border bg-card px-3 py-1.5 text-[12.5px] whitespace-nowrap shadow-lg"
    >
      <strong className="whitespace-nowrap">{count} selected</strong>
      <span className="flex-1" />
      {canAct && (
        <>
          <Button size="xs" variant="outline" onClick={onMove}>
            Move to...
          </Button>
          <Button size="xs" variant="outline" onClick={onTag}>
            Tag...
          </Button>
          <Button
            size="xs"
            variant="outline"
            className="border-destructive/30 text-destructive hover:bg-destructive/10 hover:text-destructive"
            onClick={onDelete}
          >
            Delete
          </Button>
        </>
      )}
      <Button size="icon-xs" variant="ghost" onClick={onClear} aria-label="Clear selection" title="Clear selection">
        <X />
      </Button>
    </div>
  );
}

/** Says a search or a tag spans the whole top-level folder, not the open one. */
export function SearchNote({
  query,
  tag,
  label,
  onClearTag,
}: {
  query: string;
  tag: string;
  label: string;
  onClearTag: () => void;
}) {
  return (
    <div data-testid="search-note" className="flex items-center gap-2 border-b bg-muted/50 px-3 py-1.5 text-[12.5px] text-muted-foreground">
      <span className="flex-1">
        {query
          ? `Searching everything in ${label}, not only the open folder. Each hit shows where it is.`
          : `Every file in ${label} tagged “${tag}”. Each one shows where it is.`}
      </span>
      {!query && tag && (
        <Button size="xs" variant="ghost" onClick={onClearTag}>
          Clear
        </Button>
      )}
    </div>
  );
}

/** Counts at this level, and the selection's count and size. */
export function StatusLine({ folders, files, picked }: { folders: number; files: number; picked: Entry[] }) {
  const bytes = picked.reduce((n, e) => n + (e.kind === "file" ? e.resource.size_bytes : 0), 0);
  return (
    <div
      data-testid="status-line"
      className="col-span-full flex h-7 items-center gap-4 border-t bg-card px-3 text-xs text-muted-foreground tabular-nums"
    >
      <span>
        {folders > 0 && `${plural(folders, "folder")}, `}
        {plural(files, "file")}
      </span>
      {picked.length > 0 && (
        <span>
          {picked.length} selected &middot; {formatBytes(bytes)}
        </span>
      )}
    </div>
  );
}

/**
 * An empty folder, a search that found nothing, and a top-level folder the
 * caller cannot add to are three different things to say.
 */
export function EmptyFolder({
  root,
  path,
  flat,
  canWrite,
  onUpload,
}: {
  root: ResourceRoot;
  path: string;
  flat: { query: string; tag: string } | null;
  canWrite: boolean;
  onUpload: () => void;
}) {
  if (flat) {
    return (
      <div data-testid="resources-empty" className="flex flex-col items-center gap-2.5 px-5 py-16 text-center text-muted-foreground">
        {flat.query
          ? `No files in ${root.label} match “${flat.query}”.`
          : `No files in ${root.label} are tagged “${flat.tag}”.`}
      </div>
    );
  }
  if (!canWrite) {
    return (
      <div data-testid="resources-empty" className="flex flex-col items-center gap-2.5 px-5 py-16 text-center text-muted-foreground">
        <div className="font-medium text-foreground">This folder is empty</div>
        <div data-testid="resources-read-only">{libraryCopy(root.target).source}</div>
        <div className="max-w-lg text-xs">{RESOURCE_POSITIONING}</div>
      </div>
    );
  }
  return (
    <div
      data-testid="resources-empty"
      className="m-4 flex flex-col items-center gap-2.5 rounded-[10px] border-2 border-dashed px-5 py-14 text-center text-muted-foreground"
    >
      <div className="font-medium text-foreground">This folder is empty</div>
      {path ? (
        <div>
          Drop files here or use Upload. They are filed into{" "}
          <span className="font-mono text-[11.5px]">{displayPath(root, path)}</span>.
        </div>
      ) : (
        <div>Every file is filed in a folder. Create one with New folder, or upload and name the folder there.</div>
      )}
      <Button size="sm" onClick={onUpload}>
        Upload
      </Button>
      <div className="max-w-lg text-xs">{RESOURCE_POSITIONING}</div>
    </div>
  );
}
