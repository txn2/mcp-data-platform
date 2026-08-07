import type { ReactNode } from "react";
import { Pencil } from "lucide-react";
import type { Asset, AssetVersion } from "@/api/portal/types";
import { ProvenancePanel } from "@/components/ProvenancePanel";
import { CollapsibleMarkdown } from "@/components/renderers/CollapsibleMarkdown";
import { VersionHistoryPanel } from "@/components/VersionHistoryPanel";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { formatBytes } from "@/lib/format";
import { AssetMetadataForm } from "./AssetMetadataForm";
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

/** Everything about an asset that is not its content, in a column beside it. */
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
    <Card className="w-80 shrink-0 gap-4 overflow-auto p-4">
      {editing ? (
        <AssetMetadataForm
          name={editName}
          description={editDesc}
          tags={editTags}
          onNameChange={onEditNameChange}
          onDescriptionChange={onEditDescChange}
          onTagsChange={onEditTagsChange}
          onSave={onSaveEdit}
          onCancel={onCancelEdit}
          saving={updateMutation.isPending}
        />
      ) : (
        <>
          <div className="space-y-2">
            <div className="flex items-center justify-between">
              <h3 className="text-sm font-medium">Details</h3>
              {(isOwner || isSharedEditor) && (
                <Button variant="ghost" size="icon-xs" onClick={onStartEdit} title="Edit">
                  <Pencil />
                </Button>
              )}
            </div>
            {asset.description && (
              <div className="text-sm text-muted-foreground">
                <CollapsibleMarkdown content={asset.description} maxHeightPx={120} />
              </div>
            )}
            <dl className="space-y-1.5 text-sm">
              {detailRows?.map((row) => (
                <DetailRow key={row.label} label={row.label}>
                  <span className="max-w-[160px] truncate text-xs">{row.value}</span>
                </DetailRow>
              ))}
              <DetailRow label="Type">
                <span className="font-mono text-xs">{asset.content_type}</span>
              </DetailRow>
              <DetailRow label="Size">{formatBytes(asset.size_bytes)}</DetailRow>
              <DetailRow label="Created">{new Date(asset.created_at).toLocaleString()}</DetailRow>
              <DetailRow label="Updated">{new Date(asset.updated_at).toLocaleString()}</DetailRow>
            </dl>
          </div>

          {asset.tags.length > 0 && (
            <div className="space-y-2">
              <h3 className="text-sm font-medium">Tags</h3>
              <div className="flex flex-wrap gap-1.5">
                {asset.tags.map((tag) => (
                  <Badge key={tag} variant="muted">
                    {tag}
                  </Badge>
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
    </Card>
  );
}

function DetailRow({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex justify-between gap-2">
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="min-w-0">{children}</dd>
    </div>
  );
}
