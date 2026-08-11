import { useEffect } from "react";
import { useBranding } from "@/api/portal/hooks";
import { useResolvedDark } from "@/stores/theme";
import { resolvePortalLogo, bundledPortalLogo } from "@/lib/portalLogo";
import { usePortalTitle } from "@/hooks/usePortalTitle";

/**
 * useSidebarBrand resolves the deployment's mark, name, and brand link for the
 * rail, and keeps the browser tab's icon on the same logo.
 *
 * It resolves through the shared `resolvePortalLogo`, so a deployment's
 * override cannot land on the sign-in screen and miss the rail; through
 * `usePortalTitle`, so the rail and the tab carry the same name; and through
 * `useResolvedDark`, so flipping the OS theme redraws the logo rather than
 * leaving the light mark on a dark rail until the next navigation.
 */
export function useSidebarBrand(): {
  portalLogo: string;
  fallbackLogo: string;
  portalTitle: string;
  brandURL: string;
} {
  const { data: branding } = useBranding();
  const isDark = useResolvedDark();
  const portalLogo = resolvePortalLogo(branding ?? undefined, isDark);
  const fallbackLogo = bundledPortalLogo(isDark);
  const { portalTitle, brandURL } = usePortalTitle();

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

  return { portalLogo, fallbackLogo, portalTitle, brandURL };
}
