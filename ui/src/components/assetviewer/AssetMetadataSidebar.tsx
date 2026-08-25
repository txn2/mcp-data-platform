import type { ReactNode } from "react";
import { Pencil } from "lucide-react";
import type { Asset, AssetVersion } from "@/api/portal/types";
import { ProvenancePanel } from "@/components/ProvenancePanel";
import { CollapsibleMarkdown } from "@/components/renderers/CollapsibleMarkdown";
import { TablesPanel } from "@/components/tables/TablesPanel";
import { VersionHistoryPanel } from "@/components/VersionHistoryPanel";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { DetailRow } from "@/components/viewer/DetailRow";
import { formatBytes } from "@/lib/format";
import { shortSessionId } from "@/pages/sessions/kind";
import { AssetMetadataForm } from "./AssetMetadataForm";
import type { RetentionMode } from "./AssetRetentionField";
import type { MutationLike } from "./types";

interface AssetMetadataSidebarProps {
  asset: Asset;
  editing: boolean;
  editName: string;
  editDesc: string;
  editTags: string;
  editRetentionMode: RetentionMode;
  editRetentionCustom: string;
  /** Whether this reader may set retention: the owner and an admin, not an
   * editor-share recipient, matching what the API allows. */
  canSetRetention: boolean;
  onEditNameChange: (v: string) => void;
  onEditDescChange: (v: string) => void;
  onEditTagsChange: (v: string) => void;
  onEditRetentionModeChange: (m: RetentionMode) => void;
  onEditRetentionCustomChange: (v: string) => void;
  onStartEdit: () => void;
  onSaveEdit: () => void;
  onCancelEdit: () => void;
  updateMutation: MutationLike<{
    id: string;
    name: string;
    description: string;
    tags: string[];
    // Optional: an update that did not move retention leaves the field out,
    // which is how an editor-share recipient saves a rename without sending the
    // one field the API reserves to the owner.
    max_versions?: number | null;
  }>;
  isOwner: boolean;
  isSharedEditor: boolean;
  detailRows?: { label: string; value: ReactNode }[];
  /** Where the session that produced this asset opens, when the reader can. */
  sessionPath?: (sessionId: string) => string;
  onNavigate?: (path: string) => void;
  versions?: AssetVersion[];
  versionsLoading?: boolean;
}

/** Everything about an asset that is not its content, for the viewer sidebar. */
export function AssetMetadataSidebar({
  asset,
  editing,
  editName,
  editDesc,
  editTags,
  editRetentionMode,
  editRetentionCustom,
  canSetRetention,
  onEditNameChange,
  onEditDescChange,
  onEditTagsChange,
  onEditRetentionModeChange,
  onEditRetentionCustomChange,
  onStartEdit,
  onSaveEdit,
  onCancelEdit,
  updateMutation,
  isOwner,
  isSharedEditor,
  detailRows,
  sessionPath,
  onNavigate,
  versions,
  versionsLoading,
}: AssetMetadataSidebarProps) {
  const openSession = sessionOpener(asset, sessionPath, onNavigate);
  return (
    <>
      {editing ? (
        <AssetMetadataForm
          name={editName}
          description={editDesc}
          tags={editTags}
          retentionMode={editRetentionMode}
          retentionCustom={editRetentionCustom}
          canSetRetention={canSetRetention}
          onNameChange={onEditNameChange}
          onDescriptionChange={onEditDescChange}
          onTagsChange={onEditTagsChange}
          onRetentionModeChange={onEditRetentionModeChange}
          onRetentionCustomChange={onEditRetentionCustomChange}
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
              <SessionRow sessionId={asset.session_id} onOpen={openSession} />
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
            {/*
              Keyed by asset so the panel's own state — which earlier captures
              are open — belongs to the asset it was opened on. The viewer is
              reached with an assetId prop rather than remounted per route, so
              without this the panel carries one asset's disclosures onto the
              next one's captures.
            */}
            <ProvenancePanel
              key={asset.id}
              provenance={asset.provenance}
              onOpenSession={openSession}
            />
          </div>

          {/*
            Registering an asset's file as a table puts its contents in a
            schema everyone with the connection can read, so it is the owner's
            call and an editor share does not carry it (#1327). The panel is
            absent entirely unless the asset is a CSV and somewhere can hold
            the table.
          */}
          <TablesPanel
            kind="asset"
            id={asset.id}
            contentType={asset.content_type}
            filename={fileNameOf(asset.s3_key)}
            canModify={isOwner}
          />

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
    </>
  );
}

/** fileNameOf is the last segment of an object key, which is what the file is
 * called and what a suggested table name is built from. */
function fileNameOf(key: string): string {
  const idx = key.lastIndexOf("/");
  return idx >= 0 ? key.slice(idx + 1) : key;
}

/**
 * sessionOpener returns the click handler that walks from this asset to the
 * session that made it, or undefined when the walk is not on offer. It needs
 * all three: an id to walk to, a route for this reader, and a way to navigate.
 * Missing any one of them, the id is not shown at all rather than shown as a
 * control that goes nowhere.
 */
function sessionOpener(
  asset: Asset,
  sessionPath?: (sessionId: string) => string,
  onNavigate?: (path: string) => void,
): (() => void) | undefined {
  if (!asset.session_id || !sessionPath || !onNavigate) return undefined;
  return () => onNavigate(sessionPath(asset.session_id));
}

/** The session that produced this asset, as a link to it. */
function SessionRow({
  sessionId,
  onOpen,
}: {
  sessionId: string;
  onOpen?: () => void;
}) {
  if (!onOpen) return null;
  return (
    <DetailRow label="Session">
      <button
        type="button"
        onClick={onOpen}
        title={sessionId}
        className="max-w-[160px] truncate font-mono text-xs text-primary hover:underline"
      >
        {shortSessionId(sessionId)}
      </button>
    </DetailRow>
  );
}
