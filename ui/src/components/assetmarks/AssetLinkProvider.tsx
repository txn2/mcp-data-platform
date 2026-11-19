import { useMemo, type ReactNode } from "react";
import { useAssetLinks } from "@/api/portal/hooks/assetMarks";
import { AssetLinkContext, type AssetLinkFollower } from "@/lib/assetLinks";

/**
 * Follows the links between assets in the displayed asset's content (#2432).
 * A link from its HTML or JSX frame is followed only to an id the asset's
 * current content links to, as the server reads the stored body
 * (GET .../links); a Markdown link opens in the same tab. Both open the target
 * through the portal's own navigation, where the target's page applies its
 * access check: an asset the reader cannot open shows the request-access page.
 */
export function AssetLinkProvider({
  assetId,
  onNavigate,
  children,
}: {
  assetId: string;
  onNavigate: (path: string) => void;
  children: ReactNode;
}) {
  const { data } = useAssetLinks(assetId);
  const links = data?.links;
  const value = useMemo<AssetLinkFollower>(
    () => ({ links, open: (id) => onNavigate(`/assets/${encodeURIComponent(id)}`) }),
    [links, onNavigate],
  );
  return <AssetLinkContext.Provider value={value}>{children}</AssetLinkContext.Provider>;
}
