import type { Asset } from "@/api/portal/types";
import { usePortalAccess } from "@/hooks/usePortalAccess";
import { useAuthStore } from "@/stores/auth";
import { AssetStatusMenu } from "./AssetStatusMenu";
import { VerifyControls } from "./VerifyControls";

/**
 * The asset page's status and Verified controls (#2502, #2503): the Status menu
 * for the owner and an editor, and Verify for a holder of Can verify assets.
 * A viewer-share recipient sees the badges and neither control; the server
 * refuses a change from them whatever the page shows.
 */
export function AssetMarkControls({
  asset,
  canEdit,
}: {
  asset: Pick<Asset, "id" | "name" | "status" | "verification" | "current_version">;
  /** The owner, an editor, or an administrator. */
  canEdit: boolean;
}) {
  const { verify_assets: verifyAssets } = usePortalAccess();
  const isAdmin = useAuthStore((s) => s.isAdmin());
  const email = useAuthStore((s) => s.user?.email);
  return (
    <>
      {canEdit && <AssetStatusMenu asset={asset} />}
      <VerifyControls asset={asset} canVerify={verifyAssets || isAdmin} email={email} />
    </>
  );
}
