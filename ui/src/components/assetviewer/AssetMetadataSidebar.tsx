import type { ReactNode } from "react";
import { Pencil } from "lucide-react";
import type { Asset, AssetVersion } from "@/api/portal/types";
import { ProvenancePanel } from "@/components/ProvenancePanel";
import { VersionHistoryPanel } from "@/components/VersionHistoryPanel";
import { formatBytes } from "@/lib/format";
import type { MutationLike } from "./types";

interface AssetMetadataSidebarProps {
  asset: Asset;
  editing: boolean;
  editName: string;
  editDesc: string;
  editTags: string;
  onEditNameChange: (v: string) => void;
  onEditDescChange: (v: string) => void;
  onEditTagsChange: (v: string) => void;
  onStartEdit: () => void;
  onSaveEdit: () => void;
  onCancelEdit: () => void;
  updateMutation: MutationLike<{ id: string; name: string; description: string; tags: string[] }>;
  isOwner: boolean;
  isSharedEditor: boolean;
  detailRows?: { label: string; value: ReactNode }[];
  versions?: AssetVersion[];
  versionsLoading?: boolean;
}

export function AssetMetadataSidebar({
  asset,
  editing,
  editName,
  editDesc,
  editTags,
  onEditNameChange,
  onEditDescChange,
  onEditTagsChange,
  onStartEdit,
  onSaveEdit,
  onCancelEdit,
  updateMutation,
  isOwner,
  isSharedEditor,
  detailRows,
  versions,
  versionsLoading,
}: AssetMetadataSidebarProps) {
  return (
    <div className="w-80 shrink-0 space-y-4 rounded-lg border bg-card p-4 overflow-auto">
      {editing ? (
        <div className="space-y-3">
          <div>
            <label className="text-xs font-medium text-muted-foreground">Name</label>
            <input
              type="text"
              value={editName}
              onChange={(e) => onEditNameChange(e.target.value)}
              className="mt-1 w-full rounded-md border bg-background px-3 py-1.5 text-sm outline-none ring-ring focus:ring-2"
            />
          </div>
          <div>
            <label className="text-xs font-medium text-muted-foreground">Description</label>
            <textarea
              value={editDesc}
              onChange={(e) => onEditDescChange(e.target.value)}
              rows={3}
              className="mt-1 w-full rounded-md border bg-background px-3 py-1.5 text-sm outline-none ring-ring focus:ring-2 resize-none"
            />
          </div>
          <div>
            <label className="text-xs font-medium text-muted-foreground">Tags (comma-separated)</label>
            <input
              type="text"
              value={editTags}
              onChange={(e) => onEditTagsChange(e.target.value)}
              className="mt-1 w-full rounded-md border bg-background px-3 py-1.5 text-sm outline-none ring-ring focus:ring-2"
            />
          </div>
          <div className="flex gap-2">
            <button
              type="button"
              onClick={onSaveEdit}
              disabled={updateMutation.isPending}
              className="rounded-md bg-primary px-3 py-1.5 text-sm font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
            >
              Save
            </button>
            <button
              type="button"
              onClick={onCancelEdit}
              className="rounded-md bg-secondary px-3 py-1.5 text-sm font-medium text-secondary-foreground hover:bg-secondary/80"
            >
              Cancel
            </button>
          </div>
        </div>
      ) : (
        <>
          <div className="space-y-2">
            <div className="flex items-center justify-between">
              <h3 className="text-sm font-medium">Details</h3>
              {(isOwner || isSharedEditor) && (
                <button
                  type="button"
                  onClick={onStartEdit}
                  className="rounded p-1 hover:bg-accent"
                  title="Edit"
                >
                  <Pencil className="h-3.5 w-3.5" />
                </button>
              )}
            </div>
            {asset.description && (
              <p className="text-sm text-muted-foreground">{asset.description}</p>
            )}
            <dl className="text-sm space-y-1.5">
              {detailRows?.map((row) => (
                <div key={row.label} className="flex justify-between">
                  <dt className="text-muted-foreground">{row.label}</dt>
                  <dd className="text-xs truncate max-w-[160px]">{row.value}</dd>
                </div>
              ))}
              <div className="flex justify-between">
                <dt className="text-muted-foreground">Type</dt>
                <dd className="font-mono text-xs">{asset.content_type}</dd>
              </div>
              <div className="flex justify-between">
                <dt className="text-muted-foreground">Size</dt>
                <dd>{formatBytes(asset.size_bytes)}</dd>
              </div>
              <div className="flex justify-between">
                <dt className="text-muted-foreground">Created</dt>
                <dd>{new Date(asset.created_at).toLocaleString()}</dd>
              </div>
              <div className="flex justify-between">
                <dt className="text-muted-foreground">Updated</dt>
                <dd>{new Date(asset.updated_at).toLocaleString()}</dd>
              </div>
            </dl>
          </div>

          {asset.tags.length > 0 && (
            <div className="space-y-2">
              <h3 className="text-sm font-medium">Tags</h3>
              <div className="flex flex-wrap gap-1.5">
                {asset.tags.map((tag) => (
                  <span
                    key={tag}
                    className="text-xs px-2 py-0.5 rounded-full bg-muted text-muted-foreground"
                  >
                    {tag}
                  </span>
                ))}
              </div>
            </div>
          )}

          <div className="border-t pt-4">
            <ProvenancePanel provenance={asset.provenance} />
          </div>

          {versions && versions.length > 0 && (
            <div className="border-t pt-4">
              <VersionHistoryPanel
                versions={versions}
                currentVersion={asset.current_version}
                isLoading={versionsLoading ?? false}
              />
            </div>
          )}
        </>
      )}
    </div>
  );
}
