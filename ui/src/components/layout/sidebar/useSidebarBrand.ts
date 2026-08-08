import { useEffect } from "react";
import { useBranding } from "@/api/portal/hooks";
import { useResolvedDark } from "@/stores/theme";
import { resolvePortalLogo } from "@/lib/portalLogo";

const DEFAULT_PORTAL_TITLE = "MCP Data Platform";

/**
 * useSidebarBrand resolves the deployment's mark and name for the rail, and
 * keeps the browser tab's icon on the same logo.
 *
 * It resolves through the shared `resolvePortalLogo`, so a deployment's
 * override cannot land on the sign-in screen and miss the rail; and through
 * `useResolvedDark`, so flipping the OS theme redraws the logo rather than
 * leaving the light mark on a dark rail until the next navigation.
 */
export function useSidebarBrand(): { portalLogo: string; portalTitle: string } {
  const { data: branding } = useBranding();
  const isDark = useResolvedDark();
  const portalLogo = resolvePortalLogo(branding ?? undefined, isDark);
  const portalTitle = branding?.portal_title || DEFAULT_PORTAL_TITLE;

  useEffect(() => {
    let link = document.querySelector<HTMLLinkElement>("link[rel='icon']");
    if (!link) {
      link = document.createElement("link");
      link.rel = "icon";
      document.head.appendChild(link);
    }
    link.type = "image/svg+xml";
    link.href = portalLogo;
  }, [portalLogo]);

  return { portalLogo, portalTitle };
}
