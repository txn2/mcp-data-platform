import type { ReactNode } from "react";
import { usePortalAccess } from "@/hooks/usePortalAccess";
import { portalSectionOf, portalSectionVisible } from "@/lib/portalAccess";
import { SectionHiddenNotice } from "./RouteFallbacks";
import { portalNavItems } from "./sidebar/navItems";

/**
 * SectionGate renders a portal route's pages, or the notice for a section the
 * caller's persona does not include (#2225). The server refuses the section's
 * routes either way; this is for a link or a typed path.
 */
export function SectionGate({ route, adminRoute, children }: { route: string; adminRoute: boolean; children: ReactNode }) {
  const portal = usePortalAccess();
  const section = adminRoute ? null : portalSectionOf(route);
  if (section !== null && !portalSectionVisible(section, portal)) {
    const label = portalNavItems.find((item) => item.path === section)?.label ?? "This section";
    return <SectionHiddenNotice section={label} />;
  }
  return <>{children}</>;
}
