import { useState, type ReactNode } from "react";
import { Copy, Download, ExternalLink, FolderPlus, History, Info, Maximize2, MoreHorizontal, Save, Trash2 } from "lucide-react";
import type { Asset, AssetVersion } from "@/api/portal/types";
import type { DocAction } from "@/components/renderers/docActions";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { sharedPagePath, shareTokenFromSearch } from "@/lib/shareLink";
import { cn } from "@/lib/utils";
import { VersionControls, ViewModeToggle, versionLabel, versionTime } from "./contentControls";
import type { ViewMode } from "./types";

// The asset page's toolbar on a phone (#2601): one row that never wraps.
// Preview/Source as icons, then Share for the owner and Feedback with its
// count, Expand (or Save while there are unsaved edits), and More, which holds
// everything else: the document's own controls, Versions, Details, the shared
// page, Download, Add to collection, Save to My Assets, and Delete for the
// owner. While an older version is open its picker and Revert take the place
// of Preview/Source until the reader returns to the current one.

/** PhoneMoreActions are the asset's own actions the More menu holds. */
export interface PhoneMoreActions {
  detailsOpen?: boolean;
  onToggleDetails?: () => void;
  onDownload?: () => void;
  /** A download is running; its status line says how far it has got (#2614). */
  downloadBusy?: boolean;
  onAddToCollection?: () => void;
  onCopyToMyAssets?: () => void;
  copyPending?: boolean;
  /** onDelete is the owner's only. */
  onDelete?: () => void;
}

/** PhoneViewer is what the asset page hands its content view on a phone. */
export interface PhoneViewer {
  /** actions are Share (the owner's) and Feedback, on the row. */
  actions?: ReactNode;
  more: PhoneMoreActions;
}

interface PhoneToolbarProps {
  asset: Asset;
  phone: PhoneViewer;
  docActions: DocAction[];
  canEditSource: boolean;
  viewMode: ViewMode;
  onSetViewMode: (mode: ViewMode) => void;
  viewingOldVersion: boolean;
  versions?: AssetVersion[];
  selectedVersion?: number | null;
  onSelectVersion?: (v: number | null) => void;
  canRevert: boolean;
  onRevert: () => void;
  showSave: boolean;
  hasChanges: boolean;
  savePending: boolean;
  onSaveContent: () => void;
  expandOffered: boolean;
  onExpand: () => void;
  afterVersion?: ReactNode;
}

export function PhoneToolbar(p: PhoneToolbarProps) {
  const [versionsOpen, setVersionsOpen] = useState(false);
  const versionsOffered = !!p.onSelectVersion && !!p.versions && p.versions.length > 0;
  return (
    <div className="flex flex-nowrap items-center gap-2" data-testid="phone-toolbar">
      <Leading {...p} />
      {p.afterVersion}
      <span className="flex-1" />
      {p.phone.actions}
      <SaveOrExpand {...p} />
      <PhoneMoreMenu
        more={p.phone.more}
        docActions={p.docActions}
        currentVersion={p.asset.current_version}
        onVersions={versionsOffered ? () => setVersionsOpen(true) : undefined}
      />
      {/* The document's own controls go to More here; the slot stays so the
          feedback sheet can find the toolbar to scroll past. */}
      <div data-testid="content-controls" className="hidden" />
      {versionsOffered && (
        <VersionsDialog
          open={versionsOpen}
          onOpenChange={setVersionsOpen}
          asset={p.asset}
          versions={p.versions ?? []}
          shown={p.selectedVersion ?? p.asset.current_version}
          onSelect={(v) => {
            p.onSelectVersion?.(v === p.asset.current_version ? null : v);
            setVersionsOpen(false);
          }}
        />
      )}
    </div>
  );
}

/** Leading is the row's start: Preview/Source, or while an older version is
 * open its picker and Revert. */
function Leading(p: PhoneToolbarProps) {
  if (!p.viewingOldVersion) {
    return <ViewModeToggle show={p.canEditSource} viewMode={p.viewMode} onSetViewMode={p.onSetViewMode} iconOnly />;
  }
  return (
    <VersionControls
      asset={p.asset}
      versions={p.versions}
      selectedVersion={p.selectedVersion}
      onSelectVersion={p.onSelectVersion}
      viewingOldVersion
      canRevert={p.canRevert}
      onRevert={p.onRevert}
      compact
    />
  );
}

/** SaveOrExpand is Save while there are unsaved edits, and Expand otherwise. */
function SaveOrExpand(p: PhoneToolbarProps) {
  if (p.showSave) {
    return (
      <Button size="sm" onClick={p.onSaveContent} disabled={!p.hasChanges || p.savePending}>
        <Save />
        {p.savePending ? "Saving..." : "Save"}
      </Button>
    );
  }
  if (!p.expandOffered) return null;
  return (
    <Button variant="outline" size="icon-sm" onClick={p.onExpand} aria-label="Expand" title="Expand">
      <Maximize2 />
    </Button>
  );
}

function PhoneMoreMenu({
  more,
  docActions,
  currentVersion,
  onVersions,
}: {
  more: PhoneMoreActions;
  docActions: DocAction[];
  currentVersion: number;
  onVersions?: () => void;
}) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="outline" size="icon-sm" aria-label="More" title="More">
          <MoreHorizontal />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-60">
        <DocItems actions={docActions} />
        <ViewItems more={more} currentVersion={currentVersion} onVersions={onVersions} />
        <AssetItems more={more} />
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

/** DocItems are the document's own controls, under a heading of their own. */
function DocItems({ actions }: { actions: DocAction[] }) {
  if (actions.length === 0) return null;
  return (
    <>
      <DropdownMenuLabel className="text-xs text-muted-foreground uppercase">This document</DropdownMenuLabel>
      {actions.map((a) => (
        <DropdownMenuItem key={a.id} onSelect={a.onSelect} disabled={a.disabled}>
          <a.icon />
          {a.label}
        </DropdownMenuItem>
      ))}
      <DropdownMenuSeparator />
    </>
  );
}

/** ViewItems are Versions, Details and the shared page. */
function ViewItems({ more, currentVersion, onVersions }: { more: PhoneMoreActions; currentVersion: number; onVersions?: () => void }) {
  const token = shareTokenFromSearch(typeof window === "undefined" ? "" : window.location.search);
  return (
    <>
      {onVersions && (
        <DropdownMenuItem onSelect={onVersions}>
          <History />
          <span className="flex-1">Versions</span>
          <span className="text-xs text-muted-foreground">v{currentVersion}</span>
        </DropdownMenuItem>
      )}
      {more.onToggleDetails && (
        <DropdownMenuItem onSelect={more.onToggleDetails}>
          <Info />
          {more.detailsOpen ? "Hide details" : "Details"}
        </DropdownMenuItem>
      )}
      {token && (
        <DropdownMenuItem asChild>
          <a href={sharedPagePath(token)} target="_blank" rel="noopener noreferrer">
            <ExternalLink />
            Shared page
          </a>
        </DropdownMenuItem>
      )}
    </>
  );
}

/** AssetItems are Download, Add to collection, Save to My Assets, and Delete
 * for the owner. */
function AssetItems({ more }: { more: PhoneMoreActions }) {
  return (
    <>
      {more.onDownload && (
        <DropdownMenuItem onSelect={more.onDownload} disabled={more.downloadBusy}>
          <Download />
          Download
        </DropdownMenuItem>
      )}
      {more.onAddToCollection && (
        <DropdownMenuItem onSelect={more.onAddToCollection}>
          <FolderPlus />
          Add to collection
        </DropdownMenuItem>
      )}
      {more.onCopyToMyAssets && (
        <DropdownMenuItem onSelect={more.onCopyToMyAssets} disabled={more.copyPending}>
          <Copy />
          {more.copyPending ? "Copying..." : "Save to My Assets"}
        </DropdownMenuItem>
      )}
      {more.onDelete && (
        <>
          <DropdownMenuSeparator />
          <DropdownMenuItem variant="destructive" onSelect={more.onDelete}>
            <Trash2 />
            Delete
          </DropdownMenuItem>
        </>
      )}
    </>
  );
}

/** VersionsDialog is More > Versions: every version with when it was written,
 * the one showing marked; choosing one opens it. */
function VersionsDialog({
  open,
  onOpenChange,
  asset,
  versions,
  shown,
  onSelect,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  asset: Asset;
  versions: AssetVersion[];
  shown: number;
  onSelect: (v: number) => void;
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Versions</DialogTitle>
        </DialogHeader>
        <ul className="-mx-2 max-h-[60dvh] overflow-auto">
          {versions.map((v) => (
            <li key={v.version}>
              <button
                type="button"
                onClick={() => onSelect(v.version)}
                aria-current={v.version === shown ? "true" : undefined}
                className={cn(
                  "flex w-full items-center justify-between gap-3 rounded-md px-2 py-2 text-left text-sm hover:bg-accent",
                  v.version === shown && "bg-accent font-medium",
                )}
              >
                <span>{versionLabel(v.version, asset.current_version)}</span>
                <span className="text-xs text-muted-foreground">{versionTime(v.created_at)}</span>
              </button>
            </li>
          ))}
        </ul>
      </DialogContent>
    </Dialog>
  );
}
