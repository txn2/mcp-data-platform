import { SiteDetailPage, type SiteTab } from "./SiteDetailPage";
import { SiteReleasePage } from "./SiteReleasePage";
import { SitesPage } from "./SitesPage";

const TABS: SiteTab[] = ["environments", "releases", "access", "domains", "history"];

// SiteRoutes is the portal's Sites section (#2559): the listing, a site with
// its tabs, and one release.
export function SiteRoutes({ route, onNavigate }: { route: string; onNavigate: (path: string) => void }) {
  if (route === "/sites") return <SitesPage onNavigate={onNavigate} />;
  const release = route.match(/^\/sites\/([^/]+)\/releases\/(\d+)$/);
  if (release) {
    return <SiteReleasePage site={decodeURIComponent(release[1]!)} number={Number(release[2])} onNavigate={onNavigate} />;
  }
  const detail = route.match(/^\/sites\/([^/]+)(?:\/([a-z]+))?$/);
  if (!detail) return null;
  const tab = TABS.find((t) => t === detail[2]) ?? "environments";
  return <SiteDetailPage site={decodeURIComponent(detail[1]!)} tab={tab} onNavigate={onNavigate} />;
}
