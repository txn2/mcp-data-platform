import type { ReactNode } from "react";
import { Share2, Trash2, Download, Copy, FolderPlus } from "lucide-react";
import type { SharePermission } from "@/api/portal/types";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import type { MutationLike } from "./types";

interface AssetViewerActionsProps {
  toolbarExtra?: ReactNode;
  isOwner: boolean;
  // canShare is the reader's persona allowing sharing (#2241). Share is
  // the owner's, and only when their persona lets them share.
  canShare?: boolean;
  sharePermission?: SharePermission;
  copyMutation?: MutationLike<string>;
  onDelete: () => void;
  // onDownload is absent when there is nothing to download, and the action is
  // not offered. The persona's PDF export setting governs only the PDF
  // (#2257): downloading reads the same bytes viewing does.
  onDownload?: () => void;
  onCopyToMyAssets: () => void;
  onShare: () => void;
  // onAddToCollection is absent when the reader has no collection to add to
  // and cannot create one, and the action is not offered (#2260).
  onAddToCollection?: () => void;
}

/** What the reader can do to this asset, for the viewer header's action slot.
 * The way back, the title and the sidebar toggle belong to ViewerLayout. */
export function AssetViewerActions({
  toolbarExtra,
  isOwner,
  canShare = true,
  sharePermission,
  copyMutation,
  onDelete,
  onDownload,
  onCopyToMyAssets,
  onShare,
  onAddToCollection,
}: AssetViewerActionsProps) {
  return (
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
      {onDownload && (
        <Button variant="outline" size="sm" onClick={onDownload} title="Download">
          <Download />
          Download
        </Button>
      )}
      <AddToCollectionButton onClick={onAddToCollection} />
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
      {/* Sharing with a person or to a channel is one action, the owner's,
          and only for a persona that allows sharing (#2241). */}
      {isOwner && canShare && (
        <Button size="sm" onClick={onShare}>
          <Share2 />
          Share
        </Button>
      )}
    </>
  );
}

/** Add to collection (#2260), offered only when there is somewhere to add to. */
function AddToCollectionButton({ onClick }: { onClick?: () => void }) {
  if (!onClick) return null;
  return (
    <Button variant="outline" size="sm" onClick={onClick}>
      <FolderPlus />
      Add to collection
    </Button>
  );
}
