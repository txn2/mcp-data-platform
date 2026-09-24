import { useEffect, useMemo, useRef, useState, type DragEvent, type KeyboardEvent } from "react";
import { ChevronRight, Folder, FolderOpen, Users } from "lucide-react";
import { useFacets, usePeople } from "@/api/resources/hooks";
import type { Folder as FolderRow } from "@/api/resources/types";
import { cn } from "@/lib/utils";
import { PEOPLE_LABEL, personRoot, type ResourceRoot } from "../scopes";
import { folderEntries } from "./model";

/** Where the page is standing: a top-level folder and a path inside it. */
export interface Place {
  root: string;
  path: string;
  /** A search or tag is in view rather than the folder, so no node is current. */
  searching?: boolean;
}

/** What a tree node does with something dropped on it. */
export interface TreeDrop {
  /** True when the node can take what is being dragged. */
  accepts: (root: ResourceRoot, e: DragEvent) => boolean;
  onDrop: (root: ResourceRoot, path: string, e: DragEvent) => void;
}

const PEOPLE_ID = "people|";

function nodeID(root: string, path: string): string {
  return `${root}|${path}`;
}

/** The ids of a place and every folder above it, which is what is expanded. */
function ancestorIDs(place: Place): string[] {
  const ids = [nodeID(place.root, "")];
  const parts = place.path ? place.path.split("/") : [];
  for (let i = 1; i < parts.length; i++) ids.push(nodeID(place.root, parts.slice(0, i).join("/")));
  return ids;
}

/**
 * The folder tree (#1872): every top-level folder the caller can read, each
 * expanding into its folders with a count, the current folder highlighted and
 * its ancestors open. It follows the WAI-ARIA tree pattern: Up/Down move, Right
 * opens or steps in, Left closes or steps out, Enter opens the folder.
 */
export function FolderTree({
  roots,
  people,
  ownIDs,
  current,
  onOpen,
  drop,
}: {
  roots: ResourceRoot[];
  /** True for a platform administrator, who has a People folder. */
  people: boolean;
  /** The caller's own keys, which People leaves out: they are My Resources. */
  ownIDs: string[];
  current: Place;
  onOpen: (root: ResourceRoot, path: string) => void;
  drop: TreeDrop;
}) {
  const [open, setOpen] = useState<Set<string>>(() => new Set(ancestorIDs(current)));
  const ref = useRef<HTMLDivElement>(null);

  // A place reached any other way -- the listing, a link, Back -- opens its
  // ancestors, so the highlighted folder is always on screen.
  useEffect(() => {
    setOpen((prev) => {
      const next = new Set(prev);
      for (const id of ancestorIDs(current)) next.add(id);
      if (current.root.startsWith("person:")) next.add(PEOPLE_ID);
      return next.size === prev.size ? prev : next;
    });
  }, [current]);

  const toggle = (id: string, to?: boolean) =>
    setOpen((prev) => {
      const next = new Set(prev);
      const want = to ?? !next.has(id);
      if (want) next.add(id);
      else next.delete(id);
      return next;
    });

  const ctx: NodeContext = { open, toggle, current, onOpen, drop };

  return (
    <div
      ref={ref}
      role="tree"
      aria-label="Folders"
      data-testid="folder-tree"
      className="min-h-0 overflow-auto px-1 pt-1.5 pb-3"
      onKeyDown={(e) => treeKey(e, ref.current, toggle, open)}
    >
      {roots.map((root) => (
        <RootNode key={root.key} root={root} depth={0} ctx={ctx} eager />
      ))}
      {people && <PeopleNode ownIDs={ownIDs} ctx={ctx} />}
    </div>
  );
}

interface NodeContext {
  open: Set<string>;
  toggle: (id: string, to?: boolean) => void;
  current: Place;
  onOpen: (root: ResourceRoot, path: string) => void;
  drop: TreeDrop;
}

/** One top-level folder and, when open, its folders. */
function RootNode({
  root,
  depth,
  ctx,
  eager,
  count,
}: {
  root: ResourceRoot;
  depth: number;
  ctx: NodeContext;
  /** Read its folders before it is opened, for the count beside it. */
  eager?: boolean;
  count?: number;
}) {
  const id = nodeID(root.key, "");
  const isOpen = ctx.open.has(id);
  const here = ctx.current.root === root.key;
  const facets = useFacets(root.params, Boolean(eager || isOpen || here));
  const folders = facets.data?.folders;
  const total = count ?? topCount(folders);
  return (
    <>
      <TreeRow
        root={root}
        path=""
        name={root.label}
        depth={depth}
        count={total}
        hasChildren={folders === undefined || folderEntries(folders, "").length > 0}
        ctx={ctx}
      />
      {isOpen && folders && <Children root={root} folders={folders} at="" depth={depth + 1} ctx={ctx} />}
    </>
  );
}

/** Every resource sits in a folder, so the top-level count is the sum of the first level. */
function topCount(folders: FolderRow[] | undefined): number | undefined {
  if (!folders) return undefined;
  return folders.filter((f) => !f.path.includes("/")).reduce((n, f) => n + f.count, 0);
}

function Children({
  root,
  folders,
  at,
  depth,
  ctx,
}: {
  root: ResourceRoot;
  folders: FolderRow[];
  at: string;
  depth: number;
  ctx: NodeContext;
}) {
  const kids = useMemo(
    () =>
      folderEntries(folders, at).sort((a, b) =>
        a.name.localeCompare(b.name, undefined, { sensitivity: "base" }),
      ),
    [folders, at],
  );
  return (
    <>
      {kids.map((k) => {
        if (k.kind !== "folder") return null;
        const isOpen = ctx.open.has(nodeID(root.key, k.path));
        return (
          <div key={k.path} role="none">
            <TreeRow
              root={root}
              path={k.path}
              name={k.name}
              depth={depth}
              count={k.count}
              hasChildren={folderEntries(folders, k.path).length > 0}
              ctx={ctx}
            />
            {isOpen && <Children root={root} folders={folders} at={k.path} depth={depth + 1} ctx={ctx} />}
          </div>
        );
      })}
    </>
  );
}

/** The administrator's People folder: one folder per person, read when opened. */
function PeopleNode({ ownIDs, ctx }: { ownIDs: string[]; ctx: NodeContext }) {
  const isOpen = ctx.open.has(PEOPLE_ID);
  const people = usePeople(isOpen);
  const list = (people.data?.people ?? []).filter((p) => !ownIDs.includes(p.scope_id));
  return (
    <>
      <div
        role="treeitem"
        aria-level={1}
        aria-expanded={isOpen}
        aria-selected={false}
        tabIndex={-1}
        data-node-id={PEOPLE_ID}
        data-testid="tree-people"
        className="flex h-[26px] cursor-pointer items-center gap-1 rounded-[5px] pr-2 pl-1.5 select-none hover:bg-accent/60"
        onClick={() => ctx.toggle(PEOPLE_ID)}
      >
        <Twisty open={isOpen} show />
        <Users className="size-4 shrink-0 text-muted-foreground" />
        <span className="min-w-0 flex-1 truncate">{PEOPLE_LABEL}</span>
      </div>
      {isOpen &&
        list.map((p) => (
          <RootNode key={p.scope_id} root={personRoot(p.scope_id, p.email)} depth={1} ctx={ctx} count={p.count} />
        ))}
      {isOpen && people.data && list.length === 0 && (
        <p className="py-1 pl-10 text-xs text-muted-foreground">Nobody else has files here.</p>
      )}
    </>
  );
}

function Twisty({ open, show }: { open: boolean; show: boolean }) {
  return (
    <span className="grid size-4 shrink-0 place-items-center text-muted-foreground" aria-hidden>
      {show && <ChevronRight className={cn("size-3 transition-transform", open && "rotate-90")} />}
    </span>
  );
}

/** True for the node of the folder in view; none is while a search is. */
function isCurrent(place: Place, root: string, path: string): boolean {
  return !place.searching && place.root === root && place.path === path;
}

/** One folder row of the tree. */
function TreeRow({
  root,
  path,
  name,
  depth,
  count,
  hasChildren,
  ctx,
}: {
  root: ResourceRoot;
  path: string;
  name: string;
  depth: number;
  count?: number;
  hasChildren: boolean;
  ctx: NodeContext;
}) {
  const id = nodeID(root.key, path);
  const isOpen = ctx.open.has(id);
  const here = isCurrent(ctx.current, root.key, path);
  const [over, setOver] = useState(false);
  const Icon = here ? FolderOpen : Folder;
  return (
    <div
      role="treeitem"
      aria-level={depth + 1}
      aria-expanded={hasChildren ? isOpen : undefined}
      aria-selected={here}
      aria-current={here ? "location" : undefined}
      tabIndex={here ? 0 : -1}
      data-node-id={id}
      data-testid={`tree-node-${root.key}:${path}`}
      className={cn(
        "flex h-[26px] cursor-pointer items-center gap-1 rounded-[5px] pr-2 select-none hover:bg-accent/60",
        "focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-ring",
        here && "bg-primary/10 hover:bg-primary/15",
        over && "bg-primary/20 outline-1 outline-dashed outline-primary",
      )}
      style={{ paddingLeft: 6 + depth * 14 }}
      onClick={() => ctx.onOpen(root, path)}
      onDragOver={(e) => {
        if (!ctx.drop.accepts(root, e)) return;
        e.preventDefault();
        setOver(true);
      }}
      onDragLeave={() => setOver(false)}
      onDrop={(e) => {
        setOver(false);
        if (!ctx.drop.accepts(root, e)) return;
        e.preventDefault();
        ctx.drop.onDrop(root, path, e);
      }}
    >
      <span
        onClick={(e) => {
          if (!hasChildren) return;
          e.stopPropagation();
          ctx.toggle(id);
        }}
      >
        <Twisty open={isOpen} show={hasChildren} />
      </span>
      <Icon className="size-4 shrink-0 text-[hsl(217_30%_55%)] dark:text-[hsl(217_30%_68%)]" />
      <span className="min-w-0 flex-1 truncate">{name}</span>
      {count !== undefined && count > 0 && (
        <span className="text-[11px] text-muted-foreground tabular-nums">{count}</span>
      )}
    </div>
  );
}

/** Where a tree key press is, and what it can do. */
interface TreeKeyContext {
  rows: HTMLElement[];
  i: number;
  node: HTMLElement;
  id: string;
  expandable: boolean;
  open: Set<string>;
  toggle: (id: string, to?: boolean) => void;
}

const TREE_KEYS: Record<string, (c: TreeKeyContext) => void> = {
  ArrowDown: (c) => c.rows[Math.min(c.rows.length - 1, c.i + 1)]?.focus(),
  ArrowUp: (c) => c.rows[Math.max(0, c.i - 1)]?.focus(),
  Home: (c) => c.rows[0]?.focus(),
  End: (c) => c.rows[c.rows.length - 1]?.focus(),
  ArrowRight: (c) => {
    if (c.expandable && !c.open.has(c.id)) c.toggle(c.id, true);
    else c.rows[c.i + 1]?.focus();
  },
  ArrowLeft: (c) => {
    if (c.expandable && c.open.has(c.id)) c.toggle(c.id, false);
    else parentRow(c.rows, c.i)?.focus();
  },
  Enter: (c) => c.node.click(),
  " ": (c) => c.node.click(),
};

/** The keyboard half of the tree pattern, over the rows on screen. */
function treeKey(
  e: KeyboardEvent,
  tree: HTMLDivElement | null,
  toggle: (id: string, to?: boolean) => void,
  open: Set<string>,
) {
  const node = (e.target as HTMLElement).closest<HTMLElement>("[role=treeitem]");
  const handler = TREE_KEYS[e.key];
  if (!tree || !node || !handler) return;
  e.preventDefault();
  const rows = [...tree.querySelectorAll<HTMLElement>("[role=treeitem]")];
  handler({
    rows,
    i: rows.indexOf(node),
    node,
    id: node.dataset.nodeId ?? "",
    expandable: node.getAttribute("aria-expanded") !== null,
    open,
    toggle,
  });
}

/** The nearest row above at a shallower level. */
function parentRow(rows: HTMLElement[], i: number): HTMLElement | undefined {
  const level = Number(rows[i]?.getAttribute("aria-level") ?? 1);
  for (let j = i - 1; j >= 0; j--) {
    if (Number(rows[j]!.getAttribute("aria-level")) < level) return rows[j];
  }
  return undefined;
}
