import { useEffect } from "react";
import { useBranding } from "@/api/portal/hooks";
import { useResolvedDark } from "@/stores/theme";
import { resolvePortalLogo } from "@/lib/portalLogo";

/**
 * usePortalFavicon keeps the browser tab's icon on the deployment's logo for
 * the current theme, falling back to the bundled mark (#2460).
 *
 * The shell's static `<link rel="icon">` names `/favicon.ico`, which the server
 * answers without a session, so the tab has an icon before this runs and on
 * every page the portal does not render. Mounted by the app shell rather than
 * the desktop rail, so a phone, which shows no rail, gets the same icon. The
 * link carries no `type`: a configured logo may be a PNG, and a wrong type
 * makes a browser skip the icon.
 */
export function usePortalFavicon(): void {
  const { data: branding } = useBranding();
  const isDark = useResolvedDark();
  const href = resolvePortalLogo(branding ?? undefined, isDark);

  useEffect(() => {
    let link = document.querySelector<HTMLLinkElement>("link[rel='icon']");
    if (!link) {
      link = document.createElement("link");
      link.rel = "icon";
      document.head.appendChild(link);
    }
    link.removeAttribute("type");
    link.href = href;
  }, [href]);
}
