import type { ReactNode } from "react";
import { Share2, Trash2, Download, ChevronRight, ChevronLeft, Copy } from "lucide-react";
import type { Asset, SharePermission } from "@/api/portal/types";
import { PageHeader } from "@/components/patterns/PageHeader";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
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

/** The asset viewer's page header: the way back, what this is, what it can do. */
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
    <PageHeader
      onBack={onBack}
      title={<span className="min-w-0 truncate">{asset.name}</span>}
      actions={
        <>
          {toolbarExtra}
          {/* An asset the reader does not own says so, and says what they may
              do with it, before any of the actions below are read. */}
          {!isOwner && (
            <Badge variant="warning">
              Shared{sharePermission === "editor" ? " (Editor)" : " (Viewer)"}
            </Badge>
          )}
          {isOwner && (
            <Button
              variant="outline"
              size="sm"
              onClick={onDelete}
              className="border-destructive/30 text-destructive hover:bg-destructive/10 hover:text-destructive"
            >
              <Trash2 />
              Delete
            </Button>
          )}
          <Button variant="outline" size="sm" onClick={onDownload} title="Download">
            <Download />
            Download
          </Button>
          {copyMutation && (
            <Button
              variant="outline"
              size="sm"
              onClick={onCopyToMyAssets}
              disabled={copyMutation.isPending}
              title="Save an independent copy to My Assets"
            >
              <Copy />
              {copyMutation.isPending ? "Copying..." : "Save to My Assets"}
            </Button>
          )}
          {isOwner && (
            <Button size="sm" onClick={onShare}>
              <Share2 />
              Share
            </Button>
          )}
          <Button
            variant="ghost"
            size="icon-sm"
            onClick={onToggleSidebar}
            title={sidebarOpen ? "Hide details" : "Show details"}
          >
            {sidebarOpen ? <ChevronRight /> : <ChevronLeft />}
          </Button>
        </>
      }
    />
  );
}
