import { useAsset, useAssetContent, useUpdateAsset, useDeleteAsset, useUpdateAssetContent } from "@/api/portal/hooks";
import { AssetViewer } from "@/components/AssetViewer";

interface Props {
  assetId: string;
  onNavigate: (path: string) => void;
}

export function AssetViewerPage({ assetId, onNavigate }: Props) {
  const { data: asset, isLoading } = useAsset(assetId);
  const { data: content } = useAssetContent(assetId);
  const updateMutation = useUpdateAsset();
  const deleteMutation = useDeleteAsset();
  const contentUpdateMutation = useUpdateAssetContent();

  return (
    <AssetViewer
      asset={asset}
      content={content}
      isLoading={isLoading}
      contentUrl={`/api/v1/portal/assets/${assetId}/content`}
      backPath="/"
      backLabel="Back to My Assets"
      onNavigate={onNavigate}
      updateMutation={updateMutation}
      deleteMutation={deleteMutation}
      contentUpdateMutation={contentUpdateMutation}
    />
  );
}
