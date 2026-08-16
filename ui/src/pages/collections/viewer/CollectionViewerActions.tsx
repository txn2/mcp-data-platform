import { Pencil, Share2, Trash2 } from "lucide-react";
import { FeedbackButton } from "@/components/feedback/FeedbackButton";
import { SegmentedControl } from "@/components/patterns/SegmentedControl";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { THUMB_SIZE_OPTIONS, type ThumbSize } from "./thumbSize";

interface Props {
  collectionId: string;
  isOwner: boolean;
  canEdit: boolean;
  canManage: boolean;
  thumbSize: ThumbSize;
  onChangeThumbSize: (size: ThumbSize) => void;
  onEdit: () => void;
  onShare: () => void;
  onDelete: () => void;
  deletePending: boolean;
}

/**
 * The collection viewer's action row, offering exactly what the server said
 * this reader may do. Editing and managing are separate authorities: an Editor
 * share shapes the collection, while sharing it onward and deleting it stay
 * with its owner, so the two are read from separate flags rather than from
 * ownership alone.
 */
export function CollectionViewerActions({
  collectionId,
  isOwner,
  canEdit,
  canManage,
  thumbSize,
  onChangeThumbSize,
  onEdit,
  onShare,
  onDelete,
  deletePending,
}: Props) {
  return (
    <>
      {/* A collection the reader does not own says so, and says what they may
          do with it, before any of the actions below are read — the same
          statement the asset viewer makes. */}
      {!isOwner && (
        <Badge variant="warning">Shared{canEdit ? " (Editor)" : " (Viewer)"}</Badge>
      )}
      <FeedbackButton target={{ type: "collection", id: collectionId }} canModerate={canEdit} />
      {canEdit && (
        <Button variant="outline" size="sm" onClick={onEdit}>
          <Pencil />
          Edit
        </Button>
      )}
      {canManage && (
        <>
          <Button variant="outline" size="sm" onClick={onShare}>
            <Share2 />
            Share
          </Button>
          <Button
            variant="outline"
            size="sm"
            onClick={onDelete}
            disabled={deletePending}
            className="border-destructive/50 text-destructive hover:bg-destructive/10 hover:text-destructive"
          >
            <Trash2 />
            Delete
          </Button>
        </>
      )}
      {canEdit && (
        <SegmentedControl
          label="Thumbnail size"
          value={thumbSize}
          onChange={onChangeThumbSize}
          options={THUMB_SIZE_OPTIONS}
        />
      )}
    </>
  );
}
