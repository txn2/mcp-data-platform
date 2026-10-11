import type { ReactNode } from "react";
import { Share2, Trash2, Download, Copy, Send } from "lucide-react";
import type { SharePermission } from "@/api/portal/types";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import type { MutationLike } from "./types";

interface AssetViewerActionsProps {
  toolbarExtra?: ReactNode;
  isOwner: boolean;
  sharePermission?: SharePermission;
  copyMutation?: MutationLike<string>;
  onDelete: () => void;
  onDownload: () => void;
  onCopyToMyAssets: () => void;
  onShare: () => void;
  // onPostToChannel opens the Post to a channel dialog. Absent hides the
  // action, as in a deployment with no notifications.
  onPostToChannel?: () => void;
}

/** What the reader can do to this asset, for the viewer header's action slot.
 * The way back, the title and the sidebar toggle belong to ViewerLayout. */
export function AssetViewerActions({
  toolbarExtra,
  isOwner,
  sharePermission,
  copyMutation,
  onDelete,
  onDownload,
  onCopyToMyAssets,
  onShare,
  onPostToChannel,
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
      {/* Anyone who can read the asset may post it; the channel's own
          connection decides whether they reach the channel. */}
      {onPostToChannel && (
        <Button variant="outline" size="sm" onClick={onPostToChannel}>
          <Send />
          Post to a channel
        </Button>
      )}
      {isOwner && (
        <Button size="sm" onClick={onShare}>
          <Share2 />
          Share
        </Button>
      )}
    </>
  );
}
