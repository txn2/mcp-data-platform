import { Loader2 } from "lucide-react";
import { InfiniteFooter } from "@/components/InfiniteFooter";
import { cn } from "@/lib/utils";
import { downloadResource } from "./actions";
import { EmptyFolder, SearchNote, SelectionBar, StatusLine } from "./Chrome";
import { FmDialogs } from "./FmDialogs";
import { FolderTree } from "./FolderTree";
import { Listing } from "./Listing";
import { nextSort } from "./model";
import { ContextMenu, Toast } from "./Overlays";
import { PathBar } from "./PathBar";
import { PreviewPane } from "./PreviewPane";
import { contextItems, useFileManager, type FileManagerProps } from "./useFileManager";

type FM = ReturnType<typeof useFileManager>;

/**
 * The Resources page as a file manager (#1872): a folder tree on the left, one
 * listing of the folder in view, a preview pane on the right, and the
 * selection, drag, context-menu and keyboard model every file manager shares.
 * It follows the approved POC (build/1872/poc.html).
 */
export function FileManager(props: FileManagerProps) {
  const fm = useFileManager(props);
  return (
    <div
      data-testid="file-manager"
      className={cn(
        "grid h-[calc(100dvh-11rem)] min-h-[520px] grid-cols-1 grid-rows-[auto_auto_minmax(0,1fr)_auto] overflow-hidden rounded-[10px] border bg-card text-sm",
        "min-[761px]:grid-cols-[210px_minmax(0,1fr)] min-[761px]:grid-rows-[auto_minmax(0,1fr)_auto]",
        fm.prefs.preview ? "xl:grid-cols-[240px_minmax(0,1fr)_280px]" : "xl:grid-cols-[240px_minmax(0,1fr)]",
      )}
    >
      <Bar fm={fm} />
      <aside
        aria-label="Folders"
        className="max-h-[200px] min-h-0 overflow-hidden border-b min-[761px]:max-h-none min-[761px]:border-r min-[761px]:border-b-0"
      >
        <FolderTree
          roots={fm.roots}
          people={fm.admin}
          ownIDs={[fm.user?.user_id ?? "", fm.user?.email ?? ""].filter(Boolean)}
          current={{ root: fm.root.key, path: fm.path, searching: fm.data.flat }}
          onOpen={(r, p) => fm.loc.go(r.key, p)}
          drop={{ accepts: (r, e) => fm.accepts(r, e), onDrop: fm.dropOn }}
        />
      </aside>
      <Main fm={fm} />
      <Pane fm={fm} />
      <StatusLine
        folders={fm.data.entries.filter((e) => e.kind === "folder").length}
        files={fm.data.fileTotal}
        picked={fm.picked}
      />
      {fm.menu && <ContextMenu x={fm.menu.x} y={fm.menu.y} items={contextItems(fm)} onClose={() => fm.setMenu(null)} />}
      <Toast toast={fm.toast} onDismiss={fm.dismiss} />
      <FmDialogs
        dialog={fm.dialog}
        root={fm.root}
        path={fm.path}
        folders={fm.data.folders}
        admin={fm.admin}
        personaNames={fm.personaNames}
        actions={fm.actions}
        onClose={() => fm.setDialog(null)}
        onDone={(message) => {
          fm.setDialog(null);
          fm.selection.clear();
          if (message) fm.show(message);
        }}
      />
    </div>
  );
}

function Bar({ fm }: { fm: FM }) {
  return (
    <PathBar
      root={fm.root}
      path={fm.path}
      query={fm.loc.searchInput}
      canBack={fm.loc.canBack}
      canForward={fm.loc.canForward}
      onBack={fm.loc.back}
      onForward={fm.loc.forward}
      onOpen={(p) => fm.loc.go(fm.root.key, p)}
      onQuery={fm.loc.setSearchInput}
      viewMode={fm.prefs.viewMode}
      onViewMode={fm.prefs.setViewMode}
      preview={fm.prefs.preview}
      onPreview={fm.prefs.togglePreview}
      canWrite={fm.canWrite}
      onNewFolder={fm.startNewFolder}
      onUpload={() => fm.upload(false)}
      onUploadMany={() => fm.upload(true)}
      drop={{ accepts: (e) => fm.accepts(fm.root, e), onDrop: (p, e) => fm.dropOn(fm.root, p, e) }}
    />
  );
}

/** The listing column: the selection bar, the search note, and the rows. */
function Main({ fm }: { fm: FM }) {
  const flat = fm.data.flat ? { query: fm.loc.query, tag: fm.loc.tag, label: fm.root.label } : null;
  const files = (e: React.DragEvent) => e.dataTransfer.types.includes("Files") && fm.canWrite;
  return (
    <section className="relative flex min-h-0 min-w-0 flex-col">
      <SelectionBar
        count={fm.selection.keys.length}
        canAct={fm.canAct}
        onMove={() => fm.act("move")}
        onTag={() => fm.act("tag")}
        onDelete={() => fm.act("delete")}
        onClear={fm.selection.clear}
      />
      {flat && <SearchNote {...flat} onClearTag={() => fm.loc.setTag("")} />}
      <div
        data-listing
        tabIndex={-1}
        className="relative min-h-0 flex-1 overflow-auto outline-none"
        onKeyDown={fm.onKey}
        onClick={(e) => e.target === e.currentTarget && fm.selection.clear()}
        onContextMenu={(e) => fm.handlers.onContextMenu(null, e)}
        onDragOver={(e) => files(e) && e.preventDefault()}
        onDrop={(e) => {
          if (!files(e)) return;
          e.preventDefault();
          fm.dropOn(fm.root, fm.path, e);
        }}
      >
        <Body fm={fm} flat={flat} />
      </div>
    </section>
  );
}

function Body({ fm, flat }: { fm: FM; flat: { query: string; tag: string } | null }) {
  if (fm.data.isLoading) {
    return (
      <div className="flex items-center justify-center py-12 text-muted-foreground">
        <Loader2 className="mr-2 size-5 animate-spin" />
        Loading...
      </div>
    );
  }
  if (fm.data.entries.length === 0 && fm.edit === null) {
    return (
      <EmptyFolder root={fm.root} path={fm.path} flat={flat} canWrite={fm.canWrite} onUpload={() => fm.upload(true)} />
    );
  }
  return (
    <>
      <Listing
        entries={fm.data.entries}
        viewMode={fm.prefs.viewMode}
        flat={fm.data.flat}
        root={fm.root}
        admin={fm.admin}
        selection={fm.selection}
        sort={fm.loc.sort}
        onSort={(c) => fm.loc.setSort(nextSort(c, fm.loc.sort))}
        edit={fm.edit}
        handlers={fm.handlers}
      />
      <InfiniteFooter
        hasMore={fm.data.hasNextPage}
        isLoadingMore={fm.data.isFetchingNextPage}
        onLoadMore={fm.data.fetchNextPage}
      />
    </>
  );
}

function Pane({ fm }: { fm: FM }) {
  const first = fm.picked[0];
  return (
    <aside
      aria-label="Preview"
      data-testid="preview-pane"
      className={cn("hidden min-h-0 flex-col overflow-auto border-l", fm.prefs.preview && "xl:flex")}
    >
      <PreviewPane
        picked={fm.picked}
        root={fm.root}
        path={fm.path}
        here={fm.data.here}
        canWrite={fm.canAct}
        actions={{
          onOpen: fm.openEntry,
          onDownload: (r) => void downloadResource(r),
          onCopyURI: fm.copyURI,
          onRename: () => first && fm.startRename(first.key),
          onMove: () => fm.act("move"),
          onTag: () => fm.act("tag"),
          onDelete: () => fm.act("delete"),
          onTagFilter: fm.loc.setTag,
        }}
      />
    </aside>
  );
}
