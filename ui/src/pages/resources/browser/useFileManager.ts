import { useCallback, useEffect, useMemo, useRef, useState, type DragEvent, type MouseEvent } from "react";
import { usePeople } from "@/api/resources/hooks";
import type { ViewMode } from "@/components/listView";
import type { UserProfile } from "@/stores/auth";
import type { Resource } from "@/api/resources/types";
import { canWriteScope, rootsFor, type ResourceRoot } from "../scopes";
import { fromDrop } from "../bulk/collect";
import type { SourceFile } from "../bulk/plan";
import { parentPath } from "../parts/tree";
import type { FmDialog } from "./FmDialogs";
import type { NameEdit, RowHandlers } from "./Listing";
import { freshFolderName, type Entry } from "./model";
import { useToast, type MenuItem } from "./Overlays";
import { useActions } from "./useActions";
import { useFmSelection } from "./useFmSelection";
import { useFolderData } from "./useFolderData";
import { useLocationState } from "./useLocationState";
import { listingKey } from "./keyboard";

/** The drag type a row carries: the keys of the rows being dragged. */
const DRAG_ROWS = "application/x-mcp-entries";

const PREVIEW_STORAGE_KEY = "resources.previewPane";

/**
 * Where the file manager keeps its layout. A key of its own, not the card
 * grid's: a file manager opens as a list (#1872), and a reader who chose tiles
 * for the old page chose them for a different page.
 */
const VIEW_STORAGE_KEY = "resources.fileManagerView";

function stored(key: string): string | null {
  try {
    return localStorage.getItem(key);
  } catch {
    return null;
  }
}

function store(key: string, value: string) {
  try {
    localStorage.setItem(key, value);
  } catch {
    // the choice still holds for this visit
  }
}

export interface FileManagerProps {
  admin: boolean;
  user: UserProfile | null;
  personaNames: string[];
  basePath: string;
  location: string;
  onNavigate?: (path: string, opts?: { replace?: boolean }) => void;
}

/** True when the caller uploaded this file, which lets them change it anywhere. */
function ownFile(e: Entry, user: UserProfile | null): boolean {
  if (e.kind !== "file" || !user) return false;
  return e.resource.uploader_sub === user.user_id || e.resource.uploader_email === user.email;
}

/** The view preferences a reader keeps between visits: tiles or rows, and the pane. */
function useViewPrefs() {
  const [viewMode, setViewMode] = useState<ViewMode>(() => (stored(VIEW_STORAGE_KEY) === "grid" ? "grid" : "table"));
  const [preview, setPreview] = useState(() => stored(PREVIEW_STORAGE_KEY) !== "off");
  return {
    viewMode,
    setViewMode: (m: ViewMode) => {
      setViewMode(m);
      store(VIEW_STORAGE_KEY, m);
    },
    preview,
    togglePreview: () => {
      setPreview(!preview);
      store(PREVIEW_STORAGE_KEY, preview ? "off" : "on");
    },
  };
}

/**
 * useFileManager is the Resources file manager's state (#1872): where it is
 * standing, what that folder holds, what is selected, and every write, reached
 * through the handlers the layout hands its parts.
 */
export function useFileManager(props: FileManagerProps) {
  const base = useCore(props);
  const editing = useEditing(base);
  const drag = useDragDrop(base);
  return { ...base, ...editing, ...drag, onKey: keyHandler(base, editing.startRename) };
}

/** Where the page stands, what it holds, and the state its controls share. */
function useCore({ admin, user, personaNames, basePath, location, onNavigate }: FileManagerProps) {
  const roots = useMemo(() => rootsFor(user, personaNames), [user, personaNames]);
  const people = usePeople(admin && location.includes("/lib/person:"));
  const emailOf = useCallback(
    (id: string) => people.data?.people.find((p) => p.scope_id === id)?.email ?? "",
    [people.data],
  );
  const loc = useLocationState({ basePath, location, onNavigate }, roots, emailOf);
  const { root, path } = loc;
  const data = useFolderData(root, path, loc.query, loc.tag, loc.sort);
  const selection = useFmSelection();
  const { toast, show, dismiss } = useToast();
  const actions = useActions(root, data.entries, show);
  const prefs = useViewPrefs();
  const [edit, setEdit] = useState<NameEdit | null>(null);
  const [menu, setMenu] = useState<{ x: number; y: number; key: string | null } | null>(null);
  const [dialog, setDialog] = useState<FmDialog>(null);
  const pending = useRef<string[] | null>(null);

  const canWrite = canWriteScope(user, root.target);
  const picked = useMemo(() => data.entries.filter((e) => selection.has(e.key)), [data.entries, selection]);
  const canAct = canWrite || (picked.length > 0 && picked.every((e) => ownFile(e, user)));

  // A new place starts with nothing selected, except the row a "Show in
  // folder" or a create asked to land on.
  useEffect(() => {
    selection.only(pending.current ?? []);
    pending.current = null;
    setEdit(null);
    // The row that had the keyboard is gone; the listing takes it, so the
    // keys keep working in the folder just opened (Backspace goes up).
    const listing = document.querySelector<HTMLElement>("[data-listing]");
    const active = document.activeElement;
    if (listing && (active === document.body || listing.contains(active))) listing.focus();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [root.key, path, loc.query, loc.tag]);

  const openEntry = useCallback(
    (e: Entry) => {
      if (e.kind === "folder") return loc.go(root.key, e.path);
      onNavigate?.(loc.address, { replace: true });
      onNavigate?.(`${basePath}/${e.resource.id}`);
    },
    [loc, root.key, onNavigate, basePath],
  );

  const act = (kind: "move" | "tag" | "delete") => {
    if (selection.keys.length > 0) setDialog({ kind, picked: actions.pick(selection.keys) });
  };

  const upload = (many: boolean, folder = path, files?: SourceFile[], target: ResourceRoot = root) =>
    setDialog({ kind: many ? "uploadMany" : "upload", folder, files, root: target });

  const copyURI = (r: Resource) => {
    void navigator.clipboard?.writeText(r.uri).catch(() => undefined);
    show(`Copied ${r.uri}`);
  };

  return {
    admin,
    user,
    personaNames,
    roots,
    loc,
    root,
    path,
    data,
    selection,
    actions,
    prefs,
    toast,
    show,
    dismiss,
    edit,
    setEdit,
    menu,
    setMenu,
    dialog,
    setDialog,
    pending,
    canWrite,
    canAct,
    picked,
    openEntry,
    act,
    upload,
    copyURI,
  };
}

type Base = ReturnType<typeof useCore>;

/** Inline naming: a rename (F2, the menu, the pane) and New folder. */
function useEditing(fm: Base) {
  const startRename = (key: string) => {
    const entry = fm.data.entries.find((e) => e.key === key);
    if (!entry || !fm.canAct) return;
    fm.setEdit({
      key,
      initial: entry.name,
      onCancel: () => fm.setEdit(null),
      onCommit: (value) => {
        fm.setEdit(null);
        void fm.actions.rename(entry, value).then((k) => k && fm.selection.only([k]));
      },
    });
  };
  const startNewFolder = () => {
    if (fm.loc.query || fm.loc.tag) return;
    const names = fm.data.entries.filter((e) => e.kind === "folder").map((e) => e.name);
    fm.setEdit({
      key: "new",
      initial: freshFolderName(names),
      onCancel: () => fm.setEdit(null),
      onCommit: (value) => {
        fm.setEdit(null);
        void fm.actions.create(fm.path, value).then((k) => k && fm.selection.only([k]));
      },
    });
  };
  return { startRename, startNewFolder };
}

/** Rows dragged onto a folder move there; files dropped from the desktop upload there. */
function useDragDrop(fm: Base) {
  const dragKeys = useRef<string[]>([]);
  const { selection, root } = fm;

  const accepts = (target: ResourceRoot, e: DragEvent, into?: Entry) => {
    if (e.dataTransfer.types.includes(DRAG_ROWS)) {
      return target.key === root.key && fm.canAct && !(into && dragKeys.current.includes(into.key));
    }
    return e.dataTransfer.types.includes("Files") && canWriteScope(fm.user, target.target);
  };

  const dropOn = (target: ResourceRoot, to: string, e: DragEvent) => {
    if (e.dataTransfer.types.includes(DRAG_ROWS)) {
      const keys = dragKeys.current;
      dragKeys.current = [];
      selection.clear();
      void fm.actions.move(fm.actions.pick(keys), to);
      return;
    }
    void fromDrop(e.dataTransfer).then((files) => fm.upload(true, to, files, target));
  };

  const handlers: RowHandlers = {
    onClick: (entry, e) => {
      // A folder opens on click; a file is selected and previewed, and opens
      // on double-click or Enter. Resources is the stated exception to the
      // portal's row-click rule (#1872).
      const mod = e.shiftKey || e.metaKey || e.ctrlKey;
      if (entry.kind === "folder" && !mod) return fm.loc.go(root.key, entry.path);
      selection.click(entry.key, e, fm.data.entries.map((x) => x.key));
    },
    onOpen: fm.openEntry,
    onContextMenu: (key, e: MouseEvent) => {
      e.preventDefault();
      e.stopPropagation();
      if (key && !selection.has(key)) selection.only([key]);
      if (key || fm.canWrite) fm.setMenu({ x: e.clientX, y: e.clientY, key });
    },
    onDragStart: (key, e) => {
      const keys = selection.has(key) ? selection.keys : [key];
      if (!selection.has(key)) selection.only([key]);
      dragKeys.current = keys;
      e.dataTransfer.setData(DRAG_ROWS, keys.join("\n"));
      e.dataTransfer.effectAllowed = "move";
    },
    accepts: (e, entry) => accepts(root, e, entry),
    onDrop: (to, e) => dropOn(root, to, e),
    onReveal: (entry) => {
      if (entry.kind !== "file") return;
      fm.pending.current = [entry.key];
      fm.loc.go(root.key, entry.resource.path);
    },
  };
  return { accepts, dropOn, handlers };
}

/** The listing's keyboard, over the rows on screen. */
function keyHandler(fm: Base, startRename: (key: string) => void) {
  return listingKey({
    order: fm.data.entries.map((e) => e.key),
    selection: fm.selection,
    editing: fm.edit !== null,
    onOpen: (key) => {
      const e = fm.data.entries.find((x) => x.key === key);
      if (e) fm.openEntry(e);
    },
    onUp: () => {
      if (fm.path && !fm.loc.query) fm.loc.go(fm.root.key, parentPath(fm.path));
    },
    onRename: () => {
      if (fm.selection.keys.length === 1) startRename(fm.selection.keys[0]!);
    },
    onDelete: () => {
      if (fm.canAct) fm.act("delete");
    },
  });
}

/** The context menu's items on empty space: New folder and Upload here. */
function spaceItems(fm: ReturnType<typeof useFileManager>): MenuItem[] {
  if (!fm.canWrite) return [];
  return [
    { id: "new", label: "New folder", run: fm.startNewFolder },
    { id: "upload", label: "Upload here", run: () => fm.upload(true) },
  ];
}

/** The context menu's items for a row, or for empty space. */
export function contextItems(fm: ReturnType<typeof useFileManager>): MenuItem[] {
  if (!fm.menu?.key) return spaceItems(fm);
  const first = fm.picked[0];
  const one = fm.picked.length === 1;
  const items: MenuItem[] = [{ id: "open", label: "Open", shortcut: "Enter", run: () => first && fm.openEntry(first) }];
  if (fm.canAct && one) items.push({ id: "rename", label: "Rename", shortcut: "F2", run: () => first && fm.startRename(first.key) });
  if (fm.canAct) {
    items.push({ id: "move", label: "Move to...", run: () => fm.act("move") }, { id: "tag", label: "Tag...", run: () => fm.act("tag") });
  }
  if (one && first?.kind === "file") items.push({ id: "copy", label: "Copy URI", run: () => fm.copyURI(first.resource) });
  if (fm.canAct) {
    items.push({ id: "delete", label: "Delete", shortcut: "\u2318\u232B", danger: true, separated: true, run: () => fm.act("delete") });
  }
  return items;
}
