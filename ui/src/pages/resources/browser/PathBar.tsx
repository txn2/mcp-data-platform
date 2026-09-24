import { useState, type DragEvent } from "react";
import {
  ArrowLeft,
  ArrowRight,
  ArrowUp,
  ChevronDown,
  FolderPlus,
  LayoutGrid,
  List,
  PanelRight,
  Search,
  Upload,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import type { ViewMode } from "@/components/listView";
import { cn } from "@/lib/utils";
import { PEOPLE_LABEL, PERSON_PREFIX, type ResourceRoot } from "../scopes";

/** What a breadcrumb segment does with something dropped on it. */
export interface CrumbDrop {
  accepts: (e: DragEvent) => boolean;
  onDrop: (path: string, e: DragEvent) => void;
}

const iconButton =
  "grid h-7 min-w-7 place-items-center rounded-md px-1.5 text-muted-foreground hover:bg-accent hover:text-foreground disabled:pointer-events-none disabled:opacity-35";

/**
 * The bar across the top of the file manager (#1872): Back, Forward and Up, the
 * location as a clickable path that is also a drop target, the search, the view
 * toggles, and New folder and Upload.
 */
export function PathBar({
  root,
  path,
  query,
  canBack,
  canForward,
  onBack,
  onForward,
  onOpen,
  onQuery,
  viewMode,
  onViewMode,
  preview,
  onPreview,
  canWrite,
  onNewFolder,
  onUpload,
  onUploadMany,
  drop,
}: {
  root: ResourceRoot;
  path: string;
  query: string;
  canBack: boolean;
  canForward: boolean;
  onBack: () => void;
  onForward: () => void;
  onOpen: (path: string) => void;
  onQuery: (q: string) => void;
  viewMode: ViewMode;
  onViewMode: (mode: ViewMode) => void;
  preview: boolean;
  onPreview: () => void;
  canWrite: boolean;
  onNewFolder: () => void;
  onUpload: () => void;
  onUploadMany: () => void;
  drop: CrumbDrop;
}) {
  const parts = path ? path.split("/") : [];
  const up = parts.length > 0 ? parts.slice(0, -1).join("/") : null;
  return (
    <div className="col-span-full flex flex-wrap items-center gap-2 border-b bg-card px-2.5 py-2 sm:flex-nowrap">
      <div className="flex gap-0.5">
        <button type="button" className={iconButton} onClick={onBack} disabled={!canBack} aria-label="Back" title="Back">
          <ArrowLeft className="size-4" />
        </button>
        <button
          type="button"
          className={iconButton}
          onClick={onForward}
          disabled={!canForward}
          aria-label="Forward"
          title="Forward"
        >
          <ArrowRight className="size-4" />
        </button>
        <button
          type="button"
          className={iconButton}
          onClick={() => up !== null && onOpen(up)}
          disabled={up === null || query !== ""}
          aria-label="Enclosing folder"
          title="Enclosing folder"
        >
          <ArrowUp className="size-4" />
        </button>
      </div>
      <nav
        aria-label="Location"
        data-testid="path-bar"
        className="order-last flex h-[30px] w-full min-w-0 items-center gap-0.5 overflow-hidden rounded-md border bg-muted/50 px-1.5 whitespace-nowrap sm:order-none sm:w-auto sm:flex-1"
      >
        {root.key.startsWith(PERSON_PREFIX) && (
          <>
            <span className="px-1 text-muted-foreground">{PEOPLE_LABEL}</span>
            <span className="text-muted-foreground/60">/</span>
          </>
        )}
        <Crumb label={root.label} path="" onOpen={onOpen} drop={drop} muted />
        {parts.map((seg, i) => {
          const at = parts.slice(0, i + 1).join("/");
          return (
            <span key={at} className="flex min-w-0 items-center gap-0.5">
              <span className="text-muted-foreground/60">/</span>
              <Crumb label={seg} path={at} onOpen={onOpen} drop={drop} />
            </span>
          );
        })}
        {query && (
          <>
            <span className="text-muted-foreground/60">/</span>
            <span className="truncate px-1 text-muted-foreground">Search: &ldquo;{query}&rdquo;</span>
          </>
        )}
      </nav>
      <label className="relative w-36 sm:w-56">
        <Search className="pointer-events-none absolute top-2 left-2 size-3.5 text-muted-foreground" />
        <input
          type="search"
          value={query}
          onChange={(e) => onQuery(e.target.value)}
          placeholder="Search"
          aria-label="Search"
          className="h-[30px] w-full rounded-md border bg-muted/50 pr-2 pl-7 text-sm outline-none focus:ring-2 focus:ring-ring"
        />
      </label>
      <ViewToggles viewMode={viewMode} onViewMode={onViewMode} preview={preview} onPreview={onPreview} />
      {canWrite && (
        <>
          <Button variant="outline" size="sm" className="hidden sm:inline-flex" onClick={onNewFolder}>
            <FolderPlus />
            New folder
          </Button>
          <UploadMenu onUpload={onUpload} onUploadMany={onUploadMany} />
        </>
      )}
    </div>
  );
}

function ViewToggles({
  viewMode,
  onViewMode,
  preview,
  onPreview,
}: {
  viewMode: ViewMode;
  onViewMode: (mode: ViewMode) => void;
  preview: boolean;
  onPreview: () => void;
}) {
  const on = "border bg-muted text-foreground";
  return (
    <div className="flex gap-0.5">
      <button
        type="button"
        className={cn(iconButton, viewMode === "table" && on)}
        onClick={() => onViewMode("table")}
        aria-label="List view"
        aria-pressed={viewMode === "table"}
        title="List"
      >
        <List className="size-4" />
      </button>
      <button
        type="button"
        className={cn(iconButton, viewMode === "grid" && on)}
        onClick={() => onViewMode("grid")}
        aria-label="Tile view"
        aria-pressed={viewMode === "grid"}
        title="Tiles"
      >
        <LayoutGrid className="size-4" />
      </button>
      <button
        type="button"
        className={cn(iconButton, "hidden xl:grid", preview && on)}
        onClick={onPreview}
        aria-label="Toggle preview pane"
        aria-pressed={preview}
        title="Preview pane"
      >
        <PanelRight className="size-4" />
      </button>
    </div>
  );
}

function UploadMenu({ onUpload, onUploadMany }: { onUpload: () => void; onUploadMany: () => void }) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button size="sm">
          <Upload />
          Upload
          <ChevronDown className="opacity-70" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        <DropdownMenuItem onSelect={onUpload}>A file...</DropdownMenuItem>
        <DropdownMenuItem onSelect={onUploadMany}>Many files or a folder...</DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

/** One segment of the location: opens its folder, and takes a drop. */
function Crumb({
  label,
  path,
  onOpen,
  drop,
  muted,
}: {
  label: string;
  path: string;
  onOpen: (path: string) => void;
  drop: CrumbDrop;
  muted?: boolean;
}) {
  const [over, setOver] = useState(false);
  return (
    <button
      type="button"
      data-testid={`crumb-${path || "root"}`}
      className={cn(
        "truncate rounded px-1.5 py-0.5 hover:bg-accent",
        muted ? "text-muted-foreground" : "text-foreground",
        over && "bg-primary/20",
      )}
      onClick={() => onOpen(path)}
      onDragOver={(e) => {
        if (!drop.accepts(e)) return;
        e.preventDefault();
        setOver(true);
      }}
      onDragLeave={() => setOver(false)}
      onDrop={(e) => {
        setOver(false);
        if (!drop.accepts(e)) return;
        e.preventDefault();
        drop.onDrop(path, e);
      }}
    >
      {label}
    </button>
  );
}
