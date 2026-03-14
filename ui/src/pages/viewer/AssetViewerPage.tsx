import { useAsset, useAssetContent, useUpdateAsset, useDeleteAsset, useUpdateAssetContent, useCopyAsset } from "@/api/portal/hooks";
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
  const copyMutation = useCopyAsset();

  const isOwner = asset?.is_owner ?? true;
  const sharePermission = asset?.share_permission;

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
      contentUpdateMutation={isOwner || sharePermission === "editor" ? contentUpdateMutation : undefined}
      copyMutation={!isOwner ? copyMutation : undefined}
      isOwner={isOwner}
      sharePermission={sharePermission}
    />
  );
}
