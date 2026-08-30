import { useCallback, useMemo, useState } from "react";
import { useAuthStore } from "@/stores/auth";
import { usePersonas } from "@/api/admin/hooks";
import { InfiniteFooter } from "@/components/InfiniteFooter";
import {
  getStoredViewMode,
  storeViewMode,
  RESOURCE_VIEW_STORAGE_KEY,
  type ViewMode,
} from "@/components/listView";
import type { Resource } from "@/api/resources/types";
import { BulkActionModal, type BulkAction } from "./modals/BulkActionModal";
import { FolderMoveModal } from "./modals/FolderMoveModal";
import { UploadModal } from "./modals/UploadModal";
import { FolderBreadcrumbs } from "./parts/FolderBreadcrumbs";
import { tagOptions } from "./parts/groups";
import { RecentResources } from "./parts/RecentResources";
import { ResourceFilterBar } from "./parts/ResourceFilterBar";
import { ResourceResults } from "./parts/ResourceResults";
import { SelectionBar } from "./parts/SelectionBar";
import { useSelection } from "./parts/selection";
import { childFolders, folderPaths, isUnder } from "./parts/tree";
import { useResourceLibrary, type ResourceSort } from "./parts/useResourceLibrary";
import {
  canUpload,
  canWriteScope,
  isPlatformAdmin,
  libraryChoices,
  libraryCopy,
  targetForTab,
  type ScopeTarget,
} from "./scopes";

interface Props {
  admin?: boolean;
  /**
   * The in-app location the shell is showing, query string included. The
   * library and the folder in view are read out of it (#1530), so a reload, a
   * Back, and a pasted link all land on the same place.
   */
  location: string;
  onNavigate?: (path: string, opts?: { replace?: boolean }) => void;
}

/** What dialog the library currently has open, if any. */
type Dialog =
  | { kind: "upload" }
  | { kind: "bulk"; action: BulkAction; to?: string }
  | { kind: "folder"; from: string; to?: string }
  | null;

export function ResourcesPage({ admin = false, location, onNavigate }: Props) {
  const user = useAuthStore((s) => s.user);
  // The deployment's persona list, which the picker in the Edit dialog, the
  // library picker and the upload destinations are all filled from. Fetched for
  // a platform administrator on either page: the libraries they may file a
  // resource into include every persona, and their own claims name only the
  // personas they belong to (#1527). Everyone else derives their personas from
  // those claims and never asks.
  const { data: personaData } = usePersonas(isPlatformAdmin(user));
  const personaNames = useMemo(
    () => (personaData?.personas ?? []).map((p) => p.name),
    [personaData],
  );

  // The section this library belongs to, which is both where its own address
  // is written and where a resource opened from it lives.
  const basePath = admin ? "/admin/resources" : "/resources";
  const libraries = useMemo(
    () => libraryChoices(user, personaNames),
    [user, personaNames],
  );
  const library = useResourceLibrary(libraries.map((l) => l.key), {
    basePath,
    location,
    onNavigate,
  });
  const selection = useSelection();
  const [dialog, setDialog] = useState<Dialog>(null);
  const [viewMode, setViewMode] = useState<ViewMode>(() =>
    getStoredViewMode(RESOURCE_VIEW_STORAGE_KEY),
  );

  const target = targetForTab(library.activeTab, user);
  const writable = canUpload(user, target, personaNames);
  // Renaming a folder rewrites the path of every file under it in ONE library,
  // so it is offered only where the view names one. The All view names none,
  // and a folder there can hold files from several.
  const renamable = target !== null && canWriteScope(user, target);
  const view = useMemo(
    // A search is not a location, so its hits are handed through whole and the
    // tree is not built over them: they are from all over the library.
    () =>
      library.flat
        ? { folders: [], files: library.resources }
        : {
            folders: childFolders(library.folders, library.path),
            // The files directly here. The listing is narrowed to this folder
            // AND everything beneath it -- which is what makes a subfolder's
            // count mean something -- so the ones filed deeper belong to those
            // subfolders rather than to this level.
            files: library.resources.filter((r) => r.path === library.path),
          },
    [library.folders, library.resources, library.path, library.flat],
  );
  // Every folder the library holds, for the pickers' completions. It is the
  // server's list, so it is complete rather than whatever a page happened to
  // carry (#1555).
  const folders = useMemo(() => folderPaths(library.folders), [library.folders]);
  const picked = useMemo(
    () => library.resources.filter((r) => selection.has(r.id)),
    [library.resources, selection],
  );

  // Opening a resource leaves the library, so the entry being left has to carry
  // the view: without it Back returns to a plain library rather than to this
  // folder with this filter (#1470).
  const openResource = useCallback(
    (r: Resource) => {
      onNavigate?.(library.address, { replace: true });
      onNavigate?.(`${basePath}/${r.id}`);
    },
    [onNavigate, library.address, basePath],
  );

  // Revealing a hit walks the tree to the folder it is in, which is the only
  // way a whole-library search leads anywhere but the file itself.
  const reveal = useCallback(
    (r: Resource) => onNavigate?.(library.addressOf(r.path)),
    [onNavigate, library],
  );

  // A drop is the same move the selection bar runs, with the destination
  // already chosen: the dialog opens on the folder the files were dropped on,
  // and it still asks, because dragging is easy to do by accident and this one
  // rewrites an address.
  const dropResources = useCallback(
    (ids: string[], to: string) => {
      selection.add(ids);
      setDialog({ kind: "bulk", action: "move", to });
    },
    [selection],
  );

  function changeViewMode(mode: ViewMode) {
    setViewMode(mode);
    storeViewMode(mode, RESOURCE_VIEW_STORAGE_KEY);
  }

  // The library's own name for its trail, and where its material comes from
  // when the caller may not add to it. The note is set only then: it replaces
  // the Upload control rather than sitting beside it.
  const libraryLabel = headingFor(libraries, library.activeTab, target);
  const readOnlyNote = writable ? undefined : libraryCopy(target).source;

  return (
    <div className="space-y-4">
      <FolderBreadcrumbs
        library={libraryLabel}
        path={library.path}
        onOpen={library.openFolder}
        className="text-sm"
      />

      <ResourceFilterBar
        libraries={libraries}
        activeLibrary={library.activeTab}
        onLibraryChange={library.setActiveTab}
        search={library.searchInput}
        onSearchChange={library.setSearchInput}
        tag={library.tag}
        onTagChange={library.setTag}
        tagOptions={tagOptions(library.tags, library.tag)}
        sort={library.sort}
        onSortChange={(s: ResourceSort) => library.setSort(s)}
        showSort={admin}
        viewMode={viewMode}
        onViewModeChange={changeViewMode}
        canUpload={writable}
        onUpload={() => setDialog({ kind: "upload" })}
        readOnlyNote={readOnlyNote}
      />

      <SelectionBar
        count={selection.ids.length}
        onAct={(action) => setDialog({ kind: "bulk", action })}
        onClear={selection.clear}
      />

      {/* At the root of a library, and only with nothing narrowing it: a
          search and a tag filter are already an answer to "what here is
          relevant", and a differently-ordered second answer above them
          competes with the one that was asked for. */}
      {library.path === "" && !library.filtering && (
        <RecentResources
          activeTab={library.activeTab}
          viewMode={viewMode}
          onOpen={openResource}
        />
      )}

      <ResourceResults
        view={view}
        viewMode={viewMode}
        flat={library.flat}
        searching={library.searching}
        isLoading={library.isLoading}
        filtering={library.filtering}
        admin={admin}
        canWrite={writable}
        canRenameFolder={renamable}
        selection={selection}
        readOnlyNote={readOnlyNote}
        onOpen={openResource}
        onOpenFolder={library.openFolder}
        onRenameFolder={(from) => setDialog({ kind: "folder", from })}
        onDropResources={dropResources}
        onDropFolder={(from, to) =>
          setDialog({ kind: "folder", from, to: `${to}/${from.split("/").pop()}` })
        }
        onReveal={reveal}
        onUpload={() => setDialog({ kind: "upload" })}
      />

      <InfiniteFooter
        hasMore={library.hasNextPage}
        isLoadingMore={library.isFetchingNextPage}
        onLoadMore={library.fetchNextPage}
      />

      {library.listing && library.total > library.resources.length && (
        <p className="text-center text-sm text-muted-foreground">
          Showing {library.resources.length} of {library.total} resources
        </p>
      )}

      <LibraryDialogs
        dialog={dialog}
        admin={admin}
        personaNames={personaNames}
        target={target}
        folders={folders}
        picked={picked}
        library={library}
        onClose={() => setDialog(null)}
        onBulkDone={() => {
          selection.clear();
          setDialog(null);
        }}
      />
    </div>
  );
}

/**
 * What heads the folder trail: the picker's own name for the library in view.
 *
 * The All view spans every library and is the target of none, so a heading read
 * off a move target would call it "My Resources" -- a different library, and
 * another entry in the picker beside it.
 */
function headingFor(
  libraries: { key: string; label: string }[],
  activeTab: string,
  target: ScopeTarget | null,
): string {
  return libraries.find((l) => l.key === activeTab)?.label ?? libraryCopy(target).name;
}

/**
 * Whichever dialog the library has open: the upload form, one action over a
 * selection, or a folder move.
 *
 * Together rather than inline because they share what they act on -- the
 * library in view, its folders, the files picked -- and because a page whose
 * body is three conditionals plus a filter bar is a page nobody can read.
 */
function LibraryDialogs({
  dialog,
  admin,
  personaNames,
  target,
  folders,
  picked,
  library,
  onClose,
  onBulkDone,
}: {
  dialog: Dialog;
  admin: boolean;
  personaNames: string[];
  target: ScopeTarget | null;
  folders: string[];
  picked: Resource[];
  library: ReturnType<typeof useResourceLibrary>;
  onClose: () => void;
  onBulkDone: () => void;
}) {
  if (dialog?.kind === "upload") {
    return (
      <UploadModal
        onClose={onClose}
        admin={admin}
        personaNames={personaNames}
        destination={target}
        folder={library.path}
        folders={folders}
      />
    );
  }
  if (dialog?.kind === "bulk") {
    return (
      <BulkActionModal
        action={dialog.action}
        resources={picked}
        folders={folders}
        currentPath={dialog.to ?? library.path}
        onClose={onClose}
        onDone={onBulkDone}
      />
    );
  }
  if (dialog?.kind === "folder" && target) {
    return (
      <FolderMoveModal
        library={target}
        from={dialog.from}
        suggestedTo={dialog.to}
        folders={folders}
        onClose={onClose}
        onMoved={(to) => {
          onClose();
          // A folder that has just been renamed no longer exists at the address
          // the person is standing at, so they are taken to where their files
          // went rather than left looking at nothing.
          if (isUnder(library.path, dialog.from)) {
            library.openFolder(to + library.path.slice(dialog.from.length));
          }
        }}
      />
    );
  }
  return null;
}
