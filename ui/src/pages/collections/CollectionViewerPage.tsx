import { useState } from "react";
import { ArrowLeft, Pencil, Share2, Trash2, AlertTriangle, FileText, Image, Code, File, Table2 } from "lucide-react";
import { useCollection, useDeleteCollection, useUpdateCollectionConfig } from "@/api/portal/hooks";
import { AuthImg } from "@/components/AuthImg";
import { MarkdownRenderer } from "@/components/renderers/MarkdownRenderer";
import { ShareDialog } from "@/components/ShareDialog";
import { CollectionThumbnailGenerator } from "@/components/CollectionThumbnailQueue";

type ThumbSize = "large" | "medium" | "small" | "none";

const thumbSizeConfig: Record<ThumbSize, { aspect: string; grid: string; label: string }> = {
  large:  { aspect: "aspect-[4/3]", grid: "grid-cols-1 md:grid-cols-2 lg:grid-cols-3", label: "Large" },
  medium: { aspect: "aspect-[3/2] max-h-32", grid: "grid-cols-1 md:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4", label: "Medium" },
  small:  { aspect: "aspect-[2/1] max-h-20", grid: "grid-cols-1 md:grid-cols-3 lg:grid-cols-4 xl:grid-cols-5", label: "Small" },
  none:   { aspect: "", grid: "grid-cols-1 md:grid-cols-2 lg:grid-cols-3", label: "None" },
};

interface Props {
  collectionId: string;
  onNavigate: (path: string) => void;
  onBack: () => void;
}

function contentTypeIcon(ct: string) {
  const lower = ct.toLowerCase();
  if (lower.includes("csv")) return Table2;
  if (lower.includes("html") || lower.includes("jsx")) return Code;
  if (lower.includes("svg") || lower.includes("image")) return Image;
  if (lower.includes("markdown") || lower.includes("text")) return FileText;
  return File;
}

function contentTypeBadgeColor(ct: string) {
  const lower = ct.toLowerCase();
  if (lower.includes("csv")) return "bg-emerald-100 text-emerald-700 dark:bg-emerald-950 dark:text-emerald-300";
  if (lower.includes("jsx") || lower.includes("react")) return "bg-blue-100 text-blue-700 dark:bg-blue-950 dark:text-blue-300";
  if (lower.includes("html")) return "bg-orange-100 text-orange-700 dark:bg-orange-950 dark:text-orange-300";
  if (lower.includes("svg")) return "bg-green-100 text-green-700 dark:bg-green-950 dark:text-green-300";
  if (lower.includes("markdown")) return "bg-purple-100 text-purple-700 dark:bg-purple-950 dark:text-purple-300";
  return "bg-gray-100 text-gray-700 dark:bg-gray-800 dark:text-gray-300";
}

export function CollectionViewerPage({ collectionId, onNavigate, onBack }: Props) {
  const { data: coll, isLoading } = useCollection(collectionId);
  const deleteMutation = useDeleteCollection();
  const configMutation = useUpdateCollectionConfig();
  const [deleteModalOpen, setDeleteModalOpen] = useState(false);
  const [shareOpen, setShareOpen] = useState(false);

  const thumbSize: ThumbSize = (coll?.config?.thumbnail_size as ThumbSize) || "large";

  function changeThumbSize(size: ThumbSize) {
    if (!coll) return;
    configMutation.mutate({ id: collectionId, config: { ...coll.config, thumbnail_size: size } });
  }

  if (isLoading) {
    return (
      <div className="flex items-center justify-center py-12 text-muted-foreground">
        Loading...
      </div>
    );
  }

  if (!coll) {
    return (
      <div className="flex items-center justify-center py-12 text-muted-foreground">
        Collection not found
      </div>
    );
  }

  async function confirmDelete() {
    await deleteMutation.mutateAsync(collectionId);
    setDeleteModalOpen(false);
    onBack();
  }

  return (
    <div className="space-y-6">
      <CollectionThumbnailGenerator collection={coll} />
      {/* Toolbar */}
      <div className="flex items-center gap-3">
        <button
          onClick={onBack}
          className="flex items-center gap-1.5 text-sm text-muted-foreground hover:text-foreground"
        >
          <ArrowLeft className="h-4 w-4" />
          Back
        </button>
        <div className="flex-1" />
        {coll.is_owner && (
          <>
            <button
              onClick={() => onNavigate(`/collections/${collectionId}/edit`)}
              className="flex items-center gap-1.5 rounded-md border px-3 py-1.5 text-sm hover:bg-accent/50"
            >
              <Pencil className="h-3.5 w-3.5" />
              Edit
            </button>
            <button
              onClick={() => setShareOpen(true)}
              className="flex items-center gap-1.5 rounded-md border px-3 py-1.5 text-sm hover:bg-accent/50"
            >
              <Share2 className="h-3.5 w-3.5" />
              Share
            </button>
            <button
              onClick={() => setDeleteModalOpen(true)}
              disabled={deleteMutation.isPending}
              className="flex items-center gap-1.5 rounded-md border border-destructive/50 px-3 py-1.5 text-sm text-destructive hover:bg-destructive/10 disabled:opacity-50"
            >
              <Trash2 className="h-3.5 w-3.5" />
              Delete
            </button>
          </>
        )}
        {coll.is_owner && (
          <div className="flex gap-0.5 rounded-md border p-0.5" title="Thumbnail size">
            {(["large", "medium", "small", "none"] as ThumbSize[]).map((size) => (
              <button
                key={size}
                onClick={() => changeThumbSize(size)}
                title={`${thumbSizeConfig[size].label} thumbnails`}
                className={`rounded-sm px-2 py-1 text-xs transition-colors ${thumbSize === size ? "bg-muted text-foreground" : "text-muted-foreground hover:text-foreground"}`}
              >
                {size === "large" ? "L" : size === "medium" ? "M" : size === "small" ? "S" : "Off"}
              </button>
            ))}
          </div>
        )}
      </div>

      {/* Header */}
      <div>
        <h1 className="text-2xl font-bold">{coll.name}</h1>
        {coll.description && (
          <div className="mt-3 prose prose-sm dark:prose-invert max-w-none">
            <MarkdownRenderer content={coll.description} bare />
          </div>
        )}
      </div>

      {/* Sections */}
      {coll.sections.map((section) => (
        <div key={section.id} className="space-y-3">
          {section.title && (
            <h2 className="text-lg font-semibold border-b pb-2">{section.title}</h2>
          )}
          {section.description && (
            <div className="prose prose-sm dark:prose-invert max-w-none">
              <MarkdownRenderer content={section.description} bare />
            </div>
          )}
          <div className={`grid ${thumbSizeConfig[thumbSize].grid} gap-4 mt-4`}>
            {section.items.map((item) => {
              const Icon = contentTypeIcon(item.asset_content_type || "");
              const name = item.asset_name || "Untitled Asset";
              const cfg = thumbSizeConfig[thumbSize];
              return (
                <button
                  key={item.id}
                  type="button"
                  onClick={() => onNavigate(`/collections/${collectionId}/assets/${item.asset_id}`)}
                  className={`flex ${thumbSize === "none" ? "flex-row items-center gap-3 p-3" : "flex-col items-start"} rounded-lg border bg-card text-left transition-colors hover:bg-accent/50 hover:border-primary/30 overflow-hidden`}
                >
                  {thumbSize !== "none" && (
                    <div className={`w-full ${cfg.aspect} bg-muted`}>
                      {item.asset_thumbnail_s3_key ? (
                        <AuthImg
                          src={`/api/v1/portal/assets/${item.asset_id}/thumbnail`}
                          alt=""
                          className="w-full h-full object-cover object-top"
                        />
                      ) : (
                        <div className="w-full h-full flex items-center justify-center">
                          <Icon className="h-8 w-8 text-muted-foreground/30" />
                        </div>
                      )}
                    </div>
                  )}
                  <div className={`${thumbSize === "none" ? "min-w-0 flex-1" : "p-3 w-full"}`}>
                    <div className="flex items-center gap-2 mb-1">
                      <Icon className="h-4 w-4 text-muted-foreground shrink-0" />
                      <span className="text-sm font-medium truncate flex-1">{name}</span>
                    </div>
                    {item.asset_description && (
                      <p className="text-xs text-muted-foreground line-clamp-2 mb-1.5">
                        {item.asset_description}
                      </p>
                    )}
                    {item.asset_content_type && (
                      <span className={`text-xs px-1.5 py-0.5 rounded-full font-medium ${contentTypeBadgeColor(item.asset_content_type)}`}>
                        {item.asset_content_type}
                      </span>
                    )}
                  </div>
                </button>
              );
            })}
          </div>
        </div>
      ))}

      {coll.sections.length === 0 && (
        <div className="flex flex-col items-center justify-center py-12 text-muted-foreground">
          <p className="text-sm">No sections yet</p>
          <p className="text-xs mt-1">Edit this collection to add sections and assets.</p>
        </div>
      )}

      {/* Share dialog */}
      <ShareDialog
        target={{ type: "collection", id: collectionId }}
        open={shareOpen}
        onOpenChange={setShareOpen}
      />

      {/* Delete confirmation modal */}
      {deleteModalOpen && (
        <div className="fixed inset-0 z-50 flex items-center justify-center">
          <div
            className="absolute inset-0 bg-black/50"
            onClick={() => setDeleteModalOpen(false)}
            onKeyDown={(e) => { if (e.key === "Escape") setDeleteModalOpen(false); }}
            role="button"
            tabIndex={-1}
            aria-label="Close"
          />
          <div className="relative rounded-lg border bg-card p-6 shadow-lg max-w-sm w-full mx-4 space-y-4">
            <div className="flex items-center gap-3">
              <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-full bg-destructive/10">
                <AlertTriangle className="h-5 w-5 text-destructive" />
              </div>
              <div>
                <h3 className="text-sm font-semibold">Delete collection</h3>
                <p className="text-sm text-muted-foreground">This action cannot be undone.</p>
              </div>
            </div>
            <p className="text-sm">
              Are you sure you want to delete <span className="font-medium">{coll.name}</span>?
              The assets inside will not be deleted.
            </p>
            <div className="flex justify-end gap-2">
              <button
                type="button"
                onClick={() => setDeleteModalOpen(false)}
                className="rounded-md bg-secondary px-4 py-2 text-sm font-medium text-secondary-foreground hover:bg-secondary/80"
              >
                Cancel
              </button>
              <button
                type="button"
                onClick={() => void confirmDelete()}
                disabled={deleteMutation.isPending}
                className="rounded-md bg-destructive px-4 py-2 text-sm font-medium text-destructive-foreground hover:bg-destructive/90 disabled:opacity-50"
              >
                {deleteMutation.isPending ? "Deleting..." : "Delete"}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
