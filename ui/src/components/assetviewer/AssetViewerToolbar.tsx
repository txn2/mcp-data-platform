import type { ReactNode } from "react";
import { ArrowLeft, Share2, Trash2, Download, ChevronRight, ChevronLeft, Copy } from "lucide-react";
import type { Asset, SharePermission } from "@/api/portal/types";
import type { MutationLike } from "./types";

interface AssetViewerToolbarProps {
  asset: Asset;
  onBack: () => void;
  toolbarExtra?: ReactNode;
  isOwner: boolean;
  sharePermission?: SharePermission;
  copyMutation?: MutationLike<string>;
  onDelete: () => void;
  onDownload: () => void;
  onCopyToMyAssets: () => void;
  onShare: () => void;
  sidebarOpen: boolean;
  onToggleSidebar: () => void;
}

export function AssetViewerToolbar({
  asset,
  onBack,
  toolbarExtra,
  isOwner,
  sharePermission,
  copyMutation,
  onDelete,
  onDownload,
  onCopyToMyAssets,
  onShare,
  sidebarOpen,
  onToggleSidebar,
}: AssetViewerToolbarProps) {
  return (
    <div className="flex items-center gap-2">
      <button
        type="button"
        onClick={onBack}
        className="rounded-md p-1.5 hover:bg-accent"
      >
        <ArrowLeft className="h-4 w-4" />
      </button>
      <h2 className="text-lg font-semibold truncate flex-1 min-w-0">{asset.name}</h2>
      {toolbarExtra}
      {!isOwner && (
        <span className="text-xs px-2 py-1 rounded-full bg-amber-100 text-amber-700 dark:bg-amber-950 dark:text-amber-300">
          Shared{sharePermission === "editor" ? " (Editor)" : " (Viewer)"}
        </span>
      )}
      {isOwner && (
        <button
          type="button"
          onClick={onDelete}
          className="flex items-center gap-1.5 rounded-md border border-destructive/30 px-3 py-1.5 text-sm font-medium text-destructive hover:bg-destructive/10"
        >
          <Trash2 className="h-3.5 w-3.5" />
          Delete
        </button>
      )}
      <button
        type="button"
        onClick={onDownload}
        className="flex items-center gap-1.5 rounded-md border px-3 py-1.5 text-sm font-medium hover:bg-accent"
        title="Download"
      >
        <Download className="h-3.5 w-3.5" />
        Download
      </button>
      {copyMutation && (
        <button
          type="button"
          onClick={onCopyToMyAssets}
          disabled={copyMutation.isPending}
          className="flex items-center gap-1.5 rounded-md border px-3 py-1.5 text-sm font-medium hover:bg-accent disabled:opacity-50"
          title="Save an independent copy to My Assets"
        >
          <Copy className="h-3.5 w-3.5" />
          {copyMutation.isPending ? "Copying..." : "Save to My Assets"}
        </button>
      )}
      {isOwner && (
        <button
          type="button"
          onClick={onShare}
          className="flex items-center gap-1.5 rounded-md bg-primary px-3 py-1.5 text-sm font-medium text-primary-foreground hover:bg-primary/90"
        >
          <Share2 className="h-3.5 w-3.5" />
          Share
        </button>
      )}
      <button
        type="button"
        onClick={onToggleSidebar}
        className="rounded-md p-1.5 hover:bg-accent"
        title={sidebarOpen ? "Hide details" : "Show details"}
      >
        {sidebarOpen ? <ChevronRight className="h-4 w-4" /> : <ChevronLeft className="h-4 w-4" />}
      </button>
    </div>
  );
}
