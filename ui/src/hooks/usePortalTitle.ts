import { useEffect } from "react";
import { useBranding } from "@/api/portal/hooks";

/** The title of a deployment that has configured no branding at all. */
export const DEFAULT_PORTAL_TITLE = "MCP Data Platform";

/**
 * usePortalTitle resolves the deployment's portal title and keeps the browser
 * tab on it.
 *
 * The server composes the title from the brand (portal.brand_name yields
 * "<Brand> Portal"), so the constant here is only the last resort for a
 * deployment whose branding request has not landed or failed. Every screen that
 * shows the title calls this hook — sign-in, access-denied, and the shell — so a
 * reader sitting on the login page sees the deployment's name in the tab rather
 * than the product's.
 */
export function usePortalTitle(): { portalTitle: string; brandURL: string } {
  const { data: branding } = useBranding();
  const portalTitle = branding?.portal_title || DEFAULT_PORTAL_TITLE;
  const brandURL = branding?.brand_url || "";

  useEffect(() => {
    document.title = portalTitle;
  }, [portalTitle]);

  return { portalTitle, brandURL };
}
