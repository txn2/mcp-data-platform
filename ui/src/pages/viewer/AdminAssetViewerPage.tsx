import { useState } from "react";
import { useAdminAsset, useAdminAssetContent, useAdminUpdateAsset, useAdminDeleteAsset, useAdminUpdateAssetContent, useAdminAssetVersions, useAdminRevertVersion, useAdminVersionContent } from "@/api/admin/hooks";
import { AssetViewer } from "@/components/AssetViewer";
import { Badge } from "@/components/ui/badge";
import { formatOwner } from "@/lib/format";

interface Props {
  assetId: string;
  onNavigate: (path: string) => void;
}

export function AdminAssetViewerPage({ assetId, onNavigate }: Props) {
  const { data: asset, isLoading } = useAdminAsset(assetId);
  const { data: content } = useAdminAssetContent(assetId, asset?.size_bytes);
  const updateMutation = useAdminUpdateAsset();
  const deleteMutation = useAdminDeleteAsset();
  const contentUpdateMutation = useAdminUpdateAssetContent();
  const { data: versionsData, isLoading: versionsLoading } = useAdminAssetVersions(assetId);
  const revertMutation = useAdminRevertVersion();
  const [selectedVersion, setSelectedVersion] = useState<number | null>(null);

  const needsVersionContent = selectedVersion != null && asset != null && selectedVersion !== asset.current_version;
  const { data: versionContent, isLoading: versionContentLoading } = useAdminVersionContent(
    assetId,
    needsVersionContent ? selectedVersion : 0,
  );

  return (
    <AssetViewer
      asset={asset}
      content={content}
      isLoading={isLoading}
      contentUrl={`/api/v1/admin/assets/${assetId}/content`}
      onBack={() => onNavigate("/admin/assets")}
      onNavigate={onNavigate}
      updateMutation={updateMutation}
      deleteMutation={deleteMutation}
      contentUpdateMutation={contentUpdateMutation}
      toolbarExtra={
        asset ? (
          <Badge variant="muted" className="max-w-[200px]">
            <span className="truncate">Owner: {formatOwner(asset)}</span>
          </Badge>
        ) : undefined
      }
      detailRows={
        asset ? [{ label: "Owner", value: formatOwner(asset) }] : undefined
      }
      // An operator reads any session, including one they did not run, on the
      // admin sessions surface.
      sessionPath={(sessionId) =>
        `/admin/sessions/${encodeURIComponent(sessionId)}`
      }
      // An operator opens a referenced file in the console's own library,
      // which holds every resource rather than the ones scoped to them.
      resourcePath={(resourceId) =>
        `/admin/resources/${encodeURIComponent(resourceId)}`
      }
      // An operator opens a referenced asset, and one referencing this asset,
      // in the console's own library, which holds every asset rather than the
      // ones they own.
      assetPath={(id) => `/admin/assets/${encodeURIComponent(id)}`}
      // The stored tile is read through the console's own route: the portal's
      // view grant is owner, share and collection, with no admin arm, so an
      // operator reading someone else's asset is refused the portal one.
      assetApiBase="/api/v1/admin/assets"
      versions={versionsData?.data}
      versionsLoading={versionsLoading}
      revertMutation={revertMutation}
      selectedVersion={selectedVersion}
      onSelectVersion={setSelectedVersion}
      versionContent={needsVersionContent ? versionContent : undefined}
      versionContentLoading={needsVersionContent ? versionContentLoading : false}
    />
  );
}
