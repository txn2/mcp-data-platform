import { FileUp, FolderOpen, Loader2, Search } from "lucide-react";
import { EmptyState } from "@/components/patterns/EmptyState";
import { Button } from "@/components/ui/button";
import { RESOURCE_POSITIONING } from "@/lib/positioning";
import type { ViewMode } from "@/components/listView";
import type { Resource } from "@/api/resources/types";
import { FolderList } from "./FolderList";
import { ResourceGrid } from "./ResourceGrid";
import { ResourcesTable } from "./ResourcesTable";
import { SearchHits } from "./SearchHits";
import type { Selection } from "./selection";
import type { FolderView } from "./tree";

/**
 * What the library shows at one location: the folders and files in it, or the
 * state standing in for them while it loads and when there is nothing there.
 *
 * A search replaces both with hits from across the library, because a search
 * that only looked in the open folder would make the tree worse than the flat
 * list it replaced.
 */
export function ResourceResults({
  view,
  viewMode,
  flat,
  searching,
  isLoading,
  filtering,
  admin,
  canWrite,
  canRenameFolder,
  selection,
  readOnlyNote,
  onOpen,
  onOpenFolder,
  onRenameFolder,
  onDropResources,
  onDropFolder,
  onReveal,
  onUpload,
}: {
  /** The folders and files at the location in view, or the search's hits. */
  view: FolderView;
  /** Tiles or rows, which is the reader's standing choice rather than the
   * folder's contents: a folder holding one PDF among fifty photographs used to
   * be fifty rows of filename and size (#1553). */
  viewMode: ViewMode;
  /**
   * True when the view is a flat list of hits from across the library rather
   * than one level of its tree: a search, or a tag chosen at the root. Each hit
   * names the folder it was found in, which a level of the tree does not need.
   */
  flat: boolean;
  searching: boolean;
  isLoading: boolean;
  // Set when a filter is narrowing the view, which is a different emptiness
  // from a folder nobody has uploaded to.
  filtering: boolean;
  admin: boolean;
  /** False leaves out the controls the server would refuse. */
  canWrite: boolean;
  /** False leaves out the folder rename, which names one library and so is not
   * offered on a view that spans several. */
  canRenameFolder: boolean;
  selection: Selection;
  // Where this library's material comes from, set only when the caller has no
  // write authority over the scope in view. It is the one signal for that: it
  // replaces the Upload control rather than sitting beside it, so the empty
  // state cannot end up offering an upload and naming a publisher at once. An
  // empty library the reader may not fill is not a prompt to upload.
  readOnlyNote?: string;
  onOpen: (resource: Resource) => void;
  onOpenFolder: (path: string) => void;
  onRenameFolder: (path: string) => void;
  onDropResources: (ids: string[], to: string) => void;
  onDropFolder: (from: string, to: string) => void;
  onReveal: (resource: Resource) => void;
  onUpload: () => void;
}) {
  if (isLoading) {
    return (
      <div className="flex items-center justify-center py-12 text-muted-foreground">
        <Loader2 className="mr-2 h-5 w-5 animate-spin" />
        Loading...
      </div>
    );
  }

  if (flat) {
    if (view.files.length === 0) {
      return (
        <EmptyState icon={Search} data-testid="resources-empty">
          <p className="font-medium text-foreground">
            {searching ? "No resources match this search" : "Nothing here matches"}
          </p>
          <p className="mt-1 text-xs">The whole library was looked through, not just this folder.</p>
        </EmptyState>
      );
    }
    return <SearchHits resources={view.files} admin={admin} onOpen={onOpen} onReveal={onReveal} />;
  }

  if (view.folders.length === 0 && view.files.length === 0) {
    return (
      <EmptyResults filtering={filtering} readOnlyNote={readOnlyNote} onUpload={onUpload} />
    );
  }

  return (
    <div className="space-y-3">
      <FolderList
        folders={view.folders}
        viewMode={viewMode}
        canWrite={canWrite}
        canRename={canRenameFolder}
        onOpen={onOpenFolder}
        onRename={onRenameFolder}
        onDropResources={onDropResources}
        onDropFolder={onDropFolder}
      />
      {view.files.length > 0 &&
        (viewMode === "grid" ? (
          <ResourceGrid
            resources={view.files}
            admin={admin}
            selection={selection}
            onOpen={onOpen}
          />
        ) : (
          <ResourcesTable
            resources={view.files}
            admin={admin}
            selection={selection}
            onOpen={onOpen}
          />
        ))}
    </div>
  );
}

/**
 * The three ways a location can be empty, which are three different things to
 * say: a filter that matched nothing, a library nobody may fill, and a folder
 * waiting for its first file.
 */
function EmptyResults({
  filtering,
  readOnlyNote,
  onUpload,
}: {
  filtering: boolean;
  readOnlyNote?: string;
  onUpload: () => void;
}) {
  if (filtering) {
    return (
      <EmptyState icon={FolderOpen} data-testid="resources-empty">
        <p className="font-medium text-foreground">No resources match this filter</p>
      </EmptyState>
    );
  }
  return (
    <EmptyState
      icon={FolderOpen}
      data-testid="resources-empty"
      action={
        !readOnlyNote && (
          <Button onClick={onUpload}>
            <FileUp />
            Upload Resource
          </Button>
        )
      }
    >
      <p className="font-medium text-foreground">Nothing here yet</p>
      {readOnlyNote && (
        <p data-testid="resources-read-only" className="mt-1 text-xs">
          {readOnlyNote}
        </p>
      )}
      <p className="mt-1 max-w-lg text-xs">{RESOURCE_POSITIONING}</p>
    </EmptyState>
  );
}
