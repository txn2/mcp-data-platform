import { useState } from "react";
import { FolderOpen, Pencil, Share2, Trash2 } from "lucide-react";
import {
  useAdminCollection,
  useAdminUpdateCollection,
  useAdminDeleteCollection,
} from "@/api/admin/hooks";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { EmptyState } from "@/components/patterns/EmptyState";
import { PageHeader } from "@/components/patterns/PageHeader";
import { MarkdownRenderer } from "@/components/renderers/MarkdownRenderer";
import { ShareDialog } from "@/components/ShareDialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { formatOwner } from "@/lib/format";
import { CollectionDetailsDialog } from "./admin/CollectionDetailsDialog";
import { ADMIN_ASSET_BASE, CollectionSection } from "./viewer/CollectionSection";
import { type ThumbSize } from "./viewer/thumbSize";

interface Props {
  collectionId: string;
  onNavigate: (path: string) => void;
}

/**
 * A collection read through the admin routes, whoever owns it.
 *
 * It is the counterpart of the admin asset viewer, and it is what makes an
 * orphaned collection recoverable: an admin can read it, correct its name, hand
 * it to the person who needs it, and delete it, none of which the owner-scoped
 * portal page allows for a collection owned by someone else (#1292). Editing
 * sections stays with the owner's editor, so this page does not offer it.
 */
export function AdminCollectionViewerPage({ collectionId, onNavigate }: Props) {
  const { data: coll, isLoading } = useAdminCollection(collectionId);
  const updateMutation = useAdminUpdateCollection();
  const deleteMutation = useAdminDeleteCollection();
  const [editOpen, setEditOpen] = useState(false);
  const [shareOpen, setShareOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);

  const thumbSize: ThumbSize = (coll?.config?.thumbnail_size as ThumbSize) || "large";

  function goBack() {
    onNavigate("/admin/collections");
  }

  async function saveDetails(values: { name: string; description: string }) {
    try {
      await updateMutation.mutateAsync({ id: collectionId, ...values });
      setEditOpen(false);
    } catch {
      // The dialog stays open and reports the failure from the mutation's own
      // error state; rethrowing here would only surface as an unhandled
      // rejection, since the dialog's submit is fire-and-forget.
    }
  }

  async function confirmDelete() {
    await deleteMutation.mutateAsync(collectionId);
    setDeleteOpen(false);
    goBack();
  }

  if (isLoading) {
    return <p className="py-12 text-center text-sm text-muted-foreground">Loading...</p>;
  }

  if (!coll) {
    return (
      <EmptyState icon={FolderOpen}>
        <p className="font-medium">Collection not found</p>
      </EmptyState>
    );
  }

  return (
    <div className="space-y-6">
      <PageHeader
        onBack={goBack}
        backLabel="Back to collections"
        icon={FolderOpen}
        title={coll.name}
        actions={
          <>
            <Badge variant="muted" className="max-w-[200px]">
              <span className="truncate">Owner: {formatOwner(coll)}</span>
            </Badge>
            <Button variant="outline" size="sm" onClick={() => setEditOpen(true)}>
              <Pencil />
              Edit details
            </Button>
            <Button variant="outline" size="sm" onClick={() => setShareOpen(true)}>
              <Share2 />
              Share
            </Button>
            <Button
              variant="outline"
              size="sm"
              onClick={() => setDeleteOpen(true)}
              disabled={deleteMutation.isPending}
              className="border-destructive/50 text-destructive hover:bg-destructive/10 hover:text-destructive"
            >
              <Trash2 />
              Delete
            </Button>
          </>
        }
      />

      {coll.description && (
        <div className="prose prose-sm max-w-none dark:prose-invert">
          <MarkdownRenderer content={coll.description} bare />
        </div>
      )}

      {coll.sections.map((section) => (
        <CollectionSection
          key={section.id}
          section={section}
          thumbSize={thumbSize}
          // Both the thumbnail and the item itself go through the admin asset
          // surface: the portal ones are gated on owning or being shared the
          // asset, which is exactly what the admin here does not have.
          assetBase={ADMIN_ASSET_BASE}
          onOpenItem={(assetId) => onNavigate(`/admin/assets/${assetId}`)}
        />
      ))}

      {coll.sections.length === 0 && (
        <EmptyState icon={FolderOpen}>
          <p className="font-medium">No sections yet</p>
          <p className="text-xs">Its owner can add sections and assets from the portal.</p>
        </EmptyState>
      )}

      <CollectionDetailsDialog
        open={editOpen}
        onOpenChange={setEditOpen}
        name={coll.name}
        description={coll.description}
        saving={updateMutation.isPending}
        error={updateMutation.isError ? "Failed to save. Check the name and try again." : undefined}
        onSave={saveDetails}
      />

      <ShareDialog
        target={{ type: "collection", id: collectionId }}
        open={shareOpen}
        onOpenChange={setShareOpen}
      />

      <ConfirmDialog
        open={deleteOpen}
        onOpenChange={setDeleteOpen}
        title="Delete collection"
        description={
          <>
            Deleting <span className="font-medium">{coll.name}</span> cannot be undone. The assets
            inside it are not deleted.
          </>
        }
        confirmLabel="Delete"
        destructive
        loading={deleteMutation.isPending}
        onConfirm={confirmDelete}
      />
    </div>
  );
}
