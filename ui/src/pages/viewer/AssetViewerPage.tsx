import { useState } from "react";
import { useAsset, useAssetContent, useUpdateAsset, useDeleteAsset, useUpdateAssetContent, useCopyAsset, useAssetVersions, useRevertVersion, useVersionContent } from "@/api/portal/hooks";
import { AssetViewer } from "@/components/AssetViewer";
import { FeedbackButton } from "@/components/feedback/FeedbackButton";
import { SharedPageLink } from "@/components/share/SharedPageLink";
import { mySessionPath } from "@/pages/activity/routes";

interface Props {
  assetId: string;
  onNavigate: (path: string) => void;
  onBack: () => void;
}

export function AssetViewerPage({ assetId, onNavigate, onBack }: Props) {
  const { data: asset, isLoading } = useAsset(assetId);
  const { data: content } = useAssetContent(assetId, asset?.size_bytes);
  const updateMutation = useUpdateAsset();
  const deleteMutation = useDeleteAsset();
  const contentUpdateMutation = useUpdateAssetContent();
  const copyMutation = useCopyAsset();
  const { data: versionsData, isLoading: versionsLoading } = useAssetVersions(assetId);
  const revertMutation = useRevertVersion();
  const [selectedVersion, setSelectedVersion] = useState<number | null>(null);

  const needsVersionContent = selectedVersion != null && asset != null && selectedVersion !== asset.current_version;
  const { data: versionContent, isLoading: versionContentLoading } = useVersionContent(
    assetId,
    needsVersionContent ? selectedVersion : 0,
  );

  const isOwner = asset?.is_owner ?? true;
  const sharePermission = asset?.share_permission;

  return (
    <AssetViewer
      asset={asset}
      content={content}
      isLoading={isLoading}
      contentUrl={`/api/v1/portal/assets/${assetId}/content`}
      onBack={onBack}
      onNavigate={onNavigate}
      updateMutation={updateMutation}
      deleteMutation={deleteMutation}
      contentUpdateMutation={isOwner || sharePermission === "editor" ? contentUpdateMutation : undefined}
      copyMutation={!isOwner ? copyMutation : undefined}
      isOwner={isOwner}
      sharePermission={sharePermission}
      // Only the owner is offered the session: a session refuses everyone but
      // its own caller, so on a shared asset this link would lead to a
      // not-found (#1319).
      sessionPath={isOwner ? mySessionPath : undefined}
      // A referenced file opens in the reader's own resource library. The panel
      // only links the ones the server said this reader can open on their own.
      resourcePath={(resourceId) => `/resources/${encodeURIComponent(resourceId)}`}
      // A referenced asset, and an asset referencing this one, open in the
      // reader's own asset library. Both lists only link the ones the server
      // said this reader can open on their own.
      assetPath={(id) => `/assets/${encodeURIComponent(id)}`}
      versions={versionsData?.data}
      versionsLoading={versionsLoading}
      revertMutation={revertMutation}
      selectedVersion={selectedVersion}
      onSelectVersion={setSelectedVersion}
      versionContent={needsVersionContent ? versionContent : undefined}
      versionContentLoading={needsVersionContent ? versionContentLoading : false}
      toolbarExtra={
        <>
          <SharedPageLink />
          <FeedbackButton
            target={{ type: "asset", id: assetId, version: asset?.current_version }}
            canModerate={isOwner || sharePermission === "editor"}
          />
        </>
      }
    />
  );
}
