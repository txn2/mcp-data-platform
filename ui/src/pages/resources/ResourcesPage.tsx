import { useCallback, useMemo, useState } from "react";
import {
  FileUp,
  Globe,
  Users,
  User,
  FolderOpen,
  type LucideIcon,
} from "lucide-react";
import { useAuthStore } from "@/stores/auth";
import { usePersonas } from "@/api/admin/hooks";
import { InfiniteFooter } from "@/components/InfiniteFooter";
import { FilterSelect } from "@/components/patterns/FilterSelect";
import { SearchInput } from "@/components/patterns/SearchInput";
import { Button } from "@/components/ui/button";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import type { Resource } from "@/api/resources/types";
import { BulkActionModal, type BulkAction } from "./modals/BulkActionModal";
import { FolderMoveModal } from "./modals/FolderMoveModal";
import { UploadModal } from "./modals/UploadModal";
import { FolderBreadcrumbs } from "./parts/FolderBreadcrumbs";
import { tagOptions } from "./parts/groups";
import { ResourceResults } from "./parts/ResourceResults";
import { SelectionBar } from "./parts/SelectionBar";
import { useSelection } from "./parts/selection";
import { everyFolder, folderView, isUnder, type FolderView } from "./parts/tree";
import { useResourceLibrary, type ResourceSort } from "./parts/useResourceLibrary";
import { adminReachNote, canWriteScope, libraryCopy, targetForTab, type ScopeTarget } from "./scopes";

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

const SORT_OPTIONS = [
  { value: "updated", label: "Recently updated" },
  { value: "last_read", label: "Recently read" },
];

// scopeTabs is the set of libraries a caller can look at. A reader sees their
// own, their persona's, and the global one; an admin sees every persona's plus
// the unfiltered "All".
function scopeTabs(
  admin: boolean,
  personaNames: string[],
  userPersona: string | undefined,
): { key: string; label: string; icon: LucideIcon }[] {
  if (!admin) {
    return [
      { key: "user", label: "My Resources", icon: User },
      ...(userPersona ? [{ key: userPersona, label: userPersona, icon: Users }] : []),
      { key: "global", label: "Global", icon: Globe },
    ];
  }
  return [
    { key: "all", label: "All Resources", icon: FolderOpen },
    { key: "global", label: "Global", icon: Globe },
    ...personaNames.map((name) => ({ key: name, label: name, icon: Users })),
    { key: "user", label: "User", icon: User },
  ];
}

/** What dialog the library currently has open, if any. */
type Dialog =
  | { kind: "upload" }
  | { kind: "bulk"; action: BulkAction; to?: string }
  | { kind: "folder"; from: string; to?: string }
  | null;

export function ResourcesPage({ admin = false, location, onNavigate }: Props) {
  const user = useAuthStore((s) => s.user);
  const userPersona = user?.persona;
  const { data: personaData } = usePersonas(admin);
  const personaNames = (personaData?.personas ?? []).map((p) => p.name);

  // The section this library belongs to, which is both where its own address
  // is written and where a resource opened from it lives.
  const basePath = admin ? "/admin/resources" : "/resources";
  // Which surface is asking. On the reader's own portal the platform-admin
  // override does not apply, so Upload is offered on the libraries the caller
  // themselves may add to and nowhere else; the note in its place says where
  // an administrator exercises the rest of their authority.
  const surface = admin ? "admin" : "portal";
  const reachNote = adminReachNote(user, surface);
  const tabs = scopeTabs(admin, personaNames, userPersona);
  const library = useResourceLibrary(
    admin,
    tabs.map((t) => t.key),
    { basePath, location, onNavigate },
  );
  const selection = useSelection();
  const [dialog, setDialog] = useState<Dialog>(null);

  const target = targetForTab(library.activeTab, user);
  const view = useMemo(
    // A search is not a location, so its hits are handed through whole and the
    // tree is not built over them: they are from all over the library.
    () =>
      library.searching
        ? { folders: [], files: library.resources }
        : folderView(library.resources, library.path),
    [library.resources, library.path, library.searching],
  );
  const folders = useMemo(() => everyFolder(library.resources), [library.resources]);
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

  return (
    <Tabs value={library.activeTab} onValueChange={library.setActiveTab} className="gap-4">
      {/* An admin sees a tab per persona, so this bar is the one that has to
          survive a long list: it scrolls rather than wrapping, and its faces are
          tight enough that a typical deployment's personas still fit. */}
      <TabsList
        variant="line"
        className="group-data-[orientation=horizontal]/tabs:h-auto w-full justify-start gap-0 overflow-x-auto border-b p-0"
      >
        {tabs.map((tab) => (
          <TabsTrigger
            key={tab.key}
            value={tab.key}
            className="flex-none gap-1 px-2.5 py-2 group-data-[orientation=horizontal]/tabs:after:bottom-[-1px]"
          >
            <tab.icon className="size-3.5" />
            {tab.label}
          </TabsTrigger>
        ))}
      </TabsList>

      {/* One library body redrawn per scope: only the active tab's panel is
          mounted, so the filter bar and list below are written once. */}
      {tabs.map((tab) => (
        <LibraryPanel
          key={tab.key}
          tabKey={tab.key}
          tabLabel={tab.label}
          admin={admin}
          surface={surface}
          reachNote={reachNote}
          library={library}
          selection={selection}
          view={view}
          onAct={(action) => setDialog({ kind: "bulk", action })}
          onOpenResource={openResource}
          onRenameFolder={(from) => setDialog({ kind: "folder", from })}
          onDropResources={dropResources}
          onDropFolder={(from, to) =>
            setDialog({ kind: "folder", from, to: `${to}/${from.split("/").pop()}` })
          }
          onReveal={reveal}
          onUpload={() => setDialog({ kind: "upload" })}
        />
      ))}

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
    </Tabs>
  );
}

/**
 * One library's body: its trail, its filters, its selection bar and its
 * contents.
 *
 * A component rather than an inline map body because only the active tab's
 * panel is mounted, so this is written once for every library the caller can
 * look at.
 */
function LibraryPanel({
  tabKey,
  tabLabel,
  admin,
  surface,
  reachNote,
  library,
  selection,
  view,
  onAct,
  onOpenResource,
  onRenameFolder,
  onDropResources,
  onDropFolder,
  onReveal,
  onUpload,
}: {
  tabKey: string;
  /** What the tab calls this library, which is what heads its folder trail. */
  tabLabel: string;
  admin: boolean;
  surface: "portal" | "admin";
  reachNote: string;
  library: ReturnType<typeof useResourceLibrary>;
  selection: ReturnType<typeof useSelection>;
  view: FolderView;
  onAct: (action: BulkAction) => void;
  onOpenResource: (r: Resource) => void;
  onRenameFolder: (path: string) => void;
  onDropResources: (ids: string[], to: string) => void;
  onDropFolder: (from: string, to: string) => void;
  onReveal: (r: Resource) => void;
  onUpload: () => void;
}) {
  const user = useAuthStore((s) => s.user);
  // The Upload control is offered only on a tab the caller may actually add to,
  // read from the same authority the server applies to the request. Where it is
  // not offered, the note in its place says who fills this library instead.
  const target = targetForTab(tabKey, user);
  const writable = canWriteScope(user, target, surface);
  const source = [libraryCopy(target).source, reachNote].filter(Boolean).join(" ");
  const tags = tagOptions(library.resources, library.tag);

  return (
    <TabsContent value={tabKey} className="space-y-4">
      <FolderBreadcrumbs
        // The tab's own name, not the destination copy: the administrator's
        // "All Resources" tab spans every library and is the target of none, so
        // naming it from a target would head the trail "My Resources".
        library={tabLabel}
        path={library.path}
        onOpen={library.openFolder}
        className="text-sm"
      />

      <div className="flex flex-wrap items-center gap-3">
        <SearchInput
          className="min-w-[200px] flex-1"
          value={library.searchInput}
          onChange={(e) => library.setSearchInput(e.target.value)}
          placeholder="Search the whole library..."
          aria-label="Search resources"
        />
        <FilterSelect
          label="Filter by tag"
          value={library.tag}
          onChange={library.setTag}
          options={tags}
          disabled={tags.length === 1}
          className="h-9 text-sm"
        />
        {admin && (
          <FilterSelect
            label="Sort resources"
            value={library.sort}
            onChange={(v) => library.setSort(v as ResourceSort)}
            options={SORT_OPTIONS}
            className="h-9 text-sm"
          />
        )}
        {writable ? (
          <Button onClick={onUpload}>
            <FileUp />
            Upload
          </Button>
        ) : (
          <p data-testid="scope-read-only" className="text-xs text-muted-foreground">
            {source}
          </p>
        )}
      </div>

      <SelectionBar count={selection.ids.length} onAct={onAct} onClear={selection.clear} />

      <ResourceResults
        view={view}
        searching={library.searching}
        isLoading={library.isLoading}
        filtering={library.filtering}
        admin={admin}
        complete={!library.hasNextPage}
        canWrite={writable}
        selection={selection}
        readOnlyNote={writable ? undefined : source}
        onOpen={onOpenResource}
        onOpenFolder={library.openFolder}
        onRenameFolder={onRenameFolder}
        onDropResources={onDropResources}
        onDropFolder={onDropFolder}
        onReveal={onReveal}
        onUpload={onUpload}
      />

      <InfiniteFooter
        hasMore={library.hasNextPage}
        isLoadingMore={library.isFetchingNextPage}
        onLoadMore={library.fetchNextPage}
      />

      {library.total > library.resources.length && (
        <p className="text-center text-sm text-muted-foreground">
          Showing {library.resources.length} of {library.total} resources
        </p>
      )}
    </TabsContent>
  );
}

/**
 * Whichever dialog the library has open: the upload form, one action over a
 * selection, or a folder move.
 *
 * Together rather than inline because they share what they act on -- the
 * library in view, its folders, the files picked -- and because a page whose
 * body is three conditionals plus a tab strip is a page nobody can read.
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
