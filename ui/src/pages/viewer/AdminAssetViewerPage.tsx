import { useAdminAsset, useAdminAssetContent, useAdminUpdateAsset, useAdminDeleteAsset, useAdminUpdateAssetContent } from "@/api/admin/hooks";
import { AssetViewer } from "@/components/AssetViewer";
import { formatOwner } from "@/lib/format";

interface Props {
  assetId: string;
  onNavigate: (path: string) => void;
}

export function AdminAssetViewerPage({ assetId, onNavigate }: Props) {
  const { data: asset, isLoading } = useAdminAsset(assetId);
  const { data: content } = useAdminAssetContent(assetId);
  const updateMutation = useAdminUpdateAsset();
  const deleteMutation = useAdminDeleteAsset();
  const contentUpdateMutation = useAdminUpdateAssetContent();

  return (
    <AssetViewer
      asset={asset}
      content={content}
      isLoading={isLoading}
      contentUrl={`/api/v1/admin/assets/${assetId}/content`}
      backPath="/admin/assets"
      backLabel="Back to Assets"
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
    />
  );
}
