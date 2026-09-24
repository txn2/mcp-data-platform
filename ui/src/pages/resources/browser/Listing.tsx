import { useEffect, useRef, useState, type DragEvent, type MouseEvent, type ReactNode } from "react";
import { Folder } from "lucide-react";
import { AuthImg } from "@/components/AuthImg";
import { contentTypeIcon } from "@/components/ContentTypeBadge";
import type { ViewMode } from "@/components/listView";
import { formatBytes } from "@/lib/format";
import { resourceThumbnailSrc } from "@/lib/thumbnailSupport";
import { cn } from "@/lib/utils";
import { useResolvedDark } from "@/stores/theme";
import type { ResourceSort } from "@/api/resources/types";
import { displayPath, type ResourceRoot } from "../scopes";
import { neverRead } from "../parts/groups";
import { ExtBadge } from "./ExtBadge";
import { plural, shortDate, sortDirection, type Entry, type SORT_COLUMNS } from "./model";
import type { FmSelection } from "./useFmSelection";

/** What a row does when it is clicked, opened, dragged or right-clicked. */
export interface RowHandlers {
  onClick: (entry: Entry, e: MouseEvent) => void;
  onOpen: (entry: Entry) => void;
  onContextMenu: (key: string | null, e: MouseEvent) => void;
  onDragStart: (key: string, e: DragEvent) => void;
  /** A folder row takes a drop the way a tree node does. */
  accepts: (e: DragEvent, entry: Entry) => boolean;
  onDrop: (path: string, e: DragEvent) => void;
  onReveal: (entry: Entry) => void;
}

/** The inline name field: a rename in progress, or a folder being created. */
export interface NameEdit {
  /** The key being renamed, or "new" for a folder being created. */
  key: string;
  initial: string;
  onCommit: (value: string) => void;
  onCancel: () => void;
}

interface ListingProps {
  entries: Entry[];
  viewMode: ViewMode;
  /** A search's hits, which name where each one is. */
  flat: boolean;
  root: ResourceRoot;
  admin: boolean;
  selection: FmSelection;
  sort: ResourceSort;
  onSort: (column: keyof typeof SORT_COLUMNS) => void;
  edit: NameEdit | null;
  handlers: RowHandlers;
}

/**
 * The listing of one folder (#1872): its folders first, then its files, in one
 * table whose headers sort, or as tiles. A folder row and a file row are the
 * same row.
 */
export function Listing(props: ListingProps) {
  return props.viewMode === "grid" ? <Tiles {...props} /> : <Rows {...props} />;
}

const th = "sticky top-0 z-[1] h-[30px] border-b bg-card px-2 text-left text-xs font-medium whitespace-nowrap text-muted-foreground";

function Rows({ entries, flat, root, admin, selection, sort, onSort, edit, handlers }: ListingProps) {
  const keys = entries.map((e) => e.key);
  const all = keys.length > 0 && keys.every((k) => selection.has(k));
  return (
    <table role="grid" aria-label="Files" className="w-full table-fixed border-separate border-spacing-0 text-sm" data-testid="listing">
      <Columns flat={flat} admin={admin} />
      <thead>
        <tr>
          <th className={cn(th, "pl-2.5")}>
            <input
              type="checkbox"
              checked={all}
              onChange={() => (all ? selection.clear() : selection.add(keys))}
              aria-label={all ? "Clear selection" : "Select all"}
            />
          </th>
          <SortHeader column="name" label="Name" sort={sort} onSort={onSort} />
          {flat && <th className={th}>Location</th>}
          {!flat && <th className={cn(th, "hidden md:table-cell")}>Tags</th>}
          <SortHeader column="modified" label="Modified" sort={sort} onSort={onSort} className="hidden md:table-cell" />
          <SortHeader column="size" label="Size" sort={sort} onSort={onSort} className="hidden text-right md:table-cell" />
          {admin && <SortHeader column="lastRead" label="Last read" sort={sort} onSort={onSort} className="hidden lg:table-cell" />}
        </tr>
      </thead>
      <tbody>
        {edit?.key === "new" && <NewFolderRow edit={edit} flat={flat} admin={admin} />}
        {entries.map((e) => (
          <Row
            key={e.key}
            entry={e}
            flat={flat}
            root={root}
            admin={admin}
            selected={selection.has(e.key)}
            focused={selection.focus === e.key}
            onToggle={() => selection.toggle(e.key)}
            edit={edit?.key === e.key ? edit : null}
            handlers={handlers}
          />
        ))}
      </tbody>
    </table>
  );
}

/** The widths the table's columns take. */
function Columns({ flat, admin }: { flat: boolean; admin: boolean }) {
  return (
    <colgroup>
      <col className="w-[34px]" />
      <col />
      {/* Shares, not pixels: fixed widths that outgrew a narrow listing left
          the Name column, the one auto column, at zero. */}
      {flat && <col className="w-[26%]" />}
      {/* A search's Location takes the Tags column's place. */}
      {!flat && <col className="hidden w-[16%] md:table-column" />}
      <col className="hidden w-[15%] md:table-column" />
      <col className="hidden w-[11%] md:table-column" />
      {admin && <col className="hidden w-[12%] lg:table-column" />}
    </colgroup>
  );
}

function SortHeader({
  column,
  label,
  sort,
  onSort,
  className,
}: {
  column: keyof typeof SORT_COLUMNS;
  label: string;
  sort: ResourceSort;
  onSort: (column: keyof typeof SORT_COLUMNS) => void;
  className?: string;
}) {
  const dir = sortDirection(column, sort);
  return (
    <th
      className={cn(th, "cursor-pointer select-none hover:text-foreground", className)}
      aria-sort={dir === null ? "none" : dir === "asc" ? "ascending" : "descending"}
      onClick={() => onSort(column)}
      data-testid={`sort-${column}`}
    >
      {label}
      {dir && <span className="ml-1 text-[10px]">{dir === "asc" ? "▲" : "▼"}</span>}
    </th>
  );
}

const td = "h-[30px] overflow-hidden border-b border-border/55 px-2 text-ellipsis whitespace-nowrap";

/** A drop target's highlight, and the handlers that keep it honest. */
function useDropTarget(enabled: boolean, accepts: (e: DragEvent) => boolean, onDrop: (e: DragEvent) => void) {
  const [over, setOver] = useState(false);
  if (!enabled) return { over: false, props: {} };
  return {
    over,
    props: {
      onDragOver: (e: DragEvent) => {
        if (!accepts(e)) return;
        e.preventDefault();
        setOver(true);
      },
      onDragLeave: () => setOver(false),
      onDrop: (e: DragEvent) => {
        setOver(false);
        if (!accepts(e)) return;
        e.preventDefault();
        e.stopPropagation();
        onDrop(e);
      },
    },
  };
}

function Row({
  entry,
  flat,
  root,
  admin,
  selected,
  focused,
  onToggle,
  edit,
  handlers,
}: {
  entry: Entry;
  flat: boolean;
  root: ResourceRoot;
  admin: boolean;
  selected: boolean;
  focused: boolean;
  onToggle: () => void;
  edit: NameEdit | null;
  handlers: RowHandlers;
}) {
  const ref = useRef<HTMLTableRowElement>(null);
  const folder = entry.kind === "folder";
  const drop = useDropTarget(
    folder,
    (e) => handlers.accepts(e, entry),
    (e) => folder && handlers.onDrop(entry.path, e),
  );
  useFocus(ref, focused);
  return (
    <tr
      ref={ref}
      data-key={entry.key}
      data-testid={`row-${entry.key}`}
      aria-selected={selected}
      tabIndex={focused ? 0 : -1}
      draggable={edit === null}
      className={cn(
        "cursor-default outline-none [&>td]:transition-colors hover:[&>td]:bg-accent/60 focus-visible:[&>td]:bg-accent",
        selected && "[&>td]:bg-primary/10 hover:[&>td]:bg-primary/15",
        drop.over && "[&>td]:bg-primary/20",
      )}
      onClick={(e) => handlers.onClick(entry, e)}
      onDoubleClick={() => handlers.onOpen(entry)}
      onContextMenu={(e) => handlers.onContextMenu(entry.key, e)}
      onDragStart={(e) => handlers.onDragStart(entry.key, e)}
      {...drop.props}
    >
      <td className={cn(td, "pl-2.5")} onClick={(e) => e.stopPropagation()}>
        <input type="checkbox" checked={selected} onChange={onToggle} aria-label={`Select ${entry.name}`} />
      </td>
      <td className={td}>
        <div className="flex min-w-0 items-center gap-2">
          <EntryIcon entry={entry} />
          {edit ? <NameField edit={edit} /> : <span className="truncate">{entry.name}</span>}
        </div>
      </td>
      {flat && <LocationCell entry={entry} root={root} onReveal={() => handlers.onReveal(entry)} />}
      {!flat && (
        <td className={cn(td, "hidden md:table-cell")}>
          <Tags entry={entry} />
        </td>
      )}
      <td className={cn(td, "hidden text-muted-foreground md:table-cell")}>{modifiedOf(entry)}</td>
      <td className={cn(td, "hidden text-right text-muted-foreground tabular-nums md:table-cell")}>{sizeOf(entry)}</td>
      {admin && <LastReadCell entry={entry} />}
    </tr>
  );
}

/** Scrolls a row into view and focuses it when the keyboard lands on it. */
function useFocus(ref: React.RefObject<HTMLElement | null>, focused: boolean) {
  useEffect(() => {
    const el = ref.current;
    if (!focused || !el) return;
    if (el.closest("[data-listing]")?.contains(document.activeElement)) el.focus();
  }, [focused, ref]);
}

function modifiedOf(entry: Entry): string {
  return entry.kind === "folder" ? shortDate(entry.updatedAt) : shortDate(entry.resource.updated_at);
}

function sizeOf(entry: Entry): string {
  return entry.kind === "folder" ? plural(entry.count, "file") : formatBytes(entry.resource.size_bytes);
}

function EntryIcon({ entry }: { entry: Entry }) {
  if (entry.kind === "folder") {
    return <Folder className="size-4 shrink-0 text-[hsl(217_30%_55%)] dark:text-[hsl(217_30%_68%)]" />;
  }
  return <ExtBadge filename={entry.resource.filename} />;
}

function Tags({ entry }: { entry: Entry }) {
  if (entry.kind !== "file") return null;
  return (
    <div className="flex gap-1 overflow-hidden">
      {(entry.resource.tags ?? []).map((t) => (
        <span key={t} className="rounded-full border px-1.5 text-[11px] leading-[17px] text-muted-foreground">
          {t}
        </span>
      ))}
    </div>
  );
}

function LocationCell({ entry, root, onReveal }: { entry: Entry; root: ResourceRoot; onReveal: () => void }) {
  const path = entry.kind === "file" ? entry.resource.path : entry.path;
  return (
    <td className={cn(td, "font-mono text-[11.5px] text-muted-foreground")} title={displayPath(root, path)}>
      {displayPath(root, path)}{" "}
      <button
        type="button"
        className="text-primary hover:underline"
        onClick={(e) => {
          e.stopPropagation();
          onReveal();
        }}
      >
        Show in folder
      </button>
    </td>
  );
}

function LastReadCell({ entry }: { entry: Entry }) {
  if (entry.kind !== "file") return <td className={cn(td, "hidden lg:table-cell")} />;
  const r = entry.resource;
  const stale = !r.last_read_at && neverRead(r);
  return (
    <td
      className={cn(td, "hidden text-xs lg:table-cell", stale ? "text-amber-600 dark:text-amber-400" : "text-muted-foreground")}
      title={stale ? "No reads since it was uploaded" : undefined}
    >
      {r.last_read_at ? new Date(r.last_read_at).toLocaleDateString() : "Never"}
    </td>
  );
}

/** The name field a rename or a new folder is typed into. */
function NameField({ edit }: { edit: NameEdit }) {
  const ref = useRef<HTMLInputElement>(null);
  const done = useRef(false);
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    el.focus();
    // The name without its extension is what a rename usually changes.
    const dot = el.value.lastIndexOf(".");
    el.setSelectionRange(0, dot > 0 ? dot : el.value.length);
  }, []);
  const commit = (value: string) => {
    if (done.current) return;
    done.current = true;
    edit.onCommit(value);
  };
  return (
    <input
      ref={ref}
      defaultValue={edit.initial}
      aria-label="Name"
      data-testid="name-field"
      className="h-[22px] w-full rounded border border-primary bg-card px-1 outline-none"
      onClick={(e) => e.stopPropagation()}
      onKeyDown={(e) => {
        e.stopPropagation();
        if (e.key === "Enter") commit(e.currentTarget.value);
        if (e.key === "Escape") {
          done.current = true;
          edit.onCancel();
        }
      }}
      onBlur={(e) => commit(e.currentTarget.value)}
    />
  );
}

function NewFolderRow({ edit, flat, admin }: { edit: NameEdit; flat: boolean; admin: boolean }) {
  const span = 4 + (flat ? 1 : 0) + (admin ? 1 : 0);
  return (
    <tr data-testid="row-new-folder">
      <td className={td} />
      <td className={td}>
        <div className="flex items-center gap-2">
          <Folder className="size-4 shrink-0 text-[hsl(217_30%_55%)]" />
          <NameField edit={edit} />
        </div>
      </td>
      <td className={td} colSpan={span - 2} />
    </tr>
  );
}

function Tiles({ entries, selection, edit, handlers }: ListingProps) {
  const isDark = useResolvedDark();
  return (
    <div
      className={cn("grid grid-cols-[repeat(auto-fill,minmax(128px,1fr))] gap-1 p-2.5", selection.keys.length > 0 && "selecting")}
      data-testid="tiles"
    >
      {edit?.key === "new" && (
        <TileFrame selected={false}>
          <Folder className="size-11 text-[hsl(217_30%_55%)]" strokeWidth={1} />
          <NameField edit={edit} />
        </TileFrame>
      )}
      {entries.map((e) => (
        <Tile
          key={e.key}
          entry={e}
          isDark={isDark}
          selected={selection.has(e.key)}
          focused={selection.focus === e.key}
          onToggle={() => selection.toggle(e.key)}
          edit={edit?.key === e.key ? edit : null}
          handlers={handlers}
        />
      ))}
    </div>
  );
}

function TileFrame({ selected, children }: { selected: boolean; children: ReactNode }) {
  return (
    <div
      className={cn(
        "relative flex flex-col items-center gap-1.5 rounded-lg border border-transparent px-2 pt-2.5 pb-2",
        selected && "border-primary/45 bg-primary/10",
      )}
    >
      {children}
    </div>
  );
}

function Tile({
  entry,
  isDark,
  selected,
  focused,
  onToggle,
  edit,
  handlers,
}: {
  entry: Entry;
  isDark: boolean;
  selected: boolean;
  focused: boolean;
  onToggle: () => void;
  edit: NameEdit | null;
  handlers: RowHandlers;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const folder = entry.kind === "folder";
  const drop = useDropTarget(
    folder,
    (e) => handlers.accepts(e, entry),
    (e) => folder && handlers.onDrop(entry.path, e),
  );
  useFocus(ref, focused);
  return (
    <div
      ref={ref}
      data-key={entry.key}
      data-testid={`tile-${entry.key}`}
      aria-selected={selected}
      tabIndex={focused ? 0 : -1}
      draggable={edit === null}
      className={cn(
        "group relative flex cursor-default flex-col items-center gap-1.5 rounded-lg border border-transparent px-2 pt-2.5 pb-2 outline-none hover:bg-accent/60 focus-visible:ring-2 focus-visible:ring-ring",
        selected && "border-primary/45 bg-primary/10",
        drop.over && "bg-primary/20",
      )}
      onClick={(e) => handlers.onClick(entry, e)}
      onDoubleClick={() => handlers.onOpen(entry)}
      onContextMenu={(e) => handlers.onContextMenu(entry.key, e)}
      onDragStart={(e) => handlers.onDragStart(entry.key, e)}
      {...drop.props}
    >
      <input
        type="checkbox"
        checked={selected}
        onChange={onToggle}
        onClick={(e) => e.stopPropagation()}
        aria-label={`Select ${entry.name}`}
        className={cn(
          "absolute top-2 left-2 z-[1] opacity-0 group-hover:opacity-100 [.selecting_&]:opacity-100",
          selected && "opacity-100",
        )}
      />
      <TileThumb entry={entry} isDark={isDark} />
      {edit ? (
        <NameField edit={edit} />
      ) : (
        <span className="w-full truncate text-center text-xs" title={entry.name}>
          {entry.name}
        </span>
      )}
      <span className="text-[11px] text-muted-foreground">{sizeOf(entry)}</span>
    </div>
  );
}

function TileThumb({ entry, isDark }: { entry: Entry; isDark: boolean }) {
  const [broken, setBroken] = useState(false);
  const box = "grid aspect-[4/3] w-full place-items-center overflow-hidden rounded-[5px] border bg-muted";
  if (entry.kind === "folder") {
    return (
      <div className={box}>
        <Folder className="size-11 text-[hsl(217_30%_55%)] dark:text-[hsl(217_30%_68%)]" strokeWidth={1} />
      </div>
    );
  }
  const src = resourceThumbnailSrc(entry.resource, isDark);
  const Icon = contentTypeIcon(entry.resource.mime_type);
  return (
    <div className={box}>
      {src && !broken ? (
        <AuthImg
          src={src}
          alt=""
          className="h-full w-full object-cover object-top"
          onError={() => setBroken(true)}
          onLoadFailed={() => setBroken(true)}
        />
      ) : (
        <Icon className="size-8 text-muted-foreground/40" />
      )}
    </div>
  );
}
