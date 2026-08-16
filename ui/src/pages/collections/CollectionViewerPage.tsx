import { useState } from "react";
import { FolderOpen } from "lucide-react";
import {
  useCollection,
  useDeleteCollection,
  useUpdateCollectionConfig,
} from "@/api/portal/hooks";
import { CollectionThumbnailGenerator } from "@/components/CollectionThumbnailQueue";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { KnowledgeBacklinks } from "@/components/knowledge/KnowledgeBacklinks";
import { EmptyState } from "@/components/patterns/EmptyState";
import { PageHeader } from "@/components/patterns/PageHeader";
import { MarkdownRenderer } from "@/components/renderers/MarkdownRenderer";
import { ShareDialog } from "@/components/ShareDialog";
import { CollectionSection } from "./viewer/CollectionSection";
import { CollectionViewerActions } from "./viewer/CollectionViewerActions";
import { type ThumbSize } from "./viewer/thumbSize";

interface Props {
  collectionId: string;
  onNavigate: (path: string) => void;
  onBack: () => void;
}

export function CollectionViewerPage({ collectionId, onNavigate, onBack }: Props) {
  const { data: coll, isLoading } = useCollection(collectionId);
  const deleteMutation = useDeleteCollection();
  const configMutation = useUpdateCollectionConfig();
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [shareOpen, setShareOpen] = useState(false);

  const thumbSize: ThumbSize = (coll?.config?.thumbnail_size as ThumbSize) || "large";

  function changeThumbSize(size: ThumbSize) {
    if (!coll) return;
    configMutation.mutate({
      id: collectionId,
      config: { ...coll.config, thumbnail_size: size },
    });
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

  async function confirmDelete() {
    await deleteMutation.mutateAsync(collectionId);
    setDeleteOpen(false);
    onBack();
  }

  return (
    <div className="space-y-6">
      <CollectionThumbnailGenerator collection={coll} />

      <PageHeader
        onBack={onBack}
        icon={FolderOpen}
        title={coll.name}
        actions={
          <CollectionViewerActions
            collectionId={collectionId}
            isOwner={coll.is_owner}
            canEdit={coll.can_edit}
            canManage={coll.can_manage}
            thumbSize={thumbSize}
            onChangeThumbSize={changeThumbSize}
            onEdit={() => onNavigate(`/collections/${collectionId}/edit`)}
            onShare={() => setShareOpen(true)}
            onDelete={() => setDeleteOpen(true)}
            deletePending={deleteMutation.isPending}
          />
        }
      />

      {coll.description && (
        <div className="prose prose-sm max-w-none dark:prose-invert">
          <MarkdownRenderer content={coll.description} bare />
        </div>
      )}

      <KnowledgeBacklinks urn={`mcp:collection:${collectionId}`} onNavigate={onNavigate} />

      {coll.sections.map((section) => (
        <CollectionSection
          key={section.id}
          section={section}
          thumbSize={thumbSize}
          onOpenItem={(assetId) =>
            onNavigate(`/collections/${collectionId}/assets/${assetId}`)
          }
        />
      ))}

      {coll.sections.length === 0 && (
        <EmptyState icon={FolderOpen}>
          <p className="font-medium">No sections yet</p>
          {coll.can_edit && (
            <p className="text-xs">Edit this collection to add sections and assets.</p>
          )}
        </EmptyState>
      )}

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
