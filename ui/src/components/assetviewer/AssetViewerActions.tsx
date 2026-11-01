import type { ReactNode } from "react";
import { Share2, Trash2, Download, Copy, FolderPlus, MoreHorizontal } from "lucide-react";
import type { SharePermission } from "@/api/portal/types";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { useIsMobile } from "@/hooks/useIsMobile";
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
 * The way back, the title and the sidebar toggle belong to ViewerLayout. On a
 * phone the actions are one row: Share, when it is the reader's, stays a
 * button and the rest are in a More menu (#2390). */
export function AssetViewerActions(props: AssetViewerActionsProps) {
  const isMobile = useIsMobile();
  return isMobile ? <CompactActions {...props} /> : <FullActions {...props} />;
}

function SharedBadge({ isOwner, sharePermission }: Pick<AssetViewerActionsProps, "isOwner" | "sharePermission">) {
  if (isOwner) return null;
  return <Badge variant="warning">Shared{sharePermission === "editor" ? " (Editor)" : " (Viewer)"}</Badge>;
}

function CompactActions(props: AssetViewerActionsProps) {
  const { toolbarExtra, isOwner, canShare = true, sharePermission, onShare } = props;
  return (
    <>
      <SharedBadge isOwner={isOwner} sharePermission={sharePermission} />
      {toolbarExtra}
      {isOwner && canShare && (
        <Button size="icon-sm" onClick={onShare} title="Share" aria-label="Share">
          <Share2 />
        </Button>
      )}
      <MoreMenu {...props} />
    </>
  );
}

function hasMoreItems(p: AssetViewerActionsProps): boolean {
  return !!p.onDownload || !!p.onAddToCollection || !!p.copyMutation || p.isOwner;
}

/** Every action but Share, as the items of the More menu; none when there is
 * nothing to put in it. */
function MoreMenu(props: AssetViewerActionsProps) {
  if (!hasMoreItems(props)) return null;
  return <MoreMenuItems {...props} />;
}

function MoreMenuItems({
  isOwner,
  copyMutation,
  onDelete,
  onDownload,
  onCopyToMyAssets,
  onAddToCollection,
}: AssetViewerActionsProps) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="outline" size="icon-sm" title="More" aria-label="More">
          <MoreHorizontal />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        {onDownload && (
          <DropdownMenuItem onSelect={onDownload}>
            <Download />
            Download
          </DropdownMenuItem>
        )}
        {onAddToCollection && (
          <DropdownMenuItem onSelect={onAddToCollection}>
            <FolderPlus />
            Add to collection
          </DropdownMenuItem>
        )}
        {copyMutation && (
          <DropdownMenuItem onSelect={onCopyToMyAssets} disabled={copyMutation.isPending}>
            <Copy />
            {copyMutation.isPending ? "Copying..." : "Save to My Assets"}
          </DropdownMenuItem>
        )}
        {isOwner && <DeleteItem separated={!!onDownload || !!onAddToCollection} onDelete={onDelete} />}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

function DeleteItem({ separated, onDelete }: { separated: boolean; onDelete: () => void }) {
  return (
    <>
      {separated && <DropdownMenuSeparator />}
      <DropdownMenuItem variant="destructive" onSelect={onDelete}>
        <Trash2 />
        Delete
      </DropdownMenuItem>
    </>
  );
}

function FullActions({
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
      <SharedBadge isOwner={isOwner} sharePermission={sharePermission} />
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
