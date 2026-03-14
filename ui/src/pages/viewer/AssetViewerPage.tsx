import { useAsset, useAssetContent, useUpdateAsset, useDeleteAsset, useUpdateAssetContent } from "@/api/portal/hooks";
import { AssetViewer } from "@/components/AssetViewer";

const backLabels: Record<string, string> = {
  "/": "Back to My Assets",
  "/shared": "Back to Shared With Me",
};

interface Props {
  assetId: string;
  onNavigate: (path: string) => void;
  backPath?: string;
}

export function AssetViewerPage({ assetId, onNavigate, backPath = "/" }: Props) {
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
      backPath={backPath}
      backLabel={backLabels[backPath] ?? "Back"}
      onNavigate={onNavigate}
      updateMutation={updateMutation}
      deleteMutation={deleteMutation}
      contentUpdateMutation={contentUpdateMutation}
    />
  );
}
