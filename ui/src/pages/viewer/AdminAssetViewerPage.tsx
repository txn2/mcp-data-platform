import { useState } from "react";
import { useAdminAsset, useAdminAssetContent, useAdminUpdateAsset, useAdminDeleteAsset, useAdminUpdateAssetContent, useAdminAssetVersions, useAdminRevertVersion, useAdminVersionContent } from "@/api/admin/hooks";
import { AssetViewer } from "@/components/AssetViewer";
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
          <span className="text-xs text-muted-foreground px-2 py-1 bg-muted rounded-md truncate max-w-[200px]">
            Owner: {formatOwner(asset)}
          </span>
        ) : undefined
      }
      detailRows={
        asset ? [{ label: "Owner", value: formatOwner(asset) }] : undefined
      }
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
